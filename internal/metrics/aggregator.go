package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/evandhoffman/olltop/internal/capture"
	"github.com/evandhoffman/olltop/internal/ollama"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

const (
	historySize     = 60
	activeThreshold = 5 * time.Second
)

// modelMetrics tracks the latest capture data for a model.
type modelMetrics struct {
	lastMetrics capture.EvalMetrics
	lastSeen    time.Time
}

// Aggregator merges polling data (ollama.Snapshot) and capture data
// (capture.EvalMetrics) into unified DisplaySnapshot values for the TUI.
type Aggregator struct {
	hasCapture bool

	mu             sync.Mutex
	latestSnapshot ollama.Snapshot
	modelTokSec    map[string]*modelMetrics
	tokHistory     []float64
	promptHistory  []float64
}

// NewAggregator creates a new Aggregator. Set hasCapture to true if pcap
// capture is active (i.e., running as root).
func NewAggregator(hasCapture bool) *Aggregator {
	return &Aggregator{
		hasCapture:    hasCapture,
		modelTokSec:  make(map[string]*modelMetrics),
		tokHistory:   make([]float64, 0, historySize),
		promptHistory: make([]float64, 0, historySize),
	}
}

// Run starts the aggregator loop. It receives from ollamaCh and captureCh,
// merges the data, collects system metrics, and sends DisplaySnapshot values
// on displayCh. captureCh may be nil if capture is not available.
// Run blocks until ctx is cancelled.
func (a *Aggregator) Run(ctx context.Context, ollamaCh <-chan ollama.Snapshot, captureCh <-chan capture.EvalMetrics, displayCh chan<- DisplaySnapshot) error {
	slog.Info("aggregator starting", "has_capture", a.hasCapture)

	for {
		if captureCh != nil {
			select {
			case <-ctx.Done():
				slog.Info("aggregator stopping")
				return ctx.Err()
			case snap, ok := <-ollamaCh:
				if !ok {
					slog.Warn("ollama channel closed")
					return nil
				}
				a.handleOllamaSnapshot(snap)
				a.sendDisplay(ctx, displayCh)
			case metrics, ok := <-captureCh:
				if !ok {
					slog.Warn("capture channel closed, switching to degraded mode")
					captureCh = nil
					continue
				}
				a.handleCaptureMetrics(metrics)
				a.sendDisplay(ctx, displayCh)
			}
		} else {
			select {
			case <-ctx.Done():
				slog.Info("aggregator stopping")
				return ctx.Err()
			case snap, ok := <-ollamaCh:
				if !ok {
					slog.Warn("ollama channel closed")
					return nil
				}
				a.handleOllamaSnapshot(snap)
				a.sendDisplay(ctx, displayCh)
			}
		}
	}
}

func (a *Aggregator) handleOllamaSnapshot(snap ollama.Snapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.latestSnapshot = snap
	slog.Debug("received ollama snapshot", "models", len(snap.Models), "connected", snap.Connected)
}

func (a *Aggregator) handleCaptureMetrics(m capture.EvalMetrics) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.modelTokSec[m.Model] = &modelMetrics{
		lastMetrics: m,
		lastSeen:    time.Now(),
	}

	// Update history with latest aggregate tok/s
	a.appendHistory(m.TokPerSec(), m.PromptTokPerSec())

	slog.Debug("received capture metrics",
		"model", m.Model,
		"tok_per_sec", m.TokPerSec(),
		"prompt_tok_per_sec", m.PromptTokPerSec(),
	)
}

func (a *Aggregator) appendHistory(tokPS, promptPS float64) {
	a.tokHistory = append(a.tokHistory, tokPS)
	if len(a.tokHistory) > historySize {
		a.tokHistory = a.tokHistory[len(a.tokHistory)-historySize:]
	}
	a.promptHistory = append(a.promptHistory, promptPS)
	if len(a.promptHistory) > historySize {
		a.promptHistory = a.promptHistory[len(a.promptHistory)-historySize:]
	}
}

func (a *Aggregator) sendDisplay(ctx context.Context, displayCh chan<- DisplaySnapshot) {
	snap := a.buildSnapshot()
	select {
	case displayCh <- snap:
	case <-ctx.Done():
	}
}

func (a *Aggregator) buildSnapshot() DisplaySnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	snap := a.latestSnapshot

	models := make([]ModelDisplay, 0, len(snap.Models))
	for _, m := range snap.Models {
		md := ModelDisplay{
			Name:     m.Name,
			Size:     m.Size,
			SizeVRAM: m.SizeVRAM,
			Digest:   m.Digest,
		}

		// Compute expires_in relative to now
		if !m.ExpiresAt.IsZero() {
			md.ExpiresIn = time.Until(m.ExpiresAt)
			if md.ExpiresIn < 0 {
				md.ExpiresIn = 0
			}
		}

		// Merge capture data if available
		if mm, ok := a.modelTokSec[m.Name]; ok {
			md.CurrentTokPerSec = mm.lastMetrics.TokPerSec()
			md.PromptTokPerSec = mm.lastMetrics.PromptTokPerSec()
			if now.Sub(mm.lastSeen) < activeThreshold {
				md.Status = "running"
			} else {
				md.Status = "idle"
			}
		} else {
			md.Status = "idle"
		}

		models = append(models, md)
	}

	// Compute current aggregate tok/s from the latest history entry
	var currentTPS, currentPTPS float64
	if len(a.tokHistory) > 0 {
		currentTPS = a.tokHistory[len(a.tokHistory)-1]
	}
	if len(a.promptHistory) > 0 {
		currentPTPS = a.promptHistory[len(a.promptHistory)-1]
	}

	// Copy history slices for the display
	tokHist := make([]float64, len(a.tokHistory))
	copy(tokHist, a.tokHistory)
	promptHist := make([]float64, len(a.promptHistory))
	copy(promptHist, a.promptHistory)

	sysInfo := collectSystemInfo()

	return DisplaySnapshot{
		Models:     models,
		SystemInfo: sysInfo,
		TokPerSec: ThroughputInfo{
			CurrentTokPerSec: currentTPS,
			CurrentPromptTPS: currentPTPS,
			TokPerSecHistory: tokHist,
			PromptTPSHistory: promptHist,
		},
		Connected:  snap.Connected,
		Version:    snap.Version,
		HasCapture: a.hasCapture,
		Timestamp:  now,
	}
}

func collectSystemInfo() SystemInfo {
	info := SystemInfo{}

	// CPU percent (non-blocking, 0 interval returns since-last-call value)
	cpuPcts, err := cpu.Percent(0, false)
	if err != nil {
		slog.Debug("failed to get CPU percent", "error", err)
	} else if len(cpuPcts) > 0 {
		info.CPUPercent = cpuPcts[0]
	}

	// Memory
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		slog.Debug("failed to get memory stats", "error", err)
	} else {
		info.MemUsed = vmStat.Used
		info.MemTotal = vmStat.Total
		info.MemPercent = vmStat.UsedPercent
	}

	return info
}

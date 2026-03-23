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
	// historyWindow is how far back we keep samples for the sparkline.
	historyWindow = 5 * time.Minute
	// sparkBuckets is the number of buckets in the sparkline display.
	// 5 minutes / 5 seconds per bucket = 60 buckets.
	sparkBuckets = 60
	// bucketWidth is the time width of each sparkline bucket.
	bucketWidth = historyWindow / time.Duration(sparkBuckets)
	// activeThreshold: a model is "running" if we saw capture data within this window.
	activeThreshold = 10 * time.Second
	// tickInterval is how often the aggregator emits a fresh snapshot.
	tickInterval = 1 * time.Second
)

// sample is a timestamped tok/s measurement.
type sample struct {
	tokPS    float64
	promptPS float64
	ts       time.Time
}

// modelMetrics tracks the latest capture data for a model.
type modelMetrics struct {
	lastMetrics capture.EvalMetrics
	lastSeen    time.Time
}

// Aggregator merges polling data (ollama.Snapshot) and capture data
// (capture.EvalMetrics) into unified DisplaySnapshot values for the TUI.
type Aggregator struct {
	hasCapture bool
	startedAt  time.Time

	mu             sync.Mutex
	latestSnapshot ollama.Snapshot
	modelTokSec    map[string]*modelMetrics
	samples        []sample // time-windowed samples for sparkline
}

// NewAggregator creates a new Aggregator. Set hasCapture to true if pcap
// capture is active (i.e., running as root).
func NewAggregator(hasCapture bool) *Aggregator {
	return &Aggregator{
		hasCapture:  hasCapture,
		startedAt:   time.Now(),
		modelTokSec: make(map[string]*modelMetrics),
		samples:     make([]sample, 0, sparkBuckets),
	}
}

// Run starts the aggregator loop. It receives from ollamaCh and captureCh,
// merges the data, collects system metrics, and sends DisplaySnapshot values
// on displayCh. captureCh may be nil if capture is not available.
// A 1-second ticker ensures the display refreshes continuously.
// Run blocks until ctx is cancelled.
func (a *Aggregator) Run(ctx context.Context, ollamaCh <-chan ollama.Snapshot, captureCh <-chan capture.EvalMetrics, displayCh chan<- DisplaySnapshot) error {
	slog.Info("aggregator starting", "has_capture", a.hasCapture)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

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
			case metrics, ok := <-captureCh:
				if !ok {
					slog.Warn("capture channel closed, switching to degraded mode")
					captureCh = nil
					continue
				}
				a.handleCaptureMetrics(metrics)
			case <-ticker.C:
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
			case <-ticker.C:
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

	now := time.Now()
	a.modelTokSec[m.Model] = &modelMetrics{
		lastMetrics: m,
		lastSeen:    now,
	}

	// Record timestamped sample
	a.samples = append(a.samples, sample{
		tokPS:    m.TokPerSec(),
		promptPS: m.PromptTokPerSec(),
		ts:       now,
	})

	slog.Debug("received capture metrics",
		"model", m.Model,
		"tok_per_sec", m.TokPerSec(),
		"prompt_tok_per_sec", m.PromptTokPerSec(),
	)
}

// prunesamples removes samples older than historyWindow.
func (a *Aggregator) pruneSamples(now time.Time) {
	cutoff := now.Add(-historyWindow)
	i := 0
	for i < len(a.samples) && a.samples[i].ts.Before(cutoff) {
		i++
	}
	if i > 0 {
		a.samples = a.samples[i:]
	}
}

func (a *Aggregator) sendDisplay(ctx context.Context, displayCh chan<- DisplaySnapshot) {
	snap := a.buildSnapshot()
	select {
	case displayCh <- snap:
	default:
		// Drop if display channel is full — we'll send a fresh one next tick
	}
}

func (a *Aggregator) buildSnapshot() DisplaySnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	a.pruneSamples(now)

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

		// Merge capture data if available — but zero it out if stale
		if mm, ok := a.modelTokSec[m.Name]; ok {
			if now.Sub(mm.lastSeen) < activeThreshold {
				md.CurrentTokPerSec = mm.lastMetrics.TokPerSec()
				md.PromptTokPerSec = mm.lastMetrics.PromptTokPerSec()
				md.Status = "running"
			} else {
				md.CurrentTokPerSec = 0
				md.PromptTokPerSec = 0
				md.Status = "idle"
			}
		} else {
			md.Status = "idle"
		}

		models = append(models, md)
	}

	// Build time-bucketed sparkline history from samples.
	// Each bucket covers bucketWidth (5s). We average samples within each bucket.
	tokHist, promptHist := a.buildSparklineHistory(now)

	// Current tok/s is the most recent bucket value (or 0 if empty)
	var currentTPS, currentPTPS float64
	if len(tokHist) > 0 {
		currentTPS = tokHist[len(tokHist)-1]
	}
	if len(promptHist) > 0 {
		currentPTPS = promptHist[len(promptHist)-1]
	}

	// Compute max and window start from samples
	var maxTPS, maxPTPS float64
	var windowStart time.Time
	for _, s := range a.samples {
		if s.tokPS > maxTPS {
			maxTPS = s.tokPS
		}
		if s.promptPS > maxPTPS {
			maxPTPS = s.promptPS
		}
	}
	if len(a.samples) > 0 {
		windowStart = a.samples[0].ts
	}

	// How many buckets has the app been alive for?
	appRunning := now.Sub(a.startedAt)
	activeBuckets := int(appRunning / bucketWidth)
	if activeBuckets > sparkBuckets {
		activeBuckets = sparkBuckets
	}

	sysInfo := collectSystemInfo()

	return DisplaySnapshot{
		Models:     models,
		SystemInfo: sysInfo,
		TokPerSec: ThroughputInfo{
			CurrentTokPerSec: currentTPS,
			CurrentPromptTPS: currentPTPS,
			TokPerSecHistory: tokHist,
			PromptTPSHistory: promptHist,
			ActiveBuckets:    activeBuckets,
			MaxTokPerSec:     maxTPS,
			MaxPromptTPS:     maxPTPS,
			WindowStart:      windowStart,
		},
		Connected:  snap.Connected,
		Version:    snap.Version,
		HasCapture: a.hasCapture,
		Timestamp:  now,
	}
}

// buildSparklineHistory buckets samples into sparkBuckets time slots.
// Empty buckets (no activity) show as 0.
func (a *Aggregator) buildSparklineHistory(now time.Time) (tokHist, promptHist []float64) {
	tokHist = make([]float64, sparkBuckets)
	promptHist = make([]float64, sparkBuckets)

	windowStart := now.Add(-historyWindow)

	// Count samples per bucket for averaging
	counts := make([]int, sparkBuckets)

	for _, s := range a.samples {
		elapsed := s.ts.Sub(windowStart)
		if elapsed < 0 {
			continue
		}
		bucket := int(elapsed / bucketWidth)
		if bucket >= sparkBuckets {
			bucket = sparkBuckets - 1
		}
		tokHist[bucket] += s.tokPS
		promptHist[bucket] += s.promptPS
		counts[bucket]++
	}

	// Average the buckets that have multiple samples
	for i := range tokHist {
		if counts[i] > 1 {
			tokHist[i] /= float64(counts[i])
			promptHist[i] /= float64(counts[i])
		}
	}

	return tokHist, promptHist
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

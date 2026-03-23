//go:build darwin

package capture

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/google/gopacket/tcpassembly"
	"github.com/google/gopacket/tcpassembly/tcpreader"
)

// ollamaResponse represents the JSON structure of an Ollama streaming response chunk.
type ollamaResponse struct {
	Model              string `json:"model"`
	Done               bool   `json:"done"`
	Response           string `json:"response"`            // /api/generate streaming field
	Message            *struct{ Content string } `json:"message"` // /api/chat streaming field
	EvalCount          int64  `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
	PromptEvalCount    int64  `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	TotalDuration      int64  `json:"total_duration"`
}

// hasContent returns true if this chunk contains a generated token.
func (r *ollamaResponse) hasContent() bool {
	if r.Response != "" {
		return true
	}
	if r.Message != nil && r.Message.Content != "" {
		return true
	}
	return false
}

const (
	// emitInterval controls how often each stream emits live metrics.
	emitInterval = 500 * time.Millisecond
	// rollingWindow is the window for computing live tok/s.
	rollingWindow = 2 * time.Second
)

// PcapBackend captures Ollama traffic on the loopback interface using libpcap.
type PcapBackend struct {
	port  int
	iface string
}

// NewPcapBackend creates a new PcapBackend that listens on the given port.
func NewPcapBackend(port int) *PcapBackend {
	if port <= 0 {
		port = 11434
	}
	return &PcapBackend{
		port:  port,
		iface: "lo0",
	}
}

// ollamaStreamFactory creates ollamaStream instances for TCP reassembly.
type ollamaStreamFactory struct {
	evalMetrics      chan<- EvalMetrics
	streamingMetrics chan<- StreamingMetrics
}

// ollamaStream processes a single reassembled TCP stream looking for Ollama JSON.
type ollamaStream struct {
	net, transport   gopacket.Flow
	reader           tcpreader.ReaderStream
	evalMetrics      chan<- EvalMetrics
	streamingMetrics chan<- StreamingMetrics

	// Per-stream state for live metrics
	model      string
	firstChunk time.Time     // when we saw the first chunk (any)
	firstToken time.Time     // when we saw the first content token
	tokenCount int64         // tokens received so far
	recentToks []time.Time   // timestamps of recent tokens for rolling rate
	lastEmit   time.Time     // last time we emitted streaming metrics
}

func (f *ollamaStreamFactory) New(net, transport gopacket.Flow) tcpassembly.Stream {
	s := &ollamaStream{
		net:              net,
		transport:        transport,
		reader:           tcpreader.NewReaderStream(),
		evalMetrics:      f.evalMetrics,
		streamingMetrics: f.streamingMetrics,
		recentToks:       make([]time.Time, 0, 256),
	}
	go s.run()
	return &s.reader
}

func (s *ollamaStream) run() {
	scanner := bufio.NewScanner(&s.reader)
	// Ollama responses can have large JSON lines; allow up to 1MB per line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		// Quick check: must look like JSON with a model field
		if !strings.Contains(line, `"model"`) {
			continue
		}

		var resp ollamaResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}

		if resp.Model == "" {
			continue
		}

		now := time.Now()

		// Track model name from first chunk
		if s.model == "" {
			s.model = resp.Model
			s.firstChunk = now
			slog.Debug("stream started", "model", s.model)
		}

		if resp.Done {
			// Final chunk — emit authoritative EvalMetrics
			s.emitEvalMetrics(resp)
			// Emit stream-ended signal
			s.emitStreamingUpdate(now, false)
			// Reset state for potential connection reuse
			s.resetStreamState()
			continue
		}

		// Streaming token chunk
		if resp.hasContent() {
			if s.firstToken.IsZero() {
				s.firstToken = now
				slog.Debug("first token received",
					"model", s.model,
					"ttft", now.Sub(s.firstChunk).Round(time.Millisecond),
				)
			}
			s.tokenCount++
			s.recentToks = append(s.recentToks, now)

			// Emit live metrics periodically
			if now.Sub(s.lastEmit) >= emitInterval {
				s.emitStreamingUpdate(now, true)
			}
		}
	}

	// Stream ended (connection closed) — emit inactive if we were tracking
	if s.model != "" && s.tokenCount > 0 {
		s.emitStreamingUpdate(time.Now(), false)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		slog.Debug("stream scanner error", "error", err, "net", s.net, "transport", s.transport)
	}
}

func (s *ollamaStream) emitEvalMetrics(resp ollamaResponse) {
	m := EvalMetrics{
		Model:              resp.Model,
		EvalCount:          resp.EvalCount,
		EvalDuration:       time.Duration(resp.EvalDuration) * time.Nanosecond,
		PromptEvalCount:    resp.PromptEvalCount,
		PromptEvalDuration: time.Duration(resp.PromptEvalDuration) * time.Nanosecond,
		TotalDuration:      time.Duration(resp.TotalDuration) * time.Nanosecond,
		Timestamp:          time.Now(),
	}

	slog.Info("captured eval metrics",
		"model", m.Model,
		"eval_count", m.EvalCount,
		"tok_per_sec", fmt.Sprintf("%.1f", m.TokPerSec()),
		"prompt_eval_count", m.PromptEvalCount,
		"prompt_tok_per_sec", fmt.Sprintf("%.1f", m.PromptTokPerSec()),
	)

	select {
	case s.evalMetrics <- m:
	default:
		slog.Warn("eval metrics channel full, dropping", "model", m.Model)
	}
}

func (s *ollamaStream) emitStreamingUpdate(now time.Time, active bool) {
	s.lastEmit = now

	// Prune old tokens outside rolling window
	cutoff := now.Add(-rollingWindow)
	trimIdx := 0
	for trimIdx < len(s.recentToks) && s.recentToks[trimIdx].Before(cutoff) {
		trimIdx++
	}
	if trimIdx > 0 {
		s.recentToks = s.recentToks[trimIdx:]
	}

	// Compute rolling tok/s from tokens in the window
	var liveTPS float64
	if len(s.recentToks) >= 2 {
		windowDur := s.recentToks[len(s.recentToks)-1].Sub(s.recentToks[0])
		if windowDur > 0 {
			liveTPS = float64(len(s.recentToks)) / windowDur.Seconds()
		}
	}

	var ttft time.Duration
	if !s.firstToken.IsZero() {
		ttft = s.firstToken.Sub(s.firstChunk)
	}

	sm := StreamingMetrics{
		Model:         s.model,
		TokenCount:    s.tokenCount,
		LiveTokPerSec: liveTPS,
		TTFT:          ttft,
		Active:        active,
		Timestamp:     now,
	}

	slog.Debug("streaming update",
		"model", sm.Model,
		"tokens", sm.TokenCount,
		"live_tps", fmt.Sprintf("%.1f", sm.LiveTokPerSec),
		"ttft", sm.TTFT,
		"active", sm.Active,
	)

	select {
	case s.streamingMetrics <- sm:
	default:
		slog.Debug("streaming metrics channel full, dropping", "model", s.model)
	}
}

func (s *ollamaStream) resetStreamState() {
	s.model = ""
	s.firstChunk = time.Time{}
	s.firstToken = time.Time{}
	s.tokenCount = 0
	s.recentToks = s.recentToks[:0]
	s.lastEmit = time.Time{}
}

// Start begins packet capture and TCP reassembly. It blocks until ctx is
// cancelled or an unrecoverable error occurs. Requires root privileges.
func (p *PcapBackend) Start(ctx context.Context, metrics chan<- EvalMetrics, streaming chan<- StreamingMetrics) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("pcap capture requires root privileges (run with sudo)")
	}

	handle, err := pcap.OpenLive(p.iface, 65535, false, pcap.BlockForever)
	if err != nil {
		return fmt.Errorf("failed to open pcap handle on %s: %w", p.iface, err)
	}

	filter := fmt.Sprintf("tcp port %d", p.port)
	if err := handle.SetBPFFilter(filter); err != nil {
		handle.Close()
		return fmt.Errorf("failed to set BPF filter %q: %w", filter, err)
	}

	slog.Info("pcap capture started", "interface", p.iface, "port", p.port, "filter", filter)

	factory := &ollamaStreamFactory{
		evalMetrics:      metrics,
		streamingMetrics: streaming,
	}
	pool := tcpassembly.NewStreamPool(factory)
	assembler := tcpassembly.NewAssembler(pool)

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())
	packets := packetSource.Packets()

	// Ticker for flushing old streams that haven't seen traffic.
	flushTicker := time.NewTicker(30 * time.Second)
	defer flushTicker.Stop()

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case packet, ok := <-packets:
				if !ok {
					return
				}
				tcp, ok := packet.TransportLayer().(*layers.TCP)
				if !ok {
					continue
				}
				assembler.AssembleWithTimestamp(
					packet.NetworkLayer().NetworkFlow(),
					tcp,
					packet.Metadata().Timestamp,
				)
			case <-flushTicker.C:
				assembler.FlushOlderThan(time.Now().Add(-1 * time.Minute))
			}
		}
	}()

	// Wait for context cancellation.
	<-ctx.Done()
	slog.Info("pcap capture shutting down")

	// Close the handle to unblock the packet source.
	handle.Close()

	// Wait for the packet processing goroutine to finish.
	wg.Wait()

	// Final flush.
	assembler.FlushAll()

	slog.Info("pcap capture stopped")
	return nil
}

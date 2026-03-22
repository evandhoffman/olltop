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
	EvalCount          int64  `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
	PromptEvalCount    int64  `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	TotalDuration      int64  `json:"total_duration"`
}

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
	metrics chan<- EvalMetrics
}

// ollamaStream processes a single reassembled TCP stream looking for Ollama JSON.
type ollamaStream struct {
	net, transport gopacket.Flow
	reader         tcpreader.ReaderStream
	metrics        chan<- EvalMetrics
}

func (f *ollamaStreamFactory) New(net, transport gopacket.Flow) tcpassembly.Stream {
	s := &ollamaStream{
		net:       net,
		transport: transport,
		reader:    tcpreader.NewReaderStream(),
		metrics:   f.metrics,
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

		// Fast path: skip lines that can't be the final done chunk.
		if !strings.Contains(line, `"done":true`) && !strings.Contains(line, `"done": true`) {
			continue
		}

		var resp ollamaResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Could be an HTTP header line or partial data; skip.
			continue
		}

		if !resp.Done {
			continue
		}

		if resp.Model == "" {
			slog.Debug("skipping done chunk with empty model name")
			continue
		}

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

		// Non-blocking send; if the consumer is slow, drop the metric.
		select {
		case s.metrics <- m:
		default:
			slog.Warn("metrics channel full, dropping eval metrics", "model", m.Model)
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		slog.Debug("stream scanner error", "error", err, "net", s.net, "transport", s.transport)
	}
}

// Start begins packet capture and TCP reassembly. It blocks until ctx is
// cancelled or an unrecoverable error occurs. Requires root privileges.
func (p *PcapBackend) Start(ctx context.Context, metrics chan<- EvalMetrics) error {
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

	factory := &ollamaStreamFactory{metrics: metrics}
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

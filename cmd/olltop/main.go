package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/evandhoffman/olltop/internal/capture"
	"github.com/evandhoffman/olltop/internal/metrics"
	"github.com/evandhoffman/olltop/internal/ollama"
	"github.com/evandhoffman/olltop/internal/tui"
)

var version = "dev"

func main() {
	fs := flag.NewFlagSet("olltop", flag.ExitOnError)
	host := fs.String("host", "", "Ollama host URL (default: $OLLAMA_HOST or http://localhost:11434)")
	debug := fs.Bool("debug", false, "Enable debug logging")
	showVersion := fs.Bool("version", false, "Print version and exit")
	fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("olltop %s\n", version)
		os.Exit(0)
	}

	// Configure logging — write to a file in debug mode so it doesn't
	// interfere with the TUI. In non-debug mode, discard logs.
	var logLevel slog.Level
	if *debug {
		logLevel = slog.LevelDebug
		f, err := os.OpenFile("olltop.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		logger := slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: logLevel}))
		slog.SetDefault(logger)
	} else {
		// Discard logs in normal mode — TUI owns the terminal
		logLevel = slog.LevelError
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
		slog.SetDefault(logger)
	}

	// Resolve host: flag > env > default
	ollamaHost := "http://localhost:11434"
	if envHost := os.Getenv("OLLAMA_HOST"); envHost != "" {
		ollamaHost = envHost
	}
	if *host != "" {
		ollamaHost = *host
	}

	slog.Info("olltop starting", "version", version, "host", ollamaHost)

	// Extract port from host URL for pcap filter
	port := 11434
	if u, err := url.Parse(ollamaHost); err == nil && u.Port() != "" {
		if p, err := strconv.Atoi(u.Port()); err == nil {
			port = p
		}
	}

	// Context for clean shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("received shutdown signal")
		cancel()
	}()

	// Channels
	ollamaCh := make(chan ollama.Snapshot, 8)
	captureCh := make(chan capture.EvalMetrics, 64)
	displayCh := make(chan metrics.DisplaySnapshot, 4)

	// Detect privileges
	hasCapture := os.Geteuid() == 0

	// Start Ollama API poller
	client := ollama.NewClient(ollamaHost)
	go client.Poll(ctx, 1*time.Second, ollamaCh)

	// Start pcap capture if privileged
	if hasCapture {
		backend := capture.NewPcapBackend(port)
		go func() {
			if err := backend.Start(ctx, captureCh); err != nil {
				slog.Error("pcap capture failed", "error", err)
			}
		}()
	} else {
		slog.Info("running without root — tok/s capture disabled")
	}

	// Start metrics aggregator
	agg := metrics.NewAggregator(hasCapture)
	var aggCaptureCh <-chan capture.EvalMetrics
	if hasCapture {
		aggCaptureCh = captureCh
	}
	go func() {
		if err := agg.Run(ctx, ollamaCh, aggCaptureCh, displayCh); err != nil {
			slog.Error("aggregator error", "error", err)
		}
	}()

	// Launch TUI
	model := tui.NewModel(ollamaHost)
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Forward display snapshots to the TUI
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case snap, ok := <-displayCh:
				if !ok {
					return
				}
				p.Send(tui.SnapshotMsg{Snapshot: snap})
			}
		}
	}()

	// Run TUI (blocks until quit)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		cancel()
		os.Exit(1)
	}

	cancel()
}

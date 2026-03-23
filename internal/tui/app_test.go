package tui

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/evandhoffman/olltop/internal/metrics"
)

func TestTruncatePreservesUTF8(t *testing.T) {
	got := truncate("café-世界", 7)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate returned invalid UTF-8: %q", got)
	}
	if lipgloss.Width(got) > 7 {
		t.Fatalf("truncate width = %d, want <= 7", lipgloss.Width(got))
	}
}

func TestModelUpdateStateTransitions(t *testing.T) {
	m := NewModel("http://localhost:11434")

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("window size update should not return a command")
	}
	if m.width != 100 || m.height != 40 {
		t.Fatalf("window size not applied: %+v", m)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = next.(Model)
	if !m.showHelp {
		t.Fatal("expected help overlay to toggle on")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.showHelp {
		t.Fatal("expected help overlay to close on esc")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = next.(Model)
	if m.sortMode != SortName {
		t.Fatalf("sortMode = %v, want SortName", m.sortMode)
	}

	next, _ = m.Update(tickMsg{})
	m = next.(Model)
	if m.tick != 1 {
		t.Fatalf("tick = %d, want 1", m.tick)
	}
}

func TestViewConnectedAndDisconnected(t *testing.T) {
	connected := NewModel("http://localhost:11434")
	connected.snapshot = metrics.DisplaySnapshot{
		Connected:  true,
		Version:    "0.6.2",
		HasCapture: true,
		Models: []metrics.ModelDisplay{{
			Name:             "deepseek-r1:8b",
			Size:             23300000000,
			SizeVRAM:         23300000000,
			Status:           "running",
			CurrentTokPerSec: 18.4,
			TTFT:             340 * time.Millisecond,
			ExpiresIn:        5 * time.Minute,
		}},
		TokPerSec: metrics.ThroughputInfo{
			CurrentTokPerSec: 18.4,
			CurrentPromptTPS: 4.2,
			TokPerSecHistory: []float64{0, 1, 2, 3, 4},
			PromptTPSHistory: []float64{0, 1, 1, 2, 2},
			MaxTokPerSec:     4,
			MaxPromptTPS:     2,
			WindowStart:      time.Now(),
			ActiveBuckets:    2,
		},
		SystemInfo: metrics.SystemInfo{
			CPUPercent:    42,
			GPUAvail:      true,
			GPUPercent:    67,
			MemUsed:       64 * 1024 * 1024 * 1024,
			MemTotal:      128 * 1024 * 1024 * 1024,
			MemPercent:    50,
			SensorsAvail:  true,
			CPUTemp:       73,
			GPUTemp:       68,
			FanSpeeds:     []float64{2100},
			CPUHistory:    []float64{65, 66, 68, 69, 70, 71, 72, 73},
			GPUHistory:    []float64{60, 61, 62, 64, 65, 66, 67, 68},
			FanHistory:    []float64{1500, 1600, 1700, 1800, 1900, 2000, 2050, 2100},
			ActiveBuckets: 8,
		},
	}

	out := connected.View()
	for _, want := range []string{"Loaded Models", "Throughput", "System", "TTFT", "running", "CPU", "GPU", "RAM", "Fan", "tok/s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("connected view missing %q\n%s", want, out)
		}
	}
	if !strings.ContainsAny(out, "▁▂▃▄▅▆▇█") {
		t.Fatalf("connected view missing sparkline blocks\n%s", out)
	}
	var positions []int
	for _, line := range strings.Split(out, "\n") {
		clean := stripAnsi(line)
		if strings.Contains(clean, "CPU Temp") || strings.Contains(clean, "GPU Temp") || strings.Contains(clean, "Fan") {
			pos := strings.IndexAny(clean, "▁▂▃▄▅▆▇█")
			if pos < 0 {
				t.Fatalf("missing sparkline in aligned system row: %q", clean)
			}
			positions = append(positions, pos)
		}
	}
	if len(positions) != 3 {
		t.Fatalf("expected 3 aligned spark rows, got %d", len(positions))
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] != positions[0] {
			t.Fatalf("sparkline columns not aligned: %v", positions)
		}
	}

	disconnected := NewModel("http://localhost:11434")
	disconnected.snapshot = metrics.DisplaySnapshot{Connected: false}
	disconnected.lastConnected = connected.snapshot
	disconnected.disconnectedAt = time.Now().Add(-2 * time.Second)

	out = disconnected.View()
	for _, want := range []string{"Cannot connect to Ollama", "Loaded Models", "System", "retrying"} {
		if !strings.Contains(out, want) {
			t.Fatalf("disconnected view missing %q\n%s", want, out)
		}
	}
}

func TestRenderHelpers(t *testing.T) {
	m := NewModel("http://localhost:11434")
	m.snapshot.Version = "0.6.2"

	header := m.renderHeader(100)
	if !strings.Contains(header, "olltop") || !strings.Contains(header, "0.6.2") {
		t.Fatalf("unexpected header: %q", header)
	}

	section := m.renderSectionBorder(100, "Loaded Models")
	if !strings.Contains(section, "Loaded Models") {
		t.Fatalf("unexpected section border: %q", section)
	}

	line := m.renderBorderedLine(20, "hello")
	if !strings.Contains(line, "hello") {
		t.Fatalf("unexpected bordered line: %q", line)
	}

	spark := buildSparkline([]float64{1, 2, 3, 4}, 4, 2)
	if !strings.Contains(spark, "··") || !strings.Contains(spark, "▆█") {
		t.Fatalf("unexpected sparkline: %q", spark)
	}

	row := renderSparkRow("tok/s", []float64{1, 2, 3}, 3.5, 4.2, "3:04 PM", 2, 5)
	for _, want := range []string{"tok/s", "max", "since", "tok/s"} {
		if !strings.Contains(row, want) {
			t.Fatalf("unexpected spark row: %q", row)
		}
	}
}

func TestAdditionalRenderingBranches(t *testing.T) {
	m := NewModel("http://localhost:11434")

	if got := m.renderDisconnectedBanner(60); !strings.Contains(got, "Cannot connect to Ollama") {
		t.Fatalf("unexpected disconnected banner: %q", got)
	}

	if got := m.renderThroughput(60); !strings.Contains(got, "requires root") {
		t.Fatalf("unexpected throughput warning: %q", got)
	}

	if got := renderSparkRow("tok/s", nil, 0, 0, "", 0, 5); !strings.Contains(got, "0.0 tok/s") {
		t.Fatalf("unexpected empty spark row: %q", got)
	}

	if got := buildSparkline([]float64{}, 5, 10); len(got) == 0 {
		t.Fatal("expected sparkline output")
	}

	if got := m.fanSpinner(0); got != "·" {
		t.Fatalf("fanSpinner(0) = %q, want dot", got)
	}
	if got := m.fanSpinner(500); got == "·" {
		t.Fatalf("fanSpinner(500) should animate, got %q", got)
	}
	if got := m.renderFanCell(metrics.SystemInfo{SensorsAvail: true, FanSpeeds: []float64{0}}); !strings.Contains(got, "idle") {
		t.Fatalf("expected idle fan display, got %q", got)
	}

	if got := tempStyle(95, 70, 90).Render("95°C"); got == "" {
		t.Fatal("expected temperature style output")
	}
	if got := fanStyle(6000).Render("6000 RPM"); got == "" {
		t.Fatal("expected fan style output")
	}

	help := m.overlayHelp(strings.Repeat("base\n", 20), 80, 20)
	if !strings.Contains(help, "Keyboard Shortcuts") || !strings.Contains(help, "cycle sort") {
		t.Fatalf("unexpected help overlay: %q", help)
	}
}

func TestModelTableSortBranches(t *testing.T) {
	m := NewModel("http://localhost:11434")
	m.snapshot.Models = []metrics.ModelDisplay{
		{Name: "b", SizeVRAM: 2, CurrentTokPerSec: 1, Status: "idle"},
		{Name: "a", SizeVRAM: 3, CurrentTokPerSec: 2, Status: "running"},
		{Name: "c", SizeVRAM: 1, CurrentTokPerSec: 3, Status: "thinking"},
	}

	cases := []SortMode{SortDefault, SortName, SortTokSec, SortVRAM, SortStatus}
	for _, sortMode := range cases {
		m.sortMode = sortMode
		out := m.renderModelsTable(120)
		if !strings.Contains(out, "MODEL") {
			t.Fatalf("renderModelsTable missing header for sort %v: %q", sortMode, out)
		}
	}

	m.snapshot.Models = nil
	if out := m.renderModelsTable(120); !strings.Contains(out, "No models loaded") {
		t.Fatalf("unexpected empty table output: %q", out)
	}
}

func TestRenderModelsTableFitsNarrowWidth(t *testing.T) {
	m := NewModel("http://localhost:11434")
	m.snapshot.Models = []metrics.ModelDisplay{{
		Name:      "qwen3.5:latest",
		Size:      18_700_000_000,
		SizeVRAM:  18_700_000_000,
		ExpiresIn: 4*time.Minute + 4*time.Second,
		Status:    "idle",
	}}

	inner := 84
	out := m.renderModelsTable(inner)
	for _, line := range strings.Split(out, "\n") {
		clean := stripAnsi(line)
		if clean == "" {
			continue
		}
		if got := lipgloss.Width(clean); got > inner+4 {
			t.Fatalf("line width = %d, want <= %d\n%s", got, inner+4, clean)
		}
	}
}

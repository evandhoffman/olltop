package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/evandhoffman/olltop/internal/metrics"
)

// SnapshotMsg wraps a DisplaySnapshot for delivery as a tea.Msg.
type SnapshotMsg struct {
	Snapshot metrics.DisplaySnapshot
}

// sparkBlocks are the Unicode block elements used for sparklines (lowest to highest).
var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	borderColor  = lipgloss.Color("240")
	headerColor  = lipgloss.Color("75")  // soft blue
	accentColor  = lipgloss.Color("114") // soft green
	warnColor    = lipgloss.Color("214") // amber
	dimColor     = lipgloss.Color("243")
	runningColor = lipgloss.Color("114") // green
	idleColor    = lipgloss.Color("243") // grey

	headerStyle = lipgloss.NewStyle().
			Foreground(headerColor).
			Bold(true)

	sectionTitleStyle = lipgloss.NewStyle().
				Foreground(headerColor).
				Bold(true)

	modelNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255"))

	tokSecStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(dimColor)

	warnStyle = lipgloss.NewStyle().
			Foreground(warnColor)

	barFilledStyle = lipgloss.NewStyle().
			Foreground(accentColor)

	barEmptyStyle = lipgloss.NewStyle().
			Foreground(dimColor)

	sparkStyle = lipgloss.NewStyle().
			Foreground(accentColor)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)
)

// ── Model ───────────────────────────────────────────────────────────────────

// Model is the bubbletea model for olltop's TUI.
type Model struct {
	snapshot metrics.DisplaySnapshot
	host     string
	width    int
	height   int
	quitting bool
}

// NewModel creates a new TUI model bound to the given Ollama host.
func NewModel(host string) Model {
	return Model{
		host:  host,
		width: 80,
	}
}

// Init satisfies tea.Model. We return nil because snapshots arrive externally.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update satisfies tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case SnapshotMsg:
		m.snapshot = msg.Snapshot
	}

	return m, nil
}

// View satisfies tea.Model and renders the entire TUI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	w := m.width
	if w < 40 {
		w = 40
	}

	inner := w - 4 // content width inside borders

	var b strings.Builder

	// ── Top border + header ─────────────────────────────────────────────
	b.WriteString(m.renderHeader(w))
	b.WriteByte('\n')

	if !m.snapshot.Connected {
		b.WriteString(m.renderDisconnected(w, inner))
		b.WriteString(m.renderBottomBorder(w))
		return b.String()
	}

	// ── Models ──────────────────────────────────────────────────────────
	b.WriteString(m.renderSectionBorder(w, "Loaded Models"))
	b.WriteByte('\n')
	b.WriteString(m.renderModelsTable(inner))

	// ── Throughput ──────────────────────────────────────────────────────
	b.WriteString(m.renderSectionBorder(w, "Throughput"))
	b.WriteByte('\n')
	b.WriteString(m.renderThroughput(inner))

	// ── System ──────────────────────────────────────────────────────────
	b.WriteString(m.renderSectionBorder(w, "System"))
	b.WriteByte('\n')
	b.WriteString(m.renderSystem(inner))

	// ── Bottom border ───────────────────────────────────────────────────
	b.WriteString(m.renderBottomBorder(w))

	return b.String()
}

// ── Rendering helpers ───────────────────────────────────────────────────────

func (m Model) renderHeader(w int) string {
	left := headerStyle.Render(" olltop")
	version := m.snapshot.Version
	if version == "" {
		version = "v0.x.x"
	}
	right := dimStyle.Render(fmt.Sprintf("%s  %s  q to quit ", m.host, version))

	leftLen := lipgloss.Width(left)
	rightLen := lipgloss.Width(right)

	dashCount := w - 2 - leftLen - rightLen - 4
	if dashCount < 1 {
		dashCount = 1
	}

	var b strings.Builder
	styled := lipgloss.NewStyle().Foreground(borderColor)
	b.WriteString(styled.Render("┌─"))
	b.WriteString(left)
	b.WriteString(styled.Render(" " + strings.Repeat("─", dashCount) + " "))
	b.WriteString(right)
	b.WriteString(styled.Render("─┐"))
	return b.String()
}

func (m Model) renderSectionBorder(w int, title string) string {
	styled := lipgloss.NewStyle().Foreground(borderColor)
	rendered := sectionTitleStyle.Render(title)
	titleLen := lipgloss.Width(rendered)
	dashCount := w - 2 - titleLen - 4
	if dashCount < 1 {
		dashCount = 1
	}
	return styled.Render("├─ ") + rendered + styled.Render(" "+strings.Repeat("─", dashCount)+"┤")
}

func (m Model) renderBottomBorder(w int) string {
	styled := lipgloss.NewStyle().Foreground(borderColor)
	return styled.Render("└" + strings.Repeat("─", w-2) + "┘")
}

func (m Model) renderBorderedLine(inner int, content string) string {
	styled := lipgloss.NewStyle().Foreground(borderColor)
	contentLen := lipgloss.Width(content)
	pad := inner - contentLen
	if pad < 0 {
		pad = 0
	}
	return styled.Render("│") + " " + content + strings.Repeat(" ", pad) + " " + styled.Render("│")
}

func (m Model) renderDisconnected(w, inner int) string {
	var b strings.Builder
	msg := errorStyle.Render(fmt.Sprintf("Cannot connect to Ollama at %s", m.host))
	b.WriteString(m.renderBorderedLine(inner, ""))
	b.WriteByte('\n')
	b.WriteString(m.renderBorderedLine(inner, msg))
	b.WriteByte('\n')
	b.WriteString(m.renderBorderedLine(inner, ""))
	b.WriteByte('\n')
	return b.String()
}

func (m Model) renderModelsTable(inner int) string {
	var b strings.Builder

	const (
		colModel   = 22
		colSize    = 12
		colVRAM    = 12
		colTokSec  = 10
		colStatus  = 10
		colExpires = 10
	)

	hdr := fmt.Sprintf(" %-*s %-*s %-*s %-*s %-*s %-*s",
		colModel, "MODEL",
		colSize, "SIZE",
		colVRAM, "VRAM",
		colTokSec, "tok/s",
		colStatus, "STATUS",
		colExpires, "EXPIRES")
	b.WriteString(m.renderBorderedLine(inner, dimStyle.Render(hdr)))
	b.WriteByte('\n')

	if len(m.snapshot.Models) == 0 {
		b.WriteString(m.renderBorderedLine(inner, dimStyle.Render(" No models loaded")))
		b.WriteByte('\n')
		b.WriteString(m.renderBorderedLine(inner, ""))
		b.WriteByte('\n')
		return b.String()
	}

	for _, mdl := range m.snapshot.Models {
		name := truncate(mdl.Name, colModel)
		size := formatBytes(mdl.Size)
		vram := formatBytes(mdl.SizeVRAM)

		var tps string
		if mdl.CurrentTokPerSec > 0 {
			tps = tokSecStyle.Render(fmt.Sprintf("%.1f", mdl.CurrentTokPerSec))
		} else {
			tps = dimStyle.Render("\u2014")
		}

		var status string
		if mdl.Status == "running" {
			status = lipgloss.NewStyle().Foreground(runningColor).Render("running")
		} else {
			status = lipgloss.NewStyle().Foreground(idleColor).Render("idle")
		}

		expires := formatDuration(mdl.ExpiresIn)

		row := fmt.Sprintf(" %-*s %-*s %-*s",
			colModel, modelNameStyle.Render(name),
			colSize, size,
			colVRAM, vram)

		tpsVis := lipgloss.Width(tps)
		statusVis := lipgloss.Width(status)

		row += " " + tps + strings.Repeat(" ", max(0, colTokSec-1-tpsVis))
		row += " " + status + strings.Repeat(" ", max(0, colStatus-1-statusVis))
		row += " " + expires

		b.WriteString(m.renderBorderedLine(inner, row))
		b.WriteByte('\n')
	}

	b.WriteString(m.renderBorderedLine(inner, ""))
	b.WriteByte('\n')
	return b.String()
}

func (m Model) renderThroughput(inner int) string {
	var b strings.Builder

	if !m.snapshot.HasCapture {
		msg := warnStyle.Render(" \u26a0 tok/s monitoring requires root: sudo olltop")
		b.WriteString(m.renderBorderedLine(inner, msg))
		b.WriteByte('\n')
		return b.String()
	}

	tokLine := renderSparkRow("tok/s  ", m.snapshot.TokPerSec.TokPerSecHistory, m.snapshot.TokPerSec.CurrentTokPerSec)
	b.WriteString(m.renderBorderedLine(inner, tokLine))
	b.WriteByte('\n')

	promptLine := renderSparkRow("prompt ", m.snapshot.TokPerSec.PromptTPSHistory, m.snapshot.TokPerSec.CurrentPromptTPS)
	b.WriteString(m.renderBorderedLine(inner, promptLine))
	b.WriteByte('\n')

	b.WriteString(m.renderBorderedLine(inner, ""))
	b.WriteByte('\n')
	return b.String()
}

func renderSparkRow(label string, history []float64, current float64) string {
	spark := buildSparkline(history, 20)
	var val string
	if current > 0 {
		val = tokSecStyle.Render(fmt.Sprintf("%.1f tok/s", current))
	} else {
		val = dimStyle.Render("\u2014")
	}
	return " " + dimStyle.Render(label) + " " + sparkStyle.Render(spark) + "   " + val
}

func buildSparkline(data []float64, width int) string {
	if len(data) == 0 {
		return strings.Repeat(string(sparkBlocks[0]), width)
	}

	start := 0
	if len(data) > width {
		start = len(data) - width
	}
	visible := data[start:]

	maxVal := 0.0
	for _, v := range visible {
		if v > maxVal {
			maxVal = v
		}
	}

	var sb strings.Builder
	for i := 0; i < width-len(visible); i++ {
		sb.WriteRune(sparkBlocks[0])
	}
	for _, v := range visible {
		idx := 0
		if maxVal > 0 {
			idx = int(math.Round(v / maxVal * float64(len(sparkBlocks)-1)))
			if idx >= len(sparkBlocks) {
				idx = len(sparkBlocks) - 1
			}
		}
		sb.WriteRune(sparkBlocks[idx])
	}
	return sb.String()
}

func (m Model) renderSystem(inner int) string {
	sys := m.snapshot.SystemInfo
	cpuBar := renderBar(sys.CPUPercent, 10)
	cpuPct := fmt.Sprintf("%.0f%%", sys.CPUPercent)

	memUsed := formatBytesUint64(sys.MemUsed)
	memTotal := formatBytesUint64(sys.MemTotal)
	memPct := fmt.Sprintf("%.0f%%", sys.MemPercent)

	line := fmt.Sprintf(" CPU  %s  %-6s     RAM  %s / %s  (%s)",
		cpuBar, cpuPct, memUsed, memTotal, memPct)

	var b strings.Builder
	b.WriteString(m.renderBorderedLine(inner, line))
	b.WriteByte('\n')
	return b.String()
}

func renderBar(pct float64, width int) string {
	filled := int(math.Round(pct / 100 * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return barFilledStyle.Render(strings.Repeat("\u2588", filled)) +
		barEmptyStyle.Render(strings.Repeat("\u2591", width-filled))
}

// ── Formatting utilities ────────────────────────────────────────────────────

func formatBytes(b int64) string {
	return formatBytesUint64(uint64(b))
}

func formatBytesUint64(b uint64) string {
	const gb = 1024 * 1024 * 1024
	const mb = 1024 * 1024
	if b >= gb {
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	}
	return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "\u2014"
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

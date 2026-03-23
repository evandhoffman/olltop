package tui

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/evandhoffman/olltop/internal/metrics"
)

// SortMode controls the model table sort order.
type SortMode int

const (
	SortDefault SortMode = iota // API order
	SortName                    // alphabetical
	SortTokSec                  // highest tok/s first
	SortVRAM                    // largest VRAM first
	SortStatus                  // running first, then idle
	sortModeCount               // sentinel for cycling
)

func (s SortMode) String() string {
	switch s {
	case SortName:
		return "name"
	case SortTokSec:
		return "tok/s"
	case SortVRAM:
		return "VRAM"
	case SortStatus:
		return "status"
	default:
		return "default"
	}
}

// SnapshotMsg wraps a DisplaySnapshot for delivery as a tea.Msg.
type SnapshotMsg struct {
	Snapshot metrics.DisplaySnapshot
}

// sparkBlocks are the Unicode block elements used for sparklines (lowest to highest).
var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// fanFrames are the animation frames for the spinning fan indicator.
var fanFrames = []string{"◜", "◝", "◞", "◟"}

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

// tickMsg is sent on each animation tick for fan spinner.
type tickMsg struct{}

// Model is the bubbletea model for olltop's TUI.
type Model struct {
	snapshot metrics.DisplaySnapshot
	host     string
	width    int
	height   int
	quitting bool
	tick     int // animation frame counter
	showHelp bool
	sortMode SortMode
}

// NewModel creates a new TUI model bound to the given Ollama host.
func NewModel(host string) Model {
	return Model{
		host:  host,
		width: 80,
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

// Init satisfies tea.Model. Start the animation ticker.
func (m Model) Init() tea.Cmd {
	return tickCmd()
}

// Update satisfies tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "?":
			m.showHelp = !m.showHelp
		case "esc":
			m.showHelp = false
		case "s":
			if !m.showHelp {
				m.sortMode = (m.sortMode + 1) % sortModeCount
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case SnapshotMsg:
		m.snapshot = msg.Snapshot

	case tickMsg:
		m.tick++
		return m, tickCmd()
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

	if m.showHelp {
		return m.overlayHelp(b.String(), w, m.height)
	}

	return b.String()
}

// ── Rendering helpers ───────────────────────────────────────────────────────

func (m Model) renderHeader(w int) string {
	left := headerStyle.Render(" olltop")
	version := m.snapshot.Version
	if version == "" {
		version = "v0.x.x"
	}
	right := dimStyle.Render(fmt.Sprintf("%s  %s  ? help ", m.host, version))

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

	// Column headers — highlight the active sort column
	modelHdr, sizeHdr, vramHdr, tokHdr, statusHdr, expiresHdr := "MODEL", "SIZE", "VRAM", "tok/s", "STATUS", "EXPIRES"
	switch m.sortMode {
	case SortName:
		modelHdr = "MODEL ▼"
	case SortTokSec:
		tokHdr = "tok/s ▼"
	case SortVRAM:
		vramHdr = "VRAM ▼"
	case SortStatus:
		statusHdr = "STATUS ▼"
	}

	hdr := fmt.Sprintf(" %-*s %-*s %-*s %-*s %-*s %-*s",
		colModel, modelHdr,
		colSize, sizeHdr,
		colVRAM, vramHdr,
		colTokSec, tokHdr,
		colStatus, statusHdr,
		colExpires, expiresHdr)
	b.WriteString(m.renderBorderedLine(inner, dimStyle.Render(hdr)))
	b.WriteByte('\n')

	if len(m.snapshot.Models) == 0 {
		b.WriteString(m.renderBorderedLine(inner, dimStyle.Render(" No models loaded")))
		b.WriteByte('\n')
		b.WriteString(m.renderBorderedLine(inner, ""))
		b.WriteByte('\n')
		return b.String()
	}

	models := make([]metrics.ModelDisplay, len(m.snapshot.Models))
	copy(models, m.snapshot.Models)
	switch m.sortMode {
	case SortName:
		sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	case SortTokSec:
		sort.Slice(models, func(i, j int) bool {
			tpsI := models[i].LiveTokPerSec
			if tpsI == 0 {
				tpsI = models[i].CurrentTokPerSec
			}
			tpsJ := models[j].LiveTokPerSec
			if tpsJ == 0 {
				tpsJ = models[j].CurrentTokPerSec
			}
			return tpsI > tpsJ
		})
	case SortVRAM:
		sort.Slice(models, func(i, j int) bool { return models[i].SizeVRAM > models[j].SizeVRAM })
	case SortStatus:
		sort.Slice(models, func(i, j int) bool {
			if models[i].Status == models[j].Status {
				return models[i].Name < models[j].Name
			}
			return models[i].Status == "running"
		})
	}

	for _, mdl := range models {
		name := modelNameStyle.Render(truncate(mdl.Name, colModel))
		size := formatBytes(mdl.Size)
		vram := formatBytes(mdl.SizeVRAM)

		// Prefer live streaming tok/s over final eval tok/s
		var tps string
		effectiveTPS := mdl.LiveTokPerSec
		if effectiveTPS == 0 {
			effectiveTPS = mdl.CurrentTokPerSec
		}
		if effectiveTPS > 0 {
			tps = tokSecStyle.Render(fmt.Sprintf("%.1f", effectiveTPS))
		} else {
			tps = dimStyle.Render("\u2014")
		}

		// Status with active request count
		var status string
		if mdl.Status == "running" {
			runStyle := lipgloss.NewStyle().Foreground(runningColor)
			if mdl.ActiveRequests > 1 {
				status = runStyle.Render(fmt.Sprintf("run(%d)", mdl.ActiveRequests))
			} else {
				status = runStyle.Render("running")
			}
		} else {
			status = lipgloss.NewStyle().Foreground(idleColor).Render("idle")
		}

		expires := formatDuration(mdl.ExpiresIn)

		row := " " + padRight(name, colModel) +
			" " + padRight(size, colSize) +
			" " + padRight(vram, colVRAM) +
			" " + padRight(tps, colTokSec) +
			" " + padRight(status, colStatus) +
			" " + expires

		// TTFT on a second line if available and model is active
		b.WriteString(m.renderBorderedLine(inner, row))
		b.WriteByte('\n')
		if mdl.TTFT > 0 && mdl.Status == "running" {
			ttftStr := formatTTFT(mdl.TTFT)
			detail := " " + strings.Repeat(" ", colModel+1) +
				dimStyle.Render("TTFT ") + tokSecStyle.Render(ttftStr)
			b.WriteString(m.renderBorderedLine(inner, detail))
			b.WriteByte('\n')
		}
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

	tp := m.snapshot.TokPerSec
	sinceStr := ""
	if !tp.WindowStart.IsZero() {
		sinceStr = tp.WindowStart.Local().Format("3:04 PM")
	}

	sparkWidth := 20
	tokLine := renderSparkRow("tok/s  ", tp.TokPerSecHistory, tp.CurrentTokPerSec, tp.MaxTokPerSec, sinceStr, tp.ActiveBuckets, sparkWidth)
	b.WriteString(m.renderBorderedLine(inner, tokLine))
	b.WriteByte('\n')

	promptLine := renderSparkRow("prompt ", tp.PromptTPSHistory, tp.CurrentPromptTPS, tp.MaxPromptTPS, sinceStr, tp.ActiveBuckets, sparkWidth)
	b.WriteString(m.renderBorderedLine(inner, promptLine))
	b.WriteByte('\n')

	b.WriteString(m.renderBorderedLine(inner, ""))
	b.WriteByte('\n')
	return b.String()
}

func renderSparkRow(label string, history []float64, current, maxVal float64, since string, activeBuckets, sparkWidth int) string {
	spark := buildSparkline(history, sparkWidth, activeBuckets)
	var val string
	if current > 0 {
		val = tokSecStyle.Render(fmt.Sprintf("%.1f tok/s", current))
	} else {
		val = dimStyle.Render("0.0 tok/s")
	}

	var suffix string
	if maxVal > 0 {
		suffix = dimStyle.Render(fmt.Sprintf("  max %.1f", maxVal))
		if since != "" {
			suffix += dimStyle.Render(fmt.Sprintf("  since %s", since))
		}
	}

	return " " + dimStyle.Render(label) + " " + spark + "   " + val + suffix
}

// buildSparkline renders a sparkline with activeBuckets of real data (green)
// on the right, and dim placeholder dots on the left for pre-startup time.
func buildSparkline(data []float64, width, activeBuckets int) string {
	if activeBuckets > width {
		activeBuckets = width
	}

	// Use the last `width` entries of data (or fewer if data is shorter)
	start := 0
	if len(data) > width {
		start = len(data) - width
	}
	visible := data[start:]

	// Find max for scaling
	maxVal := 0.0
	for _, v := range visible {
		if v > maxVal {
			maxVal = v
		}
	}

	// How many leading slots are pre-startup?
	inactiveCols := width - activeBuckets

	var sb strings.Builder

	// Pre-startup placeholder (dim dots)
	if inactiveCols > 0 {
		sb.WriteString(dimStyle.Render(strings.Repeat("·", inactiveCols)))
	}

	// Active portion (green sparkline)
	activeStart := 0
	if len(visible) > activeBuckets {
		activeStart = len(visible) - activeBuckets
	}
	activeData := visible[activeStart:]

	// Pad if we have fewer data points than active columns
	padCount := activeBuckets - len(activeData)
	if padCount > 0 {
		sb.WriteString(sparkStyle.Render(strings.Repeat(string(sparkBlocks[0]), padCount)))
	}

	// Render active data
	var activePart strings.Builder
	for _, v := range activeData {
		idx := 0
		if maxVal > 0 {
			idx = int(math.Round(v / maxVal * float64(len(sparkBlocks)-1)))
			if idx >= len(sparkBlocks) {
				idx = len(sparkBlocks) - 1
			}
		}
		activePart.WriteRune(sparkBlocks[idx])
	}
	sb.WriteString(sparkStyle.Render(activePart.String()))
	return sb.String()
}

// tempStyle returns a lipgloss style colored by temperature severity.
func tempStyle(temp float64, warnThresh, critThresh float64) lipgloss.Style {
	switch {
	case temp >= critThresh:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	case temp >= warnThresh:
		return lipgloss.NewStyle().Foreground(warnColor) // amber
	default:
		return lipgloss.NewStyle().Foreground(accentColor) // green
	}
}

// fanStyle returns a lipgloss style colored by fan speed severity.
func fanStyle(rpm float64) lipgloss.Style {
	switch {
	case rpm > 5000:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // red
	case rpm > 3000:
		return lipgloss.NewStyle().Foreground(warnColor) // amber
	default:
		return lipgloss.NewStyle().Foreground(accentColor) // green
	}
}

func (m Model) renderSystem(inner int) string {
	sys := m.snapshot.SystemInfo

	// Line 1: CPU and GPU utilization with temps
	cpuBar := renderBar(sys.CPUPercent, 10)
	cpuPart := fmt.Sprintf(" CPU  %s  %-4s", cpuBar, fmt.Sprintf("%.0f%%", sys.CPUPercent))
	if sys.SensorsAvail && sys.CPUTemp > 0 {
		style := tempStyle(sys.CPUTemp, 70, 90)
		cpuPart += " " + style.Render(fmt.Sprintf("%.0f°C", sys.CPUTemp))
	}

	var gpuPart string
	if sys.GPUAvail {
		gpuBar := renderBar(sys.GPUPercent, 10)
		gpuPart = fmt.Sprintf("   GPU  %s  %-4s", gpuBar, fmt.Sprintf("%.0f%%", sys.GPUPercent))
		if sys.SensorsAvail && sys.GPUTemp > 0 {
			style := tempStyle(sys.GPUTemp, 75, 95)
			gpuPart += " " + style.Render(fmt.Sprintf("%.0f°C", sys.GPUTemp))
		}
	}

	// Line 2: RAM and fans
	memUsed := formatBytesUint64(sys.MemUsed)
	memTotal := formatBytesUint64(sys.MemTotal)
	memPct := fmt.Sprintf("%.0f%%", sys.MemPercent)
	memPart := fmt.Sprintf(" RAM  %s / %s  (%s)", memUsed, memTotal, memPct)

	var fanPart string
	if sys.SensorsAvail && len(sys.FanSpeeds) > 0 {
		// Use max fan speed for color and animation
		maxRPM := 0.0
		for _, rpm := range sys.FanSpeeds {
			if rpm > maxRPM {
				maxRPM = rpm
			}
		}
		style := fanStyle(maxRPM)
		spinner := m.fanSpinner(maxRPM)

		if len(sys.FanSpeeds) == 1 {
			fanPart = "   " + style.Render(spinner) + dimStyle.Render(" Fan ") + style.Render(fmt.Sprintf("%.0f RPM", sys.FanSpeeds[0]))
		} else {
			parts := ""
			for i, rpm := range sys.FanSpeeds {
				if i > 0 {
					parts += "/"
				}
				parts += fmt.Sprintf("%.0f", rpm)
			}
			fanPart = "   " + style.Render(spinner) + dimStyle.Render(" Fans ") + style.Render(fmt.Sprintf("%s RPM", parts))
		}
	}

	var b strings.Builder
	b.WriteString(m.renderBorderedLine(inner, cpuPart+gpuPart))
	b.WriteByte('\n')
	b.WriteString(m.renderBorderedLine(inner, memPart+fanPart))
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

func formatTTFT(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
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

// fanSpinner returns the current fan animation frame. Higher RPM = faster spin.
// At 200ms ticks: 0 RPM = static, 500 RPM = ~1 rev/sec, 3000+ RPM = every frame.
func (m Model) fanSpinner(rpm float64) string {
	if rpm < 100 {
		return "·" // essentially off
	}
	// Map RPM to speed multiplier with finer granularity:
	// 100-399 RPM → 1, 400-799 → 2, 800-1199 → 3, ... 2800+ → 8 (max)
	speed := max(1, min(8, int(rpm/400)+1))
	frame := (m.tick * speed) % len(fanFrames)
	return fanFrames[frame]
}

// overlayHelp renders the help box centered over the existing TUI output.
func (m Model) overlayHelp(base string, width, height int) string {
	helpLines := []string{
		"",
		headerStyle.Render("  Keyboard Shortcuts"),
		"",
		"  " + accentStyle.Render("?") + dimStyle.Render("       toggle this help"),
		"  " + accentStyle.Render("s") + dimStyle.Render("       cycle sort: default → name → tok/s → VRAM → status"),
		"  " + accentStyle.Render("q") + dimStyle.Render("       quit"),
		"  " + accentStyle.Render("Esc") + dimStyle.Render("     close help"),
		"",
	}

	// Find widest help line for box sizing
	boxInner := 0
	for _, l := range helpLines {
		w := lipgloss.Width(l)
		if w > boxInner {
			boxInner = w
		}
	}
	boxInner += 2 // side padding

	styled := lipgloss.NewStyle().Foreground(borderColor)

	var box strings.Builder
	box.WriteString(styled.Render("┌" + strings.Repeat("─", boxInner) + "┐"))
	box.WriteByte('\n')
	for _, l := range helpLines {
		vis := lipgloss.Width(l)
		pad := boxInner - vis
		if pad < 0 {
			pad = 0
		}
		box.WriteString(styled.Render("│") + l + strings.Repeat(" ", pad) + styled.Render("│"))
		box.WriteByte('\n')
	}
	box.WriteString(styled.Render("└" + strings.Repeat("─", boxInner) + "┘"))

	boxStr := box.String()
	boxLines := strings.Split(boxStr, "\n")

	// Overlay the help box centered on the base output
	baseLines := strings.Split(base, "\n")

	// Vertical centering
	startRow := (len(baseLines) - len(boxLines)) / 2
	if startRow < 1 {
		startRow = 1
	}

	// Horizontal centering
	boxWidth := boxInner + 2 // +2 for border chars
	startCol := (width - boxWidth) / 2
	if startCol < 0 {
		startCol = 0
	}

	for i, bline := range boxLines {
		row := startRow + i
		if row >= len(baseLines) {
			break
		}
		baseLines[row] = placeOverlay(baseLines[row], bline, startCol)
	}

	return strings.Join(baseLines, "\n")
}

// placeOverlay places overlay text at a given column position in a base line.
func placeOverlay(baseLine, overlay string, col int) string {
	// Convert to runes for proper positioning
	baseRunes := []rune(stripAnsi(baseLine))
	baseLen := len(baseRunes)

	// Build: left portion + overlay + right portion
	left := ""
	if col > 0 {
		if col > baseLen {
			left = string(baseRunes) + strings.Repeat(" ", col-baseLen)
		} else {
			left = string(baseRunes[:col])
		}
	}

	overlayWidth := lipgloss.Width(overlay)
	rightStart := col + overlayWidth
	right := ""
	if rightStart < baseLen {
		right = string(baseRunes[rightStart:])
	}

	return left + overlay + right
}

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

var accentStyle = lipgloss.NewStyle().Foreground(accentColor).Bold(true)

// padRight pads a (possibly ANSI-styled) string to the given visible width.
func padRight(s string, width int) string {
	vis := lipgloss.Width(s)
	if vis >= width {
		return s
	}
	return s + strings.Repeat(" ", width-vis)
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

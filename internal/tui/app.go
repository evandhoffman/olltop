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
	SortDefault   SortMode = iota // API order
	SortName                      // alphabetical
	SortTokSec                    // highest tok/s first
	SortVRAM                      // largest VRAM first
	SortStatus                    // running first, then idle
	sortModeCount                 // sentinel for cycling
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
	snapshot       metrics.DisplaySnapshot
	lastConnected  metrics.DisplaySnapshot // last snapshot where Connected=true
	host           string
	width          int
	height         int
	quitting       bool
	tick           int // animation frame counter
	showHelp       bool
	sortMode       SortMode
	disconnectedAt time.Time // when we first noticed disconnection
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
		if msg.Snapshot.Connected {
			m.lastConnected = msg.Snapshot
			m.disconnectedAt = time.Time{}
		} else if m.snapshot.Connected && !msg.Snapshot.Connected {
			// Just became disconnected
			m.disconnectedAt = time.Now()
		}
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

	// Warn if terminal is too small for the layout
	if m.height > 0 && (m.width < 80 || m.height < 24) {
		return warnStyle.Render(fmt.Sprintf(" Terminal too small (%dx%d). Resize to at least 80x24.", m.width, m.height))
	}

	w := m.width
	if w < 80 {
		w = 80
	}

	inner := w - 4 // content width inside borders

	var b strings.Builder

	// ── Top border + header ─────────────────────────────────────────────
	b.WriteString(m.renderHeader(w))
	b.WriteByte('\n')

	if !m.snapshot.Connected {
		// Show warning banner
		b.WriteString(m.renderDisconnectedBanner(inner))

		// If we have last-known state, show it dimmed
		if len(m.lastConnected.Models) > 0 {
			b.WriteString(m.renderSectionBorder(w, "Loaded Models"))
			b.WriteByte('\n')
			// Temporarily swap in last-known data for rendering
			saved := m.snapshot
			m.snapshot = m.lastConnected
			b.WriteString(m.renderModelsTable(inner))
			m.snapshot = saved

			b.WriteString(m.renderSectionBorder(w, "System"))
			b.WriteByte('\n')
			b.WriteString(m.renderSystem(inner))
		}

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

func (m Model) renderDisconnectedBanner(inner int) string {
	var b strings.Builder
	msg := fmt.Sprintf(" \u26a0 Cannot connect to Ollama at %s", m.host)
	if !m.disconnectedAt.IsZero() {
		elapsed := time.Since(m.disconnectedAt).Truncate(time.Second)
		msg += fmt.Sprintf(" (disconnected %s ago)", elapsed)
	}
	msg += " — retrying..."
	b.WriteString(m.renderBorderedLine(inner, warnStyle.Render(msg)))
	b.WriteByte('\n')
	return b.String()
}

func (m Model) renderModelsTable(inner int) string {
	var b strings.Builder

	const (
		colSize    = 8
		colVRAM    = 8
		colTokSec  = 7
		colTTFT    = 7
		colStatus  = 8
		colExpires = 7
	)
	// MODEL column gets remaining width after fixed columns and 7 spacing chars.
	fixedCols := colSize + colVRAM + colTokSec + colTTFT + colStatus + colExpires
	colModel := inner - 7 - fixedCols
	if colModel < 12 {
		colModel = 12
	}

	// Column headers — highlight the active sort column
	modelHdr, sizeHdr, vramHdr, tokHdr, ttftHdr, statusHdr, expiresHdr := "MODEL", "SIZE", "VRAM", "tok/s", "TTFT", "STATUS", "EXPIRES"
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

	hdr := fmt.Sprintf(" %-*s %-*s %-*s %-*s %-*s %-*s %-*s",
		colModel, modelHdr,
		colSize, sizeHdr,
		colVRAM, vramHdr,
		colTokSec, tokHdr,
		colTTFT, ttftHdr,
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
		var ttft string
		if !m.snapshot.HasCapture {
			tps = dimStyle.Render("<root>")
			ttft = dimStyle.Render("<root>")
		} else {
			effectiveTPS := mdl.LiveTokPerSec
			if effectiveTPS == 0 {
				effectiveTPS = mdl.CurrentTokPerSec
			}
			if effectiveTPS > 0 {
				tps = tokSecStyle.Render(fmt.Sprintf("%.1f", effectiveTPS))
			} else {
				tps = dimStyle.Render("\u2014")
			}

			ttft = dimStyle.Render("\u2014")
			if mdl.TTFT > 0 {
				ttft = tokSecStyle.Render(formatTTFT(mdl.TTFT))
			}
		}

		// Status with active request count and phase
		var status string
		thinkingColor := lipgloss.Color("213") // pink/magenta for thinking
		switch mdl.Status {
		case "thinking":
			thinkStyle := lipgloss.NewStyle().Foreground(thinkingColor)
			if mdl.ActiveRequests > 1 {
				status = thinkStyle.Render(fmt.Sprintf("think(%d)", mdl.ActiveRequests))
			} else {
				status = thinkStyle.Render("thinking")
			}
		case "running":
			runStyle := lipgloss.NewStyle().Foreground(runningColor)
			if mdl.ActiveRequests > 1 {
				status = runStyle.Render(fmt.Sprintf("run(%d)", mdl.ActiveRequests))
			} else {
				status = runStyle.Render("running")
			}
		default:
			status = lipgloss.NewStyle().Foreground(idleColor).Render("idle")
		}

		expires := formatDuration(mdl.ExpiresIn)

		row := " " + padRight(name, colModel) +
			" " + padRight(size, colSize) +
			" " + padRight(vram, colVRAM) +
			" " + padRight(tps, colTokSec) +
			" " + padRight(ttft, colTTFT) +
			" " + padRight(status, colStatus) +
			" " + padRight(truncate(expires, colExpires), colExpires)

		b.WriteString(m.renderBorderedLine(inner, row))
		b.WriteByte('\n')

		// Detail line for active models: show TTFT, thinking metrics, TTFR
		if mdl.Status == "thinking" || mdl.Status == "running" {
			var details []string
			indent := " " + strings.Repeat(" ", colModel+1)
			thinkStyle := lipgloss.NewStyle().Foreground(thinkingColor)

			if mdl.TTFT > 0 {
				details = append(details, dimStyle.Render("TTFT ")+tokSecStyle.Render(formatTTFT(mdl.TTFT)))
			}

			if mdl.ThinkTokenCount > 0 {
				thinkInfo := fmt.Sprintf("%d tok", mdl.ThinkTokenCount)
				if mdl.ThinkDuration > 0 {
					thinkInfo += fmt.Sprintf(" %s", formatTTFT(mdl.ThinkDuration))
				}
				if mdl.ThinkTokPerSec > 0 {
					thinkInfo += fmt.Sprintf(" %.1f tok/s", mdl.ThinkTokPerSec)
				}
				details = append(details, thinkStyle.Render("think ")+dimStyle.Render(thinkInfo))
			}

			if mdl.TTFR > 0 {
				details = append(details, dimStyle.Render("TTFR ")+tokSecStyle.Render(formatTTFT(mdl.TTFR)))
			}

			if mdl.ResponseTokenCount > 0 && mdl.ResponseTokPerSec > 0 {
				respInfo := fmt.Sprintf("%d tok %.1f tok/s", mdl.ResponseTokenCount, mdl.ResponseTokPerSec)
				details = append(details, dimStyle.Render("resp ")+tokSecStyle.Render(respInfo))
			}

			if len(details) > 0 {
				detail := indent + strings.Join(details, dimStyle.Render("  "))
				b.WriteString(m.renderBorderedLine(inner, detail))
				b.WriteByte('\n')
			}
		}
	}

	b.WriteString(m.renderBorderedLine(inner, ""))
	b.WriteByte('\n')
	return b.String()
}

func (m Model) renderThroughput(inner int) string {
	var b strings.Builder

	tp := m.snapshot.TokPerSec
	sinceStr := ""
	if !tp.WindowStart.IsZero() {
		sinceStr = tp.WindowStart.Local().Format("3:04 PM")
	}

	sparkWidth := 20

	if !m.snapshot.HasCapture {
		noCapture := dimStyle.Render("<requires root>")
		tokLine := " " + dimStyle.Render("tok/s  ") + " " + dimStyle.Render(strings.Repeat("·", sparkWidth)) + "   " + noCapture
		b.WriteString(m.renderBorderedLine(inner, tokLine))
		b.WriteByte('\n')
		promptLine := " " + dimStyle.Render("prompt ") + " " + dimStyle.Render(strings.Repeat("·", sparkWidth)) + "   " + noCapture
		b.WriteString(m.renderBorderedLine(inner, promptLine))
		b.WriteByte('\n')
	} else {
		tokLine := renderSparkRow("tok/s  ", tp.TokPerSecHistory, tp.CurrentTokPerSec, tp.MaxTokPerSec, sinceStr, tp.ActiveBuckets, sparkWidth)
		b.WriteString(m.renderBorderedLine(inner, tokLine))
		b.WriteByte('\n')

		promptLine := renderSparkRow("prompt ", tp.PromptTPSHistory, tp.CurrentPromptTPS, tp.MaxPromptTPS, sinceStr, tp.ActiveBuckets, sparkWidth)
		b.WriteString(m.renderBorderedLine(inner, promptLine))
		b.WriteByte('\n')
	}

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
	return buildSparklineStyled(data, width, activeBuckets, sparkStyle)
}

func buildSparklineStyled(data []float64, width, activeBuckets int, activeStyle lipgloss.Style) string {
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
		sb.WriteString(activeStyle.Render(strings.Repeat(string(sparkBlocks[0]), padCount)))
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
	sb.WriteString(activeStyle.Render(activePart.String()))
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
	sparkWidth := 8
	barWidth := 10

	var b strings.Builder

	// CPU row: bar + percentage + temperature + sparkline
	cpuLine := " CPU  " + renderBar(sys.CPUPercent, barWidth) + fmt.Sprintf("  %3.0f%%", sys.CPUPercent)
	if sys.SensorsAvail && sys.CPUTemp > 0 {
		ts := tempStyle(sys.CPUTemp, 70, 90)
		cpuLine += "  " + ts.Render(fmt.Sprintf("%3.0f°C", sys.CPUTemp))
		if len(sys.CPUHistory) > 0 {
			cpuLine += " " + buildSparklineStyled(sys.CPUHistory, sparkWidth, sys.ActiveBuckets, ts)
		}
	}
	b.WriteString(m.renderBorderedLine(inner, cpuLine))
	b.WriteByte('\n')

	// GPU row: bar + percentage + temperature + sparkline
	if sys.GPUAvail {
		gpuLine := " GPU  " + renderBar(sys.GPUPercent, barWidth) + fmt.Sprintf("  %3.0f%%", sys.GPUPercent)
		if sys.SensorsAvail && sys.GPUTemp > 0 {
			ts := tempStyle(sys.GPUTemp, 75, 95)
			gpuLine += "  " + ts.Render(fmt.Sprintf("%3.0f°C", sys.GPUTemp))
			if len(sys.GPUHistory) > 0 {
				gpuLine += " " + buildSparklineStyled(sys.GPUHistory, sparkWidth, sys.ActiveBuckets, ts)
			}
		}
		b.WriteString(m.renderBorderedLine(inner, gpuLine))
		b.WriteByte('\n')
	}

	// RAM row: bar + percentage + used/total
	ramLine := " RAM  " + renderBar(sys.MemPercent, barWidth) + fmt.Sprintf("  %3.0f%%", sys.MemPercent) +
		"  " + formatBytesUint64(sys.MemUsed) + " / " + formatBytesUint64(sys.MemTotal)
	b.WriteString(m.renderBorderedLine(inner, ramLine))
	b.WriteByte('\n')

	// Fan row: RPM + sparkline (only if sensors available)
	if sys.SensorsAvail && len(sys.FanSpeeds) > 0 {
		var fanLine string
		maxRPM := 0.0
		for _, rpm := range sys.FanSpeeds {
			if rpm > maxRPM {
				maxRPM = rpm
			}
		}
		if maxRPM <= 0 {
			fanLine = " Fan  " + dimStyle.Render("idle")
		} else {
			fs := fanStyle(maxRPM)
			if len(sys.FanSpeeds) == 1 {
				fanLine = " Fan  " + fs.Render(fmt.Sprintf("%4.0f RPM", sys.FanSpeeds[0]))
			} else {
				parts := make([]string, len(sys.FanSpeeds))
				for i, rpm := range sys.FanSpeeds {
					parts[i] = fmt.Sprintf("%.0f", rpm)
				}
				fanLine = " Fan  " + fs.Render(strings.Join(parts, "/")+" RPM")
			}
			if len(sys.FanHistory) > 0 {
				fanLine += "  " + buildSparklineStyled(sys.FanHistory, sparkWidth, sys.ActiveBuckets, fs)
			}
		}
		b.WriteString(m.renderBorderedLine(inner, fanLine))
		b.WriteByte('\n')
	}

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
	if lipgloss.Width(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return strings.Repeat(".", maxLen)
	}
	target := maxLen - 3
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > target {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "..."
}

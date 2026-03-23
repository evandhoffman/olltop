# CLAUDE.md

## Project overview

olltop is a `top`-like TUI for monitoring a local Ollama instance with **tokens/sec** display. Written in Go, it uses libpcap (macOS) to passively observe Ollama's HTTP response traffic and extract `eval_count`/`eval_duration` from the final response chunk.

Successor to [ollama-top](https://github.com/evandhoffman/ollama-top) (Python). See ollama-top#12 for why `/api/ps` alone cannot provide tok/s data.

## Workflow

- **Commit and push to main by default.** No branches, no PRs, no confirmation needed.
- **Create GitHub issues before implementing** bug fixes or feature requests reported by the user.

## Target platform

**macOS Apple Silicon (arm64) only.** macOS Tahoe or newer. Linux (eBPF) planned later but not in scope.

## How tok/s capture works

Ollama streams responses as newline-delimited JSON over HTTP. The **final chunk** of each response contains:

```json
{"model":"deepseek-r1:8b","done":true,"eval_count":2735,"eval_duration":33057998191,...}
```

- `eval_count` = tokens generated
- `eval_duration` = nanoseconds spent generating
- `tok/s = eval_count / (eval_duration / 1e9)`
- Also: `prompt_eval_count` and `prompt_eval_duration` for prompt processing speed

**Important limitation**: tok/s only appears after a response completes. During active streaming, no metrics are available until the `"done":true` chunk arrives.

**Capture approach**: open pcap handle on `lo0`, filter `tcp port 11434`, reassemble TCP streams via gopacket tcpassembly, scan for `"done":true` JSON, extract eval fields. Requires root.

### Key technical details

- `/api/ps` has NO `status` field. Models are always "loaded."
- Activity detected by capture data recency (10s threshold), not `expires_at` changes.
- `expires_at` only updates after a request finishes, not during.
- On macOS unified memory, `size` and `size_vram` are typically equal.
- Ollama default keepalive is 5 minutes.

## Architecture

```
cmd/olltop/main.go              # entry point, CLI flags, privilege detection, wiring
internal/
├── capture/
│   ├── capture.go               # Backend interface + EvalMetrics type
│   └── pcap_darwin.go           # macOS: libpcap on lo0, TCP reassembly, JSON extraction
├── ollama/
│   ├── client.go                # HTTP client for /api/ps, /api/version with polling
│   ├── client_test.go           # Tests
│   └── types.go                 # ModelInfo, Snapshot types
├── metrics/
│   ├── aggregator.go            # Merge polling + capture, 5-min rolling window, system metrics
│   └── types.go                 # DisplaySnapshot, ModelDisplay, ThroughputInfo, SystemInfo
└── tui/
    └── app.go                   # bubbletea TUI with sparklines, model table, system bars
```

## Key components

### Aggregator (`internal/metrics/aggregator.go`)
- Receives `ollama.Snapshot` (polling) and `capture.EvalMetrics` (pcap) on channels
- Maintains time-windowed samples (5-minute window, 60 buckets of 5s each)
- Tracks per-model tok/s with 10s active threshold (decays to 0 when idle)
- Collects CPU/RAM via gopsutil
- Emits `DisplaySnapshot` every 1 second via ticker

### TUI (`internal/tui/app.go`)
- bubbletea Model receiving `SnapshotMsg` from aggregator
- Sparklines with startup boundary (dots for pre-startup, green for active)
- ANSI-aware column padding via `padRight()` using `lipgloss.Width()`
- Max tok/s and "since" window indicator

## Dependencies

- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [lipgloss](https://github.com/charmbracelet/lipgloss) — TUI styling
- [google/gopacket](https://github.com/google/gopacket) — pcap + TCP reassembly
- [shirou/gopsutil/v4](https://github.com/shirou/gopsutil) — CPU/RAM metrics
- libpcap (cgo, linked at build time via system dylib)

## Build

```bash
make build          # CGO_ENABLED=1 go build
sudo ./olltop       # full mode with tok/s
./olltop            # degraded mode (no tok/s)
make test           # go test ./...
```

- **cgo required** — gopacket/pcap links libpcap
- macOS provides system libpcap at `/usr/lib/libpcap.dylib`
- `make build VERSION=v0.2.0` injects version via ldflags

## CI/CD

- GitHub Actions workflow (`.github/workflows/release.yml`)
- Triggers on `v*` tag push
- Builds on `macos-14` runner (arm64)
- Creates GitHub Release with `olltop-darwin-arm64` binary

## Design decisions

- **Passive only** — never proxy, intercept, or modify Ollama traffic
- **Graceful degradation** — without root, polls only (model list, VRAM, CPU/RAM, no tok/s)
- **Single binary** — no runtime deps beyond system libs
- **Respect `$OLLAMA_HOST`** — same env var Ollama uses, with `--host` flag override
- **Debug logging to file** — `--debug` writes to `olltop.log` to avoid TUI interference

## User preferences

- GitHub username: **evandhoffman** (not "evanhoffman")
- Commit and push to main by default — rapid development, no branching
- Create GitHub issues before starting implementation on reported bugs/features
- Use `slog` for logging, never `fmt.Println`
- Runs on macOS Mac Mini (Ollama as Launch Agent) and MacBook (development)

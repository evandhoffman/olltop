# CLAUDE.md

## Project overview

olltop is a `top`-like TUI for monitoring a local Ollama instance with **real-time tokens/sec** display. Written in Go, it uses libpcap (macOS) to passively observe Ollama's HTTP response traffic and extract `eval_count`/`eval_duration` from streaming responses.

This is the successor to [ollama-top](https://github.com/evandhoffman/ollama-top) (Python), which proved that `/api/ps` alone cannot provide tok/s data. See ollama-top#12 for the full analysis.

## Workflow

**Commit and push to main by default.** This project is in rapid development — no branches, no PRs, no confirmation needed before pushing. Just commit with a good message and push.

## Target platform

**macOS first** (Apple Silicon MacBook). Linux (eBPF) is planned later but not in scope yet.

## How tok/s capture works

Ollama streams responses as newline-delimited JSON over HTTP. The final chunk of each response contains:

```json
{"model":"deepseek-r1:8b","done":true,"eval_count":2735,"eval_duration":33057998191,...}
```

- `eval_count` = tokens generated
- `eval_duration` = nanoseconds spent generating
- `tok/s = eval_count / (eval_duration / 1e9)`
- There are also `prompt_eval_count` and `prompt_eval_duration` for prompt processing speed

**The capture approach**: open a pcap handle on `lo0` (macOS loopback), filter `tcp port 11434`, reassemble TCP streams, scan for the `"done":true` JSON chunk, extract the eval fields. This requires root.

### Key technical details learned from prototyping

- Ollama's `/api/ps` response does NOT have a `status` field. Models are always "loaded" — there's no explicit running/idle indicator.
- Activity can be detected by watching `expires_at` changes between polls (Ollama resets the keepalive timer after each request completes, not during).
- `context_length` in `/api/ps` is the max context window, NOT current usage. It doesn't change during inference.
- `expires_at` does NOT change during inference — it only updates after the request finishes.
- The `size` field in `/api/ps` is the total model weight size. `size_vram` is the portion in VRAM. On macOS with unified memory, these are typically equal.
- Ollama's default keepalive is 5 minutes (`expires_at` is ~5min in the future after last request).

## Architecture

```
cmd/olltop/main.go           # entry point, CLI flags, privilege detection, wiring
internal/
├── capture/
│   ├── capture.go            # CaptureBackend interface + EvalMetrics type
│   └── pcap_darwin.go        # macOS: libpcap on lo0, TCP reassembly, JSON extraction
├── ollama/
│   ├── client.go             # HTTP client for /api/ps, /api/version
│   └── types.go              # ModelInfo, Snapshot types
├── metrics/
│   ├── aggregator.go         # merge polling + capture data, rolling history
│   └── types.go              # DisplaySnapshot, ModelDisplay
└── tui/
    └── app.go                # bubbletea Model
```

## Dependencies

- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [lipgloss](https://github.com/charmbracelet/lipgloss) — TUI styling
- [google/gopacket](https://github.com/google/gopacket) — pcap + TCP reassembly
- [shirou/gopsutil](https://github.com/shirou/gopsutil) — CPU/RAM metrics (or use runtime)
- libpcap (cgo, linked at build time)

## Build notes

- **cgo is required** — gopacket/pcap uses cgo to link libpcap
- `CGO_ENABLED=1` must be set
- On macOS, libpcap is provided by the system (`/usr/lib/libpcap.dylib`)
- For distribution, statically link libpcap or accept the system dylib dependency
- Binary needs to run as root (`sudo olltop`) for pcap access

## Design decisions

- **Passive only** — never proxy, intercept, or modify Ollama traffic
- **Graceful degradation** — without root, falls back to polling-only mode (model list, VRAM, CPU/RAM, no tok/s)
- **Single binary** — no runtime dependencies beyond system libs
- **Respect `$OLLAMA_HOST`** — same env var Ollama itself uses, with `--host` flag override

## User preferences

- GitHub username: **evandhoffman** (not "evanhoffman")
- Commit and push to main by default — rapid development, no branching workflow
- Use `logging`/`slog` for output, not fmt.Println for operational messages
- This will run on a macOS Mac Mini where Ollama runs as a Launch Agent (menu bar app)
- The user is a limited (non-admin) account on that machine — but has a MacBook with admin for development

## GitHub issues

Issues #1-#7 are filed with detailed specs and acceptance criteria. Designed for parallel execution:
- #1 (scaffolding) first
- #2 (ollama client) and #3 (pcap capture) can run in parallel after #1
- #4 (aggregator) after #2 and #3
- #5 (TUI) can start in parallel with #4 if types are defined early
- #6 (integration) after #4 and #5
- #7 (build/release) last

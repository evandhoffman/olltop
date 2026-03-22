# olltop

Real-time tokens/sec monitor for [Ollama](https://ollama.com) using eBPF (Linux) and pcap (macOS).

## Motivation

Ollama's `/api/ps` endpoint shows loaded models but not throughput. The actual tok/s metrics (`eval_count`, `eval_duration`) are only available in the streaming response body of `/api/generate` and `/api/chat`. Without proxying, the only way to observe these is at the kernel/packet level.

See [ollama-top#12](https://github.com/evandhoffman/ollama-top/issues/12) for the full analysis of why a pure-API approach can't work.

## How it works

**olltop** passively observes Ollama's HTTP traffic to extract token throughput, without proxying or modifying Ollama in any way.

| Platform | Capture method | Requirement |
|---|---|---|
| Linux | eBPF — traces `write()` syscalls from the Ollama process | `CAP_BPF` + `CAP_PERFMON` (or root) |
| macOS | libpcap — captures loopback traffic on port 11434 | root |

Both backends look for the final streaming response chunk containing `"done":true` and extract `eval_count` / `eval_duration` to compute real tok/s.

In addition to tok/s, olltop polls `/api/ps` for:
- Loaded models, VRAM usage, expiry countdowns
- Running/idle status

And uses OS APIs for:
- CPU and RAM utilization

## Planned architecture

```
olltop
├── cmd/olltop/              # main, CLI flags
├── internal/
│   ├── capture/
│   │   ├── capture.go       # CaptureBackend interface
│   │   ├── ebpf_linux.go    # Linux: trace Ollama write() via cilium/ebpf
│   │   ├── ebpf_linux.c     # eBPF program source
│   │   └── pcap_darwin.go   # macOS: libpcap on lo0
│   ├── ollama/
│   │   └── client.go        # /api/ps, /api/version polling
│   ├── metrics/
│   │   └── aggregator.go    # combine capture + polling, rolling tok/s
│   └── tui/
│       └── app.go           # bubbletea TUI
├── go.mod
└── Makefile
```

## Key design decisions

- **Passive observation** — no proxy, no traffic interception, no modification to Ollama
- **Elevated privileges required** — eBPF and pcap both need root-level access; this is an inherent requirement, not a design choice
- **Graceful degradation** — without root, falls back to `/api/ps`-only mode (model list, VRAM, no tok/s)
- **Single binary** — `go install` or download from GitHub releases
- **Cross-platform** — Linux (eBPF, primary) and macOS (pcap)

## Tech stack

- [Go](https://go.dev/)
- [cilium/ebpf](https://github.com/cilium/ebpf) — eBPF loader and CO-RE support
- [google/gopacket](https://github.com/google/gopacket) — packet capture for macOS
- [bubbletea](https://github.com/charmbracelet/bubbletea) — terminal UI
- [lipgloss](https://github.com/charmbracelet/lipgloss) — TUI styling

## Status

Not yet implemented. This repo was created based on the design exploration in [ollama-top](https://github.com/evandhoffman/ollama-top).

## License

MIT

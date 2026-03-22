# olltop — Specification

## Overview

A `top`-like terminal UI for monitoring a local Ollama instance with real-time tokens/sec, built in Go. Uses libpcap on macOS to passively observe Ollama's HTTP responses without proxying.

## Why this exists

Ollama's `/api/ps` shows loaded models but not throughput. The `eval_count` and `eval_duration` fields that contain tok/s are only in the streaming response body of `/api/generate` and `/api/chat`. A pure-API poller (like the Python predecessor) can never show tok/s.

Proxying doesn't help either — the Ollama Mac app talks directly to `localhost:11434` and can't be redirected.

The solution: capture loopback traffic at the packet level.

## Data sources

| Metric | Source | Requires root? |
|---|---|---|
| Loaded models, VRAM, expiry | `GET /api/ps` polled every 1s | No |
| Ollama version | `GET /api/version` at startup | No |
| Generation tok/s | pcap: `eval_count / eval_duration` from response body | **Yes** |
| Prompt eval tok/s | pcap: `prompt_eval_count / prompt_eval_duration` | **Yes** |
| Running/idle status | pcap: model is "running" while response is streaming | **Yes** |
| CPU % | gopsutil or runtime | No |
| RAM used/total | gopsutil or runtime | No |

**No GPU metrics.** Same limitation as the Python version — requires sudo on macOS.

## pcap capture details

### What Ollama sends

Ollama streams responses as newline-delimited JSON (not HTTP chunked encoding in the traditional sense — it's `Transfer-Encoding: chunked` at the HTTP level but each line is a complete JSON object).

During generation, each chunk looks like:
```json
{"model":"deepseek-r1:8b","created_at":"...","response":"token","done":false}
```

The final chunk:
```json
{
  "model": "deepseek-r1:8b",
  "done": true,
  "done_reason": "stop",
  "total_duration": 33994470083,
  "load_duration": 72771458,
  "prompt_eval_count": 9,
  "prompt_eval_duration": 482627375,
  "eval_count": 2735,
  "eval_duration": 33057998191
}
```

### Capture strategy

1. Open pcap on `lo0` with BPF filter `tcp port 11434`
2. Use gopacket's `tcpassembly` to reassemble TCP streams
3. In the reassembled stream, scan for lines containing `"done":true`
4. Parse that JSON line, extract eval fields
5. Compute tok/s: `eval_count / (eval_duration / 1e9)`
6. Send to metrics aggregator

### Edge cases

- Multiple concurrent requests (different source ports → different TCP streams)
- Large thinking-model responses (deepseek-r1 can produce thousands of tokens)
- Model loads that don't involve inference
- Ollama restarts mid-capture
- Non-inference API calls (/api/ps, /api/version, /api/tags) — ignore these

## UI layout

```
┌─ olltop ──────────────────── localhost:11434 ── v0.x.x ── q to quit ─┐
│                                                                       │
├─ Loaded Models ───────────────────────────────────────────────────────┤
│ MODEL              SIZE       VRAM       tok/s    STATUS   EXPIRES    │
│ deepseek-r1:8b     23.3 GB    23.3 GB    82.7    running  4m 32s     │
│ gemma3:1b          1.0 GB     1.0 GB     —       idle     2m 10s     │
│                                                                       │
├─ Throughput ──────────────────────────────────────────────────────────┤
│ tok/s   ▁▂▄▆█▇▅▃▂▄▆█▇▅▃   82.7 tok/s                               │
│ prompt  ▁▁▃▅▇█▅▃▁▁▃▅▇█▅   124.3 tok/s                              │
│                                                                       │
├─ System ──────────────────────────────────────────────────────────────┤
│ CPU  ████████░░  78%        RAM  12.4 GB / 32.0 GB  (38%)           │
└───────────────────────────────────────────────────────────────────────┘
```

### Degraded mode (no root)

When pcap is unavailable, the tok/s column shows `—` and the throughput panel shows:
```
├─ Throughput ──────────────────────────────────────────────────────────┤
│ ⚠ tok/s monitoring requires root: sudo olltop                        │
```

## CLI

```
Usage: olltop [flags]

Flags:
  --host string    Ollama host URL (default: $OLLAMA_HOST or http://localhost:11434)
  --debug          Enable debug logging
  --version        Print version and exit
```

## Configuration

| Priority | Source |
|---|---|
| 1 (highest) | `--host` flag |
| 2 | `$OLLAMA_HOST` env var |
| 3 (default) | `http://localhost:11434` |

## Build

```bash
# Development
make build
sudo ./olltop

# Or directly
CGO_ENABLED=1 go build -o olltop ./cmd/olltop
sudo ./olltop
```

Requires cgo for libpcap. macOS provides system libpcap.

## Out of scope (for now)

- Linux / eBPF support (planned, separate milestone)
- GPU metrics
- Multi-host monitoring
- Docker container (doesn't make sense for pcap on loopback)
- Windows

## Lessons from the Python prototype

These are things we learned the hard way in ollama-top:

1. `/api/ps` has no `status` field — don't assume one exists
2. `expires_at` only changes AFTER a request completes, not during
3. `context_length` is the max window size, not current usage — it never changes
4. On macOS unified memory, `size` and `size_vram` are typically identical
5. The streaming response final chunk is the only source of eval metrics
6. Don't show tok/s unless you can actually measure it — placeholder zeros are worse than admitting the limitation

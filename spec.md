# olltop — Specification

## Overview

A `top`-like terminal UI for monitoring a local Ollama instance with tokens/sec throughput, built in Go. Uses libpcap on macOS to passively observe Ollama's HTTP responses without proxying.

## Status

**MVP complete.** Core functionality implemented and released as v0.1.0. All original issues (#1-#8) closed plus follow-up refinements (#9-#13).

### What works

- Ollama API polling (`/api/ps`, `/api/version`) with auto-reconnect
- pcap capture on lo0 with TCP reassembly extracting tok/s from `"done":true` chunks
- TUI with model table, sparklines (5-min sliding window), CPU/RAM bars
- Graceful degradation without root (no tok/s, clear messaging)
- GitHub Actions CI building arm64 binary on tag push

### Known limitations

- tok/s only appears after a response completes (not during streaming)
- No GPU metrics
- macOS arm64 only (no Linux/Intel Mac support yet)

## Why this exists

Ollama's `/api/ps` shows loaded models but not throughput. The `eval_count` and `eval_duration` fields that contain tok/s are only in the streaming response body of `/api/generate` and `/api/chat`. A pure-API poller can never show tok/s.

Proxying doesn't help either — the Ollama Mac app talks directly to `localhost:11434` and can't be redirected.

The solution: capture loopback traffic at the packet level.

## Data sources

| Metric | Source | Requires root? |
|---|---|---|
| Loaded models, VRAM, expiry | `GET /api/ps` polled every 1s | No |
| Ollama version | `GET /api/version` at startup + reconnect | No |
| Generation tok/s | pcap: `eval_count / eval_duration` from response body | **Yes** |
| Prompt eval tok/s | pcap: `prompt_eval_count / prompt_eval_duration` | **Yes** |
| Running/idle status | Capture data recency (10s threshold) | **Yes** |
| CPU % | gopsutil | No |
| RAM used/total | gopsutil | No |

**No GPU metrics.** Requires additional macOS APIs.

## pcap capture details

### What Ollama sends

Ollama streams responses as newline-delimited JSON (`Transfer-Encoding: chunked` at HTTP level, each line a complete JSON object).

During generation:
```json
{"model":"deepseek-r1:8b","created_at":"...","response":"token","done":false}
```

Final chunk:
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
2. gopacket `tcpassembly` reassembles TCP streams
3. Scan reassembled stream for lines containing `"done":true`
4. Parse JSON, extract eval fields
5. Compute tok/s: `eval_count / (eval_duration / 1e9)`
6. Send to metrics aggregator via channel

### Edge cases handled

- Multiple concurrent requests (different TCP streams via source port)
- Large responses (1MB scanner buffer)
- Stale stream flushing (30s ticker)
- Non-inference API calls (ignored — no `"done":true`)

## UI layout

```
┌─ olltop ──────────────────── localhost:11434 ── v0.18.2 ── q to quit ─┐
├─ Loaded Models ───────────────────────────────────────────────────────┤
│ MODEL              SIZE       VRAM       tok/s    STATUS   EXPIRES    │
│ deepseek-r1:8b     23.3 GB    23.3 GB    82.7    running  4m 32s     │
│ gemma3:1b          1.0 GB     1.0 GB     —       idle     2m 10s     │
│                                                                       │
├─ Throughput ──────────────────────────────────────────────────────────┤
│ tok/s   ··········▁▁▁▁▁█▇▅▃▁   0.0 tok/s  max 82.7  since 8:10 PM  │
│ prompt  ··········▁▁▁▁▁▇█▅▃▁   0.0 tok/s  max 124.3  since 8:10 PM │
│                                                                       │
├─ System ──────────────────────────────────────────────────────────────┤
│ CPU  ████████░░  78%        RAM  12.4 GB / 32.0 GB  (38%)           │
└───────────────────────────────────────────────────────────────────────┘
```

### Sparkline features

- 5-minute sliding window, 60 buckets (5s each)
- Dim `·` for pre-startup buckets, green `▁`-`█` for active period
- Max tok/s and "since" timestamp showing window left edge
- Decays to 0 when no activity

### Degraded mode (no root)

```
├─ Throughput ──────────────────────────────────────────────────────────┤
│ ⚠ tok/s monitoring requires root: sudo olltop                        │
```

## CLI

```
Usage: olltop [flags]

Flags:
  --host string    Ollama host URL (default: $OLLAMA_HOST or http://localhost:11434)
  --debug          Enable debug logging (writes to olltop.log)
  --version        Print version and exit
```

## Build

```bash
make build                    # development build
make build VERSION=v0.2.0     # with version injection
sudo ./olltop                 # run with tok/s capture
make test                     # run tests
```

Requires cgo for libpcap. macOS provides system libpcap.

## Out of scope (for now)

- Linux / eBPF support (planned, separate milestone)
- GPU metrics
- Real-time during-generation tok/s (planned enhancement)
- Multi-host monitoring
- Docker container
- Windows / Intel Mac

## Lessons from the Python prototype

1. `/api/ps` has no `status` field — don't assume one exists
2. `expires_at` only changes AFTER a request completes, not during
3. `context_length` is the max window size, not current usage
4. On macOS unified memory, `size` and `size_vram` are typically identical
5. The streaming response final chunk is the only source of eval metrics
6. Don't show tok/s unless you can actually measure it

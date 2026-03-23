package metrics

import "time"

// DisplaySnapshot is the unified data structure sent to the TUI for rendering.
type DisplaySnapshot struct {
	Models     []ModelDisplay
	SystemInfo SystemInfo
	TokPerSec  ThroughputInfo
	Connected  bool
	Version    string
	HasCapture bool // whether pcap capture is active
	Timestamp  time.Time
}

// ModelDisplay contains per-model display data.
type ModelDisplay struct {
	Name             string
	Size             int64
	SizeVRAM         int64
	CurrentTokPerSec float64
	PromptTokPerSec  float64
	LiveTokPerSec    float64 // real-time tok/s from streaming chunks
	Status           string  // "running", "thinking", or "idle"
	ExpiresIn        time.Duration
	Digest           string
	TTFT             time.Duration // time to first token (most recent request)
	TTFR             time.Duration // time to first response token (after thinking)
	ActiveRequests   int           // number of in-flight requests

	// Thinking phase metrics (reasoning models)
	Phase              string // "thinking", "responding", or ""
	ThinkTokenCount    int64
	ThinkDuration      time.Duration
	ThinkTokPerSec     float64
	ResponseTokenCount int64
	ResponseTokPerSec  float64
}

// ThroughputInfo contains aggregate throughput data with history for sparklines.
type ThroughputInfo struct {
	CurrentTokPerSec float64
	CurrentPromptTPS float64
	TokPerSecHistory []float64 // last 60 samples for sparkline
	PromptTPSHistory []float64
	MaxTokPerSec     float64   // peak tok/s in current window
	MaxPromptTPS     float64   // peak prompt tok/s in current window
	WindowStart      time.Time // oldest data point in the window
	ActiveBuckets    int       // how many trailing buckets the app has been running for
}

// SystemInfo contains CPU, GPU, memory, and sensor metrics.
type SystemInfo struct {
	CPUPercent    float64
	GPUPercent    float64 // Apple Silicon device utilization %
	GPUAvail      bool    // whether GPU metrics are available
	MemUsed       uint64
	MemTotal      uint64
	MemPercent    float64
	CPUTemp       float64   // °C
	GPUTemp       float64   // °C
	FanSpeeds     []float64 // RPM per fan
	CPUHistory    []float64 // last 5m of CPU temps
	GPUHistory    []float64 // last 5m of GPU temps
	FanHistory    []float64 // last 5m of max fan RPM
	ActiveBuckets int       // how many trailing buckets the app has been running for
	SensorsAvail  bool
}

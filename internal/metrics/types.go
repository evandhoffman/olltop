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
	Status           string // "running" or "idle"
	ExpiresIn        time.Duration
	Digest           string
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

// SystemInfo contains CPU, GPU, and memory metrics.
type SystemInfo struct {
	CPUPercent  float64
	GPUPercent  float64 // Apple Silicon device utilization %
	GPUAvail    bool    // whether GPU metrics are available
	MemUsed     uint64
	MemTotal    uint64
	MemPercent  float64
}

package capture

import (
	"context"
	"time"
)

// Backend is the interface for packet capture implementations.
type Backend interface {
	Start(ctx context.Context, metrics chan<- EvalMetrics, streaming chan<- StreamingMetrics) error
}

// EvalMetrics contains token evaluation metrics extracted from Ollama responses.
type EvalMetrics struct {
	Model              string
	EvalCount          int64
	EvalDuration       time.Duration
	PromptEvalCount    int64
	PromptEvalDuration time.Duration
	TotalDuration      time.Duration
	Timestamp          time.Time
}

// StreamingMetrics contains live token generation metrics from an active stream.
type StreamingMetrics struct {
	Model         string
	TokenCount    int64         // tokens received so far in this stream
	LiveTokPerSec float64       // rolling tok/s computed from recent tokens
	TTFT          time.Duration // time to first token (0 if unknown)
	Active        bool          // true while generating, false when stream ends
	Timestamp     time.Time
}

// TokPerSec computes generation tokens per second.
func (m EvalMetrics) TokPerSec() float64 {
	if m.EvalDuration <= 0 {
		return 0
	}
	return float64(m.EvalCount) / m.EvalDuration.Seconds()
}

// PromptTokPerSec computes prompt evaluation tokens per second.
func (m EvalMetrics) PromptTokPerSec() float64 {
	if m.PromptEvalDuration <= 0 {
		return 0
	}
	return float64(m.PromptEvalCount) / m.PromptEvalDuration.Seconds()
}

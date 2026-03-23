package metrics

import (
	"testing"
	"time"

	"github.com/evandhoffman/olltop/internal/capture"
	"github.com/evandhoffman/olltop/internal/ollama"
)

func TestHandleStreamingMetricsTracksDistinctRequests(t *testing.T) {
	a := NewAggregator(true)
	now := time.Now()

	a.handleStreamingMetrics(capture.StreamingMetrics{
		Model:         "deepseek-r1:8b",
		RequestID:     "conn-1|1",
		Active:        true,
		LiveTokPerSec: 12.5,
		Timestamp:     now,
	})
	a.handleStreamingMetrics(capture.StreamingMetrics{
		Model:         "deepseek-r1:8b",
		RequestID:     "conn-1|1",
		Active:        true,
		LiveTokPerSec: 13.5,
		Timestamp:     now.Add(500 * time.Millisecond),
	})
	a.handleStreamingMetrics(capture.StreamingMetrics{
		Model:         "deepseek-r1:8b",
		RequestID:     "conn-2|1",
		Active:        true,
		LiveTokPerSec: 9.0,
		Timestamp:     now.Add(time.Second),
	})

	state := a.modelStreaming["deepseek-r1:8b"]
	if state.activeRequests != 2 {
		t.Fatalf("activeRequests = %d, want 2", state.activeRequests)
	}

	a.handleStreamingMetrics(capture.StreamingMetrics{
		Model:     "deepseek-r1:8b",
		RequestID: "conn-1|1",
		Active:    false,
		Timestamp: now.Add(2 * time.Second),
	})

	if state.activeRequests != 1 {
		t.Fatalf("activeRequests after end = %d, want 1", state.activeRequests)
	}

	a.handleStreamingMetrics(capture.StreamingMetrics{
		Model:     "deepseek-r1:8b",
		RequestID: "conn-2|1",
		Active:    false,
		Timestamp: now.Add(3 * time.Second),
	})

	if state.activeRequests != 0 {
		t.Fatalf("activeRequests after all ends = %d, want 0", state.activeRequests)
	}
	if state.liveTokPerSec != 0 {
		t.Fatalf("liveTokPerSec after all ends = %f, want 0", state.liveTokPerSec)
	}
}

func TestBuildSparklineHistoryIgnoresStreamingSamplesForPromptHistory(t *testing.T) {
	a := NewAggregator(true)
	now := time.Now()
	ts := now.Add(-time.Minute)

	a.samples = append(a.samples,
		sample{
			tokPS:         10,
			promptPS:      5,
			includePrompt: true,
			ts:            ts,
		},
		sample{
			tokPS:         20,
			promptPS:      0,
			includePrompt: false,
			ts:            ts,
		},
	)

	tokHist, promptHist := a.buildSparklineHistory(now)
	idx := int(ts.Sub(now.Add(-historyWindow)) / bucketWidth)

	if got := tokHist[idx]; got != 15 {
		t.Fatalf("tokHist[%d] = %v, want 15", idx, got)
	}
	if got := promptHist[idx]; got != 5 {
		t.Fatalf("promptHist[%d] = %v, want 5", idx, got)
	}
}

func TestBuildSnapshotUsesPromptEvalDurationForTTFT(t *testing.T) {
	a := NewAggregator(true)
	now := time.Now()

	a.latestSnapshot = ollama.Snapshot{
		Connected: true,
		Models: []ollama.ModelInfo{{
			Name: "deepseek-r1:8b",
		}},
	}
	a.modelTokSec["deepseek-r1:8b"] = &modelMetrics{
		lastMetrics: capture.EvalMetrics{
			Model:              "deepseek-r1:8b",
			PromptEvalDuration: 340 * time.Millisecond,
		},
		lastSeen: now,
	}

	snap := a.buildSnapshot()
	if len(snap.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1", len(snap.Models))
	}
	if got := snap.Models[0].TTFT; got != 340*time.Millisecond {
		t.Fatalf("TTFT = %v, want 340ms", got)
	}
}

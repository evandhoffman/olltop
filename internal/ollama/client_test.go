package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":"0.6.2"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	ver, err := c.GetVersion(context.Background())
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if ver != "0.6.2" {
		t.Errorf("got version %q, want %q", ver, "0.6.2")
	}
}

func TestGetModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"models": [{
				"name": "deepseek-r1:8b",
				"model": "deepseek-r1:8b",
				"size": 23300000000,
				"size_vram": 23300000000,
				"digest": "abc123",
				"details": {
					"family": "deepseek",
					"parameter_size": "8B",
					"quantization_level": "Q4_K_M"
				},
				"expires_at": "2025-01-15T12:30:00Z"
			}]
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	models, err := c.GetModels(context.Background())
	if err != nil {
		t.Fatalf("GetModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	m := models[0]
	if m.Name != "deepseek-r1:8b" {
		t.Errorf("name = %q, want %q", m.Name, "deepseek-r1:8b")
	}
	if m.Size != 23300000000 {
		t.Errorf("size = %d, want 23300000000", m.Size)
	}
	if m.SizeVRAM != 23300000000 {
		t.Errorf("size_vram = %d, want 23300000000", m.SizeVRAM)
	}
	if m.Details.Family != "deepseek" {
		t.Errorf("family = %q, want %q", m.Details.Family, "deepseek")
	}
	if m.Details.QuantizationLevel != "Q4_K_M" {
		t.Errorf("quantization_level = %q, want %q", m.Details.QuantizationLevel, "Q4_K_M")
	}
	if m.ExpiresAt.IsZero() {
		t.Error("expires_at should not be zero")
	}
}

func TestGetModels_ConnectionError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1") // nothing listening
	_, err := c.GetModels(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestPoll_SendsSnapshots(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/version":
			w.Write([]byte(`{"version":"0.6.2"}`))
		case "/api/ps":
			callCount++
			w.Write([]byte(`{"models":[]}`))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	ch := make(chan Snapshot, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	go c.Poll(ctx, 50*time.Millisecond, ch)

	// collect snapshots
	var snapshots []Snapshot
	for s := range ch {
		snapshots = append(snapshots, s)
		if len(snapshots) >= 3 {
			cancel()
			break
		}
	}

	if len(snapshots) < 2 {
		t.Fatalf("expected at least 2 snapshots, got %d", len(snapshots))
	}
	for _, s := range snapshots {
		if !s.Connected {
			t.Error("expected connected=true")
		}
		if s.Version != "0.6.2" {
			t.Errorf("version = %q, want %q", s.Version, "0.6.2")
		}
	}
}

func TestPoll_HandlesDisconnect(t *testing.T) {
	c := NewClient("http://127.0.0.1:1") // nothing listening
	ch := make(chan Snapshot, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go c.Poll(ctx, 50*time.Millisecond, ch)

	s := <-ch
	if s.Connected {
		t.Error("expected connected=false for unreachable server")
	}
	if s.Error == nil {
		t.Error("expected non-nil error")
	}
}

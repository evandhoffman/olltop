package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Client is an HTTP client for the Ollama API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Ollama API client targeting the given base URL
// (e.g. "http://localhost:11434").
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// versionResponse is the JSON shape of GET /api/version.
type versionResponse struct {
	Version string `json:"version"`
}

// psResponse is the JSON shape of GET /api/ps.
type psResponse struct {
	Models []ModelInfo `json:"models"`
}

// GetVersion fetches the Ollama server version via GET /api/version.
func (c *Client) GetVersion(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return "", fmt.Errorf("creating version request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("version endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var v versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", fmt.Errorf("decoding version response: %w", err)
	}

	return v.Version, nil
}

// GetModels fetches the list of currently loaded models via GET /api/ps.
func (c *Client) GetModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/ps", nil)
	if err != nil {
		return nil, fmt.Errorf("creating ps request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ps endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var ps psResponse
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		return nil, fmt.Errorf("decoding ps response: %w", err)
	}

	return ps.Models, nil
}

// Poll continuously fetches model and version data from Ollama at the given
// interval and sends Snapshot values on ch. It runs until ctx is cancelled.
//
// On connection errors, Poll sends a Snapshot with Connected=false and the
// error, then keeps retrying. When a connection is (re-)established, it
// fetches the version and sets Connected=true.
func (c *Client) Poll(ctx context.Context, interval time.Duration, ch chan<- Snapshot) {
	var (
		connected bool
		version   string
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// poll once immediately, then on each tick
	poll := func() {
		models, err := c.GetModels(ctx)
		if err != nil {
			if connected {
				slog.Warn("lost connection to Ollama", "error", err)
			}
			connected = false
			ch <- Snapshot{
				Connected: false,
				Error:     err,
				Timestamp: time.Now(),
			}
			return
		}

		// (re-)fetch version on first connect or reconnect
		if !connected {
			v, err := c.GetVersion(ctx)
			if err != nil {
				slog.Warn("connected but failed to fetch version", "error", err)
				// still mark connected — models are available
			} else {
				version = v
				slog.Info("connected to Ollama", "version", version)
			}
			connected = true
		}

		ch <- Snapshot{
			Models:    models,
			Connected: true,
			Version:   version,
			Timestamp: time.Now(),
		}
	}

	poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

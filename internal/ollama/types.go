package ollama

import "time"

// ModelDetails contains the model metadata from Ollama's /api/ps response.
type ModelDetails struct {
	Family            string `json:"family"`
	ParameterSize     string `json:"parameter_size"`
	QuantizationLevel string `json:"quantization_level"`
}

// ModelInfo represents a single loaded model as reported by /api/ps.
type ModelInfo struct {
	Name      string       `json:"name"`
	Model     string       `json:"model"`
	Size      int64        `json:"size"`
	SizeVRAM  int64        `json:"size_vram"`
	Digest    string       `json:"digest"`
	Details   ModelDetails `json:"details"`
	ExpiresAt time.Time    `json:"expires_at"`
}

// Snapshot represents a point-in-time view of Ollama state from polling.
type Snapshot struct {
	Models    []ModelInfo
	Connected bool
	Version   string
	Error     error
	Timestamp time.Time
}

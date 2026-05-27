package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Embedder generates vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// OllamaEmbedder calls Ollama's /api/embed endpoint.
type OllamaEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

func NewOllamaEmbedder(baseURL, model string) *OllamaEmbedder {
	return &OllamaEmbedder{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{},
	}
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	type embedReq struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	type embedResp struct {
		Embeddings [][]float64 `json:"embeddings"`
	}

	body := embedReq{Model: e.model, Input: text}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", e.baseURL+"/api/embed", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed: %s", string(b))
	}

	var result embedResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ollama embed decode: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama embed: empty result")
	}

	return result.Embeddings[0], nil
}

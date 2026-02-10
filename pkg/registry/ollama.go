package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OllamaRegistry implements ModelRegistry for the Ollama model library.
type OllamaRegistry struct {
	// BaseURL overrides the Ollama library API URL.
	BaseURL string

	client *http.Client
}

func init() {
	Register("ollama", func() ModelRegistry { return &OllamaRegistry{} })
}

func (r *OllamaRegistry) Type() string { return "ollama" }

func (r *OllamaRegistry) httpClient() *http.Client {
	if r.client != nil {
		return r.client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (r *OllamaRegistry) baseURL() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return "https://ollama.com"
}

func (r *OllamaRegistry) List(ctx context.Context, filter ListFilter) ([]ModelEntry, error) {
	// Ollama library provides a search API
	apiURL := r.baseURL() + "/api/tags"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("Ollama API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Ollama API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Models []struct {
			Name       string `json:"name"`
			ModifiedAt string `json:"modified_at"`
			Size       int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Ollama response: %w", err)
	}

	var entries []ModelEntry
	for _, m := range result.Models {
		if filter.Query != "" && !strings.Contains(strings.ToLower(m.Name), strings.ToLower(filter.Query)) {
			continue
		}
		entry := ModelEntry{
			Name:      m.Name,
			Registry:  "ollama",
			Reference: "ollama://" + m.Name,
			Size:      m.Size,
		}
		if m.ModifiedAt != "" {
			if t, err := time.Parse(time.RFC3339, m.ModifiedAt); err == nil {
				entry.UpdatedAt = &t
			}
		}
		entries = append(entries, entry)
		if filter.Limit > 0 && len(entries) >= filter.Limit {
			break
		}
	}
	return entries, nil
}

func (r *OllamaRegistry) Pull(ctx context.Context, ref string, destPath string, _ PullOptions) error {
	// Ollama models are pulled by the ollama backend itself at runtime.
	return fmt.Errorf("Ollama pull is handled by the ollama backend at runtime (no external download needed)")
}

func (r *OllamaRegistry) Resolve(ctx context.Context, ref string) (*ModelMetadata, error) {
	modelName := strings.TrimPrefix(ref, "ollama://")

	apiURL := r.baseURL() + "/api/show"
	body := fmt.Sprintf(`{"name": "%s"}`, modelName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("Ollama API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Ollama API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ModelInfo map[string]interface{} `json:"model_info"`
		Details   struct {
			Format string `json:"format"`
		} `json:"details"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &ModelMetadata{
		Name:   modelName,
		Format: result.Details.Format,
	}, nil
}

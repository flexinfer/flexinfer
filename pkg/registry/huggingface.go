package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HuggingFaceRegistry implements ModelRegistry for the HuggingFace Hub.
type HuggingFaceRegistry struct {
	// Token is an optional HuggingFace API token for private models.
	Token string
	// BaseURL overrides the HuggingFace API URL (for testing or enterprise instances).
	BaseURL string

	client *http.Client
}

func init() {
	Register("huggingface", func() ModelRegistry { return &HuggingFaceRegistry{} })
}

func (r *HuggingFaceRegistry) Type() string { return "huggingface" }

func (r *HuggingFaceRegistry) httpClient() *http.Client {
	if r.client != nil {
		return r.client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (r *HuggingFaceRegistry) baseURL() string {
	if r.BaseURL != "" {
		return r.BaseURL
	}
	return "https://huggingface.co"
}

func (r *HuggingFaceRegistry) List(ctx context.Context, filter ListFilter) ([]ModelEntry, error) {
	apiURL := r.baseURL() + "/api/models"
	params := url.Values{}
	if filter.Query != "" {
		params.Set("search", filter.Query)
	}
	if filter.Limit > 0 {
		params.Set("limit", fmt.Sprintf("%d", filter.Limit))
	} else {
		params.Set("limit", "20")
	}
	if len(filter.Tags) > 0 {
		params.Set("tags", strings.Join(filter.Tags, ","))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	r.setAuth(req)

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("HuggingFace API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HuggingFace API returned %d: %s", resp.StatusCode, string(body))
	}

	var models []struct {
		ModelID    string   `json:"modelId"`
		Tags       []string `json:"tags"`
		LastModify string   `json:"lastModified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, fmt.Errorf("decode HuggingFace response: %w", err)
	}

	entries := make([]ModelEntry, 0, len(models))
	for _, m := range models {
		entry := ModelEntry{
			Name:      m.ModelID,
			Registry:  "huggingface",
			Reference: "HF://" + m.ModelID,
			Tags:      m.Tags,
		}
		if m.LastModify != "" {
			if t, err := time.Parse(time.RFC3339, m.LastModify); err == nil {
				entry.UpdatedAt = &t
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (r *HuggingFaceRegistry) Pull(ctx context.Context, ref string, destPath string, _ PullOptions) error {
	// HuggingFace pull is handled by huggingface_hub CLI or snapshot_download.
	// This is a metadata-only registry; actual download is done by the ModelCache controller.
	return fmt.Errorf("HuggingFace pull is handled by ModelCache controller (use ModelCache CR instead)")
}

func (r *HuggingFaceRegistry) Resolve(ctx context.Context, ref string) (*ModelMetadata, error) {
	modelID := strings.TrimPrefix(ref, "HF://")
	modelID = strings.TrimPrefix(modelID, "huggingface://")

	apiURL := r.baseURL() + "/api/models/" + modelID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	r.setAuth(req)

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("HuggingFace API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HuggingFace API returned %d: %s", resp.StatusCode, string(body))
	}

	var model struct {
		ModelID    string   `json:"modelId"`
		Sha        string   `json:"sha"`
		Tags       []string `json:"tags"`
		LastModify string   `json:"lastModified"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&model); err != nil {
		return nil, err
	}

	meta := &ModelMetadata{
		Name:   model.ModelID,
		Digest: model.Sha,
		Tags:   model.Tags,
		Format: "huggingface",
	}
	if model.LastModify != "" {
		if t, err := time.Parse(time.RFC3339, model.LastModify); err == nil {
			meta.CreatedAt = &t
		}
	}
	return meta, nil
}

func (r *HuggingFaceRegistry) setAuth(req *http.Request) {
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
}

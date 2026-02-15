package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func TestFlexInferClient_EmbedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Model != "BAAI/bge-large-en-v1.5" {
			t.Errorf("unexpected model: %s", req.Model)
		}

		resp := map[string]any{
			"data": []map[string]any{
				{"embedding": []float64{0.1, 0.2, 0.3, 0.4, 0.5}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewFlexInferClient(httpclient.NewDefault(), server.URL, "", "")

	vec, err := client.EmbedQuery(context.Background(), "test query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 5 {
		t.Errorf("expected 5 dimensions, got %d", len(vec))
	}
	if vec[0] != 0.1 {
		t.Errorf("expected first element 0.1, got %f", vec[0])
	}
}

func TestFlexInferClient_EmbedDocuments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			emb := make([]float64, 128)
			emb[0] = float64(i + 1)
			data[i] = map[string]any{"embedding": emb}
		}

		resp := map[string]any{"data": data}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewFlexInferClient(httpclient.NewDefault(), server.URL, "", "BAAI/bge-large-en-v1.5")

	texts := []string{"doc 1", "doc 2", "doc 3"}
	vecs, err := client.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 3 {
		t.Errorf("expected 3 vectors, got %d", len(vecs))
	}
	for i, vec := range vecs {
		if len(vec) != 128 {
			t.Errorf("vector %d: expected 128 dims, got %d", i, len(vec))
		}
		expected := float64(i + 1)
		if vec[0] != expected {
			t.Errorf("vector %d: expected first element %f, got %f", i, expected, vec[0])
		}
	}
}

func TestFlexInferClient_EmptyDocuments(t *testing.T) {
	client := NewFlexInferClient(httpclient.NewDefault(), "http://localhost:8080/v1", "", "")

	vecs, err := client.EmbedDocuments(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vecs) != 0 {
		t.Errorf("expected 0 vectors, got %d", len(vecs))
	}
}

func TestFlexInferClient_NameAndModel(t *testing.T) {
	client := NewFlexInferClient(httpclient.NewDefault(), "http://localhost:8080/v1", "", "")

	if client.Name() != "flexinfer" {
		t.Errorf("expected name 'flexinfer', got %q", client.Name())
	}
	if client.Model() != "BAAI/bge-large-en-v1.5" {
		t.Errorf("expected model 'BAAI/bge-large-en-v1.5', got %q", client.Model())
	}
}

func TestFlexInferClient_CustomModel(t *testing.T) {
	client := NewFlexInferClient(httpclient.NewDefault(), "http://localhost:8080/v1", "", "custom-model")

	if client.Model() != "custom-model" {
		t.Errorf("expected model 'custom-model', got %q", client.Model())
	}
}

func TestFlexInferClient_NoAPIKeyRequired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify a Bearer token is sent (placeholder)
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("expected Authorization header to be set")
		}

		resp := map[string]any{
			"data": []map[string]any{
				{"embedding": []float64{0.5, 0.5}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Empty API key should still work (placeholder is injected)
	client := NewFlexInferClient(httpclient.NewDefault(), server.URL, "", "test-model")

	vec, err := client.EmbedQuery(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error with no API key: %v", err)
	}
	if len(vec) != 2 {
		t.Errorf("expected 2 dimensions, got %d", len(vec))
	}
}

func TestFlexInferClient_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewFlexInferClient(httpclient.NewDefault(), server.URL, "", "")

	_, err := client.EmbedQuery(context.Background(), "test")
	if err == nil {
		t.Error("expected error for 503 response")
	}
}

func TestFlexInferClient_Interface(t *testing.T) {
	var _ Embedder = (*FlexInferClient)(nil)
}

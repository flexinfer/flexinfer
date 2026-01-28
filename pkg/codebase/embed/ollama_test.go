package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func TestOllamaClient_EmbedQuery(t *testing.T) {
	// Create a mock Ollama server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embeddings" {
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
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if req.Model != "nomic-embed-text" {
			t.Errorf("unexpected model: %s", req.Model)
		}

		// Return a mock embedding
		resp := map[string]any{
			"embedding": []float64{0.1, 0.2, 0.3, 0.4, 0.5},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(httpclient.NewDefault(), server.URL, "nomic-embed-text")

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

func TestOllamaClient_EmbedDocuments(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		var req struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Return different embeddings based on call count
		embedding := make([]float64, 128)
		embedding[0] = float64(callCount)

		resp := map[string]any{
			"embedding": embedding,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOllamaClient(httpclient.NewDefault(), server.URL, "nomic-embed-text")

	texts := []string{"doc 1", "doc 2", "doc 3"}
	vecs, err := client.EmbedDocuments(context.Background(), texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vecs) != 3 {
		t.Errorf("expected 3 vectors, got %d", len(vecs))
	}

	if callCount != 3 {
		t.Errorf("expected 3 API calls, got %d", callCount)
	}

	// Each vector should have a different first element
	for i, vec := range vecs {
		expected := float64(i + 1)
		if vec[0] != expected {
			t.Errorf("vector %d: expected first element %f, got %f", i, expected, vec[0])
		}
	}
}

func TestOllamaClient_EmptyDocuments(t *testing.T) {
	client := NewOllamaClient(httpclient.NewDefault(), "http://localhost:11434", "nomic-embed-text")

	vecs, err := client.EmbedDocuments(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(vecs) != 0 {
		t.Errorf("expected 0 vectors, got %d", len(vecs))
	}
}

func TestOllamaClient_NameAndModel(t *testing.T) {
	client := NewOllamaClient(httpclient.NewDefault(), "http://localhost:11434", "mxbai-embed-large")

	if client.Name() != "ollama" {
		t.Errorf("expected name 'ollama', got %q", client.Name())
	}

	if client.Model() != "mxbai-embed-large" {
		t.Errorf("expected model 'mxbai-embed-large', got %q", client.Model())
	}
}

func TestOllamaClient_DefaultModel(t *testing.T) {
	client := NewOllamaClient(httpclient.NewDefault(), "http://localhost:11434", "")

	if client.Model() != "nomic-embed-text" {
		t.Errorf("expected default model 'nomic-embed-text', got %q", client.Model())
	}
}

func TestOllamaClient_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer server.Close()

	client := NewOllamaClient(httpclient.NewDefault(), server.URL, "nonexistent-model")

	_, err := client.EmbedQuery(context.Background(), "test")
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestOllamaClient_Interface(t *testing.T) {
	// Verify interface compliance at compile time
	var _ Embedder = (*OllamaClient)(nil)
}

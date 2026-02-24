package coordinator

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestExtractFromEntries_EmptyInput(t *testing.T) {
	ext := &Extractor{}

	entities, relations, err := ext.ExtractFromEntries(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entities != nil || relations != nil {
		t.Fatal("expected nil results for empty input")
	}
}

func TestExtractFromEntries_ParsesLLMResponse(t *testing.T) {
	extractionJSON := `{
		"entities": [
			{"name": "auth.go", "entity_type": "file", "properties": {"language": "go"}},
			{"name": "AuthService", "entity_type": "service"}
		],
		"relations": [
			{"source": "AuthService", "target": "auth.go", "relation_type": "modifies"}
		]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(modelsResponse{Data: []ModelInfo{{ID: "test"}}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{Message: ChatMessage{Content: extractionJSON}},
			},
		})
	}))
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", 0, breaker, slog.Default())

	ext := &Extractor{
		client: client,
		config: DefaultConfig(),
		logger: slog.Default(),
	}

	entries := []bridge.ContextEntryInfo{
		{Entry: bridge.ContextEntry{ID: "e1", Title: "Modified auth.go", Content: "Changed AuthService"}},
	}

	entities, relations, err := ext.ExtractFromEntries(context.Background(), entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entities) != 2 {
		t.Fatalf("expected 2 entities, got %d", len(entities))
	}
	if entities[0].Name != "auth.go" {
		t.Errorf("expected first entity name auth.go, got %s", entities[0].Name)
	}
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].RelationType != "modifies" {
		t.Errorf("expected relation type modifies, got %s", relations[0].RelationType)
	}
}

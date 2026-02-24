package coordinator

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlanFromGoal_ParsesLLMResponse(t *testing.T) {
	planJSON := `{
		"name": "add-auth",
		"description": "Add user authentication to the API",
		"steps": [
			{
				"id": "step-1",
				"name": "Create auth middleware",
				"type": "tool",
				"description": "Write JWT auth middleware",
				"depends_on": []
			},
			{
				"id": "step-2",
				"name": "Review auth implementation",
				"type": "approval",
				"description": "Human reviews the auth code",
				"depends_on": ["step-1"]
			},
			{
				"id": "step-3",
				"name": "Run tests",
				"type": "tool",
				"description": "Execute test suite",
				"depends_on": ["step-2"]
			}
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
				{Message: ChatMessage{Content: planJSON}},
			},
		})
	}))
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", 0, breaker, slog.Default())

	planner := &Planner{
		client: client,
		config: DefaultConfig(),
		logger: slog.Default(),
	}

	plan, err := planner.PlanFromGoal(context.Background(), "Add user authentication", "project/main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Name != "add-auth" {
		t.Fatalf("expected name 'add-auth', got %s", plan.Name)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	if plan.Steps[1].Type != "approval" {
		t.Errorf("expected step 2 type 'approval', got %s", plan.Steps[1].Type)
	}
	if len(plan.Steps[2].DependsOn) != 1 || plan.Steps[2].DependsOn[0] != "step-2" {
		t.Errorf("expected step 3 to depend on step-2")
	}
}

func TestPlanFromGoal_EmptyName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(modelsResponse{Data: []ModelInfo{{ID: "test"}}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{Message: ChatMessage{Content: `{"name": "", "steps": []}`}},
			},
		})
	}))
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", 0, breaker, slog.Default())

	planner := &Planner{
		client: client,
		config: DefaultConfig(),
		logger: slog.Default(),
	}

	_, err := planner.PlanFromGoal(context.Background(), "Do something", "")
	if err == nil {
		t.Fatal("expected error for empty workflow name")
	}
}

func TestPlanFromGoal_NoSteps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(modelsResponse{Data: []ModelInfo{{ID: "test"}}})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []ChatCompletionChoice{
				{Message: ChatMessage{Content: `{"name": "test-wf", "steps": []}`}},
			},
		})
	}))
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", 0, breaker, slog.Default())

	planner := &Planner{
		client: client,
		config: DefaultConfig(),
		logger: slog.Default(),
	}

	_, err := planner.PlanFromGoal(context.Background(), "Do something", "")
	if err == nil {
		t.Fatal("expected error for empty steps")
	}
}

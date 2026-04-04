package weaver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/flexinfer"
)

// --- Shared test helpers ---

// newTestRouter creates a Router wired to a fake FlexInfer, caller, and lister.
// The FlexInfer mock delegates to the responseFunc for /v1/chat/completions.
func newTestRouter(t *testing.T, responseFunc func(req chatCompletionRequestWithTools, callIdx int) chatCompletionResponseWithTools) (*Router, *httptest.Server, *fakeCaller) {
	t.Helper()

	var callIdx int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test-model", "object": "model"}},
			})
		case "/v1/chat/completions":
			var req chatCompletionRequestWithTools
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			idx := int(atomic.AddInt64(&callIdx, 1))
			resp := responseFunc(req, idx)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())

	caller := &fakeCaller{
		response: json.RawMessage(`{"content":[{"type":"text","text":"tool result ok"}]}`),
	}
	executor := NewDaemonToolExecutor(caller, 30*time.Second)

	lister := &fakeToolLister{
		tools: allTestTools(),
	}

	cfg := Config{
		Enabled:        true,
		RouterModel:    "test-model",
		SubagentModel:  "test-model",
		MaxIterations:  5,
		TokenBudget:    4096,
		Timeout:        10 * time.Second,
		MaxConcurrent:  4,
		HTTPTimeout:    60 * time.Second,
		ModelBehaviors: DefaultModelBehaviors(),
	}

	router := NewRouter(cfg, client, executor, lister, slog.Default())
	return router, server, caller
}

// allTestTools returns a broad set of tools covering all default domains.
func allTestTools() []ToolInfo {
	return []ToolInfo{
		// codebase
		{Name: "git__git_status", Description: "Git status"},
		{Name: "git__git_diff", Description: "Git diff"},
		{Name: "git__git_log", Description: "Git log"},
		{Name: "git__git_show", Description: "Git show"},
		{Name: "git__git_branch", Description: "Git branch"},
		{Name: "codebase_memory__codebase_search", Description: "Semantic search"},
		{Name: "codebase_memory__codebase_get_definition", Description: "Get definition"},
		{Name: "codebase_memory__codebase_find_callers", Description: "Find callers"},
		// cluster-ops
		{Name: "k8s_apps_k3s__k8s_getPods", Description: "Get pods"},
		{Name: "k8s_apps_k3s__k8s_get", Description: "Get resources"},
		{Name: "k8s_apps_k3s__k8s_describe", Description: "Describe resources"},
		{Name: "k8s_apps_k3s__k8s_logs", Description: "Get logs"},
		{Name: "k8s_apps_k3s__k8s_listNamespaces", Description: "List namespaces"},
		{Name: "ops_mcp__k8s_get_nodes", Description: "Get nodes"},
		// ci-pipeline
		{Name: "gitlab__list_pipelines", Description: "List pipelines"},
		{Name: "gitlab__get_pipeline", Description: "Get pipeline"},
		{Name: "gitlab__list_merge_requests", Description: "List MRs"},
		{Name: "gitlab__pipeline_summary", Description: "Pipeline summary"},
		{Name: "gitlab__list_pipeline_jobs", Description: "List jobs"},
		{Name: "gitlab__get_job_trace", Description: "Get job trace"},
		// observability
		{Name: "prometheus__query", Description: "Query prometheus"},
		{Name: "prometheus__list_alerts", Description: "List alerts"},
		{Name: "grafana__grafana_search", Description: "Search dashboards"},
		{Name: "alertmanager__am_list_alerts", Description: "List AM alerts"},
		{Name: "loki__loki_query_range", Description: "Loki query range"},
		{Name: "loki__loki_labels", Description: "Loki labels"},
		// infra-ops
		{Name: "flux__flux_get_kustomizations", Description: "Get kustomizations"},
		{Name: "flux__flux_get_helmreleases", Description: "Get Helm releases"},
		{Name: "flux__flux_logs", Description: "Flux logs"},
		{Name: "helm__helm_list", Description: "List Helm"},
		{Name: "helm__helm_status", Description: "Helm status"},
		// agent-fleet
		{Name: "agent_context__agent_presence_list", Description: "List presence"},
		{Name: "agent_context__agent_session_list", Description: "List sessions"},
		{Name: "agent_context__agent_task_list", Description: "List tasks"},
		{Name: "agent_context__agent_recall", Description: "Recall context"},
	}
}

// terminalResponse returns a simple text-only completion response.
func terminalResponse(text string) chatCompletionResponseWithTools {
	return chatCompletionResponseWithTools{
		ID:    "resp-test",
		Model: "test-model",
		Choices: []chatCompletionChoiceWithTools{{
			Message:      chatMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
		Usage: chatCompletionUsage{PromptTokens: 50, CompletionTokens: 20, TotalTokens: 70},
	}
}

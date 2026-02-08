package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mcperror"
)

func TestMain(m *testing.M) {
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// mustParseJSON extracts the JSON from a CallToolResult's first content block.
func mustParseJSON(t *testing.T, result any) map[string]any {
	t.Helper()
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var wrapper struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &wrapper); err != nil {
		t.Fatalf("unmarshal wrapper: %v", err)
	}
	if len(wrapper.Content) == 0 {
		t.Fatal("no content blocks in result")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal result JSON: %v (raw: %s)", err, wrapper.Content[0].Text)
	}
	return out
}

// setZepEnv configures env vars to point at the test server.
func setZepEnv(t *testing.T, tsURL string) {
	t.Helper()
	t.Setenv("ZEP_API_KEY", "test-api-key")
	t.Setenv("ZEP_API_URL", tsURL)
}

// =====================================================================
// getConfig tests
// =====================================================================

func TestGetConfig_MissingAPIKey(t *testing.T) {
	t.Setenv("ZEP_API_KEY", "")
	t.Setenv("ZEP_API_URL", "")

	_, _, err := getConfig()
	if err == nil {
		t.Fatal("expected error when ZEP_API_KEY is missing")
	}
	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if !strings.Contains(mcpErr.Message, "ZEP_API_KEY") {
		t.Fatalf("error should mention ZEP_API_KEY: %q", mcpErr.Message)
	}
}

func TestGetConfig_DefaultURL(t *testing.T) {
	t.Setenv("ZEP_API_KEY", "test-key")
	t.Setenv("ZEP_API_URL", "")

	apiURL, apiKey, err := getConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apiKey != "test-key" {
		t.Fatalf("expected apiKey %q, got %q", "test-key", apiKey)
	}
	if apiURL != "https://api.getzep.com" {
		t.Fatalf("expected default URL, got %q", apiURL)
	}
}

func TestGetConfig_CustomURL(t *testing.T) {
	t.Setenv("ZEP_API_KEY", "test-key")
	t.Setenv("ZEP_API_URL", "http://localhost:8080/")

	apiURL, _, err := getConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if apiURL != "http://localhost:8080" {
		t.Fatalf("expected trailing slash stripped, got %q", apiURL)
	}
}

// =====================================================================
// handleHealth tests
// =====================================================================

func TestHandleHealth_MissingConfig(t *testing.T) {
	t.Setenv("ZEP_API_KEY", "")
	t.Setenv("ZEP_API_URL", "")

	result, err := handleHealth(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data := mustParseJSON(t, result)
	if data["ok"] != false {
		t.Fatalf("expected ok=false, got %v", data["ok"])
	}
}

func TestHandleHealth_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("missing or wrong Authorization header: %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)

	result, err := handleHealth(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data := mustParseJSON(t, result)
	if data["ok"] != true {
		t.Fatalf("expected ok=true, got %v", data["ok"])
	}
}

func TestHandleHealth_AllEndpoints500_FallbackSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "health") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Fallback /v2/sessions returns 200
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)

	result, err := handleHealth(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data := mustParseJSON(t, result)
	if data["ok"] != true {
		t.Fatalf("expected ok=true from fallback, got %v", data["ok"])
	}
}

func TestHandleHealth_AllEndpointsFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)

	result, err := handleHealth(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data := mustParseJSON(t, result)
	if data["ok"] != false {
		t.Fatalf("expected ok=false when all endpoints fail, got %v", data["ok"])
	}
}

// =====================================================================
// handleAddMessages tests
// =====================================================================

func TestHandleAddMessages_MissingRequired(t *testing.T) {
	setZepEnv(t, "http://unused")

	result, err := handleAddMessages(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required params")
	}
}

func TestHandleAddMessages_Success(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)
	// Override package-level httpClient for this test
	origClient := httpClient
	httpClient = httpclient.NewDefault()
	t.Cleanup(func() { httpClient = origClient })

	result, err := handleAddMessages(context.Background(), map[string]any{
		"session_id": "sess-123",
		"messages":   []any{"Hello", "Hi there"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["ok"] != true {
		t.Fatalf("expected ok=true, got %v", data["ok"])
	}
	if data["count"] != float64(2) {
		t.Fatalf("expected count=2, got %v", data["count"])
	}
	if gotPath != "/v2/sessions/sess-123/memory" {
		t.Fatalf("expected path /v2/sessions/sess-123/memory, got %q", gotPath)
	}

	// Verify message roles alternate user/assistant
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected 2 messages in body, got %v", gotBody["messages"])
	}
	msg0 := msgs[0].(map[string]any)
	msg1 := msgs[1].(map[string]any)
	if msg0["role_type"] != "user" {
		t.Fatalf("expected first message role_type=user, got %q", msg0["role_type"])
	}
	if msg1["role_type"] != "assistant" {
		t.Fatalf("expected second message role_type=assistant, got %q", msg1["role_type"])
	}
}

func TestHandleAddMessages_CustomRoles(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)
	origClient := httpClient
	httpClient = httpclient.NewDefault()
	t.Cleanup(func() { httpClient = origClient })

	result, err := handleAddMessages(context.Background(), map[string]any{
		"session_id": "sess-456",
		"messages":   []any{"System prompt", "User message"},
		"roles":      []any{"system", "user"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["ok"] != true {
		t.Fatalf("expected ok=true, got %v", data["ok"])
	}

	msgs := gotBody["messages"].([]any)
	msg0 := msgs[0].(map[string]any)
	msg1 := msgs[1].(map[string]any)
	if msg0["role_type"] != "system" {
		t.Fatalf("expected first message role_type=system, got %q", msg0["role_type"])
	}
	if msg1["role_type"] != "user" {
		t.Fatalf("expected second message role_type=user, got %q", msg1["role_type"])
	}
}

func TestHandleAddMessages_EmptyMessagesSkipped(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)
	origClient := httpClient
	httpClient = httpclient.NewDefault()
	t.Cleanup(func() { httpClient = origClient })

	result, err := handleAddMessages(context.Background(), map[string]any{
		"session_id": "sess-789",
		"messages":   []any{"Hello", "", "World"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["count"] != float64(2) {
		t.Fatalf("expected count=2 (empty message skipped), got %v", data["count"])
	}
}

func TestHandleAddMessages_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"error":"invalid session"}`)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)
	origClient := httpClient
	httpClient = httpclient.NewDefault()
	t.Cleanup(func() { httpClient = origClient })

	_, err := handleAddMessages(context.Background(), map[string]any{
		"session_id": "bad-session",
		"messages":   []any{"Hello"},
	})
	if err == nil {
		t.Fatal("expected error for 422 response")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if !strings.Contains(mcpErr.Message, "Zep") {
		t.Fatalf("error should mention Zep: %q", mcpErr.Message)
	}
}

// =====================================================================
// handleGetMessages tests
// =====================================================================

func TestHandleGetMessages_MissingRequired(t *testing.T) {
	setZepEnv(t, "http://unused")

	result, err := handleGetMessages(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected IsError=true for missing required params")
	}
}

func TestHandleGetMessages_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/v2/sessions/sess-123/memory") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("lastK") != "5" {
			t.Errorf("expected lastK=5, got %q", r.URL.Query().Get("lastK"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"messages": []map[string]string{
				{"role": "user", "role_type": "user", "content": "Hello"},
				{"role": "assistant", "role_type": "assistant", "content": "Hi!"},
			},
		})
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)
	origClient := httpClient
	httpClient = httpclient.NewDefault()
	t.Cleanup(func() { httpClient = origClient })

	result, err := handleGetMessages(context.Background(), map[string]any{
		"session_id": "sess-123",
		"last_k":     float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := mustParseJSON(t, result)
	if data["ok"] != true {
		t.Fatalf("expected ok=true, got %v", data["ok"])
	}
	if data["count"] != float64(2) {
		t.Fatalf("expected count=2, got %v", data["count"])
	}
	msgs, ok := data["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %v", data["messages"])
	}
}

func TestHandleGetMessages_DefaultLastK(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"messages": []any{}})
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)
	origClient := httpClient
	httpClient = httpclient.NewDefault()
	t.Cleanup(func() { httpClient = origClient })

	_, err := handleGetMessages(context.Background(), map[string]any{
		"session_id": "sess-default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "lastK=10") {
		t.Fatalf("expected default lastK=10, got query %q", gotQuery)
	}
}

func TestHandleGetMessages_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"session not found"}`)
	}))
	defer ts.Close()

	setZepEnv(t, ts.URL)
	origClient := httpClient
	httpClient = httpclient.NewDefault()
	t.Cleanup(func() { httpClient = origClient })

	_, err := handleGetMessages(context.Background(), map[string]any{
		"session_id": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T: %v", err, err)
	}
	if !strings.Contains(mcpErr.Message, "Zep") {
		t.Fatalf("error should mention Zep: %q", mcpErr.Message)
	}
}

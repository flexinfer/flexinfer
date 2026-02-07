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
)

func TestMain(m *testing.M) {
	// Force JSON output format so test assertions can parse the result text.
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// newTestPromServer creates a promServer pointing at the given httptest.Server.
func newTestPromServer(ts *httptest.Server) *promServer {
	return &promServer{
		url:        ts.URL,
		httpClient: httpclient.NewDefault(),
	}
}

// jsonHandler returns an http.HandlerFunc that writes the given JSON body with 200 OK.
func jsonHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, body)
	}
}

func TestPromRequest_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "access denied by policy")
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	_, err := prom.request(context.Background(), "/api/v1/query", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Prometheus") {
		t.Fatalf("error did not mention Prometheus: %q", msg)
	}
	if !strings.Contains(msg, "forbidden") {
		t.Fatalf("error did not include forbidden status info: %q", msg)
	}
}

func TestHandleQuery(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": {
			"resultType": "vector",
			"result": [
				{
					"metric": {"__name__": "up", "job": "prometheus"},
					"value": [1700000000, "1"]
				}
			]
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query().Get("query")
		if q != "up" {
			t.Errorf("unexpected query param: %s", q)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleQuery(context.Background(), map[string]any{
		"query": "up",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	// Verify the JSON content contains the expected data
	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["status"] != "success" {
		t.Fatalf("expected status=success, got %v", parsed["status"])
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data to be a map")
	}
	if data["resultType"] != "vector" {
		t.Fatalf("expected resultType=vector, got %v", data["resultType"])
	}
}

func TestHandleQuery_MissingRequired(t *testing.T) {
	ts := httptest.NewServer(jsonHandler(`{"status":"success","data":{}}`))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleQuery(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	// Missing required "query" should produce an error result
	if !result.IsError {
		t.Fatal("expected IsError=true for missing required field")
	}
}

func TestHandleQueryRange(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": {
			"resultType": "matrix",
			"result": [
				{
					"metric": {"__name__": "up", "job": "prometheus"},
					"values": [[1700000000, "1"], [1700000060, "1"]]
				}
			]
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("query") != "up" {
			t.Errorf("unexpected query: %s", q.Get("query"))
		}
		if q.Get("start") != "2024-01-01T00:00:00Z" {
			t.Errorf("unexpected start: %s", q.Get("start"))
		}
		if q.Get("step") != "5m" {
			t.Errorf("unexpected step: %s", q.Get("step"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleQueryRange(context.Background(), map[string]any{
		"query": "up",
		"start": "2024-01-01T00:00:00Z",
		"end":   "2024-01-02T00:00:00Z",
		"step":  "5m",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["status"] != "success" {
		t.Fatalf("expected status=success, got %v", parsed["status"])
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data to be a map")
	}
	if data["resultType"] != "matrix" {
		t.Fatalf("expected resultType=matrix, got %v", data["resultType"])
	}
}

func TestHandleQueryRange_MissingRequired(t *testing.T) {
	ts := httptest.NewServer(jsonHandler(`{"status":"success","data":{}}`))
	defer ts.Close()

	prom := newTestPromServer(ts)

	// Missing both "query" and "start"
	result, err := prom.handleQueryRange(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for missing required fields")
	}
}

func TestHandleListMetrics(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": [
			"up",
			"process_cpu_seconds_total",
			"go_goroutines",
			"node_cpu_seconds_total",
			"node_memory_MemTotal_bytes"
		]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/label/__name__/values" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	// Test without filter
	result, err := prom.handleListMetrics(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	data, ok := parsed["data"].([]any)
	if !ok {
		t.Fatal("expected data to be an array")
	}
	if len(data) != 5 {
		t.Fatalf("expected 5 metrics, got %d", len(data))
	}
}

func TestHandleListMetrics_WithFilter(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": [
			"up",
			"process_cpu_seconds_total",
			"go_goroutines",
			"node_cpu_seconds_total",
			"node_memory_MemTotal_bytes"
		]
	}`

	ts := httptest.NewServer(jsonHandler(mockResp))
	defer ts.Close()

	prom := newTestPromServer(ts)

	// Test with regex filter matching only "node_*" metrics
	result, err := prom.handleListMetrics(context.Background(), map[string]any{
		"match": "^node_",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	data, ok := parsed["data"].([]any)
	if !ok {
		t.Fatal("expected data to be an array")
	}
	if len(data) != 2 {
		t.Fatalf("expected 2 filtered metrics, got %d: %v", len(data), data)
	}
	for _, m := range data {
		name := m.(string)
		if !strings.HasPrefix(name, "node_") {
			t.Fatalf("unexpected metric after filter: %s", name)
		}
	}
}

func TestHandleListAlerts(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": {
			"alerts": [
				{
					"labels": {"alertname": "HighMemory", "severity": "warning"},
					"annotations": {"summary": "Memory usage above 90%"},
					"state": "firing",
					"activeAt": "2024-01-01T00:00:00Z",
					"value": "92.5"
				},
				{
					"labels": {"alertname": "DiskFull", "severity": "critical"},
					"annotations": {"summary": "Disk nearly full"},
					"state": "firing",
					"activeAt": "2024-01-01T01:00:00Z",
					"value": "98.1"
				}
			]
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/alerts" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleListAlerts(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	if parsed["status"] != "success" {
		t.Fatalf("expected status=success, got %v", parsed["status"])
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data to be a map")
	}
	alerts, ok := data["alerts"].([]any)
	if !ok {
		t.Fatal("expected alerts to be an array")
	}
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}

	// Check first alert details
	first := alerts[0].(map[string]any)
	labels := first["labels"].(map[string]any)
	if labels["alertname"] != "HighMemory" {
		t.Fatalf("expected alertname=HighMemory, got %v", labels["alertname"])
	}
}

func TestHandleListLabels(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": ["__name__", "instance", "job", "namespace", "pod"]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/labels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleListLabels(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	data, ok := parsed["data"].([]any)
	if !ok {
		t.Fatal("expected data to be an array")
	}
	if len(data) != 5 {
		t.Fatalf("expected 5 labels, got %d", len(data))
	}
}

func TestHandleLabelValues(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": ["prometheus", "node-exporter", "kube-state-metrics"]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/label/job/values" {
			t.Errorf("unexpected path: %s, want /api/v1/label/job/values", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleLabelValues(context.Background(), map[string]any{
		"label": "job",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	data, ok := parsed["data"].([]any)
	if !ok {
		t.Fatal("expected data to be an array")
	}
	if len(data) != 3 {
		t.Fatalf("expected 3 label values, got %d", len(data))
	}
}

func TestHandleListTargets(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": {
			"activeTargets": [
				{
					"discoveredLabels": {"__address__": "localhost:9090", "job": "prometheus"},
					"labels": {"instance": "localhost:9090", "job": "prometheus"},
					"scrapePool": "prometheus",
					"scrapeUrl": "http://localhost:9090/metrics",
					"health": "up",
					"lastScrape": "2024-01-01T00:00:00Z",
					"lastScrapeDuration": 0.005
				}
			],
			"droppedTargets": []
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/targets" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		// Default state should be "active"
		if s := r.URL.Query().Get("state"); s != "active" {
			t.Errorf("expected state=active, got %q", s)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleListTargets(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data to be a map")
	}
	active, ok := data["activeTargets"].([]any)
	if !ok {
		t.Fatal("expected activeTargets to be an array")
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active target, got %d", len(active))
	}
}

func TestHandleListRules(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": {
			"groups": [
				{
					"name": "test-group",
					"rules": [
						{
							"name": "HighMemory",
							"type": "alerting",
							"query": "node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes < 0.1"
						}
					]
				}
			]
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/rules" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if ruleType := r.URL.Query().Get("type"); ruleType != "alert" {
			t.Errorf("expected type=alert, got %q", ruleType)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleListRules(context.Background(), map[string]any{
		"type": "alert",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data to be a map")
	}
	groups, ok := data["groups"].([]any)
	if !ok {
		t.Fatal("expected groups to be an array")
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 rule group, got %d", len(groups))
	}
}

func TestHandleRuntimeInfo(t *testing.T) {
	mockResp := `{
		"status": "success",
		"data": {
			"startTime": "2024-01-01T00:00:00Z",
			"CWD": "/prometheus",
			"reloadConfigSuccess": true,
			"lastConfigTime": "2024-01-01T00:00:00Z",
			"goroutineCount": 42,
			"GOMAXPROCS": 4,
			"GOGC": "",
			"GODEBUG": "",
			"storageRetention": "15d"
		}
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/status/runtimeinfo" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, mockResp)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleRuntimeInfo(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("result should not be an error")
	}

	text := result.Content[0].Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatal("expected data to be a map")
	}
	if data["storageRetention"] != "15d" {
		t.Fatalf("expected storageRetention=15d, got %v", data["storageRetention"])
	}
}

func TestPromRequest_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "internal server error")
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	_, err := prom.request(context.Background(), "/api/v1/query", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Prometheus") {
		t.Fatalf("error did not mention Prometheus: %q", msg)
	}
	if !strings.Contains(msg, "unavailable") {
		t.Fatalf("error did not include unavailable status info: %q", msg)
	}
}

func TestHandleQuery_HTTPError_ReturnsErrorResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"status":"error","error":"parse error"}`)
	}))
	defer ts.Close()

	prom := newTestPromServer(ts)

	result, err := prom.handleQuery(context.Background(), map[string]any{
		"query": "invalid{{{",
	})
	if err != nil {
		t.Fatalf("handleQuery should not return error, got: %v", err)
	}
	// handleQuery wraps request errors in mcp.ErrorResult
	if !result.IsError {
		t.Fatal("expected IsError=true for HTTP error response")
	}
}

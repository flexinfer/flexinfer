package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeCaller struct {
	last requestOptions
	resp *jobsearchResponse
	err  error
}

func (f *fakeCaller) Request(ctx context.Context, opts requestOptions) (*jobsearchResponse, error) {
	f.last = opts
	if f.err != nil {
		return nil, f.err
	}
	if f.resp != nil {
		return f.resp, nil
	}
	return &jobsearchResponse{StatusCode: 200, ContentType: "application/json", Data: map[string]any{"ok": true}}, nil
}

func parseToolJSON(t *testing.T, result any) map[string]any {
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
		t.Fatal("expected non-empty content")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(wrapper.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal content JSON: %v", err)
	}
	return out
}

func TestHandleAPICall_RequiresConfirmWriteForMutating(t *testing.T) {
	s := &jobsearchServer{
		client:                  &fakeCaller{},
		defaultMaxResponseBytes: 1024,
	}

	result, err := s.handleAPICall(context.Background(), map[string]any{
		"method": "POST",
		"path":   "/entities",
		"body":   map[string]any{"title": "x"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when confirm_write is missing for mutating request")
	}
}

func TestHandleAPICall_Success(t *testing.T) {
	fc := &fakeCaller{resp: &jobsearchResponse{StatusCode: 200, ContentType: "application/json", Data: map[string]any{"ok": true}}}
	s := &jobsearchServer{
		client:                  fc,
		defaultMaxResponseBytes: 1024,
	}

	result, err := s.handleAPICall(context.Background(), map[string]any{
		"method": "GET",
		"path":   "/health",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	if fc.last.Path != "/health" {
		t.Fatalf("expected path /health, got %q", fc.last.Path)
	}
	parsed := parseToolJSON(t, result)
	if parsed["status"] != float64(200) {
		t.Fatalf("expected status 200, got %v", parsed["status"])
	}
}

func TestHandleEndpointSpec_RequiresConfirmForDestructive(t *testing.T) {
	s := &jobsearchServer{
		client:                  &fakeCaller{},
		defaultMaxResponseBytes: 1024,
	}
	spec := endpointToolSpec{
		Name:         "jobsearch_entities_delete",
		Method:       httpMethodDelete,
		PathTemplate: "/entities/{entity_id}",
		PathArgs:     []string{"entity_id"},
		ConfirmField: "confirm",
	}

	result, err := s.handleEndpointSpec(context.Background(), spec, map[string]any{"entity_id": "123"})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when confirm is missing")
	}

	result, err = s.handleEndpointSpec(context.Background(), spec, map[string]any{"entity_id": "123", "confirm": false})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when confirm=false")
	}
}

func TestHandleEndpointSpec_IntegrationStyleSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/entities/abc" {
			t.Errorf("expected path /entities/abc, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "abc"})
	}))
	defer ts.Close()

	t.Setenv("JOBSEARCH_API_URL", ts.URL)
	t.Setenv("JOBSEARCH_API_TOKEN", "token-abc")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_ID", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_SECRET", "")
	t.Setenv("CF_ACCESS_CLIENT_ID", "")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "")

	client, err := newJobsearchClientFromEnv(testLogger())
	if err != nil {
		t.Fatalf("unexpected client error: %v", err)
	}

	s := &jobsearchServer{client: client, defaultMaxResponseBytes: 1024}
	spec := endpointToolSpec{
		Name:         "jobsearch_entities_get",
		Method:       httpMethodGet,
		PathTemplate: "/entities/{entity_id}",
		PathArgs:     []string{"entity_id"},
	}

	result, err := s.handleEndpointSpec(context.Background(), spec, map[string]any{"entity_id": "abc"})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	parsed := parseToolJSON(t, result)
	if parsed["status"] != float64(200) {
		t.Fatalf("expected status 200, got %v", parsed["status"])
	}
}

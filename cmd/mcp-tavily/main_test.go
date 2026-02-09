package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func TestTavilyRequest_SendsAPIKeyAndParsesJSON(t *testing.T) {
	tavilyKey := "tvly-test-key"
	var gotAPIKey string
	var gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method: got %q, want %q", r.Method, http.MethodPost)
		}
		if r.URL.Path != "/search" {
			t.Fatalf("path: got %q, want %q", r.URL.Path, "/search")
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		gotAPIKey, _ = payload["api_key"].(string)
		gotQuery, _ = payload["query"].(string)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"results":[{"title":"t"}]}`))
	}))
	t.Cleanup(srv.Close)

	tav := &tavilyServer{
		apiKey:     tavilyKey,
		baseURL:    srv.URL,
		httpClient: httpclient.New(httpclient.DefaultConfig()),
	}

	got, err := tav.request(context.Background(), "/search", map[string]any{"query": "hello"})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if gotAPIKey != tavilyKey {
		t.Fatalf("api_key: got %q, want %q", gotAPIKey, tavilyKey)
	}
	if gotQuery != "hello" {
		t.Fatalf("query: got %q, want %q", gotQuery, "hello")
	}
	if _, ok := got["results"]; !ok {
		t.Fatalf("expected results key in response, got: %v", got)
	}
}

func TestTavilyRequest_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	t.Cleanup(srv.Close)

	tav := &tavilyServer{
		apiKey:     "bad",
		baseURL:    srv.URL,
		httpClient: httpclient.New(httpclient.DefaultConfig()),
	}

	_, err := tav.request(context.Background(), "/search", map[string]any{"query": "x"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("error: got %q, want to contain %q", err.Error(), "authentication failed")
	}
}

func TestTavilyRequest_TruncatedResponse(t *testing.T) {
	t.Setenv("TAVILY_MAX_RESPONSE_BYTES", "32")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Large-ish JSON payload (larger than 32 bytes)
		_, _ = w.Write([]byte(`{"ok":true,"data":"` + strings.Repeat("x", 128) + `"}`))
	}))
	t.Cleanup(srv.Close)

	tav := &tavilyServer{
		apiKey:     "k",
		baseURL:    srv.URL,
		httpClient: httpclient.New(httpclient.DefaultConfig()),
	}

	_, err := tav.request(context.Background(), "/search", map[string]any{"query": "x"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "response exceeded") {
		t.Fatalf("error: got %q, want to contain %q", err.Error(), "response exceeded")
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRequest_InjectsAuthAndCloudflareHeaders(t *testing.T) {
	var gotAuth string
	var gotCFID string
	var gotCFSecret string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCFID = r.Header.Get("CF-Access-Client-Id")
		gotCFSecret = r.Header.Get("CF-Access-Client-Secret")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	t.Setenv("JOBSEARCH_API_URL", ts.URL)
	t.Setenv("JOBSEARCH_API_TOKEN", "token-abc")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_ID", "cf-id")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_SECRET", "cf-secret")
	t.Setenv("CF_ACCESS_CLIENT_ID", "")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "")

	client, err := newJobsearchClientFromEnv(testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.Request(context.Background(), requestOptions{Method: "GET", Path: "/health"})
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}

	if gotAuth != "Bearer token-abc" {
		t.Fatalf("expected auth header, got %q", gotAuth)
	}
	if gotCFID != "cf-id" {
		t.Fatalf("expected CF-Access-Client-Id, got %q", gotCFID)
	}
	if gotCFSecret != "cf-secret" {
		t.Fatalf("expected CF-Access-Client-Secret, got %q", gotCFSecret)
	}
}

func TestClientRequest_DoesNotInjectAuthHeaderWhenTokenUnset(t *testing.T) {
	var gotAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	t.Setenv("JOBSEARCH_API_URL", ts.URL)
	t.Setenv("JOBSEARCH_API_TOKEN", "")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_ID", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_SECRET", "")
	t.Setenv("CF_ACCESS_CLIENT_ID", "")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "")

	client, err := newJobsearchClientFromEnv(testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = client.Request(context.Background(), requestOptions{Method: "GET", Path: "/health"})
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}

	if gotAuth != "" {
		t.Fatalf("expected no auth header, got %q", gotAuth)
	}
}

func TestClientRequest_NoCloudflareHeadersOnPartialConfig(t *testing.T) {
	var gotCFID string
	var gotCFSecret string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCFID = r.Header.Get("CF-Access-Client-Id")
		gotCFSecret = r.Header.Get("CF-Access-Client-Secret")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer ts.Close()

	t.Setenv("JOBSEARCH_API_URL", ts.URL)
	t.Setenv("JOBSEARCH_API_TOKEN", "token-abc")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_ID", "only-id")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_SECRET", "")
	t.Setenv("CF_ACCESS_CLIENT_ID", "")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "")

	client, err := newJobsearchClientFromEnv(testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.hasCloudflareAccess {
		t.Fatal("expected Cloudflare header injection to be disabled with partial config")
	}

	_, err = client.Request(context.Background(), requestOptions{Method: "GET", Path: "/health"})
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}

	if gotCFID != "" || gotCFSecret != "" {
		t.Fatalf("expected no Cloudflare headers, got id=%q secret=%q", gotCFID, gotCFSecret)
	}
}

func TestClientRequest_NoCloudflareHeadersWhenUnset(t *testing.T) {
	var gotCFID string
	var gotCFSecret string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCFID = r.Header.Get("CF-Access-Client-Id")
		gotCFSecret = r.Header.Get("CF-Access-Client-Secret")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
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
		t.Fatalf("unexpected error: %v", err)
	}
	if client.hasCloudflareAccess {
		t.Fatal("expected Cloudflare header injection to be disabled when config is unset")
	}

	_, err = client.Request(context.Background(), requestOptions{Method: "GET", Path: "/health"})
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}

	if gotCFID != "" || gotCFSecret != "" {
		t.Fatalf("expected no Cloudflare headers, got id=%q secret=%q", gotCFID, gotCFSecret)
	}
}

func TestNewJobsearchClientFromEnv_PartialCloudflareConfigWarns(t *testing.T) {
	t.Setenv("JOBSEARCH_API_URL", "http://localhost:8000")
	t.Setenv("JOBSEARCH_API_TOKEN", "token-abc")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_ID", "only-id")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_SECRET", "")
	t.Setenv("CF_ACCESS_CLIENT_ID", "")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "")

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))

	client, err := newJobsearchClientFromEnv(logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.hasCloudflareAccess {
		t.Fatal("expected Cloudflare header injection to be disabled with partial config")
	}
	if !strings.Contains(logs.String(), "partial Cloudflare Access configuration detected") {
		t.Fatalf("expected partial Cloudflare warning, logs=%q", logs.String())
	}
}

func TestClientRequest_ParsesJSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"value": 42})
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
		t.Fatalf("unexpected error: %v", err)
	}

	resp, err := client.Request(context.Background(), requestOptions{Method: "GET", Path: "/health"})
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}

	obj, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected JSON object response, got %T", resp.Data)
	}
	if obj["value"] != float64(42) {
		t.Fatalf("expected value 42, got %v", obj["value"])
	}
}

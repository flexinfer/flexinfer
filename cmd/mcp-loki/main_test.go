package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func TestLokiAPIURL_NormalizesBasePath(t *testing.T) {
	cases := []struct {
		base     string
		endpoint string
		want     string
	}{
		{"https://loki.lan", "query", "https://loki.lan/loki/api/v1/query"},
		{"https://loki.lan/", "query", "https://loki.lan/loki/api/v1/query"},
		{"https://loki.lan/loki", "query", "https://loki.lan/loki/api/v1/query"},
		{"https://loki.lan/loki/", "query", "https://loki.lan/loki/api/v1/query"},
		{"https://loki.lan/loki/api/v1", "query", "https://loki.lan/loki/api/v1/query"},
		{"https://loki.lan/loki/api/v1/", "query", "https://loki.lan/loki/api/v1/query"},
	}

	for _, tc := range cases {
		got, err := lokiAPIURL(tc.base, tc.endpoint)
		if err != nil {
			t.Fatalf("lokiAPIURL(%q, %q) error: %v", tc.base, tc.endpoint, err)
		}
		if got != tc.want {
			t.Fatalf("lokiAPIURL(%q, %q) = %q, want %q", tc.base, tc.endpoint, got, tc.want)
		}
	}
}

func TestLokiRequest_NonJSONBodyIncludesSnippet(t *testing.T) {
	// Create test server that returns non-JSON response
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "permission denied")
	}))
	defer ts.Close()

	prevPortForward := portForward
	prevLokiURL := lokiURL
	prevHTTPClient := httpClient
	t.Cleanup(func() {
		portForward = prevPortForward
		lokiURL = prevLokiURL
		httpClient = prevHTTPClient
	})

	portForward = false
	lokiURL = ts.URL
	httpClient = httpclient.NewDefault()

	_, err := lokiRequest(context.Background(), "query_range", url.Values{"query": []string{"{job=\"test\"}"}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "not JSON") {
		t.Fatalf("error did not mention non-JSON response: %q", msg)
	}
	if !strings.Contains(msg, "permission denied") {
		t.Fatalf("error did not include body snippet: %q", msg)
	}
}

func TestLokiRequest_HTTPErrorIncludesSnippet(t *testing.T) {
	// Create test server that returns HTTP error
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "permission denied")
	}))
	defer ts.Close()

	prevPortForward := portForward
	prevLokiURL := lokiURL
	prevHTTPClient := httpClient
	t.Cleanup(func() {
		portForward = prevPortForward
		lokiURL = prevLokiURL
		httpClient = prevHTTPClient
	})

	portForward = false
	lokiURL = ts.URL
	httpClient = httpclient.NewDefault()

	_, err := lokiRequest(context.Background(), "query", url.Values{"query": []string{"{job=\"test\"}"}})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "loki API error 403") {
		t.Fatalf("error did not include HTTP status: %q", msg)
	}
	if !strings.Contains(msg, "permission denied") {
		t.Fatalf("error did not include body snippet: %q", msg)
	}
}

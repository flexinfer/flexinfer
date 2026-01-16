package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
	prevHTTPClientFactory := httpClientFactory
	t.Cleanup(func() {
		portForward = getEnvBool("LOKI_PORT_FORWARD", true)
		lokiURL = getEnv("LOKI_URL", "http://loki.logging.svc.cluster.local:3100")
		httpClientFactory = prevHTTPClientFactory
	})

	portForward = false
	lokiURL = "http://loki.example"
	httpClientFactory = func() *http.Client {
		return &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/loki/api/v1/query_range" {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
						Body:       io.NopCloser(strings.NewReader("not found")),
						Request:    r,
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader("permission denied")),
					Request:    r,
				}, nil
			}),
		}
	}

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
	prevHTTPClientFactory := httpClientFactory
	t.Cleanup(func() {
		portForward = getEnvBool("LOKI_PORT_FORWARD", true)
		lokiURL = getEnv("LOKI_URL", "http://loki.logging.svc.cluster.local:3100")
		httpClientFactory = prevHTTPClientFactory
	})

	portForward = false
	lokiURL = "http://loki.example"
	httpClientFactory = func() *http.Client {
		return &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
					Body:       io.NopCloser(strings.NewReader("permission denied")),
					Request:    r,
				}, nil
			}),
		}
	}

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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

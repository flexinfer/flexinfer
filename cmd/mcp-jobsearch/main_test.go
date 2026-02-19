package main

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestMain(m *testing.M) {
	_ = os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

func TestNewJobsearchClientFromEnv_MissingURL(t *testing.T) {
	t.Setenv("JOBSEARCH_API_URL", "")
	t.Setenv("JOBSEARCH_API_TOKEN", "test-token")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "")

	_, err := newJobsearchClientFromEnv(testLogger())
	if err == nil {
		t.Fatal("expected error when JOBSEARCH_API_URL is missing")
	}
}

func TestNewJobsearchClientFromEnv_MissingToken(t *testing.T) {
	t.Setenv("JOBSEARCH_API_URL", "http://localhost:8000")
	t.Setenv("JOBSEARCH_API_TOKEN", "")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "")

	_, err := newJobsearchClientFromEnv(testLogger())
	if err == nil {
		t.Fatal("expected error when token is missing")
	}
}

func TestNewJobsearchClientFromEnv_UsesFallbackToken(t *testing.T) {
	t.Setenv("JOBSEARCH_API_URL", "http://localhost:8000")
	t.Setenv("JOBSEARCH_API_TOKEN", "")
	t.Setenv("JOBSEARCH_BEARER_TOKEN", "fallback-token")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_ID", "")
	t.Setenv("JOBSEARCH_CF_ACCESS_CLIENT_SECRET", "")
	t.Setenv("CF_ACCESS_CLIENT_ID", "")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "")

	client, err := newJobsearchClientFromEnv(testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.token != "fallback-token" {
		t.Fatalf("expected fallback token to be used, got %q", client.token)
	}
}

package main

import (
	"errors"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

func TestDecodeToolJSONSupportsTOON(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "toon")

	res, err := mcp.JSONResult(map[string]any{
		"job_id": "job-123",
		"found":  true,
	})
	if err != nil {
		t.Fatalf("JSONResult: %v", err)
	}

	var decoded struct {
		JobID string `json:"job_id"`
		Found bool   `json:"found"`
	}
	if err := decodeToolJSON(res, &decoded); err != nil {
		t.Fatalf("decodeToolJSON: %v", err)
	}
	if decoded.JobID != "job-123" || !decoded.Found {
		t.Fatalf("decoded mismatch: %+v", decoded)
	}
}

func TestDecodeToolJSONSupportsJSON(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	res, err := mcp.JSONResult(map[string]any{
		"watch_id": "watch-123",
	})
	if err != nil {
		t.Fatalf("JSONResult: %v", err)
	}

	var decoded struct {
		WatchID string `json:"watch_id"`
	}
	if err := decodeToolJSON(res, &decoded); err != nil {
		t.Fatalf("decodeToolJSON: %v", err)
	}
	if decoded.WatchID != "watch-123" {
		t.Fatalf("decoded mismatch: %+v", decoded)
	}
}

func TestDecodeToolJSONReturnsEnvelopeErrors(t *testing.T) {
	err := decodeToolJSON(mcp.ErrorResult(errors.New("benchmark blocked")), &struct{}{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); got != "benchmark blocked" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestWatchRepoIDFallsBackToFixtureName(t *testing.T) {
	got := watchRepoID(benchConfig{FixtureRoot: "pkg/codebase/testdata/mixedrepo"})
	if got != "mixedrepo-watch" {
		t.Fatalf("unexpected repo id: %q", got)
	}
}

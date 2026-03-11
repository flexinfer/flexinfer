package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

func TestParseToolResultMap_DecodesTOONEnvelope(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "toon")

	res, err := mcp.JSONResult(map[string]any{
		"running":         1,
		"projects":        []string{"loom-core"},
		"total_sandboxes": 1,
	})
	if err != nil {
		t.Fatalf("JSONResult: %v", err)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got, err := ParseToolResultMap(raw)
	if err != nil {
		t.Fatalf("ParseToolResultMap: %v", err)
	}

	if got["running"] != float64(1) {
		t.Fatalf("expected running=1, got %#v", got["running"])
	}
}

func TestUnmarshalToolResult_PropagatesToolError(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"boom"}],"isError":true}`)

	var out map[string]any
	err := UnmarshalToolResult(raw, &out)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected tool error, got %v", err)
	}
}

func TestUnmarshalToolResult_PropagatesToolErrorWithNilTarget(t *testing.T) {
	raw := json.RawMessage(`{"content":[{"type":"text","text":"boom"}],"isError":true}`)

	err := UnmarshalToolResult(raw, nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected tool error with nil target, got %v", err)
	}
}

package spawn

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// TestDispatchDAGNode_StubOK covers the happy path: a valid DAG node request
// returns a stub_ok response with the request identifiers echoed back.
func TestDispatchDAGNode_StubOK(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewK8sController(nil, "", nil, logger)

	req := DAGNodeRequest{
		FlowID:   "autonomous-refactor",
		NodeID:   "spawn",
		NodeType: "agent_spawn",
		Input:    map[string]any{"plan": "reformat widget pkg"},
	}

	resp, err := c.DispatchDAGNode(context.Background(), req)
	if err != nil {
		t.Fatalf("DispatchDAGNode returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("DispatchDAGNode returned nil response")
	}
	if resp.FlowID != req.FlowID || resp.NodeID != req.NodeID {
		t.Fatalf("response identifiers mismatch: got %+v, want flow=%s node=%s", resp, req.FlowID, req.NodeID)
	}
	if resp.Status != "stub_ok" {
		t.Fatalf("status = %q, want stub_ok", resp.Status)
	}
	if resp.DispatchedAt.IsZero() {
		t.Fatal("DispatchedAt should be set")
	}
}

// TestDispatchDAGNode_ValidationErrors ensures the stub rejects malformed
// requests rather than silently returning success.
func TestDispatchDAGNode_ValidationErrors(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	c := NewK8sController(nil, "", nil, logger)

	cases := []struct {
		name string
		req  DAGNodeRequest
		want string
	}{
		{"missing flow_id", DAGNodeRequest{NodeID: "n", NodeType: "shell"}, "flow_id"},
		{"missing node_id", DAGNodeRequest{FlowID: "f", NodeType: "shell"}, "node_id"},
		{"missing node_type", DAGNodeRequest{FlowID: "f", NodeID: "n"}, "node_type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.DispatchDAGNode(context.Background(), tc.req)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

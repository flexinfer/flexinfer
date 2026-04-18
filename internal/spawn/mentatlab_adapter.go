package spawn

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DAGNodeRequest describes a single MentatLab autonomous-refactor DAG node
// dispatched into the spawn orchestrator. v1 is stub-only: the controller
// validates the request and logs it. Full integration with the MentatLab
// engine (via cmd/mcp-mentatlab) lands in F7 v2.
type DAGNodeRequest struct {
	FlowID   string         `json:"flow_id"`
	NodeID   string         `json:"node_id"`
	NodeType string         `json:"node_type"`
	Input    map[string]any `json:"input,omitempty"`
}

// DAGNodeResponse is the stub response returned by DispatchDAGNode.
type DAGNodeResponse struct {
	FlowID       string    `json:"flow_id"`
	NodeID       string    `json:"node_id"`
	Status       string    `json:"status"`
	DispatchedAt time.Time `json:"dispatched_at"`
	Note         string    `json:"note,omitempty"`
}

// DispatchDAGNode stubs execution of an autonomous DAG node. In v1 it only
// validates the incoming request and logs a "would spawn" line so the rest of
// the pipeline (template seed, validator, adapter wiring) can be exercised
// without a real engine. Full dispatch into pods/agents will replace the stub
// once the mcp-mentatlab proxy or MentatLab engine is wired up.
func (c *K8sController) DispatchDAGNode(ctx context.Context, req DAGNodeRequest) (*DAGNodeResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	c.logger.Info("would spawn DAG node (stub)",
		"flow_id", req.FlowID,
		"node_id", req.NodeID,
		"node_type", req.NodeType,
	)
	return &DAGNodeResponse{
		FlowID:       req.FlowID,
		NodeID:       req.NodeID,
		Status:       "stub_ok",
		DispatchedAt: time.Now().UTC(),
		Note:         "F7 v1 stub; real dispatch lands when the MentatLab engine is wired",
	}, nil
}

func (r DAGNodeRequest) validate() error {
	if r.FlowID == "" {
		return errors.New("DAGNodeRequest: flow_id required")
	}
	if r.NodeID == "" {
		return errors.New("DAGNodeRequest: node_id required")
	}
	if r.NodeType == "" {
		return fmt.Errorf("DAGNodeRequest %s/%s: node_type required", r.FlowID, r.NodeID)
	}
	return nil
}

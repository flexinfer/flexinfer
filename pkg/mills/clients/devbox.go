package clients

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// DevboxServerName is the registered name of mcp-devbox in the MCP hub.
// Matches the registry.yaml entry the operator's hub profile references.
const DevboxServerName = "devbox"

// DevboxClient implements pipeline.DevboxClient against the
// devbox_quality_gate MCP tool, called via MCPHubClient.
//
// Production deployments wire one shared MCPHubClient and pass it to
// every wrapper client constructor — the hub manages connection
// lifetime, the wrappers translate domain types.
type DevboxClient struct {
	Hub        *MCPHubClient
	ServerName string // overridable for tests / non-default hub registries
}

// NewDevboxClient returns a DevboxClient bound to hub. ServerName falls
// back to DevboxServerName.
func NewDevboxClient(hub *MCPHubClient) *DevboxClient {
	return &DevboxClient{Hub: hub, ServerName: DevboxServerName}
}

// devboxQualityGateResult mirrors mcp-devbox's qualityGateResult.
type devboxQualityGateResult struct {
	Language        string                  `json:"language"`
	Passed          bool                    `json:"passed"`
	Checks          []devboxQualityCheckRow `json:"checks"`
	TotalDurationMs int64                   `json:"total_duration_ms"`
}

type devboxQualityCheckRow struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	OutputTail string `json:"output_tail,omitempty"`
}

// QualityGate implements pipeline.DevboxClient.
func (c *DevboxClient) QualityGate(ctx context.Context, req pipeline.DevboxRequest) (pipeline.DevboxResponse, error) {
	if c == nil || c.Hub == nil {
		return pipeline.DevboxResponse{}, errors.New("devbox: client not configured")
	}
	if req.Project == "" {
		return pipeline.DevboxResponse{}, errors.New("devbox: Project required")
	}
	args := map[string]any{"project": req.Project}
	if req.AgentID != "" {
		args["agent_id"] = req.AgentID
	}
	server := c.ServerName
	if server == "" {
		server = DevboxServerName
	}
	body, err := c.Hub.CallTool(ctx, server, "devbox_quality_gate", args)
	// devbox_quality_gate returns IsError=true with a structured body
	// when checks fail; we treat that as a real (non-passing) result
	// rather than a transport error so the runner can surface the
	// failure path normally.
	if err != nil && body == "" {
		return pipeline.DevboxResponse{}, fmt.Errorf("devbox quality_gate: %w", err)
	}
	var parsed devboxQualityGateResult
	if perr := json.Unmarshal([]byte(body), &parsed); perr != nil {
		if err != nil {
			return pipeline.DevboxResponse{}, fmt.Errorf("devbox quality_gate: %w; raw=%q", err, body)
		}
		return pipeline.DevboxResponse{}, fmt.Errorf("devbox: decode body: %w; raw=%q", perr, body)
	}
	checks := make([]pipeline.DevboxCheck, 0, len(parsed.Checks))
	for _, row := range parsed.Checks {
		checks = append(checks, pipeline.DevboxCheck{
			Name:     row.Name,
			Passed:   row.Passed,
			ExitCode: row.ExitCode,
			Duration: float64(row.DurationMs) / 1000.0,
			Output:   row.OutputTail,
		})
	}
	return pipeline.DevboxResponse{
		Passed:   parsed.Passed,
		CostUSD:  0, // devbox_quality_gate runs locally; no LLM cost.
		LogTail:  buildDevboxLogTail(parsed),
		Checks:   checks,
		Language: parsed.Language,
	}, nil
}

// buildDevboxLogTail collapses the per-check output into a single
// human-readable string for stage_results.log_tail. Useful when a gate
// fails and the operator needs to surface what broke without exposing
// the full schema to the HUD.
func buildDevboxLogTail(result devboxQualityGateResult) string {
	if len(result.Checks) == 0 {
		return ""
	}
	var b []byte
	for _, c := range result.Checks {
		status := "PASS"
		if !c.Passed {
			status = "FAIL"
		}
		b = append(b, []byte(fmt.Sprintf("%s %s (%dms)\n", status, c.Name, c.DurationMs))...)
		if !c.Passed && c.OutputTail != "" {
			b = append(b, []byte(c.OutputTail)...)
			if len(c.OutputTail) > 0 && c.OutputTail[len(c.OutputTail)-1] != '\n' {
				b = append(b, '\n')
			}
		}
	}
	return string(b)
}

// Compile-time assertion that DevboxClient satisfies the pipeline interface.
var _ pipeline.DevboxClient = (*DevboxClient)(nil)

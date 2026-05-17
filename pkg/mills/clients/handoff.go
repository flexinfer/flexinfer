package clients

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// AgentContextServerName is the registered name of mcp-agent-context
// in the MCP hub. Same server backs HandoffClient and WorktreeAllocator.
const AgentContextServerName = "agent_context"

// HandoffClient implements pipeline.HandoffClient against
// agent_handoff_create on mcp-agent-context, called via MCPHubClient.
//
// Source-session model: every handoff is "from" a source agent session
// that already exists in agent-context. The operator pre-creates a
// long-lived session at startup (via agent_session_start) and passes
// its id here as SourceSessionID; the wrapper attaches that id to
// every handoff so the service can package the operator's recorded
// context.
type HandoffClient struct {
	Hub             *MCPHubClient
	ServerName      string
	SourceSessionID string
	// SourceSessionIDFunc, when set, supplies the current operator
	// session id. Mills uses this so a hub/backend outage at boot can
	// recover without rebuilding the escalator.
	SourceSessionIDFunc func() string
	// HandoffType is the agent-context "type" classification. The hub
	// expects one of: "full", "selective", "summary_only". Default
	// "summary_only" — escalations are short, structured records.
	HandoffType string
	// TokenBudget caps the size of the handoff. Default 4000 tokens —
	// enough for the failure record + last-log tail without ballooning.
	TokenBudget int
}

// NewHandoffClient returns a HandoffClient bound to hub. SourceSessionID
// must be filled before the client is usable; see operator main.go
// where it's populated from the operator's startup session.
func NewHandoffClient(hub *MCPHubClient, sourceSessionID string) *HandoffClient {
	return &HandoffClient{
		Hub:             hub,
		ServerName:      AgentContextServerName,
		SourceSessionID: sourceSessionID,
		HandoffType:     "summary_only",
		TokenBudget:     4000,
	}
}

// handoffCreateResponse mirrors the payload agent_handoff_create emits.
// We accept both JSON and YAML serialisations: the YAML form ships from
// MCP servers running in "concise text output" mode. We only need
// handoff_id for the wrapper return, but keep the rest for diagnostics.
type handoffCreateResponse struct {
	OK         bool   `json:"ok" yaml:"ok"`
	HandoffID  string `json:"handoff_id" yaml:"handoff_id"`
	TokenCount int    `json:"token_count" yaml:"token_count"`
	EntryCount int    `json:"entry_count" yaml:"entry_count"`
	Summary    string `json:"summary" yaml:"summary"`
}

// CreateHandoff implements pipeline.HandoffClient.
func (c *HandoffClient) CreateHandoff(ctx context.Context, req pipeline.HandoffRequest) (pipeline.HandoffResponse, error) {
	if c == nil || c.Hub == nil {
		return pipeline.HandoffResponse{}, errors.New("handoff: client not configured")
	}
	sourceSessionID := c.sourceSessionID()
	if sourceSessionID == "" {
		return pipeline.HandoffResponse{}, errors.New("handoff: SourceSessionID required (start an operator session at boot)")
	}
	if req.To == "" {
		return pipeline.HandoffResponse{}, errors.New("handoff: To (target_agent_id) required")
	}
	server := c.ServerName
	if server == "" {
		server = AgentContextServerName
	}
	args := map[string]any{
		"session_id":      sourceSessionID,
		"target_agent_id": req.To,
		"handoff_type":    handoffTypeOrDefault(c.HandoffType),
		"instructions":    buildHandoffInstructions(req),
	}
	if c.TokenBudget > 0 {
		args["token_budget"] = c.TokenBudget
	}
	body, err := c.Hub.CallTool(ctx, server, "agent_handoff_create", args)
	if err != nil && body == "" {
		return pipeline.HandoffResponse{}, fmt.Errorf("handoff: %w", err)
	}
	parsed, perr := decodeHandoffCreateResponse(body)
	if perr != nil {
		return pipeline.HandoffResponse{}, fmt.Errorf("handoff: decode: %w; raw=%q", perr, body)
	}
	if !parsed.OK && parsed.HandoffID == "" {
		return pipeline.HandoffResponse{}, fmt.Errorf("handoff: service reported failure: %q", body)
	}
	return pipeline.HandoffResponse{HandoffID: parsed.HandoffID}, nil
}

// decodeHandoffCreateResponse parses the body returned by
// agent_handoff_create. MCP servers may emit either JSON or YAML for tool
// result text (the "concise text output" mode produces YAML), so we try
// JSON first and fall back to YAML when the payload starts with a non-JSON
// token. The fallback covers the live-cluster output observed during
// escalation, e.g.:
//
//	entry_count: 0
//	handoff_id: bcd72b1b8f9ad438
//	ok: true
//	summary: ""
//	token_count: 0
func decodeHandoffCreateResponse(body string) (handoffCreateResponse, error) {
	trimmed := strings.TrimSpace(body)
	var parsed handoffCreateResponse
	if trimmed == "" {
		return parsed, errors.New("empty body")
	}
	// JSON objects/arrays start with '{' or '['. Anything else is almost
	// certainly YAML; skip the JSON attempt so the JSON error doesn't mask
	// the YAML decode failure.
	if c := trimmed[0]; c == '{' || c == '[' {
		if err := json.Unmarshal([]byte(body), &parsed); err == nil {
			return parsed, nil
		}
	}
	if err := yaml.Unmarshal([]byte(body), &parsed); err != nil {
		return handoffCreateResponse{}, err
	}
	return parsed, nil
}

func (c *HandoffClient) sourceSessionID() string {
	if c == nil {
		return ""
	}
	if c.SourceSessionIDFunc != nil {
		return c.SourceSessionIDFunc()
	}
	return c.SourceSessionID
}

// buildHandoffInstructions renders the handoff request into a markdown
// block that the receiving agent (or human) reads on accept. The
// agent_handoff_create tool doesn't have a structured "context" field;
// we serialise our typed bundle into instructions.
func buildHandoffInstructions(req pipeline.HandoffRequest) string {
	var b strings.Builder
	b.WriteString("# Mills escalation handoff\n\n")
	if req.From != "" {
		fmt.Fprintf(&b, "**From**: %s\n", req.From)
	}
	if req.PipelineRun != "" {
		fmt.Fprintf(&b, "**Pipeline run**: `%s`\n", req.PipelineRun)
	}
	if req.BacklogID != "" {
		fmt.Fprintf(&b, "**Backlog item**: `%s`\n", req.BacklogID)
	}
	if req.IssueURL != "" {
		fmt.Fprintf(&b, "**GitLab issue**: %s\n", req.IssueURL)
	}
	if req.Reason != "" {
		fmt.Fprintf(&b, "\n## Reason\n\n%s\n", req.Reason)
	}
	if len(req.Context) > 0 {
		// Render the failure record (or any other context) as JSON
		// inside a fenced code block. Receivers parse this back into a
		// FailureRecord struct when they want structured access.
		buf, err := json.MarshalIndent(req.Context, "", "  ")
		if err == nil {
			fmt.Fprintf(&b, "\n## Context\n\n```json\n%s\n```\n", string(buf))
		}
	}
	return b.String()
}

func handoffTypeOrDefault(t string) string {
	switch t {
	case "full", "selective", "summary_only":
		return t
	default:
		return "summary_only"
	}
}

// Compile-time interface assertion.
var _ pipeline.HandoffClient = (*HandoffClient)(nil)

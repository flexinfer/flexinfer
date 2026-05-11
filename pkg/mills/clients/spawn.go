package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// HUDSpawnConfig captures the connection settings for the loom HUD's
// mobile API. The operator runs in-cluster; the HUD usually runs on the
// developer's laptop OR cluster-deployed depending on the topology. In
// either case the operator reaches it via HTTP with a bearer token
// configured as HUD_MOBILE_OPERATOR_TOKEN on the HUD side.
type HUDSpawnConfig struct {
	// BaseURL is the HUD's HTTP base, e.g.
	// "http://hud.loom-system.svc.cluster.local:8090". Trailing slash
	// is tolerated.
	BaseURL string
	// Token is the mobile bearer token. Sent as "Authorization: Bearer <token>".
	Token string
	// PollInterval is how often the spawn detail endpoint is polled
	// for terminal status. Defaults to 5s.
	PollInterval time.Duration
	// PollDeadline caps total wait. Defaults to 30 minutes.
	PollDeadline time.Duration
	// Timeout caps any individual HTTP call. Default 30s.
	Timeout time.Duration
}

// HUDSpawnClient implements pipeline.SpawnClient against the HUD mobile
// API at /api/mobile/v1/agent/spawn. The flow is:
//
//	POST /spawn with the spawn.Request body → returns spawn_id immediately
//	GET /spawn/{id} repeatedly until status is terminal (completed/failed/stopped)
//	Map the final state.Telemetry into pipeline.SpawnResponse
type HUDSpawnClient struct {
	cfg  HUDSpawnConfig
	http *httpclient.Client
}

// NewHUDSpawnClient validates config and returns a ready client.
func NewHUDSpawnClient(cfg HUDSpawnConfig) (*HUDSpawnClient, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("hud spawn: BaseURL required")
	}
	if cfg.Token == "" {
		return nil, errors.New("hud spawn: Token required")
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.PollDeadline == 0 {
		cfg.PollDeadline = 30 * time.Minute
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	hcfg := httpclient.DefaultConfig()
	hcfg.Timeout = cfg.Timeout
	c := httpclient.New(hcfg)
	return &HUDSpawnClient{cfg: cfg, http: c}, nil
}

// SetTransport is for tests.
func (c *HUDSpawnClient) SetTransport(rt http.RoundTripper) {
	c.http.HTTP().Transport = rt
}

// hudSpawnRequestBody mirrors the subset of internal/spawn.Request the
// operator needs to populate. We keep it as a local typed struct rather
// than importing internal/spawn (to avoid pulling the HUD's internal
// package into the operator's dependency tree).
type hudSpawnRequestBody struct {
	AgentType       string            `json:"agent_type"`
	Namespace       string            `json:"namespace"`
	Branch          string            `json:"branch"`
	BaseBranch      string            `json:"base_branch,omitempty"`
	TaskDescription string            `json:"task_description"`
	Project         string            `json:"project"`
	TimeoutMinutes  int               `json:"timeout_minutes,omitempty"`
	MaxCostUSD      float64           `json:"max_cost_usd,omitempty"`
	MaxTurns        int               `json:"max_turns,omitempty"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// hudSpawnAcceptResponse is what POST /spawn returns on success.
type hudSpawnAcceptResponse struct {
	SpawnID string `json:"spawn_id"`
	AgentID string `json:"agent_id"`
	Status  string `json:"status"`
}

// hudFileChange mirrors bridge.FileChangeEntry for the operator side.
type hudFileChange struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	LinesAdded   int    `json:"lines_added,omitempty"`
	LinesRemoved int    `json:"lines_removed,omitempty"`
}

// hudSpawnTelemetry mirrors bridge.SpawnTelemetry — only the fields
// the operator actually consumes.
type hudSpawnTelemetry struct {
	TurnCount    int             `json:"turn_count"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	FileChanges  []hudFileChange `json:"file_changes,omitempty"`
	StopReason   string          `json:"stop_reason,omitempty"`
	LastMessage  string          `json:"last_message,omitempty"`
}

// hudSpawnState mirrors spawn.State — only what the operator reads.
type hudSpawnState struct {
	SpawnID   string             `json:"spawn_id"`
	AgentID   string             `json:"agent_id"`
	Status    string             `json:"status"`
	Error     string             `json:"error,omitempty"`
	Telemetry *hudSpawnTelemetry `json:"telemetry,omitempty"`
}

// Run implements pipeline.SpawnClient.
func (c *HUDSpawnClient) Run(ctx context.Context, req pipeline.SpawnRequest) (pipeline.SpawnResponse, error) {
	if c == nil || c.http == nil {
		return pipeline.SpawnResponse{}, errors.New("hud spawn: client not configured")
	}
	if req.Project == "" {
		return pipeline.SpawnResponse{}, errors.New("hud spawn: SpawnRequest.Project required")
	}
	if req.Branch == "" {
		return pipeline.SpawnResponse{}, errors.New("hud spawn: SpawnRequest.Branch required")
	}
	if req.Prompt == "" {
		return pipeline.SpawnResponse{}, errors.New("hud spawn: SpawnRequest.Prompt required")
	}

	body := hudSpawnRequestBody{
		AgentType:       agentTypeOrDefault(req.Model),
		Namespace:       req.Namespace,
		Branch:          req.Branch,
		BaseBranch:      req.BaseBranch,
		TaskDescription: req.Prompt,
		Project:         req.Project,
		TimeoutMinutes:  req.BudgetMinutes,
		MaxCostUSD:      req.BudgetUSD,
		MaxTurns:        req.BudgetTurns,
		ParentSessionID: req.ParentSessionID,
		Metadata:        buildSpawnMetadata(req),
	}

	spawnID, err := c.startSpawn(ctx, body)
	if err != nil {
		return pipeline.SpawnResponse{}, err
	}

	// Poll until terminal.
	pollCtx, cancel := context.WithTimeout(ctx, c.cfg.PollDeadline)
	defer cancel()
	for {
		if err := pollCtx.Err(); err != nil {
			return pipeline.SpawnResponse{
				SpawnID: spawnID,
				LogTail: fmt.Sprintf("hud spawn poll deadline (%s) exceeded", c.cfg.PollDeadline),
			}, fmt.Errorf("hud spawn: poll timeout after %s", c.cfg.PollDeadline)
		}
		state, err := c.getSpawnState(pollCtx, spawnID)
		if err != nil {
			if pollCtx.Err() != nil {
				return pipeline.SpawnResponse{SpawnID: spawnID}, fmt.Errorf("hud spawn: poll cancelled: %w", err)
			}
			return pipeline.SpawnResponse{SpawnID: spawnID}, err
		}
		if isTerminalSpawnStatus(state.Status) {
			resp := mapTelemetryToResponse(state)
			if state.Status != "completed" {
				return resp, fmt.Errorf("hud spawn %s status=%s: %s", spawnID, state.Status, state.Error)
			}
			return resp, nil
		}
		select {
		case <-pollCtx.Done():
			return pipeline.SpawnResponse{SpawnID: spawnID}, fmt.Errorf("hud spawn: poll timeout after %s", c.cfg.PollDeadline)
		case <-time.After(c.cfg.PollInterval):
		}
	}
}

// startSpawn POSTs the spawn request and returns the new spawn id.
func (c *HUDSpawnClient) startSpawn(ctx context.Context, body hudSpawnRequestBody) (string, error) {
	reqBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("hud spawn: marshal: %w", err)
	}
	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/mobile/v1/agent/spawn"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("hud spawn: POST: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("hud spawn: POST status %d: %s", resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("hud spawn: read accept: %w", err)
	}
	var accept hudSpawnAcceptResponse
	if err := decodeHUDResponse(buf, &accept); err != nil {
		return "", fmt.Errorf("hud spawn: decode accept: %w", err)
	}
	if accept.SpawnID == "" {
		return "", errors.New("hud spawn: server returned empty spawn_id")
	}
	return accept.SpawnID, nil
}

// getSpawnState polls the detail endpoint once.
func (c *HUDSpawnClient) getSpawnState(ctx context.Context, spawnID string) (*hudSpawnState, error) {
	url := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/mobile/v1/agent/spawn/" + spawnID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hud spawn: GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("hud spawn: GET status %d: %s", resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("hud spawn: read state: %w", err)
	}
	var state hudSpawnState
	if err := decodeHUDResponse(buf, &state); err != nil {
		return nil, fmt.Errorf("hud spawn: decode state: %w", err)
	}
	return &state, nil
}

func decodeHUDResponse(data []byte, out any) error {
	var envelope struct {
		OK    *bool           `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.OK != nil {
		if !*envelope.OK {
			msg := envelope.Error.Message
			if msg == "" {
				msg = "mobile API returned ok=false"
			}
			if envelope.Error.Code != "" {
				return fmt.Errorf("%s: %s", envelope.Error.Code, msg)
			}
			return errors.New(msg)
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return errors.New("mobile envelope missing data")
		}
		return json.Unmarshal(envelope.Data, out)
	}
	return json.Unmarshal(data, out)
}

// agentTypeOrDefault returns a valid spawn AgentType. The pipeline
// model field allows any FlexInfer / frontier id; we map common
// shorthands to the loom spawn AgentType vocabulary.
func agentTypeOrDefault(model string) string {
	switch strings.ToLower(model) {
	case "claude", "claude-code", "claude-sonnet", "claude-opus":
		return "claude-code"
	case "codex", "openai-codex":
		return "codex"
	case "gemini":
		return "gemini"
	default:
		// Unknown / empty falls through to claude-code — the most
		// common pipeline path. Operators can override per-stage by
		// setting SpawnWorker.Model.
		if model == "" {
			return "claude-code"
		}
		return model
	}
}

// buildSpawnMetadata stuffs the LOOM_MILLS_* env-vars + stage id into
// the spawn metadata map so the spawn pod's env carries them through
// to the agent process.
func buildSpawnMetadata(req pipeline.SpawnRequest) map[string]string {
	out := map[string]string{}
	for k, v := range req.Env {
		out[k] = v
	}
	if req.StageID != "" {
		out["loom_mills_stage"] = req.StageID
	}
	if req.BacklogID != "" {
		out["loom_mills_backlog_id"] = req.BacklogID
	}
	return out
}

// isTerminalSpawnStatus mirrors spawn.IsTerminal (we don't import the
// internal package; the string set is part of the persisted contract).
func isTerminalSpawnStatus(s string) bool {
	switch s {
	case "completed", "failed", "stopped":
		return true
	default:
		return false
	}
}

// mapTelemetryToResponse turns a HUD spawn state into the runner's
// SpawnResponse, computing FilesChanged + LinesAdded/Removed totals.
func mapTelemetryToResponse(state *hudSpawnState) pipeline.SpawnResponse {
	resp := pipeline.SpawnResponse{
		SpawnID: state.SpawnID,
	}
	if state.Telemetry == nil {
		resp.LogTail = state.Error
		return resp
	}
	tel := state.Telemetry
	resp.CostUSD = tel.TotalCostUSD
	for _, fc := range tel.FileChanges {
		resp.FilesChanged = append(resp.FilesChanged, fc.Path)
		resp.LinesAdded += fc.LinesAdded
		resp.LinesRemoved += fc.LinesRemoved
	}
	logParts := []string{}
	if tel.StopReason != "" {
		logParts = append(logParts, "stop_reason="+tel.StopReason)
	}
	if tel.LastMessage != "" {
		logParts = append(logParts, "last_message="+tel.LastMessage)
	}
	if state.Error != "" {
		logParts = append(logParts, "error="+state.Error)
	}
	resp.LogTail = strings.Join(logParts, "\n")
	resp.Artifacts = map[string]any{
		"turn_count": tel.TurnCount,
		"agent_id":   state.AgentID,
		"status":     state.Status,
	}
	return resp
}

// Compile-time interface assertion.
var _ pipeline.SpawnClient = (*HUDSpawnClient)(nil)

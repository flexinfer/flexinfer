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
	// MaxRetries controls transient HTTP retries for the HUD spawn API.
	// Defaults to 6 so short Kubernetes rollouts do not burn Mills stage
	// attempts before a replacement HUD pod becomes ready.
	MaxRetries int
	// RetryBaseDelay is the initial retry delay. Defaults to 1s.
	RetryBaseDelay time.Duration
	// RetryMaxDelay caps exponential retry delay. Defaults to 5s.
	RetryMaxDelay time.Duration
	// GitRunner runs `git` commands in the spawn worktree to capture the
	// unified diff + commit messages once the spawn completes. The
	// operator + spawn pod share an NFS-backed worktree, so the path
	// passed in SpawnRequest.WorkingDir is readable from the operator's
	// process. Defaults to execCommandRunner{} (real os/exec). Tests
	// inject a fake.
	GitRunner CommandRunner
	// MaxDiffBytes caps how much of the working-tree diff we serialize
	// into SpawnResponse.DiffPatch. The rubric prompt has its own 8 KiB
	// cap; we cap higher here so secret-scan and other downstream gates
	// can see more context. Defaults to 32 KiB.
	MaxDiffBytes int
	// MaxCommitMessagesBytes caps the joined byte length of
	// SpawnResponse.CommitMessages. Defaults to 8 KiB.
	MaxCommitMessagesBytes int
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

// Default caps on captured diff/commit-message content. Chosen to match
// gateInputFor + composePrompt expectations: the rubric template
// re-truncates the diff at 8 KiB before sending to the judge; we keep
// 32 KiB here so secret-scan and other gates that read the raw
// SpawnResponse.DiffPatch see more context.
const (
	defaultMaxDiffBytes           = 32 * 1024
	defaultMaxCommitMessagesBytes = 8 * 1024
)

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
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 6
	}
	if cfg.RetryBaseDelay == 0 {
		cfg.RetryBaseDelay = time.Second
	}
	if cfg.RetryMaxDelay == 0 {
		cfg.RetryMaxDelay = 5 * time.Second
	}
	if cfg.GitRunner == nil {
		cfg.GitRunner = execCommandRunner{}
	}
	if cfg.MaxDiffBytes <= 0 {
		cfg.MaxDiffBytes = defaultMaxDiffBytes
	}
	if cfg.MaxCommitMessagesBytes <= 0 {
		cfg.MaxCommitMessagesBytes = defaultMaxCommitMessagesBytes
	}
	hcfg := httpclient.DefaultConfig()
	hcfg.Timeout = cfg.Timeout
	hcfg.MaxRetries = cfg.MaxRetries
	hcfg.RetryBaseDelay = cfg.RetryBaseDelay
	hcfg.RetryMaxDelay = cfg.RetryMaxDelay
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
	if req.OnAccepted != nil {
		if err := req.OnAccepted(spawnID); err != nil {
			return pipeline.SpawnResponse{SpawnID: spawnID}, fmt.Errorf("hud spawn: record accepted spawn: %w", err)
		}
	}

	return c.pollSpawn(ctx, spawnID, req.WorkingDir, req.BaseBranch)
}

// Resume implements pipeline.SpawnResumeClient by polling an already
// accepted HUD spawn id. This lets the Mills operator re-attach after a
// rollout instead of starting duplicate stage attempts.
//
// Resume does not have the original SpawnRequest.WorkingDir/BaseBranch
// in scope, so the post-terminal git capture is skipped. The M2.5
// unparseable-retry path is the safety net when a resumed spawn's
// rubric judge can't see the diff; a subsequent Run attempt will
// repopulate it.
func (c *HUDSpawnClient) Resume(ctx context.Context, spawnID string) (pipeline.SpawnResponse, error) {
	if c == nil || c.http == nil {
		return pipeline.SpawnResponse{}, errors.New("hud spawn: client not configured")
	}
	if spawnID == "" {
		return pipeline.SpawnResponse{}, errors.New("hud spawn: resume spawn id required")
	}
	return c.pollSpawn(ctx, spawnID, "", "")
}

func (c *HUDSpawnClient) pollSpawn(ctx context.Context, spawnID, workingDir, baseBranch string) (pipeline.SpawnResponse, error) {
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
			return pipeline.SpawnResponse{SpawnID: spawnID}, fmt.Errorf("hud spawn %s: %w", spawnID, err)
		}
		if isTerminalSpawnStatus(state.Status) {
			resp := mapTelemetryToResponse(state)
			c.attachGitContext(ctx, &resp, workingDir, baseBranch)
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
		Artifacts: map[string]any{
			"agent_id": state.AgentID,
			"status":   state.Status,
		},
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
	resp.Artifacts["turn_count"] = tel.TurnCount
	return resp
}

// attachGitContext fills resp.DiffPatch + resp.CommitMessages by
// shelling `git diff` and `git log` in the spawn's worktree. The
// operator + spawn pods share an NFS-backed worktree, so the operator
// process can read the working tree HEAD after the spawn pod
// terminates.
//
// Failure modes are best-effort: any git error sets the fields to zero
// values (empty diff, nil commits) so downstream gates see "nothing to
// grade" rather than getting an infrastructure error here. The M2.5
// unparseable-retry path is the safety net for the legitimate
// nothing-changed case (the canary's no-op edit).
//
// The function never returns nil for DiffPatch when there's a worktree
// to inspect: it returns []byte{} (empty slice) on success-with-no-diff
// so downstream code can distinguish "ran git, got nothing" from
// "didn't run git at all".
func (c *HUDSpawnClient) attachGitContext(ctx context.Context, resp *pipeline.SpawnResponse, workingDir, baseBranch string) {
	if c == nil || resp == nil {
		return
	}
	if workingDir == "" || baseBranch == "" {
		// No worktree path or base ref → operator never told us where to
		// look. Leave Diff/Commits at zero values; gate fallback path
		// will retry via M2.5's unparseable-handler.
		return
	}
	if c.cfg.GitRunner == nil {
		return
	}

	diff := captureGitDiff(ctx, c.cfg.GitRunner, workingDir, baseBranch, c.cfg.MaxDiffBytes)
	commits := captureGitCommitMessages(ctx, c.cfg.GitRunner, workingDir, baseBranch, c.cfg.MaxCommitMessagesBytes)

	resp.DiffPatch = diff
	resp.CommitMessages = commits
}

// captureGitDiff runs `git diff <baseBranch>...HEAD` in workingDir and
// returns the unified diff capped at maxBytes. The triple-dot form
// produces the symmetric-difference diff between base and HEAD — i.e.
// "what changed on this branch since fork from base", which is the
// view the rubric judge needs to score code review questions.
//
// On any git error (worktree missing, base ref unknown, etc.) we
// return an empty slice — never nil — so callers can distinguish
// "ran git, no changes" from "git capture was skipped entirely".
func captureGitDiff(ctx context.Context, runner CommandRunner, workingDir, baseBranch string, maxBytes int) []byte {
	if runner == nil || workingDir == "" || baseBranch == "" {
		return nil
	}
	stdout, _, code, err := runner.Run(ctx, workingDir, "git", "diff", baseBranch+"...HEAD")
	if err != nil || code != 0 {
		// best-effort: return empty slice (not nil) so the response
		// shape is consistent.
		return []byte{}
	}
	return truncateBytes([]byte(stdout), maxBytes)
}

// captureGitCommitMessages returns the per-commit message bodies on
// the current branch since fork from baseBranch. Uses a NUL delimiter
// so multi-paragraph commit messages don't get mangled by newline
// splitting.
func captureGitCommitMessages(ctx context.Context, runner CommandRunner, workingDir, baseBranch string, maxBytes int) []string {
	if runner == nil || workingDir == "" || baseBranch == "" {
		return nil
	}
	stdout, _, code, err := runner.Run(ctx, workingDir, "git", "log", "--pretty=format:%B%x00", baseBranch+"..HEAD")
	if err != nil || code != 0 {
		return nil
	}
	parts := strings.Split(strings.TrimRight(stdout, "\x00\n"), "\x00")
	out := make([]string, 0, len(parts))
	total := 0
	for _, p := range parts {
		msg := strings.TrimSpace(p)
		if msg == "" {
			continue
		}
		// Reserve room for joining commas in the byte budget so a single
		// runaway commit message still hits the cap.
		if maxBytes > 0 && total+len(msg) > maxBytes {
			remaining := maxBytes - total
			if remaining > 0 {
				marker := truncationMarker(len(msg) - remaining)
				out = append(out, msg[:remaining]+marker)
			}
			break
		}
		out = append(out, msg)
		total += len(msg)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncateBytes returns the input capped at maxBytes, appending a
// truncation marker that tells the reader exactly how much was dropped.
// maxBytes <= 0 disables truncation.
func truncateBytes(b []byte, maxBytes int) []byte {
	if maxBytes <= 0 || len(b) <= maxBytes {
		return b
	}
	dropped := len(b) - maxBytes
	marker := truncationMarker(dropped)
	out := make([]byte, 0, maxBytes+len(marker))
	out = append(out, b[:maxBytes]...)
	out = append(out, marker...)
	return out
}

func truncationMarker(dropped int) string {
	return fmt.Sprintf("\n... [truncated %d bytes]\n", dropped)
}

// Compile-time interface assertion.
var _ pipeline.SpawnClient = (*HUDSpawnClient)(nil)

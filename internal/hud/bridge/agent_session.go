package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// --- Session lifecycle DTOs ---

// SessionStartParams holds parameters for starting an agent session.
type SessionStartParams struct {
	Namespace             string `json:"namespace"`
	AgentID               string `json:"agent_id"`
	AgentType             string `json:"agent_type"`
	Description           string `json:"description"`
	AutoRecall            bool   `json:"auto_recall"`
	AutoRecallStrategy    string `json:"auto_recall_strategy,omitempty"`
	AutoRecallQuery       string `json:"auto_recall_query,omitempty"`
	AutoRecallTokenBudget int    `json:"auto_recall_token_budget,omitempty"`

	// Pipeline linking (optional, set when session is tied to a CI pipeline)
	PipelineProject string `json:"pipeline_project,omitempty"`
	PipelineID      int    `json:"pipeline_id,omitempty"`
}

// SessionStartResult holds the result of starting a session.
type SessionStartResult struct {
	SessionID              string `json:"session_id"`
	RecalledContext        string `json:"recalled_context,omitempty"`
	StartupBriefing        string `json:"startup_briefing,omitempty"`
	StartupBriefingEntries int    `json:"startup_briefing_entries,omitempty"`
	AlreadyExisted         bool   `json:"already_existed"`
}

const sessionStartActiveLookupTimeout = 1500 * time.Millisecond

const (
	autoRecallStrategyFast     = "fast"
	autoRecallStrategyBalanced = "balanced"
	autoRecallStrategyDeep     = "deep"
	autoRecallBudgetMin        = 256
	autoRecallBudgetMax        = 32000
)

type autoRecallProfile struct {
	TokenBudget   int
	IncludeTasks  bool
	RecencyWeight float64
	Timeout       time.Duration
	BriefingItems int
	BriefingChars int
	MemoryTiers   []string
}

var autoRecallProfiles = map[string]autoRecallProfile{
	autoRecallStrategyFast: {
		TokenBudget:   1500,
		IncludeTasks:  false,
		RecencyWeight: 0.45,
		Timeout:       900 * time.Millisecond,
		BriefingItems: 3,
		BriefingChars: 420,
		MemoryTiers:   []string{"working", "short_term"},
	},
	autoRecallStrategyBalanced: {
		TokenBudget:   4000,
		IncludeTasks:  true,
		RecencyWeight: 0.20,
		Timeout:       1500 * time.Millisecond,
		BriefingItems: 4,
		BriefingChars: 640,
		MemoryTiers:   []string{"working", "short_term", "long_term"},
	},
	autoRecallStrategyDeep: {
		TokenBudget:   8000,
		IncludeTasks:  true,
		RecencyWeight: 0.10,
		Timeout:       2500 * time.Millisecond,
		BriefingItems: 6,
		BriefingChars: 960,
		MemoryTiers:   []string{"working", "short_term", "long_term"},
	},
}

func normalizeAutoRecallStrategy(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case autoRecallStrategyFast:
		return autoRecallStrategyFast
	case autoRecallStrategyDeep:
		return autoRecallStrategyDeep
	default:
		return autoRecallStrategyBalanced
	}
}

func clampAutoRecallBudget(budget int) int {
	switch {
	case budget < autoRecallBudgetMin:
		return autoRecallBudgetMin
	case budget > autoRecallBudgetMax:
		return autoRecallBudgetMax
	default:
		return budget
	}
}

func defaultAutoRecallQuery(p SessionStartParams) string {
	if q := strings.TrimSpace(p.Description); q != "" {
		return q
	}
	if q := strings.TrimSpace(p.Namespace); q != "" {
		return q
	}
	return "recent implementation context and open tasks"
}

func buildSessionStartRecallArgs(p SessionStartParams) map[string]any {
	strategy := normalizeAutoRecallStrategy(p.AutoRecallStrategy)
	profile := autoRecallProfiles[strategy]

	query := strings.TrimSpace(p.AutoRecallQuery)
	if query == "" {
		query = defaultAutoRecallQuery(p)
	}

	tokenBudget := profile.TokenBudget
	if p.AutoRecallTokenBudget > 0 {
		tokenBudget = clampAutoRecallBudget(p.AutoRecallTokenBudget)
	}

	args := map[string]any{
		"query":             query,
		"scope":             "all",
		"token_budget":      tokenBudget,
		"include_decisions": true,
		"include_summaries": true,
		"include_tasks":     profile.IncludeTasks,
		"recency_weight":    profile.RecencyWeight,
		"memory_tiers":      profile.MemoryTiers,
	}
	if id := strings.TrimSpace(p.AgentID); id != "" {
		args["agent_id"] = id
	}
	if ns := strings.TrimSpace(p.Namespace); ns != "" {
		args["file_context"] = ns
	}

	return args
}

func sessionStartRecallProfile(p SessionStartParams) autoRecallProfile {
	return autoRecallProfiles[normalizeAutoRecallStrategy(p.AutoRecallStrategy)]
}

// StartSession creates a session, registers presence, and optionally recalls context.
// It is idempotent: if the agent already has an active session in the same namespace,
// it returns the existing session ID instead of creating a new one.
//
// Presence registration and context recall are fire-and-forget: they run in
// background goroutines so the caller is not blocked by non-critical MCP calls.
func (a *AgentBridge) StartSession(p SessionStartParams) (*SessionStartResult, error) {
	// Check for existing active session in the same namespace (cached, fast path).
	// Bound this lookup to avoid delaying startup if agent-context is slow.
	if existing, err := a.getActiveSession(p.AgentID, sessionStartActiveLookupTimeout); err == nil && existing != nil {
		if existing.Namespace == p.Namespace && existing.Status == "active" {
			presenceArgs := map[string]any{
				"agent_id":   p.AgentID,
				"session_id": existing.ID,
			}
			if p.AgentType != "" {
				presenceArgs["agent_type"] = p.AgentType
			}
			if p.Description != "" {
				presenceArgs["description"] = p.Description
			}
			go func() { _ = a.callAgentTool("agent_presence_register", presenceArgs, nil) }()

			result := &SessionStartResult{
				SessionID:      existing.ID,
				AlreadyExisted: true,
			}
			a.attachSessionStartBriefing(result, p)
			return result, nil
		}
	}

	// Start a new session (blocking, required, 8s timeout).
	args := map[string]any{
		"namespace":   p.Namespace,
		"description": p.Description,
	}
	if p.AgentID != "" {
		args["agent_id"] = p.AgentID
	}
	var sessionResult struct {
		SessionID string `json:"session_id"`
	}
	if err := a.callAgentToolTimeout("agent_session_start", args, &sessionResult, 8*time.Second); err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	// Invalidate the session cache so subsequent GetActiveSession picks up
	// the newly created session.
	a.invalidateSessionCache(p.AgentID)

	result := &SessionStartResult{
		SessionID: sessionResult.SessionID,
	}

	// Fire-and-forget: register presence (non-critical, error already ignored).
	presenceArgs := map[string]any{
		"agent_id":   p.AgentID,
		"session_id": sessionResult.SessionID,
	}
	if p.AgentType != "" {
		presenceArgs["agent_type"] = p.AgentType
	}
	if p.Description != "" {
		presenceArgs["description"] = p.Description
	}
	go func() { _ = a.callAgentTool("agent_presence_register", presenceArgs, nil) }()

	a.attachSessionStartBriefing(result, p)

	return result, nil
}

func (a *AgentBridge) attachSessionStartBriefing(result *SessionStartResult, p SessionStartParams) {
	if a == nil || result == nil || !p.AutoRecall {
		return
	}
	briefing, entryCount, err := a.buildSessionStartBriefing(p)
	if err != nil || strings.TrimSpace(briefing) == "" {
		return
	}
	result.StartupBriefing = briefing
	result.RecalledContext = briefing
	result.StartupBriefingEntries = entryCount
}

func (a *AgentBridge) buildSessionStartBriefing(p SessionStartParams) (string, int, error) {
	recallArgs := buildSessionStartRecallArgs(p)
	profile := sessionStartRecallProfile(p)

	var recall KnowledgeResult
	if err := a.callAgentToolTimeout("agent_recall", recallArgs, &recall, profile.Timeout); err != nil {
		return "", 0, err
	}

	briefing := formatSessionStartBriefing(&recall, profile)
	return briefing, len(recall.Entries), nil
}

func formatSessionStartBriefing(result *KnowledgeResult, profile autoRecallProfile) string {
	if result == nil || len(result.Entries) == 0 {
		return ""
	}

	lines := []string{
		fmt.Sprintf("Recalled %d entries (%d tokens).", len(result.Entries), result.TotalTokens),
	}
	limit := profile.BriefingItems
	if limit <= 0 || limit > len(result.Entries) {
		limit = len(result.Entries)
	}
	for _, entry := range result.Entries[:limit] {
		line := summarizeRecallEntry(entry)
		if line != "" {
			lines = append(lines, "- "+line)
		}
	}
	if omitted := len(result.Entries) - limit; omitted > 0 {
		lines = append(lines, fmt.Sprintf("- +%d more recalled entries", omitted))
	}

	return truncateSessionBriefing(strings.Join(lines, "\n"), profile.BriefingChars)
}

func summarizeRecallEntry(entry KnowledgeEntry) string {
	parts := []string{}
	if kind := strings.TrimSpace(entry.EntryType); kind != "" {
		parts = append(parts, kind+":")
	}
	title := compactRecallText(entry.Title)
	content := compactRecallText(entry.Content)
	switch {
	case title != "" && content != "" && !strings.EqualFold(title, content):
		parts = append(parts, title+" - "+truncateSessionBriefing(content, 140))
	case title != "":
		parts = append(parts, title)
	case content != "":
		parts = append(parts, truncateSessionBriefing(content, 180))
	}
	if entry.Namespace != "" {
		parts = append(parts, "("+entry.Namespace+")")
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func compactRecallText(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func truncateSessionBriefing(text string, limit int) string {
	if limit <= 0 {
		return strings.TrimSpace(text)
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

// SessionEndParams holds parameters for ending an agent session.
type SessionEndParams struct {
	SessionID    string `json:"session_id"`
	AgentID      string `json:"agent_id"`
	Summarize    *bool  `json:"summarize,omitempty"`
	SummaryAsync bool   `json:"summary_async,omitempty"`
}

func (p SessionEndParams) summarizeEnabled() bool {
	if p.Summarize == nil {
		return true
	}
	return *p.Summarize
}

// EndSession ends a session, optionally summarizes context, and deregisters presence.
// If SessionID is empty, it finds the active session by AgentID.
// Returns (true, nil) when a session was ended, (false, nil) when no session was
// found (not an error — hooks may fire against a restarted HUD), or (false, err)
// on actual failures.
func (a *AgentBridge) EndSession(p SessionEndParams) (bool, error) {
	sessionID := p.SessionID
	if sessionID == "" && p.AgentID != "" {
		if active, err := a.GetActiveSession(p.AgentID); err == nil && active != nil {
			sessionID = active.ID
		}
	}
	if sessionID == "" {
		return false, nil
	}

	args := map[string]any{
		"session_id": sessionID,
		"summarize":  p.summarizeEnabled(),
	}
	if p.SummaryAsync {
		args["summary_async"] = true
	}
	if err := a.callAgentTool("agent_session_end", args, nil); err != nil {
		return false, fmt.Errorf("end session: %w", err)
	}

	// Invalidate the session cache so subsequent lookups reflect the ended session.
	if p.AgentID != "" {
		a.invalidateSessionCache(p.AgentID)
	}

	// Deregister presence (best-effort).
	if p.AgentID != "" {
		_ = a.callAgentTool("agent_presence_deregister", map[string]any{
			"agent_id": p.AgentID,
		}, nil)
	}

	return true, nil
}

// PresenceHeartbeat updates the heartbeat timestamp for an agent.
type PresenceHeartbeatParams struct {
	Status         string
	ActiveFiles    []string
	CurrentTask    string
	Branch         string
	PipelineStatus string `json:"pipeline_status,omitempty"` // Current pipeline status (running/success/failed)
}

func (a *AgentBridge) PresenceHeartbeat(agentID string, p PresenceHeartbeatParams) (*PresenceHeartbeatResult, error) {
	args := map[string]any{
		"agent_id": agentID,
	}
	if p.Status != "" {
		args["status"] = p.Status
	}
	if len(p.ActiveFiles) > 0 {
		args["active_files"] = p.ActiveFiles
	}
	if p.CurrentTask != "" {
		args["current_task"] = p.CurrentTask
	}
	if p.Branch != "" {
		args["branch"] = p.Branch
	}
	if p.PipelineStatus != "" {
		args["pipeline_status"] = p.PipelineStatus
	}

	var result PresenceHeartbeatResult
	if err := a.callAgentTool("agent_presence_heartbeat", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// PresenceRegister registers an agent's presence without starting a session.
// This is useful for clients that only want heartbeat-style liveness tracking.
func (a *AgentBridge) PresenceRegister(agentID, sessionID, agentType, description string, heartbeatTTLSeconds int) error {
	args := map[string]any{
		"agent_id": agentID,
	}
	if sessionID != "" {
		args["session_id"] = sessionID
	}
	if agentType != "" {
		args["agent_type"] = agentType
	}
	if description != "" {
		args["description"] = description
	}
	if heartbeatTTLSeconds > 0 {
		args["heartbeat_ttl_seconds"] = heartbeatTTLSeconds
	}
	return a.callAgentTool("agent_presence_register", args, nil)
}

// GetActiveSession finds the currently active session for an agent.
// Results are cached for 30 seconds to avoid repeated full session list
// fetches from the MCP server. Returns nil if no active session exists.
func (a *AgentBridge) GetActiveSession(agentID string) (*SessionInfo, error) {
	return a.getActiveSession(agentID, 0)
}

func (a *AgentBridge) getActiveSession(agentID string, timeout time.Duration) (*SessionInfo, error) {
	if strings.TrimSpace(agentID) == "" {
		return nil, nil
	}

	cacheKey := "active_session:" + agentID
	if cached, ok := a.cache.Get(cacheKey); ok {
		// Cache hit — may be nil (*SessionInfo) for "no active session".
		s, _ := cached.(*SessionInfo)
		return s, nil
	}

	// Query with agent_id + status filter to avoid hitting the default 20-item
	// limit when the agent's sessions fall outside the unfiltered window.
	var listResult struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	args := map[string]any{
		"agent_id": agentID,
		"status":   "active",
		"limit":    1,
	}
	var err error
	if timeout > 0 {
		err = a.callAgentToolTimeout("agent_session_list", args, &listResult, timeout)
	} else {
		err = a.callAgentTool("agent_session_list", args, &listResult)
	}
	if err != nil {
		return nil, err
	}

	var result *SessionInfo
	if len(listResult.Sessions) > 0 {
		result = &listResult.Sessions[0]
	}

	// Cache both hits and misses to avoid redundant fetches.
	a.cache.Set(cacheKey, result, 30*time.Second)
	return result, nil
}

// Sessions returns all agent sessions.
func (a *AgentBridge) Sessions() ([]SessionInfo, error) {
	var result struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	if err := a.callAgentTool("agent_session_list", map[string]any{
		"limit": defaultSessionListLimit,
	}, &result); err != nil {
		return nil, err
	}
	return result.Sessions, nil
}

// ListSessions calls agent_session_list with arbitrary parameters and returns raw JSON.
func (a *AgentBridge) ListSessions(params map[string]any) (json.RawMessage, error) {
	var result json.RawMessage
	if err := a.callAgentTool("agent_session_list", params, &result); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(result)
	return raw, nil
}

// PruneSessions calls agent_session_prune with arbitrary parameters and returns raw JSON.
func (a *AgentBridge) PruneSessions(params map[string]any) (json.RawMessage, error) {
	var result json.RawMessage
	if err := a.callAgentTool("agent_session_prune", params, &result); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(result)
	return raw, nil
}

// DeleteSession calls agent_session_delete for a single session.
func (a *AgentBridge) DeleteSession(sessionID string) error {
	return a.callAgentTool("agent_session_delete", map[string]any{
		"session_id": sessionID,
	}, nil)
}

// PresenceList returns all active agents in the presence registry.
func (a *AgentBridge) PresenceList(includeOffline bool) ([]PresenceInfo, error) {
	args := map[string]any{}
	if includeOffline {
		args["include_offline"] = true
	}
	var result struct {
		Agents []PresenceInfo `json:"agents"`
	}
	if err := a.callAgentTool("agent_presence_list", args, &result); err != nil {
		return nil, err
	}
	return result.Agents, nil
}

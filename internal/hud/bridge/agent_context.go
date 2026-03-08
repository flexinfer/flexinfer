package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ContextAdd adds context entries (findings, decisions, etc.) via agent_context_add.
// The entries parameter should be a slice of maps with entry_type, title, content, etc.
func (a *AgentBridge) ContextAdd(sessionID string, entries []map[string]any) error {
	args := map[string]any{
		"entries": entries,
	}
	if sessionID != "" {
		args["session_id"] = sessionID
	}
	return a.callAgentTool("agent_context_add", args, nil)
}

// SessionEntries returns context entries for a specific session.
func (a *AgentBridge) SessionEntries(sessionID string, limit int) ([]ContextEntryInfo, error) {
	args := map[string]any{
		"session_id": sessionID,
		// agent_context_search requires a non-empty query string.
		// Session filter does the heavy lifting; keep query generic.
		"query": "session context entries",
	}
	if limit > 0 {
		args["limit"] = limit
	}
	var result struct {
		Results []ContextEntryInfo `json:"results"`
	}
	if err := a.callAgentTool("agent_context_search", args, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

// ContextInspect builds a context budget breakdown for a session.
//
// Resolution order:
//   - If sessionID is empty, uses the active session for agentID.
//   - If sessionID is set, uses that session (and backfills metadata when available).
func (a *AgentBridge) ContextInspect(agentID, sessionID string, detail bool, limit int) (*ContextInspectResult, error) {
	if sessionID == "" && agentID == "" {
		return nil, fmt.Errorf("agent_id or session_id is required")
	}
	if limit <= 0 {
		limit = 200
	}

	var sessionMeta *SessionInfo
	if sessionID == "" {
		active, err := a.GetActiveSession(agentID)
		if err != nil {
			return nil, fmt.Errorf("get active session: %w", err)
		}
		if active == nil {
			return nil, fmt.Errorf("no active session found for agent %s", agentID)
		}
		sessionMeta = active
		sessionID = active.ID
	} else {
		if sessions, err := a.Sessions(); err == nil {
			for i := range sessions {
				if sessions[i].ID == sessionID {
					sessionMeta = &sessions[i]
					break
				}
			}
		}
	}

	entries, err := a.SessionEntries(sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("session entries: %w", err)
	}

	byType := make(map[string]*ContextInspectBucket)
	top := make([]ContextInspectTopEntry, 0, len(entries))
	totalContextChars := 0
	totalContextTokens := 0
	contextEntryChars := 0
	contextEntryTokens := 0
	fileInjectionChars := 0
	fileInjectionTokens := 0

	for _, wrapped := range entries {
		entry := wrapped.Entry
		entryType := strings.TrimSpace(entry.EntryType)
		if entryType == "" {
			entryType = "note"
		}
		chars := estimateContextChars(entry)
		tokens := entry.TokenCount
		if tokens <= 0 {
			tokens = estimateContextTokens(chars)
		}
		totalContextChars += chars
		totalContextTokens += tokens
		if isFileInjectionEntry(entry, entryType) {
			fileInjectionChars += chars
			fileInjectionTokens += tokens
		} else {
			contextEntryChars += chars
			contextEntryTokens += tokens
		}

		b := byType[entryType]
		if b == nil {
			b = &ContextInspectBucket{EntryType: entryType}
			byType[entryType] = b
		}
		b.Count++
		b.Chars += chars
		b.EstimatedTokens += tokens

		if detail {
			top = append(top, ContextInspectTopEntry{
				ID:              entry.ID,
				EntryType:       entryType,
				Title:           entry.Title,
				Timestamp:       entry.Timestamp,
				Chars:           chars,
				EstimatedTokens: tokens,
			})
		}
	}

	buckets := make([]ContextInspectBucket, 0, len(byType))
	for _, b := range byType {
		buckets = append(buckets, *b)
	}
	sort.SliceStable(buckets, func(i, j int) bool {
		if buckets[i].EstimatedTokens == buckets[j].EstimatedTokens {
			return buckets[i].EntryType < buckets[j].EntryType
		}
		return buckets[i].EstimatedTokens > buckets[j].EstimatedTokens
	})

	if detail {
		sort.SliceStable(top, func(i, j int) bool {
			if top[i].EstimatedTokens == top[j].EstimatedTokens {
				return top[i].Timestamp > top[j].Timestamp
			}
			return top[i].EstimatedTokens > top[j].EstimatedTokens
		})
		if len(top) > 20 {
			top = top[:20]
		}
	}

	systemPromptTokens, responseBudgetTokens, promptBudgetSource := contextInspectPromptBudget(agentID)
	systemPromptChars := systemPromptTokens * 4
	toolSchemaChars, toolSchemaTokens := a.estimateToolSchemaBudget()
	responseBudgetChars := responseBudgetTokens * 4

	sections := []ContextInspectSection{
		{
			Section:         "system_prompt",
			Chars:           systemPromptChars,
			EstimatedTokens: systemPromptTokens,
			Source:          promptBudgetSource,
		},
		{
			Section:         "tools_schema",
			Chars:           toolSchemaChars,
			EstimatedTokens: toolSchemaTokens,
			Source:          "measured",
		},
		{
			Section:         "context_entries",
			Chars:           contextEntryChars,
			EstimatedTokens: contextEntryTokens,
			Source:          "measured",
		},
		{
			Section:         "file_injections",
			Chars:           fileInjectionChars,
			EstimatedTokens: fileInjectionTokens,
			Source:          "measured",
		},
		{
			Section:         "response_budget",
			Chars:           responseBudgetChars,
			EstimatedTokens: responseBudgetTokens,
			Source:          promptBudgetSource,
		},
	}
	promptEstimatedTokens := 0
	for _, s := range sections {
		promptEstimatedTokens += s.EstimatedTokens
	}

	tasksSummary := ContextInspectTasks{}
	if tasks, err := a.Tasks(sessionID); err == nil {
		tasksSummary.Total = len(tasks)
		for _, t := range tasks {
			switch strings.ToLower(strings.TrimSpace(t.Status)) {
			case "completed":
				tasksSummary.Completed++
			case "in_progress":
				tasksSummary.InProgress++
			default:
				tasksSummary.Pending++
			}
		}
	}

	var memory *MemoryStatsResult
	if stats, err := a.MemoryStats(); err == nil {
		memory = stats
	}

	result := &ContextInspectResult{
		SessionID:              sessionID,
		Limit:                  limit,
		EntryCount:             len(entries),
		ContextChars:           totalContextChars,
		ContextEstimatedTokens: totalContextTokens,
		EstimatedTokens:        promptEstimatedTokens,
		Truncated:              len(entries) >= limit,
		ByEntryType:            buckets,
		TopEntries:             top,
		Sections:               sections,
		Tasks:                  tasksSummary,
		Memory:                 memory,
		RetrievedAt:            time.Now().UTC().Format(time.RFC3339),
	}
	if sessionMeta != nil {
		result.AgentID = sessionMeta.AgentID
		result.Namespace = sessionMeta.Namespace
		result.SessionStatus = sessionMeta.Status
		if agentID == "" {
			agentID = sessionMeta.AgentID
		}
	}
	if result.AgentID == "" {
		result.AgentID = agentID
	}
	return result, nil
}

func estimateContextChars(entry ContextEntry) int {
	chars := len(entry.Title) + len(entry.Content) + len(entry.FilePath)
	// Include minimal metadata overhead so very short entries are still represented.
	chars += len(entry.EntryType) + len(entry.Timestamp)
	if entry.LineStart > 0 || entry.LineEnd > 0 {
		chars += 12
	}
	return chars
}

func estimateContextTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	// Simple approximation used elsewhere in HUD docs: ~4 chars/token.
	return (chars + 3) / 4
}

func contextInspectPromptBudget(agentID string) (systemPromptTokens int, responseBudgetTokens int, source string) {
	systemPromptTokens = contextInspectSystemPromptTokensDefault
	responseBudgetTokens = contextInspectResponseBudgetTokensDefault
	source = "heuristic:default"

	lowerAgentID := strings.ToLower(strings.TrimSpace(agentID))
	switch {
	case strings.Contains(lowerAgentID, "claude"):
		systemPromptTokens = 1024
		responseBudgetTokens = 4096
		source = "heuristic:claude"
	case strings.Contains(lowerAgentID, "gemini"):
		systemPromptTokens = 900
		responseBudgetTokens = 3072
		source = "heuristic:gemini"
	case strings.Contains(lowerAgentID, "codex"), strings.Contains(lowerAgentID, "openai"):
		systemPromptTokens = 896
		responseBudgetTokens = 2048
		source = "heuristic:codex"
	}

	if v, ok := parsePositiveIntEnv("LOOM_HUD_CONTEXT_SYSTEM_PROMPT_TOKENS"); ok {
		systemPromptTokens = v
		source = "configured:env"
	}
	if v, ok := parsePositiveIntEnv("LOOM_HUD_CONTEXT_RESPONSE_BUDGET_TOKENS"); ok {
		responseBudgetTokens = v
		source = "configured:env"
	}

	return systemPromptTokens, responseBudgetTokens, source
}

func parsePositiveIntEnv(key string) (int, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

func isFileInjectionEntry(entry ContextEntry, entryType string) bool {
	t := strings.ToLower(strings.TrimSpace(entryType))
	if t == "file_read" || t == "code_context" {
		return true
	}
	return strings.TrimSpace(entry.FilePath) != ""
}

func (a *AgentBridge) estimateToolSchemaBudget() (chars int, tokens int) {
	if a == nil || a.client == nil {
		return 0, 0
	}
	toolsResult, err := a.client.Tools()
	if err != nil || toolsResult == nil {
		return 0, 0
	}
	for _, tool := range toolsResult.Tools {
		chars += len(tool.Name) + len(tool.Description)
		if schemaJSON, err := json.Marshal(tool.InputSchema); err == nil {
			chars += len(schemaJSON)
		}
	}
	return chars, estimateContextTokens(chars)
}

// KnowledgeRecall performs a cross-agent enhanced recall, searching across all
// sessions and agents. It returns entries with source agent_id attribution.
func (a *AgentBridge) KnowledgeRecall(query string, category string, tokenBudget int) (*KnowledgeResult, error) {
	args := map[string]any{
		"query":       query,
		"cross_agent": true,
	}
	if tokenBudget > 0 {
		args["token_budget"] = tokenBudget
	}
	var result KnowledgeResult
	if err := a.callAgentTool("agent_recall", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ContextStream returns context entries since a given time, up to limit.
func (a *AgentBridge) ContextStream(since time.Time, limit int) ([]ContextEntryInfo, error) {
	args := map[string]any{
		// agent_context_search requires a non-empty query string.
		// Keep the existing since: marker used by HUD stream callers.
		"query": "since:1970-01-01T00:00:00Z",
	}
	if !since.IsZero() {
		args["query"] = fmt.Sprintf("since:%s", since.UTC().Format(time.RFC3339))
	}
	if limit > 0 {
		args["limit"] = limit
	}
	var result struct {
		Results []ContextEntryInfo `json:"results"`
	}
	if err := a.callAgentTool("agent_context_search", args, &result); err != nil {
		return nil, err
	}
	return result.Results, nil
}

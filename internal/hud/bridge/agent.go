package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// AgentBridge wraps agent-context tool calls, routing them through the daemon's
// tools/call endpoint. Each method calls the appropriate agent_context__* tool
// and unmarshals the result into a clean Go struct.
type AgentBridge struct {
	client *DaemonClient
	cache  *Cache // session lookup cache (internal, always in-memory)
}

// NewAgentBridge creates an AgentBridge backed by the given DaemonClient.
func NewAgentBridge(client *DaemonClient) *AgentBridge {
	return &AgentBridge{client: client, cache: NewCache()}
}

// --- DTO structs ---

// SessionInfo describes an agent session.
type SessionInfo struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	Namespace   string `json:"namespace"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at,omitempty"`
	Status      string `json:"status"`
	Description string `json:"description"`
	EntryCount  int    `json:"entry_count"`
	TotalTokens int    `json:"total_tokens"`
}

// TaskInfo describes an agent task.
type TaskInfo struct {
	ID        string   `json:"id"`
	SessionID string   `json:"session_id"`
	AgentID   string   `json:"agent_id"`
	Namespace string   `json:"namespace"`
	Title     string   `json:"title"`
	Context   string   `json:"context,omitempty"`
	Priority  string   `json:"priority"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags,omitempty"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

// WorkflowInfo describes a workflow summary (MCP field names for deserialization).
type WorkflowInfo struct {
	ID          string  `json:"workflow_id"`
	Name        string  `json:"name,omitempty"`
	Status      string  `json:"status"`
	Progress    float64 `json:"progress,omitempty"`
	CurrentStep string  `json:"current_step"`
	CreatedAt   string  `json:"created_at"`
	Error       string  `json:"error,omitempty"`
}

// WorkflowStep describes a single step within a workflow.
type WorkflowStep struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Type   string `json:"type,omitempty"`
	Error  string `json:"error,omitempty"`
}

// WorkflowDetail is the full workflow status including steps (MCP field names).
type WorkflowDetail struct {
	ID             string          `json:"workflow_id"`
	Name           string          `json:"name,omitempty"`
	Status         string          `json:"status"`
	CurrentStep    string          `json:"current_step"`
	Progress       float64         `json:"progress,omitempty"`
	CompletedSteps int             `json:"completed_steps,omitempty"`
	TotalSteps     int             `json:"total_steps,omitempty"`
	Steps          []WorkflowStep  `json:"steps,omitempty"`
	CreatedAt      string          `json:"created_at"`
	StartedAt      string          `json:"started_at,omitempty"`
	CompletedAt    string          `json:"completed_at,omitempty"`
	Error          string          `json:"error,omitempty"`
	Events         []WorkflowEvent `json:"events,omitempty"`
}

// WorkflowEvent is a single workflow execution event.
type WorkflowEvent struct {
	ID        string         `json:"id"`
	EventType string         `json:"event_type"`
	Timestamp string         `json:"timestamp"`
	StepID    string         `json:"step_id,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// MemoryTierStats describes statistics for a single memory tier.
type MemoryTierStats struct {
	Items  int `json:"item_count"`
	Tokens int `json:"token_count"`
}

// MemoryStatsResult holds the full memory hierarchy statistics.
type MemoryStatsResult struct {
	WorkingMemory   MemoryTierStats `json:"working_memory"`
	ShortTermMemory MemoryTierStats `json:"short_term_memory"`
	LongTermMemory  MemoryTierStats `json:"long_term_memory"`
	TotalItems      int             `json:"total_items"`
	TotalTokens     int             `json:"total_tokens"`
}

// MemoryItem describes a single memory item.
type MemoryItem struct {
	ID              string  `json:"id"`
	Title           string  `json:"title"`
	Content         string  `json:"content,omitempty"`
	Tier            string  `json:"tier"`
	Importance      string  `json:"importance"`
	ImportanceScore float64 `json:"importance_score"`
	Tokens          int     `json:"original_tokens"`
	Status          string  `json:"status,omitempty"`
	Category        string  `json:"category,omitempty"`
	AccessedAt      string  `json:"created_at,omitempty"`
	LastAccessed    string  `json:"last_accessed_at,omitempty"`
}

// GraphStatsResult holds knowledge graph statistics.
type GraphStatsResult struct {
	EntityCount   int            `json:"total_entities"`
	RelationCount int            `json:"total_relations"`
	EntityTypes   map[string]int `json:"entities_by_type"`
	RelationTypes map[string]int `json:"relations_by_type"`
}

// EntityInfo describes an entity in the knowledge graph.
type EntityInfo struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Type        string         `json:"type,omitempty"`
	EntityType  string         `json:"entity_type,omitempty"`
	Description string         `json:"description,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Properties  map[string]any `json:"properties,omitempty"`
}

// ContextEntryInfo describes a context stream entry.
type ContextEntryInfo struct {
	Score float64      `json:"score"`
	Entry ContextEntry `json:"entry"`
}

// ContextEntry is the inner entry within a context search result.
type ContextEntry struct {
	ID         string `json:"id"`
	EntryType  string `json:"entry_type"`
	AgentID    string `json:"agent_id"`
	Namespace  string `json:"namespace"`
	Title      string `json:"title"`
	Content    string `json:"content,omitempty"`
	FilePath   string `json:"file_path,omitempty"`
	LineStart  int    `json:"line_start,omitempty"`
	LineEnd    int    `json:"line_end,omitempty"`
	TokenCount int    `json:"token_count,omitempty"`
	Timestamp  string `json:"timestamp"`
}

// ContextInspectBucket describes aggregate context weight by entry type.
type ContextInspectBucket struct {
	EntryType       string `json:"entry_type"`
	Count           int    `json:"count"`
	Chars           int    `json:"chars"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

// ContextInspectTopEntry highlights the heaviest entries in a session.
type ContextInspectTopEntry struct {
	ID              string `json:"id"`
	EntryType       string `json:"entry_type"`
	Title           string `json:"title"`
	Timestamp       string `json:"timestamp"`
	Chars           int    `json:"chars"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

// ContextInspectTasks summarizes task state for the inspected session.
type ContextInspectTasks struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
}

// ContextInspectSection describes estimated prompt budget by section.
type ContextInspectSection struct {
	Section         string `json:"section"`
	Chars           int    `json:"chars"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Source          string `json:"source"`
}

// ContextInspectResult is a context budget breakdown for a session.
type ContextInspectResult struct {
	SessionID              string                   `json:"session_id"`
	AgentID                string                   `json:"agent_id,omitempty"`
	Namespace              string                   `json:"namespace,omitempty"`
	SessionStatus          string                   `json:"session_status,omitempty"`
	Limit                  int                      `json:"limit"`
	EntryCount             int                      `json:"entry_count"`
	ContextChars           int                      `json:"context_chars"`
	ContextEstimatedTokens int                      `json:"context_estimated_tokens"`
	EstimatedTokens        int                      `json:"estimated_tokens"`
	Truncated              bool                     `json:"truncated"`
	ByEntryType            []ContextInspectBucket   `json:"by_entry_type"`
	TopEntries             []ContextInspectTopEntry `json:"top_entries,omitempty"`
	Sections               []ContextInspectSection  `json:"sections"`
	Tasks                  ContextInspectTasks      `json:"tasks"`
	Memory                 *MemoryStatsResult       `json:"memory,omitempty"`
	RetrievedAt            string                   `json:"retrieved_at"`
}

const (
	contextInspectSystemPromptTokensDefault   = 768
	contextInspectResponseBudgetTokensDefault = 2048
)

func normalizeEntityInfo(e *EntityInfo) {
	if e == nil {
		return
	}
	if e.EntityType == "" {
		e.EntityType = e.Type
	}
	if e.Type == "" {
		e.Type = e.EntityType
	}
}

func normalizeRelationInfo(r *RelationInfo) {
	if r == nil {
		return
	}
	if r.RelationType == "" {
		r.RelationType = r.Type
	}
	if r.Type == "" {
		r.Type = r.RelationType
	}
}

// --- Helper to call an agent tool and unmarshal ---

// mcpContent represents an item in an MCP CallToolResult's content array.
type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// mcpCallToolResult is the MCP envelope returned by tools/call.
type mcpCallToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

// callAgentTool invokes an agent_context tool and unmarshals the response
// into the provided target. It unwraps the MCP CallToolResult envelope and
// supports both JSON and TOON (Token-Optimized Object Notation) text payloads.
func (a *AgentBridge) callAgentTool(toolName string, args map[string]any, target any) error {
	raw, err := a.client.CallTool("agent_context__"+toolName, args)
	if err != nil {
		return fmt.Errorf("agent tool %s: %w", toolName, err)
	}

	// Try to unwrap MCP content envelope first so tool-level errors are
	// surfaced even when the caller does not expect a response payload.
	var envelope mcpCallToolResult
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if envelope.IsError {
			errText := "tool returned error"
			for _, c := range envelope.Content {
				if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
					errText = strings.TrimSpace(c.Text)
					break
				}
			}
			return fmt.Errorf("agent tool %s: %s", toolName, errText)
		}
		if target == nil {
			return nil
		}

		if len(envelope.Content) == 0 {
			return fmt.Errorf("unmarshal %s result: empty content envelope", toolName)
		}

		// Extract the text payload from the first content item.
		for _, c := range envelope.Content {
			if c.Type == "text" && c.Text != "" {
				// Try JSON first, fall back to TOON.
				if err := json.Unmarshal([]byte(c.Text), target); err != nil {
					jsonBytes, toonErr := mcp.DecodeTOONToJSON(c.Text)
					if toonErr != nil {
						return fmt.Errorf("unmarshal %s text (json: %v, toon: %v)", toolName, err, toonErr)
					}
					if err := json.Unmarshal(jsonBytes, target); err != nil {
						return fmt.Errorf("unmarshal %s decoded toon: %w", toolName, err)
					}
				}
				return nil
			}
		}
		return fmt.Errorf("unmarshal %s result: no text content in envelope", toolName)
	}

	if target == nil {
		return nil
	}

	// Fallback: try direct unmarshal (in case the daemon returns unwrapped JSON).
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("unmarshal %s result: %w", toolName, err)
	}
	return nil
}

// callAgentToolTimeout is like callAgentTool but uses a per-call timeout
// override on the underlying DaemonClient RPC.
func (a *AgentBridge) callAgentToolTimeout(toolName string, args map[string]any, target any, timeout time.Duration) error {
	raw, err := a.client.CallToolWithTimeout("agent_context__"+toolName, args, timeout)
	if err != nil {
		return fmt.Errorf("agent tool %s: %w", toolName, err)
	}

	var envelope mcpCallToolResult
	if err := json.Unmarshal(raw, &envelope); err == nil {
		if envelope.IsError {
			errText := "tool returned error"
			for _, c := range envelope.Content {
				if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
					errText = strings.TrimSpace(c.Text)
					break
				}
			}
			return fmt.Errorf("agent tool %s: %s", toolName, errText)
		}
		if target == nil {
			return nil
		}
		for _, c := range envelope.Content {
			if c.Type == "text" && c.Text != "" {
				if err := json.Unmarshal([]byte(c.Text), target); err != nil {
					jsonBytes, toonErr := mcp.DecodeTOONToJSON(c.Text)
					if toonErr != nil {
						return fmt.Errorf("unmarshal %s text (json: %v, toon: %v)", toolName, err, toonErr)
					}
					if err := json.Unmarshal(jsonBytes, target); err != nil {
						return fmt.Errorf("unmarshal %s decoded toon: %w", toolName, err)
					}
				}
				return nil
			}
		}
		return fmt.Errorf("unmarshal %s result: no text content in envelope", toolName)
	}

	if target == nil {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("unmarshal %s result: %w", toolName, err)
	}
	return nil
}

// invalidateSessionCache removes the cached active-session entry for an agent.
func (a *AgentBridge) invalidateSessionCache(agentID string) {
	a.cache.Invalidate("active_session:" + agentID)
}

// --- Public methods ---

// Sessions returns all agent sessions.
func (a *AgentBridge) Sessions() ([]SessionInfo, error) {
	var result struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	if err := a.callAgentTool("agent_session_list", nil, &result); err != nil {
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

// Tasks returns tasks for a specific session.
func (a *AgentBridge) Tasks(sessionID string) ([]TaskInfo, error) {
	args := map[string]any{}
	if sessionID != "" {
		args["session_id"] = sessionID
	}
	var result struct {
		Tasks []TaskInfo `json:"tasks"`
	}
	if err := a.callAgentTool("agent_task_list", args, &result); err != nil {
		return nil, err
	}
	return result.Tasks, nil
}

// AllTasks returns all tasks across all sessions.
func (a *AgentBridge) AllTasks() ([]TaskInfo, error) {
	return a.Tasks("")
}

// WorkflowDefineResult holds the result of defining a workflow.
type WorkflowDefineResult struct {
	OK           bool   `json:"ok"`
	DefinitionID string `json:"definition_id"`
	Name         string `json:"name"`
	StepCount    int    `json:"step_count"`
}

// WorkflowDefinitionInfo describes a registered workflow definition.
type WorkflowDefinitionInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Namespace   string `json:"namespace"`
	StepCount   int    `json:"step_count"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
}

// WorkflowDefine registers a new workflow definition.
func (a *AgentBridge) WorkflowDefine(args map[string]any) (*WorkflowDefineResult, error) {
	var result WorkflowDefineResult
	if err := a.callAgentTool("agent_workflow_define", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// WorkflowDefinitions lists registered workflow definitions.
func (a *AgentBridge) WorkflowDefinitions(namespace string) ([]WorkflowDefinitionInfo, error) {
	args := map[string]any{}
	if namespace != "" {
		args["namespace"] = namespace
	}
	var result struct {
		Definitions []WorkflowDefinitionInfo `json:"definitions"`
	}
	if err := a.callAgentTool("agent_workflow_definitions", args, &result); err != nil {
		return nil, err
	}
	return result.Definitions, nil
}

// WorkflowList returns all workflows.
func (a *AgentBridge) WorkflowList() ([]WorkflowInfo, error) {
	var result struct {
		Workflows []WorkflowInfo `json:"workflows"`
	}
	if err := a.callAgentTool("agent_workflow_list", nil, &result); err != nil {
		return nil, err
	}
	return result.Workflows, nil
}

// WorkflowStatus returns the full detail for a single workflow.
func (a *AgentBridge) WorkflowStatus(id string) (*WorkflowDetail, error) {
	args := map[string]any{"workflow_id": id}
	var result WorkflowDetail
	if err := a.callAgentTool("agent_workflow_status", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// WorkflowEvents returns workflow execution events for a single workflow.
func (a *AgentBridge) WorkflowEvents(id string) ([]WorkflowEvent, error) {
	args := map[string]any{"workflow_id": id}
	var result struct {
		Events []WorkflowEvent `json:"events"`
	}
	if err := a.callAgentTool("agent_workflow_events", args, &result); err != nil {
		return nil, err
	}
	return result.Events, nil
}

// ApproveStep approves a pending step in a workflow.
func (a *AgentBridge) ApproveStep(workflowID, stepID string) error {
	args := map[string]any{
		"workflow_id": workflowID,
		"step_id":     stepID,
	}
	return a.callAgentTool("agent_workflow_approve", args, nil)
}

// RejectStep rejects a pending step in a workflow.
func (a *AgentBridge) RejectStep(workflowID, stepID string) error {
	args := map[string]any{
		"workflow_id": workflowID,
		"step_id":     stepID,
	}
	return a.callAgentTool("agent_workflow_reject", args, nil)
}

// CancelWorkflow cancels a running workflow.
func (a *AgentBridge) CancelWorkflow(workflowID string) error {
	args := map[string]any{"workflow_id": workflowID}
	return a.callAgentTool("agent_workflow_cancel", args, nil)
}

// MemoryStats returns the memory hierarchy statistics.
func (a *AgentBridge) MemoryStats() (*MemoryStatsResult, error) {
	var result MemoryStatsResult
	if err := a.callAgentTool("agent_memory_stats", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// MemoryRecall retrieves memory items by tier and/or query.
func (a *AgentBridge) MemoryRecall(tier, query string, limit int) ([]MemoryItem, error) {
	args := map[string]any{}
	if tier != "" {
		args["tiers"] = []string{tier}
	}
	if query != "" {
		args["query"] = query
	}
	if limit > 0 {
		args["limit"] = limit
	}
	var result struct {
		Items []MemoryItem `json:"items"`
	}
	if err := a.callAgentTool("agent_memory_recall", args, &result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

// MemoryPromote promotes a memory item to a higher tier.
func (a *AgentBridge) MemoryPromote(id string) error {
	args := map[string]any{"item_ids": []string{id}}
	return a.callAgentTool("agent_memory_promote", args, nil)
}

// MemoryDemote demotes a memory item to a lower tier.
func (a *AgentBridge) MemoryDemote(id string) error {
	args := map[string]any{"item_ids": []string{id}}
	return a.callAgentTool("agent_memory_demote", args, nil)
}

// GraphStats returns knowledge graph statistics.
func (a *AgentBridge) GraphStats() (*GraphStatsResult, error) {
	var result GraphStatsResult
	if err := a.callAgentTool("agent_graph_stats", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// EntityFind searches for entities in the knowledge graph.
func (a *AgentBridge) EntityFind(query string, entityType string, limit int) ([]EntityInfo, error) {
	args := map[string]any{}
	if query != "" {
		args["name_pattern"] = query
	}
	if entityType != "" {
		args["type"] = entityType
	}
	if limit > 0 {
		args["limit"] = limit
	}
	var result struct {
		Entities []EntityInfo `json:"entities"`
	}
	if err := a.callAgentTool("agent_entity_find", args, &result); err != nil {
		return nil, err
	}
	for i := range result.Entities {
		normalizeEntityInfo(&result.Entities[i])
	}
	return result.Entities, nil
}

// --- Presence / Coordination DTOs ---

// PresenceInfo describes an agent in the presence registry.
type PresenceInfo struct {
	AgentID       string   `json:"agent_id"`
	SessionID     string   `json:"session_id,omitempty"`
	Status        string   `json:"status"`
	AgentType     string   `json:"agent_type"`
	Description   string   `json:"description"`
	CurrentTask   string   `json:"current_task"`
	ActiveFiles   []string `json:"active_files"`
	Branch        string   `json:"branch"`
	PRUrl         string   `json:"pr_url,omitempty"`
	WorktreeID    string   `json:"worktree_id"`
	LastHeartbeat string   `json:"last_heartbeat"`
	RegisteredAt  string   `json:"registered_at"`
}

// PresenceHeartbeatResult is the response from agent_presence_heartbeat.
type PresenceHeartbeatResult struct {
	OK            bool             `json:"ok"`
	AgentID       string           `json:"agent_id"`
	LastHeartbeat string           `json:"last_heartbeat"`
	HasConflicts  bool             `json:"has_conflicts,omitempty"`
	Conflicts     []map[string]any `json:"conflicts,omitempty"`
	Warning       string           `json:"_warning,omitempty"`
}

// FileClaimInfo describes a file claim (advisory lock).
type FileClaimInfo struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	FilePath  string `json:"file_path"`
	ClaimType string `json:"claim_type"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// WorktreeInfo describes a git worktree assignment.
type WorktreeInfo struct {
	AssignmentID string `json:"assignment_id"`
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	BaseBranch   string `json:"base_branch"`
	Purpose      string `json:"purpose"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
	ReleasedAt   string `json:"released_at,omitempty"`
	GitStatus    string `json:"git_status,omitempty"`
}

// CompactionInfo describes the compaction scheduler status.
type CompactionInfo struct {
	Running        bool   `json:"running"`
	LastRun        string `json:"last_run,omitempty"`
	ItemsCompacted int    `json:"items_compacted"`
	ItemsPromoted  int    `json:"items_promoted"`
	ItemsExpired   int    `json:"items_expired"`
}

// --- Presence / Coordination methods ---

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

// FileClaimList returns file claims, optionally filtered by agent.
func (a *AgentBridge) FileClaimList(agentID string) ([]FileClaimInfo, error) {
	args := map[string]any{}
	if agentID != "" {
		args["agent_id"] = agentID
	}
	var result struct {
		Claims []FileClaimInfo `json:"claims"`
	}
	if err := a.callAgentTool("agent_file_claim_list", args, &result); err != nil {
		return nil, err
	}
	return result.Claims, nil
}

// WorktreeList returns worktree assignments, optionally filtered by agent and status.
func (a *AgentBridge) WorktreeList(agentID, status string) ([]WorktreeInfo, error) {
	args := map[string]any{}
	if agentID != "" {
		args["agent_id"] = agentID
	}
	if status != "" {
		args["status"] = status
	}
	var result struct {
		Assignments []WorktreeInfo `json:"assignments"`
	}
	if err := a.callAgentTool("agent_worktree_list", args, &result); err != nil {
		return nil, err
	}
	return result.Assignments, nil
}

// CompactionStatus returns the compaction scheduler status.
func (a *AgentBridge) CompactionStatus() (*CompactionInfo, error) {
	var result CompactionInfo
	if err := a.callAgentTool("agent_compaction_status", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- CRUD methods (v2) ---

// CreateTaskParams holds all fields for task creation.
type CreateTaskParams struct {
	SessionID  string
	Title      string
	Priority   string
	Tags       []string
	Context    string   // Description of what needs to be done
	FilePath   string   // Related file
	LineNumber int      // Related line
	BlockedBy  []string // Task IDs this is blocked by
}

// CreateTask creates a new task in a session.
func (a *AgentBridge) CreateTask(p CreateTaskParams) error {
	if p.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	task := map[string]any{
		"title": p.Title,
	}
	if p.Priority != "" {
		task["priority"] = p.Priority
	}
	if len(p.Tags) > 0 {
		task["tags"] = p.Tags
	}
	if p.Context != "" {
		task["context"] = p.Context
	}
	if p.FilePath != "" {
		task["file_path"] = p.FilePath
	}
	if p.LineNumber > 0 {
		task["line_number"] = p.LineNumber
	}
	if len(p.BlockedBy) > 0 {
		task["blocked_by"] = p.BlockedBy
	}
	args := map[string]any{
		"session_id": p.SessionID,
		"tasks":      []map[string]any{task},
	}
	return a.callAgentTool("agent_task_add", args, nil)
}

// UpdateTaskParams holds all fields for task updates.
type UpdateTaskParams struct {
	ID         string `json:"task_id"`
	Status     string `json:"status"`
	Priority   string `json:"priority,omitempty"`
	Resolution string `json:"resolution,omitempty"`
}

// UpdateTask updates a task's status, priority, and/or resolution.
func (a *AgentBridge) UpdateTask(p UpdateTaskParams) error {
	args := map[string]any{"task_id": p.ID}
	if p.Status != "" {
		args["status"] = p.Status
	}
	if p.Priority != "" {
		args["priority"] = p.Priority
	}
	if p.Resolution != "" {
		args["resolution"] = p.Resolution
	}
	return a.callAgentTool("agent_task_update", args, nil)
}

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

// MemoryAdd adds a new memory item.
func (a *AgentBridge) MemoryAdd(title, content, tier, importance, category string) error {
	item := map[string]any{
		"title":   title,
		"content": content,
	}
	if tier != "" {
		item["tier"] = tier
	}
	if importance != "" {
		item["importance"] = importance
	}
	if category != "" {
		item["category"] = category
	}
	args := map[string]any{
		"items": []map[string]any{item},
	}
	return a.callAgentTool("agent_memory_add", args, nil)
}

// MemoryDelete deletes a memory item by ID.
func (a *AgentBridge) MemoryDelete(id string) error {
	args := map[string]any{
		"item_ids": []string{id},
		"confirm":  true,
	}
	return a.callAgentTool("agent_memory_delete", args, nil)
}

// EntityAdd creates a new entity in the knowledge graph.
func (a *AgentBridge) EntityAdd(name, entityType, namespace string, props map[string]any) error {
	entity := map[string]any{
		"name": name,
		"type": entityType,
	}
	if namespace != "" {
		entity["namespace"] = namespace
	}
	if len(props) > 0 {
		entity["properties"] = props
	}
	args := map[string]any{
		"entities": []map[string]any{entity},
	}
	return a.callAgentTool("agent_entity_add", args, nil)
}

// EntityDelete deletes an entity by ID.
func (a *AgentBridge) EntityDelete(id string) error {
	args := map[string]any{
		"entity_ids": []string{id},
		"confirm":    true,
	}
	return a.callAgentTool("agent_entity_delete", args, nil)
}

// EntityDetail describes a single entity with its relations.
type EntityDetail struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Type              string         `json:"type,omitempty"`
	EntityType        string         `json:"entity_type,omitempty"`
	Namespace         string         `json:"namespace,omitempty"`
	Properties        map[string]any `json:"properties,omitempty"`
	InboundRelations  []RelationInfo `json:"inbound_relations,omitempty"`
	OutboundRelations []RelationInfo `json:"outbound_relations,omitempty"`
}

// RelationInfo describes a relation in the knowledge graph.
type RelationInfo struct {
	ID           string `json:"id"`
	Source       string `json:"source_id"`
	SourceName   string `json:"source_name,omitempty"`
	Target       string `json:"target_id"`
	TargetName   string `json:"target_name,omitempty"`
	Type         string `json:"type,omitempty"`
	RelationType string `json:"relation_type,omitempty"`
}

// EntityGet retrieves a single entity with its relations.
func (a *AgentBridge) EntityGet(id string) (*EntityDetail, error) {
	args := map[string]any{"entity_ids": []string{id}}
	var result struct {
		Entities []EntityDetail `json:"entities"`
	}
	if err := a.callAgentTool("agent_entity_get", args, &result); err != nil {
		return nil, err
	}
	if len(result.Entities) == 0 {
		return nil, fmt.Errorf("entity not found: %s", id)
	}
	entity := result.Entities[0]
	if entity.EntityType == "" {
		entity.EntityType = entity.Type
	}
	if entity.Type == "" {
		entity.Type = entity.EntityType
	}

	var relResult struct {
		Relations []RelationInfo `json:"relations"`
	}
	if err := a.callAgentTool("agent_relation_get", map[string]any{"entity_id": id}, &relResult); err == nil {
		for i := range relResult.Relations {
			normalizeRelationInfo(&relResult.Relations[i])
			if relResult.Relations[i].Source == id {
				entity.OutboundRelations = append(entity.OutboundRelations, relResult.Relations[i])
			}
			if relResult.Relations[i].Target == id {
				entity.InboundRelations = append(entity.InboundRelations, relResult.Relations[i])
			}
		}
	}

	return &entity, nil
}

// RelationAdd creates a relation between two entities.
func (a *AgentBridge) RelationAdd(sourceID, targetID, relationType string) error {
	args := map[string]any{
		"relations": []map[string]any{
			{
				"source_id": sourceID,
				"target_id": targetID,
				"type":      relationType,
			},
		},
	}
	return a.callAgentTool("agent_relation_add", args, nil)
}

// RelationDelete deletes a relation by ID.
func (a *AgentBridge) RelationDelete(id string) error {
	args := map[string]any{
		"relation_ids": []string{id},
		"confirm":      true,
	}
	return a.callAgentTool("agent_relation_delete", args, nil)
}

// GraphFindPath finds the shortest path between two entities.
func (a *AgentBridge) GraphFindPath(fromID, toID string, maxDepth int) ([]EntityInfo, error) {
	args := map[string]any{
		"source_id": fromID,
		"target_id": toID,
	}
	if maxDepth > 0 {
		args["max_depth"] = maxDepth
	}
	var result struct {
		Path []string `json:"path"`
	}
	if err := a.callAgentTool("agent_graph_find_path", args, &result); err != nil {
		return nil, err
	}
	if len(result.Path) == 0 {
		return nil, nil
	}

	var entitiesResult struct {
		Entities []EntityInfo `json:"entities"`
	}
	if err := a.callAgentTool("agent_entity_get", map[string]any{"entity_ids": result.Path}, &entitiesResult); err != nil {
		fallback := make([]EntityInfo, 0, len(result.Path))
		for _, id := range result.Path {
			fallback = append(fallback, EntityInfo{
				ID:         id,
				Name:       id,
				Type:       "entity",
				EntityType: "entity",
			})
		}
		return fallback, nil
	}

	byID := make(map[string]EntityInfo, len(entitiesResult.Entities))
	for i := range entitiesResult.Entities {
		normalizeEntityInfo(&entitiesResult.Entities[i])
		byID[entitiesResult.Entities[i].ID] = entitiesResult.Entities[i]
	}

	path := make([]EntityInfo, 0, len(result.Path))
	for _, id := range result.Path {
		if e, ok := byID[id]; ok {
			path = append(path, e)
			continue
		}
		path = append(path, EntityInfo{
			ID:         id,
			Name:       id,
			Type:       "entity",
			EntityType: "entity",
		})
	}
	return path, nil
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

// --- Reasoning chain methods ---

// ReasoningChainInfo describes a reasoning chain.
type ReasoningChainInfo struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	StepCount   int     `json:"step_count"`
	Confidence  float64 `json:"confidence,omitempty"`
	CreatedAt   string  `json:"created_at"`
	CompletedAt string  `json:"completed_at,omitempty"`
}

// ReasoningStepInfo describes a step in a reasoning chain.
type ReasoningStepInfo struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Evidence    string  `json:"evidence,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// ReasoningChainDetail is a chain with its steps.
type ReasoningChainDetail struct {
	ReasoningChainInfo
	Steps []ReasoningStepInfo `json:"steps"`
}

// ReasoningChainList returns all reasoning chains.
func (a *AgentBridge) ReasoningChainList() ([]ReasoningChainInfo, error) {
	var result struct {
		Chains []ReasoningChainInfo `json:"chains"`
	}
	if err := a.callAgentTool("agent_reasoning_chain_list", nil, &result); err != nil {
		return nil, err
	}
	return result.Chains, nil
}

// ReasoningChainGet returns a chain with its steps.
func (a *AgentBridge) ReasoningChainGet(id string) (*ReasoningChainDetail, error) {
	args := map[string]any{"chain_id": id}
	var result ReasoningChainDetail
	if err := a.callAgentTool("agent_reasoning_chain_get", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReasoningChainAdd creates a new reasoning chain.
func (a *AgentBridge) ReasoningChainAdd(title, description string) error {
	args := map[string]any{
		"title":       title,
		"description": description,
	}
	return a.callAgentTool("agent_reasoning_chain_add", args, nil)
}

// --- Handoff methods ---

// HandoffInfo describes a handoff between agents.
type HandoffInfo struct {
	ID         string `json:"id"`
	FromAgent  string `json:"from_agent"`
	ToAgent    string `json:"to_agent,omitempty"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	Context    string `json:"context,omitempty"`
	CreatedAt  string `json:"created_at"`
	AcceptedAt string `json:"accepted_at,omitempty"`
}

// HandoffList returns pending handoffs.
func (a *AgentBridge) HandoffList() ([]HandoffInfo, error) {
	var result struct {
		Handoffs []HandoffInfo `json:"handoffs"`
	}
	if err := a.callAgentTool("agent_handoff_list", nil, &result); err != nil {
		return nil, err
	}
	return result.Handoffs, nil
}

// HandoffCreate creates a new handoff.
func (a *AgentBridge) HandoffCreate(toAgent, summary, context string) error {
	args := map[string]any{
		"summary": summary,
	}
	if toAgent != "" {
		args["to_agent"] = toAgent
	}
	if context != "" {
		args["context"] = context
	}
	return a.callAgentTool("agent_handoff_create", args, nil)
}

// HandoffAccept accepts a handoff.
func (a *AgentBridge) HandoffAccept(id string) error {
	args := map[string]any{"handoff_id": id}
	return a.callAgentTool("agent_handoff_accept", args, nil)
}

// --- Template methods ---

// TemplateInfo describes a session template.
type TemplateInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

// TemplateList returns all session templates.
func (a *AgentBridge) TemplateList() ([]TemplateInfo, error) {
	var result struct {
		Templates []TemplateInfo `json:"templates"`
	}
	if err := a.callAgentTool("agent_template_list", nil, &result); err != nil {
		return nil, err
	}
	return result.Templates, nil
}

// --- Annotation methods ---

// AnnotationInfo describes a code annotation.
type AnnotationInfo struct {
	ID       string `json:"id"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line,omitempty"`
	AgentID  string `json:"agent_id"`
	Content  string `json:"content"`
	Category string `json:"category,omitempty"`
}

// AnnotationGet retrieves code annotations, optionally filtered by file.
func (a *AgentBridge) AnnotationGet(filePath string) ([]AnnotationInfo, error) {
	args := map[string]any{}
	if filePath != "" {
		args["file_path"] = filePath
	}
	var result struct {
		Annotations []AnnotationInfo `json:"annotations"`
	}
	if err := a.callAgentTool("agent_code_annotations_get", args, &result); err != nil {
		return nil, err
	}
	return result.Annotations, nil
}

// AnnotationAdd creates a code annotation.
func (a *AgentBridge) AnnotationAdd(filePath, content, category string, line int) error {
	args := map[string]any{
		"file_path": filePath,
		"content":   content,
	}
	if category != "" {
		args["category"] = category
	}
	if line > 0 {
		args["line"] = line
	}
	return a.callAgentTool("agent_code_annotate", args, nil)
}

// --- Agent lifecycle methods ---

// SessionStartParams holds parameters for starting an agent session.
type SessionStartParams struct {
	Namespace   string `json:"namespace"`
	AgentID     string `json:"agent_id"`
	AgentType   string `json:"agent_type"`
	Description string `json:"description"`
	AutoRecall  bool   `json:"auto_recall"`
}

// SessionStartResult holds the result of starting a session.
type SessionStartResult struct {
	SessionID       string `json:"session_id"`
	RecalledContext string `json:"recalled_context,omitempty"`
	AlreadyExisted  bool   `json:"already_existed"`
}

// StartSession creates a session, registers presence, and optionally recalls context.
// It is idempotent: if the agent already has an active session in the same namespace,
// it returns the existing session ID instead of creating a new one.
//
// Presence registration and context recall are fire-and-forget: they run in
// background goroutines so the caller is not blocked by non-critical MCP calls.
func (a *AgentBridge) StartSession(p SessionStartParams) (*SessionStartResult, error) {
	// Check for existing active session in the same namespace (cached, fast path).
	if existing, err := a.GetActiveSession(p.AgentID); err == nil && existing != nil {
		if existing.Namespace == p.Namespace && existing.Status == "active" {
			return &SessionStartResult{
				SessionID:      existing.ID,
				AlreadyExisted: true,
			}, nil
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

	// Fire-and-forget: recall context (best-effort, not returned to caller).
	if p.AutoRecall {
		recallArgs := map[string]any{
			"query":        p.Description,
			"token_budget": 4000,
		}
		if p.Namespace != "" {
			recallArgs["file_context"] = p.Namespace
		}
		go func() { _ = a.callAgentTool("agent_context_recall_enhanced", recallArgs, nil) }()
	}

	return result, nil
}

// SessionEndParams holds parameters for ending an agent session.
type SessionEndParams struct {
	SessionID string `json:"session_id"`
	AgentID   string `json:"agent_id"`
	Summarize bool   `json:"summarize"`
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
	}
	if p.Summarize {
		args["summarize"] = true
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
	Status      string
	ActiveFiles []string
	CurrentTask string
	Branch      string
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
	if err := a.callAgentTool("agent_session_list", args, &listResult); err != nil {
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

// --- Dispatch + Claim methods ---

// DispatchTaskParams holds parameters for dispatching a task to an agent.
type DispatchTaskParams struct {
	TargetAgentID string
	Title         string
	Context       string
	Priority      string
	Tags          []string
	FilePath      string
	LineNumber    int
	BlockedBy     []string
}

// DispatchTask creates a task and a handoff targeting a specific agent.
// This enables the HUD or CLI to push work to an active agent.
func (a *AgentBridge) DispatchTask(p DispatchTaskParams) (map[string]any, error) {
	// Find target agent's active session for task creation.
	session, err := a.GetActiveSession(p.TargetAgentID)
	if err != nil {
		return nil, fmt.Errorf("find target session: %w", err)
	}

	sessionID := ""
	if session != nil {
		sessionID = session.ID
	}

	taskCreated := false

	// Create the task.
	if sessionID != "" {
		if err := a.CreateTask(CreateTaskParams{
			SessionID:  sessionID,
			Title:      p.Title,
			Context:    p.Context,
			Priority:   p.Priority,
			Tags:       mergeDispatchTags(p.Tags),
			FilePath:   p.FilePath,
			LineNumber: p.LineNumber,
			BlockedBy:  p.BlockedBy,
		}); err != nil {
			return nil, fmt.Errorf("create task: %w", err)
		}
		taskCreated = true
	}

	// Create a handoff targeting the agent.
	handoffSummary := fmt.Sprintf("[Dispatched] %s", p.Title)
	if err := a.HandoffCreate(p.TargetAgentID, handoffSummary, p.Context); err != nil {
		return nil, fmt.Errorf("create handoff: %w", err)
	}

	return map[string]any{
		"ok":              true,
		"target_agent_id": p.TargetAgentID,
		"session_id":      sessionID,
		"title":           p.Title,
		"priority":        p.Priority,
		"task_created":    taskCreated,
		"handoff_created": true,
	}, nil
}

func mergeDispatchTags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags)+1)

	for _, tag := range append([]string{"dispatched"}, tags...) {
		normalized := strings.TrimSpace(tag)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

// ReleaseFileClaim releases a specific file claim for an agent.
func (a *AgentBridge) ReleaseFileClaim(agentID, filePath string) error {
	args := map[string]any{
		"agent_id":  agentID,
		"file_path": filePath,
	}
	return a.callAgentTool("agent_file_claim_release", args, nil)
}

// HandoffListForAgent returns pending handoffs targeted at a specific agent.
func (a *AgentBridge) HandoffListForAgent(agentID string) ([]HandoffInfo, error) {
	all, err := a.HandoffList()
	if err != nil {
		return nil, err
	}
	var result []HandoffInfo
	for _, h := range all {
		if h.Status == "pending" && (h.ToAgent == agentID || h.ToAgent == "") {
			result = append(result, h)
		}
	}
	return result, nil
}

// --- Tunnel/cache methods ---

// TunnelInfo describes an SSH tunnel status.
type TunnelInfo struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	RemoteHost string `json:"remote_host"`
	Uptime     string `json:"uptime,omitempty"`
	Reconnects int    `json:"reconnects"`
}

// CacheStatsInfo describes the response cache statistics.
type CacheStatsInfo struct {
	Entries int     `json:"entries"`
	Size    string  `json:"size"`
	HitRate float64 `json:"hit_rate"`
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
	if err := a.callAgentTool("agent_context_recall_enhanced", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// KnowledgeResult is the response from cross-agent enhanced recall.
type KnowledgeResult struct {
	OK          bool             `json:"ok"`
	Entries     []KnowledgeEntry `json:"entries"`
	Count       int              `json:"count"`
	TotalTokens int              `json:"total_tokens"`
	TokenBudget int              `json:"token_budget"`
}

// KnowledgeEntry represents a context entry with source attribution.
type KnowledgeEntry struct {
	ID         string         `json:"id"`
	AgentID    string         `json:"agent_id"`
	SessionID  string         `json:"session_id"`
	Namespace  string         `json:"namespace,omitempty"`
	EntryType  string         `json:"entry_type"`
	Title      string         `json:"title"`
	Content    string         `json:"content"`
	FilePath   string         `json:"file_path,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Timestamp  string         `json:"timestamp"`
	TokenCount int            `json:"token_count"`
	Metadata   map[string]any `json:"metadata,omitempty"`
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

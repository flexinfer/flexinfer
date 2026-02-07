package bridge

import (
	"encoding/json"
	"fmt"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// AgentBridge wraps agent-context tool calls, routing them through the daemon's
// tools/call endpoint. Each method calls the appropriate agent_context__* tool
// and unmarshals the result into a clean Go struct.
type AgentBridge struct {
	client *DaemonClient
}

// NewAgentBridge creates an AgentBridge backed by the given DaemonClient.
func NewAgentBridge(client *DaemonClient) *AgentBridge {
	return &AgentBridge{client: client}
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
	ID             string         `json:"workflow_id"`
	Name           string         `json:"name,omitempty"`
	Status         string         `json:"status"`
	CurrentStep    string         `json:"current_step"`
	Progress       float64        `json:"progress,omitempty"`
	CompletedSteps int            `json:"completed_steps,omitempty"`
	TotalSteps     int            `json:"total_steps,omitempty"`
	Steps          []WorkflowStep `json:"steps,omitempty"`
	CreatedAt      string         `json:"created_at"`
	StartedAt      string         `json:"started_at,omitempty"`
	CompletedAt    string         `json:"completed_at,omitempty"`
	Error          string         `json:"error,omitempty"`
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
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	EntityType string         `json:"entity_type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// ContextEntryInfo describes a context stream entry.
type ContextEntryInfo struct {
	Score float64      `json:"score"`
	Entry ContextEntry `json:"entry"`
}

// ContextEntry is the inner entry within a context search result.
type ContextEntry struct {
	ID        string `json:"id"`
	EntryType string `json:"entry_type"`
	AgentID   string `json:"agent_id"`
	Namespace string `json:"namespace"`
	Title     string `json:"title"`
	Content   string `json:"content,omitempty"`
	Timestamp string `json:"timestamp"`
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
}

// callAgentTool invokes an agent_context tool and unmarshals the response
// into the provided target. It unwraps the MCP CallToolResult envelope and
// supports both JSON and TOON (Token-Optimized Object Notation) text payloads.
func (a *AgentBridge) callAgentTool(toolName string, args map[string]any, target any) error {
	raw, err := a.client.CallTool("agent_context__"+toolName, args)
	if err != nil {
		return fmt.Errorf("agent tool %s: %w", toolName, err)
	}
	if target == nil {
		return nil
	}

	// Try to unwrap MCP content envelope first.
	var envelope mcpCallToolResult
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Content) > 0 {
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
	}

	// Fallback: try direct unmarshal (in case the daemon returns unwrapped JSON).
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("unmarshal %s result: %w", toolName, err)
	}
	return nil
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
	args := map[string]any{"id": id}
	var result WorkflowDetail
	if err := a.callAgentTool("agent_workflow_status", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
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
	args := map[string]any{"id": id}
	return a.callAgentTool("agent_memory_promote", args, nil)
}

// MemoryDemote demotes a memory item to a lower tier.
func (a *AgentBridge) MemoryDemote(id string) error {
	args := map[string]any{"id": id}
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
		args["query"] = query
	}
	if entityType != "" {
		args["entity_type"] = entityType
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
	WorktreeID    string   `json:"worktree_id"`
	LastHeartbeat string   `json:"last_heartbeat"`
	RegisteredAt  string   `json:"registered_at"`
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

// ContextStream returns context entries since a given time, up to limit.
func (a *AgentBridge) ContextStream(since time.Time, limit int) ([]ContextEntryInfo, error) {
	args := map[string]any{}
	if !since.IsZero() {
		args["query"] = fmt.Sprintf("since:%s", since.Format(time.RFC3339))
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

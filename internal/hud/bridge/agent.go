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
	args := map[string]any{
		"title":    p.Title,
		"priority": p.Priority,
	}
	if p.SessionID != "" {
		args["session_id"] = p.SessionID
	}
	if len(p.Tags) > 0 {
		args["tags"] = p.Tags
	}
	if p.Context != "" {
		args["context"] = p.Context
	}
	if p.FilePath != "" {
		args["file_path"] = p.FilePath
	}
	if p.LineNumber > 0 {
		args["line_number"] = p.LineNumber
	}
	if len(p.BlockedBy) > 0 {
		args["blocked_by"] = p.BlockedBy
	}
	return a.callAgentTool("agent_task_add", args, nil)
}

// UpdateTaskParams holds all fields for task updates.
type UpdateTaskParams struct {
	ID         string
	Status     string
	Priority   string
	Resolution string // For completed tasks
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

// MemoryAdd adds a new memory item.
func (a *AgentBridge) MemoryAdd(title, content, tier, importance, category string) error {
	args := map[string]any{
		"title":   title,
		"content": content,
	}
	if tier != "" {
		args["tier"] = tier
	}
	if importance != "" {
		args["importance"] = importance
	}
	if category != "" {
		args["category"] = category
	}
	return a.callAgentTool("agent_memory_add", args, nil)
}

// MemoryDelete deletes a memory item by ID.
func (a *AgentBridge) MemoryDelete(id string) error {
	args := map[string]any{"id": id}
	return a.callAgentTool("agent_memory_delete", args, nil)
}

// EntityAdd creates a new entity in the knowledge graph.
func (a *AgentBridge) EntityAdd(name, entityType, namespace string, props map[string]any) error {
	args := map[string]any{
		"name":        name,
		"entity_type": entityType,
	}
	if namespace != "" {
		args["namespace"] = namespace
	}
	if len(props) > 0 {
		args["properties"] = props
	}
	return a.callAgentTool("agent_entity_add", args, nil)
}

// EntityDelete deletes an entity by ID.
func (a *AgentBridge) EntityDelete(id string) error {
	args := map[string]any{"id": id}
	return a.callAgentTool("agent_entity_delete", args, nil)
}

// EntityDetail describes a single entity with its relations.
type EntityDetail struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	EntityType        string         `json:"entity_type"`
	Namespace         string         `json:"namespace,omitempty"`
	Properties        map[string]any `json:"properties,omitempty"`
	InboundRelations  []RelationInfo `json:"inbound_relations,omitempty"`
	OutboundRelations []RelationInfo `json:"outbound_relations,omitempty"`
}

// RelationInfo describes a relation in the knowledge graph.
type RelationInfo struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceName   string `json:"source_name,omitempty"`
	Target       string `json:"target"`
	TargetName   string `json:"target_name,omitempty"`
	RelationType string `json:"relation_type"`
}

// EntityGet retrieves a single entity with its relations.
func (a *AgentBridge) EntityGet(id string) (*EntityDetail, error) {
	args := map[string]any{"id": id}
	var result EntityDetail
	if err := a.callAgentTool("agent_entity_get", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RelationAdd creates a relation between two entities.
func (a *AgentBridge) RelationAdd(sourceID, targetID, relationType string) error {
	args := map[string]any{
		"source_id":     sourceID,
		"target_id":     targetID,
		"relation_type": relationType,
	}
	return a.callAgentTool("agent_relation_add", args, nil)
}

// RelationDelete deletes a relation by ID.
func (a *AgentBridge) RelationDelete(id string) error {
	args := map[string]any{"id": id}
	return a.callAgentTool("agent_relation_delete", args, nil)
}

// GraphFindPath finds the shortest path between two entities.
func (a *AgentBridge) GraphFindPath(fromID, toID string, maxDepth int) ([]EntityInfo, error) {
	args := map[string]any{
		"from_id": fromID,
		"to_id":   toID,
	}
	if maxDepth > 0 {
		args["max_depth"] = maxDepth
	}
	var result struct {
		Path []EntityInfo `json:"path"`
	}
	if err := a.callAgentTool("agent_graph_find_path", args, &result); err != nil {
		return nil, err
	}
	return result.Path, nil
}

// SessionEntries returns context entries for a specific session.
func (a *AgentBridge) SessionEntries(sessionID string, limit int) ([]ContextEntryInfo, error) {
	args := map[string]any{
		"session_id": sessionID,
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
func (a *AgentBridge) StartSession(p SessionStartParams) (*SessionStartResult, error) {
	// Check for existing active session in the same namespace.
	if existing, err := a.GetActiveSession(p.AgentID); err == nil && existing != nil {
		if existing.Namespace == p.Namespace && existing.Status == "active" {
			return &SessionStartResult{
				SessionID:      existing.ID,
				AlreadyExisted: true,
			}, nil
		}
	}

	// Start a new session.
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
	if err := a.callAgentTool("agent_session_start", args, &sessionResult); err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	// Register presence.
	presenceArgs := map[string]any{
		"agent_id": p.AgentID,
		"status":   "active",
	}
	if p.AgentType != "" {
		presenceArgs["agent_type"] = p.AgentType
	}
	_ = a.callAgentTool("agent_presence_register", presenceArgs, nil)

	result := &SessionStartResult{
		SessionID: sessionResult.SessionID,
	}

	// Optional: recall context.
	if p.AutoRecall {
		recallArgs := map[string]any{
			"query":        p.Description,
			"token_budget": 4000,
		}
		if p.Namespace != "" {
			recallArgs["file_context"] = p.Namespace
		}
		var recallResult struct {
			Summary string `json:"summary"`
		}
		if err := a.callAgentTool("agent_context_recall_enhanced", recallArgs, &recallResult); err == nil {
			result.RecalledContext = recallResult.Summary
		}
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
func (a *AgentBridge) EndSession(p SessionEndParams) error {
	sessionID := p.SessionID
	if sessionID == "" && p.AgentID != "" {
		if active, err := a.GetActiveSession(p.AgentID); err == nil && active != nil {
			sessionID = active.ID
		}
	}
	if sessionID == "" {
		return fmt.Errorf("no active session found for agent %q", p.AgentID)
	}

	args := map[string]any{
		"session_id": sessionID,
	}
	if p.Summarize {
		args["summarize"] = true
	}
	if err := a.callAgentTool("agent_session_end", args, nil); err != nil {
		return fmt.Errorf("end session: %w", err)
	}

	// Deregister presence (best-effort).
	if p.AgentID != "" {
		_ = a.callAgentTool("agent_presence_deregister", map[string]any{
			"agent_id": p.AgentID,
		}, nil)
	}

	return nil
}

// PresenceHeartbeat updates the heartbeat timestamp for an agent.
func (a *AgentBridge) PresenceHeartbeat(agentID, status string) error {
	args := map[string]any{
		"agent_id": agentID,
	}
	if status != "" {
		args["status"] = status
	}
	return a.callAgentTool("agent_presence_heartbeat", args, nil)
}

// GetActiveSession finds the currently active session for an agent.
// Returns nil if no active session exists.
func (a *AgentBridge) GetActiveSession(agentID string) (*SessionInfo, error) {
	sessions, err := a.Sessions()
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].AgentID == agentID && sessions[i].Status == "active" {
			return &sessions[i], nil
		}
	}
	return nil, nil
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

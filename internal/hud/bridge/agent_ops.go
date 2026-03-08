package bridge

import (
	"strings"
)

// --- Workflow methods ---

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

// --- Memory methods ---

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

// --- Reasoning chain methods ---

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

func (a *AgentBridge) handoffInbox(agentID string, includeViewed bool) ([]HandoffInfo, error) {
	args := map[string]any{
		"agent_id": agentID,
	}
	if includeViewed {
		args["include_viewed"] = true
	}

	var result struct {
		Handoffs []handoffInboxEntry `json:"handoffs"`
	}
	if err := a.callAgentTool("agent_handoff_inbox", args, &result); err != nil {
		return nil, err
	}

	out := make([]HandoffInfo, 0, len(result.Handoffs))
	for _, h := range result.Handoffs {
		out = append(out, HandoffInfo{
			ID:        h.HandoffID,
			FromAgent: h.SourceAgent,
			ToAgent:   agentID,
			Status:    h.Status,
			Summary:   h.Summary,
			Context:   h.Instructions,
			CreatedAt: h.CreatedAt,
		})
	}
	return out, nil
}

// HandoffList returns pending/viewed handoffs across active/offline agents
// by querying each agent's inbox via agent_handoff_inbox.
func (a *AgentBridge) HandoffList() ([]HandoffInfo, error) {
	agents, err := a.PresenceList(true)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	combined := make([]HandoffInfo, 0)
	var inboxErr error

	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			continue
		}
		handoffs, err := a.handoffInbox(agentID, true)
		if err != nil {
			if isUnknownToolErr(err, "agent_handoff_inbox") {
				return nil, nil // tool unavailable, return empty
			}
			if inboxErr == nil {
				inboxErr = err
			}
			continue
		}
		for _, h := range handoffs {
			if strings.TrimSpace(h.ID) == "" {
				continue
			}
			if _, ok := seen[h.ID]; ok {
				continue
			}
			seen[h.ID] = struct{}{}
			combined = append(combined, h)
		}
	}

	if inboxErr != nil && len(combined) == 0 {
		return nil, inboxErr
	}
	return combined, nil
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

// HandoffListForAgent returns pending handoffs targeted at a specific agent.
func (a *AgentBridge) HandoffListForAgent(agentID string) ([]HandoffInfo, error) {
	if strings.TrimSpace(agentID) == "" {
		return []HandoffInfo{}, nil
	}

	handoffs, err := a.handoffInbox(agentID, false)
	if err != nil {
		if isUnknownToolErr(err, "agent_handoff_inbox") {
			return nil, nil // tool unavailable, return empty
		}
		return nil, err
	}
	return handoffs, nil
}

// --- Template methods ---

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

// --- Coordination methods ---

// CompactionStatus returns the compaction scheduler status.
func (a *AgentBridge) CompactionStatus() (*CompactionInfo, error) {
	var result CompactionInfo
	if err := a.callAgentTool("agent_compaction_status", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
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

// ReleaseFileClaim releases a specific file claim for an agent.
func (a *AgentBridge) ReleaseFileClaim(agentID, filePath string) error {
	args := map[string]any{
		"agent_id":  agentID,
		"file_path": filePath,
	}
	return a.callAgentTool("agent_file_claim_release", args, nil)
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

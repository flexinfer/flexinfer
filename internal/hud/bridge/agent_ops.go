package bridge

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
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
// Uses the unified agent_recall tool with scope="memory".
func (a *AgentBridge) MemoryRecall(tier, query string, limit int) ([]MemoryItem, error) {
	args := map[string]any{
		"scope": "memory",
	}
	if tier != "" {
		args["memory_tiers"] = []string{tier}
	}
	if query != "" {
		args["query"] = query
	}
	if limit > 0 {
		args["token_budget"] = limit * 200 // approximate tokens per item
	}
	var result struct {
		MemoryItems []MemoryItem `json:"memory_items"`
	}
	if err := a.callAgentTool("agent_recall", args, &result); err != nil {
		return nil, err
	}
	return result.MemoryItems, nil
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
		summary := strings.TrimSpace(h.Summary)
		instructions := strings.TrimSpace(h.Instructions)
		// Keep dispatched-task routing deterministic even when the server
		// summary payload is empty by elevating the first instruction line.
		if strings.HasPrefix(instructions, "[Dispatched] ") {
			firstLine := instructions
			if idx := strings.Index(firstLine, "\n"); idx >= 0 {
				firstLine = firstLine[:idx]
			}
			if strings.TrimSpace(firstLine) != "" {
				summary = strings.TrimSpace(firstLine)
			}
		}
		out = append(out, HandoffInfo{
			ID:            h.HandoffID,
			FromAgent:     h.SourceAgent,
			ToAgent:       agentID,
			TargetAgentID: agentID,
			Status:        h.Status,
			Summary:       summary,
			Context:       instructions,
			CreatedAt:     h.CreatedAt,
		})
	}
	return out, nil
}

// HandoffList returns pending/viewed handoffs across active/offline agents
// by querying each agent's inbox via agent_handoff_inbox concurrently.
func (a *AgentBridge) HandoffList() ([]HandoffInfo, error) {
	agents, err := a.PresenceList(true)
	if err != nil {
		return nil, err
	}

	g, _ := errgroup.WithContext(context.Background())
	var mu sync.Mutex
	seen := make(map[string]struct{})
	combined := make([]HandoffInfo, 0)
	var inboxErr error

	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.AgentID)
		if agentID == "" {
			continue
		}
		g.Go(func() error {
			handoffs, err := a.handoffInbox(agentID, true)
			if err != nil {
				if isUnknownToolErr(err, "agent_handoff_inbox") {
					return err // signal tool unavailable
				}
				mu.Lock()
				if inboxErr == nil {
					inboxErr = err
				}
				mu.Unlock()
				return nil // partial failure OK
			}
			mu.Lock()
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
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		if isUnknownToolErr(err, "agent_handoff_inbox") {
			return nil, nil
		}
	}

	if inboxErr != nil && len(combined) == 0 {
		return nil, inboxErr
	}
	return combined, nil
}

// HandoffCreate creates a new handoff package using the current tool schema.
func (a *AgentBridge) HandoffCreate(p HandoffCreateParams) (*HandoffCreateResult, error) {
	args := map[string]any{
		"session_id":      strings.TrimSpace(p.SessionID),
		"target_agent_id": strings.TrimSpace(p.TargetAgentID),
	}
	if strings.TrimSpace(p.Instructions) != "" {
		args["instructions"] = strings.TrimSpace(p.Instructions)
	}
	if strings.TrimSpace(p.HandoffType) != "" {
		args["handoff_type"] = strings.TrimSpace(p.HandoffType)
	}
	if len(p.EntryIDs) > 0 {
		args["entry_ids"] = p.EntryIDs
	}
	if p.TokenBudget > 0 {
		args["token_budget"] = p.TokenBudget
	}

	var result HandoffCreateResult
	if err := a.callAgentTool("agent_handoff_create", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// HandoffAccept accepts a handoff.
func (a *AgentBridge) HandoffAccept(p HandoffAcceptParams) (*HandoffAcceptResult, error) {
	args := map[string]any{
		"handoff_id": strings.TrimSpace(p.HandoffID),
		"session_id": strings.TrimSpace(p.SessionID),
	}
	if p.ImportEntries {
		args["import_entries"] = true
	}

	var result HandoffAcceptResult
	if err := a.callAgentTool("agent_handoff_accept", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
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

// WorktreeAllocate allocates a managed git worktree for an agent/session.
func (a *AgentBridge) WorktreeAllocate(p WorktreeAllocateParams) (*WorktreeAllocateResult, error) {
	args := map[string]any{
		"agent_id":    strings.TrimSpace(p.AgentID),
		"session_id":  strings.TrimSpace(p.SessionID),
		"branch_name": strings.TrimSpace(p.BranchName),
	}
	if strings.TrimSpace(p.BaseBranch) != "" {
		args["base_branch"] = strings.TrimSpace(p.BaseBranch)
	}
	if strings.TrimSpace(p.Purpose) != "" {
		args["purpose"] = strings.TrimSpace(p.Purpose)
	}
	if p.TTLHours > 0 {
		args["ttl_hours"] = p.TTLHours
	}

	var result WorktreeAllocateResult
	if err := a.callAgentTool("agent_worktree_allocate", args, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Engram methods ---

// EngramSummary returns aggregate counts of engrams by proof_status and tier.
// Powers the HUD catalog summary line.
func (a *AgentBridge) EngramSummary() (*EngramSummaryResult, error) {
	var result struct {
		Items []map[string]any `json:"items"`
	}
	if err := a.callAgentTool("agent_engram_list", map[string]any{"limit": 1000}, &result); err != nil {
		return nil, err
	}
	summary := &EngramSummaryResult{
		ByStatus: map[string]int{
			"unverified": 0,
			"verified":   0,
			"stale":      0,
			"failing":    0,
		},
		ByTier: map[string]int{},
	}
	for _, item := range result.Items {
		summary.Total++

		status, _ := item["proof_status"].(string)
		if status == "" {
			status = "unverified"
		}
		summary.ByStatus[status]++

		tierKey := "tier:1"
		switch t := item["tier"].(type) {
		case float64:
			tierKey = fmt.Sprintf("tier:%d", int(t))
		case int:
			tierKey = fmt.Sprintf("tier:%d", t)
		}
		summary.ByTier[tierKey]++
	}
	return summary, nil
}

// EngramSummaryResult is the shape returned by EngramSummary.
type EngramSummaryResult struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"by_status"`
	ByTier   map[string]int `json:"by_tier"`
}

package coordination

import (
	"fmt"
	"sort"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Snapshot is the shared coordination view consumed by HUD web/mobile/TUI.
type Snapshot struct {
	Summary    Summary            `json:"summary"`
	Namespaces []NamespaceSummary `json:"namespaces"`
	Agents     []AgentSummary     `json:"agents"`
	Blockers   []BlockerRelation  `json:"blockers"`
	Relations  []RelationEdge     `json:"relations"`
}

// Summary highlights system-wide coordination pressure points.
type Summary struct {
	ActiveNamespaces       int `json:"active_namespaces"`
	NamespacesAtRisk       int `json:"namespaces_at_risk"`
	AgentsNeedingAttention int `json:"agents_needing_attention"`
	SharedBranches         int `json:"shared_branches"`
	ConflictFiles          int `json:"conflict_files"`
	CrossAgentBlockers     int `json:"cross_agent_blockers"`
	OrphanTasks            int `json:"orphan_tasks"`
	IdleClaimHolders       int `json:"idle_claim_holders"`
	MergeReadyBranches     int `json:"merge_ready_branches"`
}

// NamespaceSummary tracks coordination state for a single namespace.
type NamespaceSummary struct {
	Namespace          string   `json:"namespace"`
	SessionCount       int      `json:"session_count"`
	AgentCount         int      `json:"agent_count"`
	TaskCount          int      `json:"task_count"`
	BlockedTasks       int      `json:"blocked_tasks"`
	OrphanTasks        int      `json:"orphan_tasks"`
	ConflictFiles      int      `json:"conflict_files"`
	SharedBranches     int      `json:"shared_branches"`
	CrossAgentBlockers int      `json:"cross_agent_blockers"`
	NeedsAttention     bool     `json:"needs_attention"`
	AttentionScore     int      `json:"attention_score"`
	AttentionReasons   []string `json:"attention_reasons,omitempty"`
	Agents             []string `json:"agents,omitempty"`
	Branches           []string `json:"branches,omitempty"`
}

// AgentSummary tracks coordination signals for a single agent.
type AgentSummary struct {
	AgentID           string   `json:"agent_id"`
	SessionID         string   `json:"session_id,omitempty"`
	Namespace         string   `json:"namespace,omitempty"`
	Status            string   `json:"status"`
	Branch            string   `json:"branch,omitempty"`
	WorktreeStatus    string   `json:"worktree_status,omitempty"`
	TaskCount         int      `json:"task_count"`
	BlockedTasks      int      `json:"blocked_tasks"`
	ClaimCount        int      `json:"claim_count"`
	ConflictFiles     int      `json:"conflict_files"`
	BlockingOthers    int      `json:"blocking_others"`
	BlockedByOthers   int      `json:"blocked_by_others"`
	IdleHoldingClaims bool     `json:"idle_holding_claims"`
	MergeReady        bool     `json:"merge_ready"`
	MergeBlockers     []string `json:"merge_blockers,omitempty"`
	NeedsAttention    bool     `json:"needs_attention"`
	AttentionReasons  []string `json:"attention_reasons,omitempty"`
}

// BlockerRelation resolves a task dependency into coordination metadata.
type BlockerRelation struct {
	TaskID             string `json:"task_id"`
	TaskTitle          string `json:"task_title"`
	TaskStatus         string `json:"task_status"`
	TaskAgentID        string `json:"task_agent_id,omitempty"`
	TaskNamespace      string `json:"task_namespace,omitempty"`
	BlockedByTaskID    string `json:"blocked_by_task_id"`
	BlockedByTaskTitle string `json:"blocked_by_task_title,omitempty"`
	BlockedByStatus    string `json:"blocked_by_status,omitempty"`
	BlockedByAgentID   string `json:"blocked_by_agent_id,omitempty"`
	BlockedByNamespace string `json:"blocked_by_namespace,omitempty"`
	CrossAgent         bool   `json:"cross_agent"`
	Resolved           bool   `json:"resolved"`
}

// RelationEdge models a notable relation between coordination entities.
type RelationEdge struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	SourceLabel string `json:"source_label"`
	Target      string `json:"target"`
	TargetLabel string `json:"target_label"`
	Namespace   string `json:"namespace,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Severity    string `json:"severity,omitempty"`
	CrossAgent  bool   `json:"cross_agent"`
}

type namespaceState struct {
	Namespace          string
	SessionCount       int
	AgentIDs           map[string]struct{}
	TaskCount          int
	BlockedTasks       int
	OrphanTasks        int
	ConflictFiles      map[string]struct{}
	SharedBranches     map[string]struct{}
	CrossAgentBlockers int
	AttentionReasons   map[string]struct{}
	Branches           map[string]struct{}
}

type agentState struct {
	AgentID          string
	SessionID        string
	Namespace        string
	Status           string
	Branch           string
	WorktreeStatus   string
	TaskCount        int
	BlockedTasks     int
	ClaimCount       int
	ConflictFiles    map[string]struct{}
	BlockingOthers   int
	BlockedByOthers  int
	AttentionReasons map[string]struct{}
	MergeBlockers    []string
}

// Build derives shared coordination state from sessions, tasks, presence, claims, and worktrees.
func Build(
	sessions []bridge.SessionInfo,
	tasks []bridge.TaskInfo,
	agents []bridge.PresenceInfo,
	claims []bridge.FileClaimInfo,
	worktrees []bridge.WorktreeInfo,
) Snapshot {
	result := Snapshot{
		Namespaces: []NamespaceSummary{},
		Agents:     []AgentSummary{},
		Blockers:   []BlockerRelation{},
		Relations:  []RelationEdge{},
	}

	sessionByID := make(map[string]bridge.SessionInfo, len(sessions))
	sessionByAgent := make(map[string]bridge.SessionInfo, len(sessions))
	namespaceStates := map[string]*namespaceState{}
	agentStates := map[string]*agentState{}
	taskByID := make(map[string]bridge.TaskInfo, len(tasks))

	for _, session := range sessions {
		sessionByID[session.ID] = session
		if session.AgentID != "" {
			sessionByAgent[session.AgentID] = session
		}
		ns := ensureNamespace(namespaceStates, session.Namespace)
		ns.SessionCount++
		if session.AgentID != "" {
			ns.AgentIDs[session.AgentID] = struct{}{}
			ensureAgent(agentStates, session.AgentID).SessionID = session.ID
			ensureAgent(agentStates, session.AgentID).Namespace = session.Namespace
		}
	}

	for _, agent := range agents {
		state := ensureAgent(agentStates, agent.AgentID)
		state.Status = normalizeStatus(agent.Status, "offline")
		if state.SessionID == "" {
			state.SessionID = agent.SessionID
		}
		if state.Namespace == "" {
			if session, ok := sessionByID[agent.SessionID]; ok {
				state.Namespace = session.Namespace
			}
		}
		if branch := strings.TrimSpace(agent.Branch); branch != "" {
			state.Branch = branch
		}
		if state.Namespace != "" {
			ns := ensureNamespace(namespaceStates, state.Namespace)
			ns.AgentIDs[agent.AgentID] = struct{}{}
			if state.Branch != "" {
				ns.Branches[state.Branch] = struct{}{}
			}
		}
	}

	for _, worktree := range worktrees {
		if worktree.AgentID == "" {
			continue
		}
		state := ensureAgent(agentStates, worktree.AgentID)
		if state.SessionID == "" {
			state.SessionID = worktree.SessionID
		}
		if state.Namespace == "" {
			if session, ok := sessionByID[worktree.SessionID]; ok {
				state.Namespace = session.Namespace
			}
		}
		if state.Branch == "" && strings.TrimSpace(worktree.Branch) != "" {
			state.Branch = strings.TrimSpace(worktree.Branch)
		}
		if state.WorktreeStatus == "" {
			state.WorktreeStatus = strings.TrimSpace(worktree.Status)
		}
		if state.Namespace != "" && state.Branch != "" {
			ns := ensureNamespace(namespaceStates, state.Namespace)
			ns.Branches[state.Branch] = struct{}{}
		}
	}

	for _, task := range tasks {
		taskByID[task.ID] = task
	}

	for _, task := range tasks {
		agentID := taskOwner(task, sessionByID)
		namespace := taskNamespace(task, sessionByID)
		ns := ensureNamespace(namespaceStates, namespace)
		ns.TaskCount++
		if taskIsOrphan(task, sessionByID) {
			ns.OrphanTasks++
			ns.AttentionReasons["orphan tasks"] = struct{}{}
			result.Summary.OrphanTasks++
		}
		if agentID != "" {
			ns.AgentIDs[agentID] = struct{}{}
			agentState := ensureAgent(agentStates, agentID)
			agentState.TaskCount++
			if agentState.Namespace == "" {
				agentState.Namespace = namespace
			}
		}
		if strings.EqualFold(task.Status, "blocked") {
			ns.BlockedTasks++
			ns.AttentionReasons["blocked tasks"] = struct{}{}
			if agentID != "" {
				agentState := ensureAgent(agentStates, agentID)
				agentState.BlockedTasks++
			}
		}
	}

	branchAgents := make(map[string]map[string]struct{})
	for _, agent := range agentStates {
		if agent.AgentID == "" || strings.TrimSpace(agent.Branch) == "" {
			continue
		}
		owners := branchAgents[agent.Branch]
		if owners == nil {
			owners = map[string]struct{}{}
			branchAgents[agent.Branch] = owners
		}
		owners[agent.AgentID] = struct{}{}
	}

	fileAgents := make(map[string]map[string]struct{})
	for _, claim := range claims {
		if claim.AgentID == "" || strings.TrimSpace(claim.FilePath) == "" {
			continue
		}
		state := ensureAgent(agentStates, claim.AgentID)
		state.ClaimCount++
		if state.Namespace == "" {
			if session, ok := sessionByID[claim.SessionID]; ok {
				state.Namespace = session.Namespace
			}
		}
		owners := fileAgents[claim.FilePath]
		if owners == nil {
			owners = map[string]struct{}{}
			fileAgents[claim.FilePath] = owners
		}
		owners[claim.AgentID] = struct{}{}
	}

	for branch, owners := range branchAgents {
		agentsOnBranch := sortedKeys(owners)
		if len(agentsOnBranch) < 2 {
			continue
		}
		result.Summary.SharedBranches++
		for _, agentID := range agentsOnBranch {
			state := ensureAgent(agentStates, agentID)
			state.AttentionReasons["shared branch"] = struct{}{}
			if state.Namespace != "" {
				ns := ensureNamespace(namespaceStates, state.Namespace)
				ns.SharedBranches[branch] = struct{}{}
				ns.AttentionReasons["shared branches"] = struct{}{}
			}
		}
		for i := 0; i < len(agentsOnBranch); i++ {
			for j := i + 1; j < len(agentsOnBranch); j++ {
				left := ensureAgent(agentStates, agentsOnBranch[i])
				right := ensureAgent(agentStates, agentsOnBranch[j])
				result.Relations = append(result.Relations, RelationEdge{
					Kind:        "shared_branch",
					Source:      left.AgentID,
					SourceLabel: left.AgentID,
					Target:      right.AgentID,
					TargetLabel: right.AgentID,
					Namespace:   preferValue(left.Namespace, right.Namespace),
					Detail:      branch,
					Severity:    "warn",
					CrossAgent:  left.AgentID != right.AgentID,
				})
			}
		}
	}

	for filePath, owners := range fileAgents {
		claimants := sortedKeys(owners)
		if len(claimants) < 2 {
			continue
		}
		result.Summary.ConflictFiles++
		for _, agentID := range claimants {
			state := ensureAgent(agentStates, agentID)
			state.ConflictFiles[filePath] = struct{}{}
			state.AttentionReasons["file conflicts"] = struct{}{}
			if state.Namespace != "" {
				ns := ensureNamespace(namespaceStates, state.Namespace)
				ns.ConflictFiles[filePath] = struct{}{}
				ns.AttentionReasons["file conflicts"] = struct{}{}
			}
		}
		for i := 0; i < len(claimants); i++ {
			for j := i + 1; j < len(claimants); j++ {
				left := ensureAgent(agentStates, claimants[i])
				right := ensureAgent(agentStates, claimants[j])
				result.Relations = append(result.Relations, RelationEdge{
					Kind:        "file_conflict",
					Source:      left.AgentID,
					SourceLabel: left.AgentID,
					Target:      right.AgentID,
					TargetLabel: right.AgentID,
					Namespace:   preferValue(left.Namespace, right.Namespace),
					Detail:      filePath,
					Severity:    "critical",
					CrossAgent:  left.AgentID != right.AgentID,
				})
			}
		}
	}

	for _, task := range tasks {
		taskAgentID := taskOwner(task, sessionByID)
		taskNS := taskNamespace(task, sessionByID)
		for _, blockerID := range task.BlockedBy {
			relation := BlockerRelation{
				TaskID:          task.ID,
				TaskTitle:       task.Title,
				TaskStatus:      normalizeStatus(task.Status, "pending"),
				TaskAgentID:     taskAgentID,
				TaskNamespace:   taskNS,
				BlockedByTaskID: blockerID,
			}

			if blocker, ok := taskByID[blockerID]; ok {
				relation.BlockedByTaskTitle = blocker.Title
				relation.BlockedByStatus = normalizeStatus(blocker.Status, "pending")
				relation.BlockedByAgentID = taskOwner(blocker, sessionByID)
				relation.BlockedByNamespace = taskNamespace(blocker, sessionByID)
				relation.CrossAgent = relation.TaskAgentID != "" && relation.BlockedByAgentID != "" && relation.TaskAgentID != relation.BlockedByAgentID
				relation.Resolved = blockerResolved(blocker.Status)
				if relation.CrossAgent {
					result.Summary.CrossAgentBlockers++
					if relation.TaskAgentID != "" {
						ensureAgent(agentStates, relation.TaskAgentID).BlockedByOthers++
					}
					if relation.BlockedByAgentID != "" {
						ensureAgent(agentStates, relation.BlockedByAgentID).BlockingOthers++
					}
					if relation.TaskNamespace != "" {
						ns := ensureNamespace(namespaceStates, relation.TaskNamespace)
						ns.CrossAgentBlockers++
						ns.AttentionReasons["cross-agent blockers"] = struct{}{}
					}
					if relation.BlockedByNamespace != "" && relation.BlockedByNamespace != relation.TaskNamespace {
						ns := ensureNamespace(namespaceStates, relation.BlockedByNamespace)
						ns.CrossAgentBlockers++
						ns.AttentionReasons["cross-agent blockers"] = struct{}{}
					}
				}
				result.Relations = append(result.Relations, RelationEdge{
					Kind:        "task_blocker",
					Source:      relation.TaskID,
					SourceLabel: relation.TaskTitle,
					Target:      relation.BlockedByTaskID,
					TargetLabel: preferValue(relation.BlockedByTaskTitle, relation.BlockedByTaskID),
					Namespace:   relation.TaskNamespace,
					Detail:      blockerDetail(relation),
					Severity:    blockerSeverity(relation),
					CrossAgent:  relation.CrossAgent,
				})
			}

			result.Blockers = append(result.Blockers, relation)
		}
	}

	for _, state := range agentStates {
		if state.Status == "idle" && state.ClaimCount > 0 {
			state.AttentionReasons["idle agent holding claims"] = struct{}{}
			result.Summary.IdleClaimHolders++
		}
		if state.BlockedTasks > 0 {
			state.AttentionReasons["blocked tasks"] = struct{}{}
		}
		if len(state.ConflictFiles) > 0 {
			state.AttentionReasons["file conflicts"] = struct{}{}
		}
		if state.BlockingOthers > 0 {
			state.AttentionReasons["blocking other agents"] = struct{}{}
		}
		if state.BlockedByOthers > 0 {
			state.AttentionReasons["waiting on other agents"] = struct{}{}
		}
		if strings.EqualFold(state.Status, "offline") && state.SessionID != "" {
			state.AttentionReasons["offline with active session"] = struct{}{}
		}
	}

	for _, ns := range namespaceStates {
		summary := NamespaceSummary{
			Namespace:          ns.Namespace,
			SessionCount:       ns.SessionCount,
			AgentCount:         len(ns.AgentIDs),
			TaskCount:          ns.TaskCount,
			BlockedTasks:       ns.BlockedTasks,
			OrphanTasks:        ns.OrphanTasks,
			ConflictFiles:      len(ns.ConflictFiles),
			SharedBranches:     len(ns.SharedBranches),
			CrossAgentBlockers: ns.CrossAgentBlockers,
			AttentionReasons:   sortedReasonKeys(ns.AttentionReasons),
			Agents:             sortedKeys(ns.AgentIDs),
			Branches:           sortedKeys(ns.Branches),
		}
		summary.AttentionScore = summary.BlockedTasks + summary.OrphanTasks + summary.ConflictFiles + summary.CrossAgentBlockers + summary.SharedBranches
		summary.NeedsAttention = summary.AttentionScore > 0
		if summary.SessionCount > 0 || summary.TaskCount > 0 || summary.AgentCount > 0 {
			result.Summary.ActiveNamespaces++
		}
		if summary.NeedsAttention {
			result.Summary.NamespacesAtRisk++
		}
		result.Namespaces = append(result.Namespaces, summary)
	}

	for _, state := range agentStates {
		state.MergeBlockers = computeMergeBlockers(state, branchAgents)
	}

	for _, state := range agentStates {
		summary := AgentSummary{
			AgentID:           state.AgentID,
			SessionID:         state.SessionID,
			Namespace:         state.Namespace,
			Status:            normalizeStatus(state.Status, "offline"),
			Branch:            state.Branch,
			WorktreeStatus:    state.WorktreeStatus,
			TaskCount:         state.TaskCount,
			BlockedTasks:      state.BlockedTasks,
			ClaimCount:        state.ClaimCount,
			ConflictFiles:     len(state.ConflictFiles),
			BlockingOthers:    state.BlockingOthers,
			BlockedByOthers:   state.BlockedByOthers,
			IdleHoldingClaims: normalizeStatus(state.Status, "offline") == "idle" && state.ClaimCount > 0,
			MergeReady:        len(state.MergeBlockers) == 0 && isMergeBranch(state.Branch),
			MergeBlockers:     state.MergeBlockers,
			AttentionReasons:  sortedReasonKeys(state.AttentionReasons),
		}
		summary.NeedsAttention = len(summary.AttentionReasons) > 0
		if summary.NeedsAttention {
			result.Summary.AgentsNeedingAttention++
		}
		if summary.MergeReady {
			result.Summary.MergeReadyBranches++
		}
		result.Agents = append(result.Agents, summary)
	}

	sort.Slice(result.Namespaces, func(i, j int) bool {
		left := result.Namespaces[i]
		right := result.Namespaces[j]
		if left.NeedsAttention != right.NeedsAttention {
			return left.NeedsAttention
		}
		if left.AttentionScore != right.AttentionScore {
			return left.AttentionScore > right.AttentionScore
		}
		return left.Namespace < right.Namespace
	})
	sort.Slice(result.Agents, func(i, j int) bool {
		left := result.Agents[i]
		right := result.Agents[j]
		if left.NeedsAttention != right.NeedsAttention {
			return left.NeedsAttention
		}
		if len(left.AttentionReasons) != len(right.AttentionReasons) {
			return len(left.AttentionReasons) > len(right.AttentionReasons)
		}
		return left.AgentID < right.AgentID
	})
	sort.Slice(result.Blockers, func(i, j int) bool {
		left := result.Blockers[i]
		right := result.Blockers[j]
		if left.CrossAgent != right.CrossAgent {
			return left.CrossAgent
		}
		if left.Resolved != right.Resolved {
			return !left.Resolved
		}
		if left.TaskStatus != right.TaskStatus {
			return left.TaskStatus < right.TaskStatus
		}
		if left.TaskTitle != right.TaskTitle {
			return left.TaskTitle < right.TaskTitle
		}
		return left.BlockedByTaskID < right.BlockedByTaskID
	})
	sort.Slice(result.Relations, func(i, j int) bool {
		left := result.Relations[i]
		right := result.Relations[j]
		if severityRank(left.Severity) != severityRank(right.Severity) {
			return severityRank(left.Severity) < severityRank(right.Severity)
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.SourceLabel != right.SourceLabel {
			return left.SourceLabel < right.SourceLabel
		}
		return left.TargetLabel < right.TargetLabel
	})

	return result
}

func ensureNamespace(states map[string]*namespaceState, namespace string) *namespaceState {
	key := strings.TrimSpace(namespace)
	if key == "" {
		key = "(unscoped)"
	}
	if state, ok := states[key]; ok {
		return state
	}
	state := &namespaceState{
		Namespace:        key,
		AgentIDs:         map[string]struct{}{},
		ConflictFiles:    map[string]struct{}{},
		SharedBranches:   map[string]struct{}{},
		AttentionReasons: map[string]struct{}{},
		Branches:         map[string]struct{}{},
	}
	states[key] = state
	return state
}

func ensureAgent(states map[string]*agentState, agentID string) *agentState {
	key := strings.TrimSpace(agentID)
	if key == "" {
		key = "(unassigned)"
	}
	if state, ok := states[key]; ok {
		return state
	}
	state := &agentState{
		AgentID:          key,
		ConflictFiles:    map[string]struct{}{},
		AttentionReasons: map[string]struct{}{},
	}
	states[key] = state
	return state
}

func taskNamespace(task bridge.TaskInfo, sessionByID map[string]bridge.SessionInfo) string {
	if ns := strings.TrimSpace(task.Namespace); ns != "" {
		return ns
	}
	if session, ok := sessionByID[task.SessionID]; ok {
		return session.Namespace
	}
	return "(unscoped)"
}

func taskOwner(task bridge.TaskInfo, sessionByID map[string]bridge.SessionInfo) string {
	if agentID := strings.TrimSpace(task.AgentID); agentID != "" {
		return agentID
	}
	if session, ok := sessionByID[task.SessionID]; ok {
		return strings.TrimSpace(session.AgentID)
	}
	return ""
}

func taskIsOrphan(task bridge.TaskInfo, sessionByID map[string]bridge.SessionInfo) bool {
	return strings.TrimSpace(task.SessionID) == "" || sessionByID[task.SessionID].ID == ""
}

func blockerResolved(status string) bool {
	switch normalizeStatus(status, "") {
	case "completed", "cancelled":
		return true
	default:
		return false
	}
}

func blockerDetail(relation BlockerRelation) string {
	if relation.BlockedByTaskTitle == "" {
		return relation.BlockedByTaskID
	}
	if relation.BlockedByAgentID != "" && relation.TaskAgentID != "" && relation.BlockedByAgentID != relation.TaskAgentID {
		return fmt.Sprintf("%s via %s", relation.BlockedByTaskTitle, relation.BlockedByAgentID)
	}
	return relation.BlockedByTaskTitle
}

func blockerSeverity(relation BlockerRelation) string {
	if relation.CrossAgent && !relation.Resolved {
		return "critical"
	}
	if !relation.Resolved {
		return "warn"
	}
	return "info"
}

func normalizeStatus(value, fallback string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return fallback
	}
	return normalized
}

func preferValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func sortedReasonKeys(values map[string]struct{}) []string {
	keys := sortedKeys(values)
	if len(keys) == 0 {
		return nil
	}
	return keys
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// computeMergeBlockers returns the list of reasons an agent's branch is not
// ready to merge. An empty list means the branch is merge-ready.
func computeMergeBlockers(state *agentState, branchAgents map[string]map[string]struct{}) []string {
	if !isMergeBranch(state.Branch) {
		return nil
	}
	var blockers []string
	status := normalizeStatus(state.Status, "offline")
	if status == "offline" {
		blockers = append(blockers, "agent_offline")
	}
	if state.BlockedTasks > 0 {
		blockers = append(blockers, "blocked_tasks")
	}
	if len(state.ConflictFiles) > 0 {
		blockers = append(blockers, "file_conflicts")
	}
	if owners := branchAgents[state.Branch]; len(owners) > 1 {
		blockers = append(blockers, "shared_branch")
	}
	if state.BlockedByOthers > 0 {
		blockers = append(blockers, "blocked_by_others")
	}
	return blockers
}

// isMergeBranch returns true if the branch name indicates a feature branch
// (not main/master) that would eventually be merged.
func isMergeBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "main" || branch == "master" {
		return false
	}
	return true
}

func severityRank(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "warn":
		return 1
	case "info":
		return 2
	default:
		return 3
	}
}

package coordination

import (
	"fmt"
	"sort"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

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

func sessionIsLive(status string) bool {
	return normalizeStatus(status, "") == "active"
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

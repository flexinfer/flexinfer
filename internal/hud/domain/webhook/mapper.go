package webhook

import (
	"fmt"

	"github.com/crb2nu/loom/internal/spawn"
)

// gitLabFailureBranch returns the source branch a failed pipeline
// should be routed against. Pipelines tied to an MR follow the MR's
// source branch; otherwise the pipeline ref is used.
func gitLabFailureBranch(ev *GitLabPipelineEvent) string {
	if ev.MergeRequest != nil && ev.MergeRequest.SourceBranch != "" {
		return ev.MergeRequest.SourceBranch
	}
	return ev.ObjectAttributes.Ref
}

// gitLabFailureTask renders the human-readable task for the GitLab
// failure, used both for spawn-fallback and the active-session
// notification.
func gitLabFailureTask(ev *GitLabPipelineEvent, branch string) string {
	if ev.MergeRequest != nil {
		return fmt.Sprintf("MR !%d pipeline failed — fix CI on branch %s",
			ev.MergeRequest.IID, branch)
	}
	return fmt.Sprintf("CI pipeline %d failed on %s — diagnose and fix",
		ev.ObjectAttributes.ID, branch)
}

// mapGitLabEvent maps a GitLab pipeline event to a spawn request.
// Returns nil if the event should not trigger a spawn.
func mapGitLabEvent(ev *GitLabPipelineEvent) *spawn.Request {
	if ev.ObjectAttributes.Status != "failed" {
		return nil
	}

	branch := gitLabFailureBranch(ev)
	task := gitLabFailureTask(ev, branch)

	return &spawn.Request{
		Project:         projectFromPath(ev.Project.PathWithNamespace),
		AgentType:       "claude-code",
		TaskDescription: task,
		BaseBranch:      branch,
	}
}

// activeAgentIDs flattens an []ActiveAgent slice into a list of
// {agent_id, session_id, agent_type, status} maps suitable for an
// EventBus payload. Keeps the wire shape stable across mapper calls.
func activeAgentIDs(agents []ActiveAgent) []map[string]any {
	out := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		out = append(out, map[string]any{
			"agent_id":   a.AgentID,
			"session_id": a.SessionID,
			"agent_type": a.AgentType,
			"status":     a.Status,
		})
	}
	return out
}

// matchActiveAgentsForBranch filters a presence list to the agents
// currently working the supplied branch and not offline. Returns nil
// when no match — callers should treat that as "no active session,
// fall back to spawn-fresh".
func matchActiveAgentsForBranch(agents []ActiveAgent, branch string) []ActiveAgent {
	if branch == "" || len(agents) == 0 {
		return nil
	}
	var matched []ActiveAgent
	for _, a := range agents {
		if a.Branch != branch {
			continue
		}
		if a.Status == "offline" || a.Status == "expired" {
			continue
		}
		matched = append(matched, a)
	}
	return matched
}

// mapGitHubCheckSuiteEvent maps a GitHub check_suite event to a spawn request.
// Returns nil if the event should not trigger a spawn.
func mapGitHubCheckSuiteEvent(ev *GitHubCheckSuiteEvent) *spawn.Request {
	if ev.Action != "completed" || ev.CheckSuite.Conclusion != "failure" {
		return nil
	}

	branch := ev.CheckSuite.HeadBranch
	task := fmt.Sprintf("CI check suite %d failed on %s — diagnose and fix",
		ev.CheckSuite.ID, branch)

	return &spawn.Request{
		Project:         projectFromFullName(ev.Repository.FullName),
		AgentType:       "claude-code",
		TaskDescription: task,
		BaseBranch:      branch,
	}
}

// projectFromPath extracts the project name from a GitLab path_with_namespace.
// e.g., "homelab/services/loom-core" → "loom-core"
func projectFromPath(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// projectFromFullName extracts the project name from a GitHub full_name.
// e.g., "user/loom-core" → "loom-core"
func projectFromFullName(fullName string) string {
	return projectFromPath(fullName)
}

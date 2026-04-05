package webhook

import (
	"fmt"

	"github.com/crb2nu/loom/internal/spawn"
)

// mapGitLabEvent maps a GitLab pipeline event to a spawn request.
// Returns nil if the event should not trigger a spawn.
func mapGitLabEvent(ev *GitLabPipelineEvent) *spawn.Request {
	if ev.ObjectAttributes.Status != "failed" {
		return nil
	}

	branch := ev.ObjectAttributes.Ref
	task := fmt.Sprintf("CI pipeline %d failed on %s — diagnose and fix", ev.ObjectAttributes.ID, branch)

	// MR pipeline failed → fix on MR source branch
	if ev.MergeRequest != nil {
		branch = ev.MergeRequest.SourceBranch
		task = fmt.Sprintf("MR !%d pipeline failed — fix CI on branch %s",
			ev.MergeRequest.IID, branch)
	}

	return &spawn.Request{
		Project:         projectFromPath(ev.Project.PathWithNamespace),
		AgentType:       "claude-code",
		TaskDescription: task,
		BaseBranch:      branch,
	}
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

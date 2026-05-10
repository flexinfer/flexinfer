package clients

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// WorktreeAllocator implements pipeline.WorktreeAllocator against
// agent_worktree_allocate / agent_worktree_release on
// mcp-agent-context, called via MCPHubClient.
//
// Just like HandoffClient it needs an operator session id — the
// allocate tool ties each worktree to a session so the auto-orphan TTL
// reaper can clean up after a crashed operator. The operator pre-creates
// one session at boot and reuses it across the operator's lifetime.
type WorktreeAllocator struct {
	Hub        *MCPHubClient
	ServerName string
	// AgentID identifies the operator to the worktree-allocate service.
	// Recorded on the WorktreeAssignment for audit + operator-cleanup.
	AgentID string
	// SourceSessionID is the operator's persistent session id (same one
	// HandoffClient uses).
	SourceSessionID string
	// RepoPath is the absolute path to the loom-core checkout the
	// operator manages. Forwarded as the repo_path arg so the tool
	// runs `git worktree add` in the right repo regardless of the
	// session's working_dir.
	RepoPath string
	// TTLHours bounds the worktree lifetime. 0 = no auto-orphan.
	// Defaults to 24 — long enough for a multi-stage pipeline run,
	// short enough that a crashed operator's worktrees don't
	// accumulate.
	TTLHours int
}

// NewWorktreeAllocator returns an allocator bound to hub. AgentID +
// SourceSessionID + RepoPath must be set before use; the operator's
// main.go fills them after the startup session is established.
func NewWorktreeAllocator(hub *MCPHubClient, agentID, sessionID, repoPath string) *WorktreeAllocator {
	return &WorktreeAllocator{
		Hub:             hub,
		ServerName:      AgentContextServerName,
		AgentID:         agentID,
		SourceSessionID: sessionID,
		RepoPath:        repoPath,
		TTLHours:        24,
	}
}

type worktreeAllocateResponse struct {
	AssignmentID string `json:"assignment_id"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	BaseBranch   string `json:"base_branch"`
}

type worktreeReleaseResponse struct {
	AssignmentID string `json:"assignment_id"`
	Removed      bool   `json:"removed"`
	Status       string `json:"status"`
}

// Allocate implements pipeline.WorktreeAllocator.
func (a *WorktreeAllocator) Allocate(ctx context.Context, req pipeline.WorktreeRequest) (pipeline.WorktreeHandle, error) {
	if a == nil || a.Hub == nil {
		return pipeline.WorktreeHandle{}, errors.New("worktree: allocator not configured")
	}
	if a.AgentID == "" {
		return pipeline.WorktreeHandle{}, errors.New("worktree: AgentID required")
	}
	if a.SourceSessionID == "" {
		return pipeline.WorktreeHandle{}, errors.New("worktree: SourceSessionID required (start an operator session at boot)")
	}
	if req.SliceName == "" {
		return pipeline.WorktreeHandle{}, errors.New("worktree: SliceName required")
	}
	branchName := req.BranchName
	if branchName == "" {
		branchName = pipeline.SliceBranchName(req.BacklogID, req.SliceName)
	}
	if branchName == "" {
		return pipeline.WorktreeHandle{}, errors.New("worktree: BranchName required")
	}
	args := map[string]any{
		"agent_id":    a.AgentID,
		"session_id":  a.SourceSessionID,
		"branch_name": branchName,
		"purpose":     req.Purpose,
	}
	if req.BaseBranch != "" {
		args["base_branch"] = req.BaseBranch
	} else {
		args["base_branch"] = "main"
	}
	if a.RepoPath != "" {
		args["repo_path"] = a.RepoPath
	}
	if a.TTLHours > 0 {
		args["ttl_hours"] = a.TTLHours
	}
	server := a.ServerName
	if server == "" {
		server = AgentContextServerName
	}
	body, err := a.Hub.CallTool(ctx, server, "agent_worktree_allocate", args)
	if err != nil && body == "" {
		return pipeline.WorktreeHandle{}, fmt.Errorf("worktree allocate: %w", err)
	}
	var parsed worktreeAllocateResponse
	if perr := json.Unmarshal([]byte(body), &parsed); perr != nil {
		return pipeline.WorktreeHandle{}, fmt.Errorf("worktree: decode allocate: %w; raw=%q", perr, body)
	}
	if parsed.WorktreePath == "" || parsed.Branch == "" {
		return pipeline.WorktreeHandle{}, fmt.Errorf("worktree allocate returned incomplete payload: %q", body)
	}
	return pipeline.WorktreeHandle{
		Path:   parsed.WorktreePath,
		Branch: parsed.Branch,
	}, nil
}

// Release implements pipeline.WorktreeAllocator.
//
// The agent_worktree_release tool requires assignment_id, but our
// pipeline.WorktreeHandle only carries Path + Branch (it doesn't know
// about agent-context's internal id). We resolve assignment_id by
// listing this agent's worktrees and matching on branch — the
// allocate-then-release lifecycle on a single Integrator run keeps
// branches unique.
func (a *WorktreeAllocator) Release(ctx context.Context, h pipeline.WorktreeHandle) error {
	if a == nil || a.Hub == nil {
		return errors.New("worktree: allocator not configured")
	}
	if h.Branch == "" {
		return errors.New("worktree: handle.Branch required for release lookup")
	}
	server := a.ServerName
	if server == "" {
		server = AgentContextServerName
	}
	// agent_worktree_list returns rows {assignment_id, worktree_path,
	// branch, agent_id, ...}. We filter on branch + agent_id.
	listBody, err := a.Hub.CallTool(ctx, server, "agent_worktree_list", map[string]any{
		"agent_id": a.AgentID,
	})
	if err != nil && listBody == "" {
		return fmt.Errorf("worktree list: %w", err)
	}
	assignmentID, err := findAssignmentByBranch(listBody, h.Branch)
	if err != nil {
		return err
	}
	releaseBody, err := a.Hub.CallTool(ctx, server, "agent_worktree_release", map[string]any{
		"assignment_id":   assignmentID,
		"remove_worktree": true,
		"force":           false,
	})
	if err != nil && releaseBody == "" {
		return fmt.Errorf("worktree release: %w", err)
	}
	var parsed worktreeReleaseResponse
	if perr := json.Unmarshal([]byte(releaseBody), &parsed); perr != nil {
		return fmt.Errorf("worktree: decode release: %w; raw=%q", perr, releaseBody)
	}
	return nil
}

// findAssignmentByBranch parses the agent_worktree_list response and
// returns the assignment_id whose branch matches. Returns an error if
// no assignment matches — releasing an unknown branch is a programming
// error worth surfacing rather than silently succeeding.
func findAssignmentByBranch(body, branch string) (string, error) {
	var resp struct {
		Worktrees []struct {
			AssignmentID string `json:"assignment_id"`
			Branch       string `json:"branch"`
		} `json:"worktrees"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		// Some hub deployments wrap the array differently; try a bare
		// array fallback before giving up.
		var bare []struct {
			AssignmentID string `json:"assignment_id"`
			Branch       string `json:"branch"`
		}
		if berr := json.Unmarshal([]byte(body), &bare); berr == nil {
			for _, a := range bare {
				if a.Branch == branch {
					return a.AssignmentID, nil
				}
			}
		}
		return "", fmt.Errorf("worktree: decode list: %w; raw=%q", err, body)
	}
	for _, a := range resp.Worktrees {
		if a.Branch == branch {
			return a.AssignmentID, nil
		}
	}
	return "", fmt.Errorf("worktree: no assignment matching branch %q in list response", branch)
}

// Compile-time interface assertion.
var _ pipeline.WorktreeAllocator = (*WorktreeAllocator)(nil)

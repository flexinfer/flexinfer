package agentcontext

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// Git worktree integration: agents request isolated worktrees for parallel work.

// HandleWorktreeAllocate creates a worktree + branch and assigns it to an agent
func (s *Service) HandleWorktreeAllocate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	sessionID := v.Required("session_id")
	branchName := v.Required("branch_name")
	baseBranch := v.String("base_branch", "HEAD")
	purpose := v.String("purpose", "")
	worktreePath := v.String("worktree_path", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	repoPath := s.cfg.GitRepoPath
	if repoPath == "" {
		return mcp.ErrorResult(fmt.Errorf("AGENT_CONTEXT_GIT_REPO_PATH or REPO_PATH must be set")), nil
	}

	// Determine worktree path
	if worktreePath == "" {
		baseDir := s.cfg.GitWorktreeBaseDir
		if baseDir == "" {
			baseDir = filepath.Join(repoPath, ".worktrees")
		}
		worktreePath = filepath.Join(baseDir, branchName)
	}

	// Validate path is safe
	absPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid worktree path: %w", err)), nil
	}

	// Create the worktree with a new branch
	_, err = s.runGit(ctx, repoPath, "worktree", "add", "-b", branchName, absPath, baseBranch)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("create worktree: %w", err)), nil
	}

	now := time.Now()
	assignment := &WorktreeAssignment{
		ID:           GenerateID(agentID, branchName, "worktree", now),
		AgentID:      agentID,
		SessionID:    sessionID,
		WorktreePath: absPath,
		Branch:       branchName,
		BaseBranch:   baseBranch,
		Purpose:      purpose,
		Status:       WorktreeStatusActive,
		CreatedAt:    now,
	}

	s.worktreeMu.Lock()
	s.worktreeAssns[assignment.ID] = assignment
	s.worktreeMu.Unlock()

	// Update presence with worktree ID
	s.presenceMu.Lock()
	if p, ok := s.presenceMap[agentID]; ok {
		p.WorktreeID = assignment.ID
	}
	s.presenceMu.Unlock()

	result := map[string]any{
		"ok":            true,
		"assignment_id": assignment.ID,
		"worktree_path": absPath,
		"branch":        branchName,
		"base_branch":   baseBranch,
		"agent_id":      agentID,
	}

	// Persist (non-fatal)
	if err := s.persistWorktreeAssignment(ctx, assignment); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist worktree assignment: %v", err)
	}

	return mcp.JSONResult(result)
}

// HandleWorktreeRelease releases a worktree assignment
func (s *Service) HandleWorktreeRelease(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	assignmentID := v.Required("assignment_id")
	removeWorktree := v.Bool("remove_worktree", false)
	force := v.Bool("force", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.worktreeMu.Lock()
	assignment, ok := s.worktreeAssns[assignmentID]
	if !ok {
		s.worktreeMu.Unlock()
		return mcp.ErrorResult(fmt.Errorf("assignment %s not found", assignmentID)), nil
	}

	now := time.Now()
	assignment.Status = WorktreeStatusReleased
	assignment.ReleasedAt = &now
	s.worktreeMu.Unlock()

	// Remove worktree from disk if requested
	if removeWorktree && s.cfg.GitRepoPath != "" {
		gitArgs := []string{"worktree", "remove"}
		if force {
			gitArgs = append(gitArgs, "--force")
		}
		gitArgs = append(gitArgs, assignment.WorktreePath)

		if _, err := s.runGit(ctx, s.cfg.GitRepoPath, gitArgs...); err != nil {
			return mcp.ErrorResult(fmt.Errorf("remove worktree: %w", err)), nil
		}
	}

	// Update presence
	s.presenceMu.Lock()
	if p, ok := s.presenceMap[assignment.AgentID]; ok {
		if p.WorktreeID == assignmentID {
			p.WorktreeID = ""
		}
	}
	s.presenceMu.Unlock()

	result := map[string]any{
		"ok":               true,
		"assignment_id":    assignmentID,
		"status":           string(assignment.Status),
		"worktree_removed": removeWorktree,
	}

	// Persist update (non-fatal)
	if err := s.persistWorktreeAssignment(ctx, assignment); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist worktree release: %v", err)
	}

	return mcp.JSONResult(result)
}

// HandleWorktreeList lists worktree assignments
func (s *Service) HandleWorktreeList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	statusFilter := v.String("status", "")
	includeGitStatus := v.Bool("include_git_status", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.worktreeMu.RLock()
	defer s.worktreeMu.RUnlock()

	var assignments []map[string]any

	for _, a := range s.worktreeAssns {
		if agentID != "" && a.AgentID != agentID {
			continue
		}
		if statusFilter != "" && string(a.Status) != statusFilter {
			continue
		}

		entry := map[string]any{
			"assignment_id": a.ID,
			"agent_id":      a.AgentID,
			"session_id":    a.SessionID,
			"worktree_path": a.WorktreePath,
			"branch":        a.Branch,
			"base_branch":   a.BaseBranch,
			"purpose":       a.Purpose,
			"status":        string(a.Status),
			"created_at":    a.CreatedAt.Format(time.RFC3339),
		}
		if a.ReleasedAt != nil {
			entry["released_at"] = a.ReleasedAt.Format(time.RFC3339)
		}

		if includeGitStatus && a.Status == WorktreeStatusActive && s.cfg.GitRepoPath != "" {
			if status, err := s.runGit(ctx, a.WorktreePath, "status", "--porcelain"); err == nil {
				entry["git_status"] = status
			}
			if branch, err := s.runGit(ctx, a.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
				entry["current_branch"] = branch
			}
		}

		assignments = append(assignments, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"assignments": assignments,
		"count":       len(assignments),
	})
}

// HandleWorktreeStatus returns detailed status for a specific assignment
func (s *Service) HandleWorktreeStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	assignmentID := v.Required("assignment_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.worktreeMu.RLock()
	assignment, ok := s.worktreeAssns[assignmentID]
	s.worktreeMu.RUnlock()

	if !ok {
		return mcp.ErrorResult(fmt.Errorf("assignment %s not found", assignmentID)), nil
	}

	result := map[string]any{
		"ok":            true,
		"assignment_id": assignment.ID,
		"agent_id":      assignment.AgentID,
		"session_id":    assignment.SessionID,
		"worktree_path": assignment.WorktreePath,
		"branch":        assignment.Branch,
		"base_branch":   assignment.BaseBranch,
		"purpose":       assignment.Purpose,
		"status":        string(assignment.Status),
		"created_at":    assignment.CreatedAt.Format(time.RFC3339),
	}
	if assignment.ReleasedAt != nil {
		result["released_at"] = assignment.ReleasedAt.Format(time.RFC3339)
	}

	// Get git info if active
	if assignment.Status == WorktreeStatusActive && s.cfg.GitRepoPath != "" {
		if status, err := s.runGit(ctx, assignment.WorktreePath, "status", "--porcelain"); err == nil {
			result["git_status"] = status
			lines := strings.Split(strings.TrimSpace(status), "\n")
			if status == "" {
				result["changes_count"] = 0
			} else {
				result["changes_count"] = len(lines)
			}
		}
		if log, err := s.runGit(ctx, assignment.WorktreePath, "log", "--oneline", "-5"); err == nil {
			result["recent_commits"] = log
		}
		if branch, err := s.runGit(ctx, assignment.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			result["current_branch"] = branch
		}
		if head, err := s.runGit(ctx, assignment.WorktreePath, "rev-parse", "HEAD"); err == nil {
			result["head_commit"] = head
		}
	}

	return mcp.JSONResult(result)
}

// HandleWorktreeCleanup removes orphaned worktrees
func (s *Service) HandleWorktreeCleanup(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	dryRun := v.Bool("dry_run", true)
	force := v.Bool("force", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	repoPath := s.cfg.GitRepoPath
	if repoPath == "" {
		return mcp.ErrorResult(fmt.Errorf("AGENT_CONTEXT_GIT_REPO_PATH or REPO_PATH must be set")), nil
	}

	s.worktreeMu.RLock()
	var orphaned []map[string]any
	for _, a := range s.worktreeAssns {
		if a.Status == WorktreeStatusOrphaned {
			orphaned = append(orphaned, map[string]any{
				"assignment_id": a.ID,
				"worktree_path": a.WorktreePath,
				"branch":        a.Branch,
				"agent_id":      a.AgentID,
			})
		}
	}
	s.worktreeMu.RUnlock()

	removed := 0
	if !dryRun {
		for _, o := range orphaned {
			path := toString(o["worktree_path"])
			gitArgs := []string{"worktree", "remove"}
			if force {
				gitArgs = append(gitArgs, "--force")
			}
			gitArgs = append(gitArgs, path)

			if _, err := s.runGit(ctx, repoPath, gitArgs...); err == nil {
				removed++
				// Remove from assignments
				s.worktreeMu.Lock()
				delete(s.worktreeAssns, toString(o["assignment_id"]))
				s.worktreeMu.Unlock()
			}
		}

		// Also prune stale worktree metadata
		_, _ = s.runGit(ctx, repoPath, "worktree", "prune")
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"dry_run":  dryRun,
		"orphaned": orphaned,
		"removed":  removed,
	})
}

// orphanWorktreesForAgent marks all worktrees for an agent as orphaned
func (s *Service) orphanWorktreesForAgent(agentID string) {
	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()

	for _, a := range s.worktreeAssns {
		if a.AgentID == agentID && a.Status == WorktreeStatusActive {
			a.Status = WorktreeStatusOrphaned
		}
	}
}

// runGit executes a git command in the given directory
func (s *Service) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %v, output: %s", args[0], err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// persistWorktreeAssignment stores an assignment to Qdrant
func (s *Service) persistWorktreeAssignment(ctx context.Context, a *WorktreeAssignment) error {
	if err := s.worktreeQdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      a.ID,
		Vector:  make([]float64, sessionsVectorSize),
		Payload: worktreeAssignmentToPayload(a),
	}

	return s.worktreeQdrant.Upsert(ctx, []Point{point}, true)
}

// loadWorktreeAssignmentsFromQdrant loads worktree assignments on startup
func (s *Service) loadWorktreeAssignmentsFromQdrant(ctx context.Context) error {
	points, err := s.worktreeQdrant.ScrollPoints(ctx, nil, 500, false)
	if err != nil {
		return err
	}

	s.worktreeMu.Lock()
	defer s.worktreeMu.Unlock()

	for _, p := range points {
		a := payloadToWorktreeAssignment(p.Payload)
		if a == nil {
			continue
		}
		s.worktreeAssns[a.ID] = a
	}
	return nil
}

// Payload converters

func worktreeAssignmentToPayload(a *WorktreeAssignment) map[string]any {
	payload := map[string]any{
		"id":            a.ID,
		"agent_id":      a.AgentID,
		"session_id":    a.SessionID,
		"worktree_path": a.WorktreePath,
		"branch":        a.Branch,
		"base_branch":   a.BaseBranch,
		"purpose":       a.Purpose,
		"status":        string(a.Status),
		"created_at":    a.CreatedAt.Format(time.RFC3339Nano),
	}
	if a.ReleasedAt != nil {
		payload["released_at"] = a.ReleasedAt.Format(time.RFC3339Nano)
	}
	return payload
}

func payloadToWorktreeAssignment(payload map[string]any) *WorktreeAssignment {
	if payload == nil {
		return nil
	}
	a := &WorktreeAssignment{
		ID:           toString(payload["id"]),
		AgentID:      toString(payload["agent_id"]),
		SessionID:    toString(payload["session_id"]),
		WorktreePath: toString(payload["worktree_path"]),
		Branch:       toString(payload["branch"]),
		BaseBranch:   toString(payload["base_branch"]),
		Purpose:      toString(payload["purpose"]),
		Status:       WorktreeStatus(toString(payload["status"])),
	}
	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			a.CreatedAt = t
		}
	}
	if ts := toString(payload["released_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			a.ReleasedAt = &t
		}
	}
	return a
}

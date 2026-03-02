package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// WorktreeSvc manages git worktree assignments, lifecycle, and reconciliation.
type WorktreeSvc struct {
	mu    sync.RWMutex
	assns map[string]*WorktreeAssignment

	qdrant  *QdrantClient // CollWorktree
	cfg     Config
	logger  *slog.Logger
	metrics *Metrics

	// Reconciler (optional, set after construction).
	reconciler *WorktreeReconciler

	// Cross-domain callbacks (wired by Service).
	setPresenceWorktreeID   func(agentID, worktreeID string)
	clearPresenceWorktreeID func(agentID, worktreeID string)
}

// NewWorktreeSvc creates a new WorktreeSvc.
func NewWorktreeSvc(qdrant *QdrantClient, cfg Config, logger *slog.Logger, metrics *Metrics) *WorktreeSvc {
	return &WorktreeSvc{
		assns:   make(map[string]*WorktreeAssignment),
		qdrant:  qdrant,
		cfg:     cfg,
		logger:  logger,
		metrics: metrics,
	}
}

// Allocate creates a worktree + branch and assigns it to an agent.
func (wt *WorktreeSvc) Allocate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	sessionID := v.Required("session_id")
	branchName := v.Required("branch_name")
	baseBranch := v.String("base_branch", "HEAD")
	purpose := v.String("purpose", "")
	worktreePath := v.String("worktree_path", "")
	ttlHours := v.Int("ttl_hours", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	repoPath := wt.cfg.GitRepoPath
	if repoPath == "" {
		return mcp.ErrorResult(fmt.Errorf("AGENT_CONTEXT_GIT_REPO_PATH or REPO_PATH must be set")), nil
	}

	// Determine worktree path
	if worktreePath == "" {
		baseDir := wt.cfg.GitWorktreeBaseDir
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
	_, err = wt.RunGit(ctx, repoPath, "worktree", "add", "-b", branchName, absPath, baseBranch)
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
		TTL:          ttlHours,
	}

	wt.mu.Lock()
	wt.assns[assignment.ID] = assignment
	wt.mu.Unlock()

	// Update presence with worktree ID
	if wt.setPresenceWorktreeID != nil {
		wt.setPresenceWorktreeID(agentID, assignment.ID)
	}

	result := map[string]any{
		"ok":            true,
		"assignment_id": assignment.ID,
		"worktree_path": absPath,
		"branch":        branchName,
		"base_branch":   baseBranch,
		"agent_id":      agentID,
	}

	// Persist (non-fatal)
	if err := wt.Persist(ctx, assignment); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist worktree assignment: %v", err)
	}

	return mcp.JSONResult(result)
}

// Release releases a worktree assignment.
func (wt *WorktreeSvc) Release(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	assignmentID := v.Required("assignment_id")
	removeWorktree := v.Bool("remove_worktree", false)
	force := v.Bool("force", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	wt.mu.Lock()
	assignment, ok := wt.assns[assignmentID]
	if !ok {
		wt.mu.Unlock()
		return mcp.ErrorResult(fmt.Errorf("assignment %s not found", assignmentID)), nil
	}

	now := time.Now()
	assignment.Status = WorktreeStatusReleased
	assignment.ReleasedAt = &now
	wt.mu.Unlock()

	// Remove worktree from disk if requested
	if removeWorktree && wt.cfg.GitRepoPath != "" {
		gitArgs := []string{"worktree", "remove"}
		if force {
			gitArgs = append(gitArgs, "--force")
		}
		gitArgs = append(gitArgs, assignment.WorktreePath)

		if _, err := wt.RunGit(ctx, wt.cfg.GitRepoPath, gitArgs...); err != nil {
			return mcp.ErrorResult(fmt.Errorf("remove worktree: %w", err)), nil
		}
	}

	// Update presence
	if wt.clearPresenceWorktreeID != nil {
		wt.clearPresenceWorktreeID(assignment.AgentID, assignmentID)
	}

	result := map[string]any{
		"ok":               true,
		"assignment_id":    assignmentID,
		"status":           string(assignment.Status),
		"worktree_removed": removeWorktree,
	}

	// Persist update (non-fatal)
	if err := wt.Persist(ctx, assignment); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist worktree release: %v", err)
	}

	return mcp.JSONResult(result)
}

// List lists worktree assignments.
func (wt *WorktreeSvc) List(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	statusFilter := v.String("status", "")
	includeGitStatus := v.Bool("include_git_status", false)
	includeDiskUsage := v.Bool("include_disk_usage", false)
	includeUntracked := v.Bool("include_untracked", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Optionally trigger untracked detection first
	if includeUntracked && wt.reconciler != nil {
		wt.reconciler.detectUntrackedWorktrees(ctx)
	}

	wt.mu.RLock()
	defer wt.mu.RUnlock()

	var assignments []map[string]any
	var totalDiskUsage int64

	for _, a := range wt.assns {
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
		if a.OrphanedAt != nil {
			entry["orphaned_at"] = a.OrphanedAt.Format(time.RFC3339)
		}
		if a.TTL > 0 {
			entry["ttl_hours"] = a.TTL
		}

		// Include disk usage (from cache or live scan)
		if includeDiskUsage && a.Status != WorktreeStatusReleased {
			diskUsage := a.DiskUsage
			if diskUsage == 0 {
				if size, err := dirDiskUsage(a.WorktreePath); err == nil {
					diskUsage = size
				}
			}
			entry["disk_usage_bytes"] = diskUsage
			entry["disk_usage_human"] = humanizeBytes(diskUsage)
			if a.DiskMeasuredAt != nil {
				entry["disk_measured_at"] = a.DiskMeasuredAt.Format(time.RFC3339)
			}
			totalDiskUsage += diskUsage
		}

		if includeGitStatus && a.Status == WorktreeStatusActive && wt.cfg.GitRepoPath != "" {
			if status, err := wt.RunGit(ctx, a.WorktreePath, "status", "--porcelain"); err == nil {
				entry["git_status"] = status
			}
			if branch, err := wt.RunGit(ctx, a.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
				entry["current_branch"] = branch
			}
		}

		assignments = append(assignments, entry)
	}

	result := map[string]any{
		"ok":          true,
		"assignments": assignments,
		"count":       len(assignments),
	}
	if includeDiskUsage {
		result["total_disk_usage_bytes"] = totalDiskUsage
		result["total_disk_usage_human"] = humanizeBytes(totalDiskUsage)
	}

	return mcp.JSONResult(result)
}

// Status returns detailed status for a specific assignment.
func (wt *WorktreeSvc) Status(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	assignmentID := v.Required("assignment_id")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	wt.mu.RLock()
	assignment, ok := wt.assns[assignmentID]
	wt.mu.RUnlock()

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
	if assignment.OrphanedAt != nil {
		result["orphaned_at"] = assignment.OrphanedAt.Format(time.RFC3339)
	}
	if assignment.TTL > 0 {
		result["ttl_hours"] = assignment.TTL
	}

	// Disk usage
	if assignment.Status != WorktreeStatusReleased {
		if size, err := dirDiskUsage(assignment.WorktreePath); err == nil {
			result["disk_usage_bytes"] = size
			result["disk_usage_human"] = humanizeBytes(size)
		}
		// Artifact scan
		patterns := parseArtifactPatterns(wt.cfg.WorktreeArtifactCleanupPatterns)
		if len(patterns) > 0 {
			if artifacts, err := findArtifactDirs(assignment.WorktreePath, patterns); err == nil && len(artifacts) > 0 {
				var artifactTotal int64
				artifactEntries := make([]map[string]any, 0, len(artifacts))
				for _, a := range artifacts {
					artifactTotal += a.SizeBytes
					artifactEntries = append(artifactEntries, map[string]any{
						"path":       a.Path,
						"pattern":    a.Pattern,
						"size_bytes": a.SizeBytes,
						"size_human": humanizeBytes(a.SizeBytes),
					})
				}
				result["artifacts"] = artifactEntries
				result["artifact_total_bytes"] = artifactTotal
				result["artifact_total_human"] = humanizeBytes(artifactTotal)
			}
		}
	}

	// Get git info if active
	if assignment.Status == WorktreeStatusActive && wt.cfg.GitRepoPath != "" {
		if status, err := wt.RunGit(ctx, assignment.WorktreePath, "status", "--porcelain"); err == nil {
			result["git_status"] = status
			lines := strings.Split(strings.TrimSpace(status), "\n")
			if status == "" {
				result["changes_count"] = 0
			} else {
				result["changes_count"] = len(lines)
			}
		}
		if log, err := wt.RunGit(ctx, assignment.WorktreePath, "log", "--oneline", "-5"); err == nil {
			result["recent_commits"] = log
		}
		if branch, err := wt.RunGit(ctx, assignment.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			result["current_branch"] = branch
		}
		if head, err := wt.RunGit(ctx, assignment.WorktreePath, "rev-parse", "HEAD"); err == nil {
			result["head_commit"] = head
		}
	}

	return mcp.JSONResult(result)
}

// Cleanup removes orphaned worktrees.
func (wt *WorktreeSvc) Cleanup(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	dryRun := v.Bool("dry_run", true)
	force := v.Bool("force", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	repoPath := wt.cfg.GitRepoPath
	if repoPath == "" {
		return mcp.ErrorResult(fmt.Errorf("AGENT_CONTEXT_GIT_REPO_PATH or REPO_PATH must be set")), nil
	}

	wt.mu.RLock()
	var orphaned []map[string]any
	for _, a := range wt.assns {
		if a.Status == WorktreeStatusOrphaned {
			orphaned = append(orphaned, map[string]any{
				"assignment_id": a.ID,
				"worktree_path": a.WorktreePath,
				"branch":        a.Branch,
				"agent_id":      a.AgentID,
			})
		}
	}
	wt.mu.RUnlock()

	removed := 0
	var totalBytesFreed int64
	artifactsCleaned := 0

	if !dryRun {
		patterns := parseArtifactPatterns(wt.cfg.WorktreeArtifactCleanupPatterns)
		for _, o := range orphaned {
			path := toString(o["worktree_path"])

			// Pre-clean build artifacts
			if wt.cfg.WorktreeArtifactCleanupEnabled && len(patterns) > 0 {
				freed, paths, err := removeArtifactDirs(path, patterns, false)
				if err != nil {
					wt.logger.Warn("artifact cleanup failed during worktree cleanup", "path", path, "error", err)
				}
				totalBytesFreed += freed
				artifactsCleaned += len(paths)
			}

			gitArgs := []string{"worktree", "remove"}
			if force {
				gitArgs = append(gitArgs, "--force")
			}
			gitArgs = append(gitArgs, path)

			if _, err := wt.RunGit(ctx, repoPath, gitArgs...); err == nil {
				removed++
				// Remove from assignments
				wt.mu.Lock()
				delete(wt.assns, toString(o["assignment_id"]))
				wt.mu.Unlock()
			}
		}

		// Also prune stale worktree metadata
		if _, err := wt.RunGit(ctx, repoPath, "worktree", "prune"); err != nil {
			wt.logger.Warn("failed to prune git worktree metadata", "repo_path", repoPath, "error", err)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":                true,
		"dry_run":           dryRun,
		"orphaned":          orphaned,
		"removed":           removed,
		"artifacts_cleaned": artifactsCleaned,
		"bytes_freed":       totalBytesFreed,
		"bytes_freed_human": humanizeBytes(totalBytesFreed),
	})
}

// Reconcile manually triggers worktree reconciliation.
func (wt *WorktreeSvc) Reconcile(ctx context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	if wt.reconciler == nil {
		return mcp.ErrorResult(fmt.Errorf("worktree reconciler is not enabled")), nil
	}

	stats, err := wt.reconciler.TriggerReconcile(ctx)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("reconcile failed: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":                true,
		"disk_scanned":      stats.DiskScanned,
		"ttl_expired":       stats.TTLExpired,
		"orphans_removed":   stats.OrphansRemoved,
		"artifacts_cleaned": stats.ArtifactsCleaned,
		"bytes_freed":       stats.BytesFreed,
		"bytes_freed_human": humanizeBytes(stats.BytesFreed),
		"untracked_found":   stats.UntrackedFound,
		"errors":            stats.Errors,
		"duration_ms":       stats.Duration.Milliseconds(),
	})
}

// OrphanForAgent marks all active worktrees for an agent as orphaned.
func (wt *WorktreeSvc) OrphanForAgent(agentID string) {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	now := time.Now()
	for _, a := range wt.assns {
		if a.AgentID == agentID && a.Status == WorktreeStatusActive {
			a.Status = WorktreeStatusOrphaned
			a.OrphanedAt = &now
		}
	}
}

// RunGit executes a git command in the given directory.
func (wt *WorktreeSvc) RunGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %v, output: %s", args[0], err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// Persist stores an assignment to Qdrant.
func (wt *WorktreeSvc) Persist(ctx context.Context, a *WorktreeAssignment) error {
	if wt.qdrant == nil {
		return nil
	}
	if err := wt.qdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      a.ID,
		Vector:  make([]float64, sessionsVectorSize),
		Payload: worktreeAssignmentToPayload(a),
	}

	return wt.qdrant.Upsert(ctx, []Point{point}, true)
}

// LoadFromQdrant loads worktree assignments on startup.
func (wt *WorktreeSvc) LoadFromQdrant(ctx context.Context) error {
	if wt.qdrant == nil {
		return nil
	}

	points, err := wt.qdrant.ScrollPoints(ctx, nil, 500, false)
	if err != nil {
		return err
	}

	wt.mu.Lock()
	defer wt.mu.Unlock()

	for _, p := range points {
		a := payloadToWorktreeAssignment(p.Payload)
		if a == nil {
			continue
		}
		wt.assns[a.ID] = a
	}
	return nil
}

// StartReconciler starts the reconciler loop if configured.
func (wt *WorktreeSvc) StartReconciler(ctx context.Context) {
	if wt.reconciler != nil {
		wt.reconciler.Start(ctx)
	}
}

// StopReconciler stops the reconciler if running.
func (wt *WorktreeSvc) StopReconciler() {
	if wt.reconciler != nil {
		wt.reconciler.Stop()
	}
}

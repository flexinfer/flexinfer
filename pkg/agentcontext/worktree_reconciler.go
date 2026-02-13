package agentcontext

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// WorktreeReconcilerConfig configures automatic worktree lifecycle management.
type WorktreeReconcilerConfig struct {
	Enabled       bool          `json:"enabled"`
	CheckInterval time.Duration `json:"check_interval"`

	// Grace period before removing orphaned worktrees.
	OrphanGracePeriod time.Duration `json:"orphan_grace_period"`

	// Default max TTL for worktrees (0 = unlimited).
	MaxTTLHours int `json:"max_ttl_hours"`

	// Artifact cleanup settings.
	ArtifactCleanupEnabled bool     `json:"artifact_cleanup_enabled"`
	ArtifactPatterns       []string `json:"artifact_patterns"`

	// Whether to measure disk usage per worktree.
	DiskScanEnabled bool `json:"disk_scan_enabled"`

	// Whether to detect worktrees created outside mcp-agent-context.
	DetectUntracked bool `json:"detect_untracked"`
}

// DefaultWorktreeReconcilerConfig returns sensible defaults.
func DefaultWorktreeReconcilerConfig() WorktreeReconcilerConfig {
	return WorktreeReconcilerConfig{
		Enabled:                true,
		CheckInterval:          5 * time.Minute,
		OrphanGracePeriod:      30 * time.Minute,
		MaxTTLHours:            0,
		ArtifactCleanupEnabled: true,
		ArtifactPatterns:       []string{".next", "node_modules", "target", "dist", ".cache", "__pycache__", ".tox"},
		DiskScanEnabled:        true,
		DetectUntracked:        true,
	}
}

// WorktreeReconcileStats contains statistics from a single reconciliation run.
type WorktreeReconcileStats struct {
	StartTime        time.Time     `json:"start_time"`
	Duration         time.Duration `json:"duration"`
	DiskScanned      int           `json:"disk_scanned"`
	TTLExpired       int           `json:"ttl_expired"`
	OrphansRemoved   int           `json:"orphans_removed"`
	ArtifactsCleaned int           `json:"artifacts_cleaned"`
	BytesFreed       int64         `json:"bytes_freed"`
	UntrackedFound   int           `json:"untracked_found"`
	Errors           int           `json:"errors"`
}

// WorktreeReconciler runs periodic lifecycle management on worktrees.
type WorktreeReconciler struct {
	mu sync.RWMutex

	config WorktreeReconcilerConfig
	svc    *Service
	logger *slog.Logger

	running   bool
	stopCh    chan struct{}
	lastRun   time.Time
	runCount  int64
	lastStats WorktreeReconcileStats
}

// NewWorktreeReconciler creates a worktree reconciler.
func NewWorktreeReconciler(config WorktreeReconcilerConfig, svc *Service, logger *slog.Logger) *WorktreeReconciler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorktreeReconciler{
		config: config,
		svc:    svc,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// Start begins the reconciliation loop.
func (r *WorktreeReconciler) Start(ctx context.Context) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.stopCh = make(chan struct{})
	r.mu.Unlock()

	go r.runLoop(ctx)
}

// Stop stops the reconciler.
func (r *WorktreeReconciler) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return
	}
	close(r.stopCh)
	r.running = false
}

// TriggerReconcile manually triggers a reconciliation run.
func (r *WorktreeReconciler) TriggerReconcile(ctx context.Context) (*WorktreeReconcileStats, error) {
	return r.reconcile(ctx)
}

// LastStats returns stats from the most recent run.
func (r *WorktreeReconciler) LastStats() WorktreeReconcileStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastStats
}

func (r *WorktreeReconciler) runLoop(ctx context.Context) {
	ticker := time.NewTicker(r.config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			stats, err := r.reconcile(ctx)
			if err != nil {
				r.logger.Warn("worktree reconciliation failed", "error", err)
			} else if stats != nil && (stats.OrphansRemoved+stats.ArtifactsCleaned+stats.TTLExpired+stats.UntrackedFound) > 0 {
				r.logger.Info("worktree reconciliation completed",
					"disk_scanned", stats.DiskScanned,
					"ttl_expired", stats.TTLExpired,
					"orphans_removed", stats.OrphansRemoved,
					"artifacts_cleaned", stats.ArtifactsCleaned,
					"bytes_freed", humanizeBytes(stats.BytesFreed),
					"untracked_found", stats.UntrackedFound,
					"errors", stats.Errors,
					"duration", stats.Duration,
				)
			}
		}
	}
}

func (r *WorktreeReconciler) reconcile(ctx context.Context) (*WorktreeReconcileStats, error) {
	start := time.Now()
	stats := WorktreeReconcileStats{StartTime: start}
	now := time.Now()

	r.svc.worktreeMu.Lock()
	// Snapshot current assignments for processing
	assignments := make([]*WorktreeAssignment, 0, len(r.svc.worktreeAssns))
	for _, a := range r.svc.worktreeAssns {
		assignments = append(assignments, a)
	}
	r.svc.worktreeMu.Unlock()

	// ── Pass 1: Disk scan ──
	if r.config.DiskScanEnabled {
		for _, a := range assignments {
			if a.Status == WorktreeStatusReleased {
				continue
			}
			size, err := dirDiskUsage(a.WorktreePath)
			if err != nil {
				continue
			}
			r.svc.worktreeMu.Lock()
			a.DiskUsage = size
			measuredAt := now
			a.DiskMeasuredAt = &measuredAt
			r.svc.worktreeMu.Unlock()
			stats.DiskScanned++

			// Best-effort persist
			_ = r.svc.persistWorktreeAssignment(ctx, a)
		}
	}

	// ── Pass 2: TTL expiration ──
	maxTTL := r.config.MaxTTLHours
	for _, a := range assignments {
		if a.Status != WorktreeStatusActive {
			continue
		}

		ttl := a.TTL
		if ttl == 0 && maxTTL > 0 {
			ttl = maxTTL
		}
		if ttl <= 0 {
			continue
		}

		deadline := a.CreatedAt.Add(time.Duration(ttl) * time.Hour)
		if now.After(deadline) {
			r.svc.worktreeMu.Lock()
			a.Status = WorktreeStatusOrphaned
			orphanedAt := now
			a.OrphanedAt = &orphanedAt
			r.svc.worktreeMu.Unlock()
			stats.TTLExpired++

			r.logger.Info("worktree TTL expired",
				"assignment_id", a.ID,
				"branch", a.Branch,
				"ttl_hours", ttl,
				"age_hours", int(now.Sub(a.CreatedAt).Hours()),
			)

			_ = r.svc.persistWorktreeAssignment(ctx, a)
			r.svc.metrics.WorktreeOrphansRemoved.Add(1)
		}
	}

	// ── Pass 3: Orphan removal (after grace period) ──
	for _, a := range assignments {
		if a.Status != WorktreeStatusOrphaned {
			continue
		}

		orphanedAt := a.OrphanedAt
		if orphanedAt == nil {
			// Legacy orphan without timestamp — set it now and skip this round
			r.svc.worktreeMu.Lock()
			ot := now
			a.OrphanedAt = &ot
			r.svc.worktreeMu.Unlock()
			_ = r.svc.persistWorktreeAssignment(ctx, a)
			continue
		}

		if now.Sub(*orphanedAt) < r.config.OrphanGracePeriod {
			continue // still within grace period
		}

		var bytesFreed int64

		// Clean artifacts first (if enabled)
		if r.config.ArtifactCleanupEnabled && len(r.config.ArtifactPatterns) > 0 {
			freed, paths, err := removeArtifactDirs(a.WorktreePath, r.config.ArtifactPatterns, false)
			if err != nil {
				r.logger.Warn("artifact cleanup failed", "worktree", a.WorktreePath, "error", err)
				stats.Errors++
			}
			if len(paths) > 0 {
				stats.ArtifactsCleaned += len(paths)
				bytesFreed += freed
				r.logger.Info("cleaned artifacts before worktree removal",
					"worktree", a.WorktreePath,
					"artifacts", len(paths),
					"freed", humanizeBytes(freed),
				)
			}
		}

		// Remove the worktree via git
		repoPath := r.svc.cfg.GitRepoPath
		if repoPath != "" {
			_, err := r.svc.runGit(ctx, repoPath, "worktree", "remove", "--force", a.WorktreePath)
			if err != nil {
				r.logger.Warn("failed to remove orphaned worktree",
					"assignment_id", a.ID,
					"worktree_path", a.WorktreePath,
					"error", err,
				)
				stats.Errors++
				continue
			}
		}

		stats.OrphansRemoved++
		stats.BytesFreed += bytesFreed + a.DiskUsage

		r.logger.Info("removed orphaned worktree",
			"assignment_id", a.ID,
			"branch", a.Branch,
			"agent_id", a.AgentID,
			"disk_usage", humanizeBytes(a.DiskUsage),
		)

		// Remove from in-memory map and Qdrant
		r.svc.worktreeMu.Lock()
		delete(r.svc.worktreeAssns, a.ID)
		r.svc.worktreeMu.Unlock()

		if r.svc.worktreeQdrant != nil {
			_ = r.svc.worktreeQdrant.Delete(ctx, []string{a.ID})
		}

		r.svc.metrics.WorktreeOrphansRemoved.Add(1)
		r.svc.metrics.WorktreeBytesFreed.Add(bytesFreed + a.DiskUsage)
	}

	// ── Pass 4: Untracked worktree detection ──
	if r.config.DetectUntracked && r.svc.cfg.GitRepoPath != "" {
		untrackedCount := r.detectUntrackedWorktrees(ctx)
		stats.UntrackedFound += untrackedCount
	}

	// ── Pass 5: Git prune ──
	if r.svc.cfg.GitRepoPath != "" {
		if _, err := r.svc.runGit(ctx, r.svc.cfg.GitRepoPath, "worktree", "prune"); err != nil {
			r.logger.Warn("git worktree prune failed", "error", err)
			stats.Errors++
		}
	}

	stats.Duration = time.Since(start)

	r.mu.Lock()
	r.lastRun = start
	r.runCount++
	r.lastStats = stats
	r.mu.Unlock()

	r.svc.metrics.WorktreeReconcileRuns.Add(1)

	return &stats, nil
}

// detectUntrackedWorktrees cross-references `git worktree list` with known
// assignments and adopts untracked worktrees as orphaned entries.
func (r *WorktreeReconciler) detectUntrackedWorktrees(ctx context.Context) int {
	output, err := r.svc.runGit(ctx, r.svc.cfg.GitRepoPath, "worktree", "list", "--porcelain")
	if err != nil {
		r.logger.Warn("git worktree list failed", "error", err)
		return 0
	}

	// Parse porcelain output: blocks separated by blank lines
	// Each block has "worktree <path>", "HEAD <sha>", "branch refs/heads/<name>"
	type gitWorktree struct {
		path   string
		branch string
	}

	var gitWorktrees []gitWorktree
	var current gitWorktree

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if current.path != "" {
				gitWorktrees = append(gitWorktrees, current)
			}
			current = gitWorktree{}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			current.path = strings.TrimPrefix(line, "worktree ")
		}
		if strings.HasPrefix(line, "branch ") {
			ref := strings.TrimPrefix(line, "branch ")
			current.branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	if current.path != "" {
		gitWorktrees = append(gitWorktrees, current)
	}

	// Build set of known worktree paths
	r.svc.worktreeMu.RLock()
	knownPaths := make(map[string]struct{}, len(r.svc.worktreeAssns))
	for _, a := range r.svc.worktreeAssns {
		knownPaths[a.WorktreePath] = struct{}{}
	}
	r.svc.worktreeMu.RUnlock()

	// The main repo worktree is the first entry — skip it
	found := 0
	for i, gw := range gitWorktrees {
		if i == 0 {
			continue // skip main repo
		}
		if _, known := knownPaths[gw.path]; known {
			continue
		}

		// Found an untracked worktree — adopt it as orphaned
		now := time.Now()
		assignment := &WorktreeAssignment{
			ID:           GenerateID("untracked", gw.branch, gw.path, now),
			AgentID:      "untracked",
			SessionID:    "",
			WorktreePath: gw.path,
			Branch:       gw.branch,
			Purpose:      "auto-detected untracked worktree",
			Status:       WorktreeStatusOrphaned,
			CreatedAt:    now,
			OrphanedAt:   &now,
		}

		r.svc.worktreeMu.Lock()
		r.svc.worktreeAssns[assignment.ID] = assignment
		r.svc.worktreeMu.Unlock()

		_ = r.svc.persistWorktreeAssignment(ctx, assignment)

		r.logger.Info("detected untracked worktree",
			"path", gw.path,
			"branch", gw.branch,
			"assignment_id", assignment.ID,
		)

		found++
		r.svc.metrics.WorktreeUntrackedFound.Add(1)
	}

	return found
}

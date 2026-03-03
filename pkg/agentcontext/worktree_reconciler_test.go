package agentcontext

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"
)

func newTestServiceForWorktree() *Service {
	logger := slog.Default()
	metrics := NewMetrics()
	cfg := Config{
		PresenceHeartbeatTTL:            120,
		PresenceCleanupInterval:         60,
		WorktreeArtifactCleanupPatterns: ".next,node_modules,target",
	}
	return &Service{
		cfg:     cfg,
		logger:  logger,
		tracer:  noop.NewTracerProvider().Tracer("test"),
		metrics: metrics,

		presence:  NewPresenceSvc(nil, cfg, logger, metrics),
		claims:    NewClaimSvc(nil, logger, metrics),
		worktrees: NewWorktreeSvc(nil, cfg, logger, metrics),

		sess: NewSessionSvc(nil, cfg, logger, metrics),
	}
}

func TestWorktreeReconciler_StartStop(t *testing.T) {
	svc := newTestServiceForWorktree()
	config := DefaultWorktreeReconcilerConfig()
	config.CheckInterval = 100 * time.Millisecond

	r := NewWorktreeReconciler(config, svc.worktrees, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx)

	// Verify it's running
	r.mu.RLock()
	running := r.running
	r.mu.RUnlock()
	if !running {
		t.Error("reconciler should be running after Start")
	}

	// Double start should be a no-op
	r.Start(ctx)

	r.Stop()

	r.mu.RLock()
	running = r.running
	r.mu.RUnlock()
	if running {
		t.Error("reconciler should not be running after Stop")
	}

	// Double stop should be a no-op
	r.Stop()
}

func TestWorktreeReconciler_OrphanGracePeriod(t *testing.T) {
	svc := newTestServiceForWorktree()
	config := DefaultWorktreeReconcilerConfig()
	config.OrphanGracePeriod = 1 * time.Millisecond // Very short for testing
	config.DiskScanEnabled = false
	config.DetectUntracked = false

	r := NewWorktreeReconciler(config, svc.worktrees, slog.Default())

	// Create an orphaned worktree that expired a while ago
	pastOrphan := time.Now().Add(-1 * time.Hour)
	svc.worktrees.assns["wt-old"] = &WorktreeAssignment{
		ID:           "wt-old",
		AgentID:      "agent-1",
		WorktreePath: t.TempDir(), // Use temp dir so disk scan doesn't fail
		Branch:       "old-branch",
		Status:       WorktreeStatusOrphaned,
		CreatedAt:    time.Now().Add(-2 * time.Hour),
		OrphanedAt:   &pastOrphan,
	}

	// Create an orphaned worktree that was JUST orphaned (no OrphanedAt yet)
	svc.worktrees.assns["wt-new"] = &WorktreeAssignment{
		ID:           "wt-new",
		AgentID:      "agent-2",
		WorktreePath: t.TempDir(),
		Branch:       "new-branch",
		Status:       WorktreeStatusOrphaned,
		CreatedAt:    time.Now(),
		OrphanedAt:   nil, // No OrphanedAt yet
	}

	// Create an active worktree — should not be touched
	svc.worktrees.assns["wt-active"] = &WorktreeAssignment{
		ID:           "wt-active",
		AgentID:      "agent-3",
		WorktreePath: t.TempDir(),
		Branch:       "active-branch",
		Status:       WorktreeStatusActive,
		CreatedAt:    time.Now(),
	}

	ctx := context.Background()
	stats, err := r.TriggerReconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// wt-old should have been "removed" (but git worktree remove will fail since
	// it's a temp dir, not a real worktree — that's OK, we're testing the logic)
	// wt-new should get an OrphanedAt set but NOT be removed yet
	// wt-active should be untouched

	// Check wt-new got an OrphanedAt timestamp
	if svc.worktrees.assns["wt-new"] != nil && svc.worktrees.assns["wt-new"].OrphanedAt == nil {
		t.Error("wt-new should have OrphanedAt set")
	}

	// wt-active should still be active
	if active, ok := svc.worktrees.assns["wt-active"]; !ok || active.Status != WorktreeStatusActive {
		t.Error("wt-active should still be active")
	}

	// Stats should show the work done
	if stats.Errors > 0 {
		// We expect errors from git worktree remove on temp dirs — that's fine for logic test
		t.Logf("reconciliation had %d errors (expected for non-git temp dirs)", stats.Errors)
	}
}

func TestWorktreeReconciler_TTLExpiration(t *testing.T) {
	svc := newTestServiceForWorktree()
	config := DefaultWorktreeReconcilerConfig()
	config.DiskScanEnabled = false
	config.DetectUntracked = false

	r := NewWorktreeReconciler(config, svc.worktrees, slog.Default())

	// Worktree with TTL that has expired
	svc.worktrees.assns["wt-expired"] = &WorktreeAssignment{
		ID:           "wt-expired",
		AgentID:      "agent-1",
		WorktreePath: t.TempDir(),
		Branch:       "expired-branch",
		Status:       WorktreeStatusActive,
		CreatedAt:    time.Now().Add(-5 * time.Hour),
		TTL:          2, // 2 hours TTL, but created 5 hours ago
	}

	// Worktree with TTL that has NOT expired
	svc.worktrees.assns["wt-valid"] = &WorktreeAssignment{
		ID:           "wt-valid",
		AgentID:      "agent-2",
		WorktreePath: t.TempDir(),
		Branch:       "valid-branch",
		Status:       WorktreeStatusActive,
		CreatedAt:    time.Now(),
		TTL:          24, // 24 hours TTL, just created
	}

	// Worktree with no TTL
	svc.worktrees.assns["wt-no-ttl"] = &WorktreeAssignment{
		ID:           "wt-no-ttl",
		AgentID:      "agent-3",
		WorktreePath: t.TempDir(),
		Branch:       "no-ttl-branch",
		Status:       WorktreeStatusActive,
		CreatedAt:    time.Now().Add(-100 * time.Hour),
		TTL:          0, // No TTL
	}

	ctx := context.Background()
	stats, err := r.TriggerReconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if stats.TTLExpired != 1 {
		t.Errorf("TTLExpired = %d, want 1", stats.TTLExpired)
	}

	// wt-expired should be orphaned
	if svc.worktrees.assns["wt-expired"].Status != WorktreeStatusOrphaned {
		t.Errorf("wt-expired status = %q, want orphaned", svc.worktrees.assns["wt-expired"].Status)
	}
	if svc.worktrees.assns["wt-expired"].OrphanedAt == nil {
		t.Error("wt-expired should have OrphanedAt set")
	}

	// wt-valid should still be active
	if svc.worktrees.assns["wt-valid"].Status != WorktreeStatusActive {
		t.Errorf("wt-valid status = %q, want active", svc.worktrees.assns["wt-valid"].Status)
	}

	// wt-no-ttl should still be active
	if svc.worktrees.assns["wt-no-ttl"].Status != WorktreeStatusActive {
		t.Errorf("wt-no-ttl status = %q, want active", svc.worktrees.assns["wt-no-ttl"].Status)
	}
}

func TestWorktreeReconciler_TTLExpiration_GlobalMaxTTL(t *testing.T) {
	svc := newTestServiceForWorktree()
	config := DefaultWorktreeReconcilerConfig()
	config.DiskScanEnabled = false
	config.DetectUntracked = false
	config.MaxTTLHours = 4 // Global max TTL

	r := NewWorktreeReconciler(config, svc.worktrees, slog.Default())

	// Worktree with no per-assignment TTL, but exceeds global max
	svc.worktrees.assns["wt-global"] = &WorktreeAssignment{
		ID:           "wt-global",
		AgentID:      "agent-1",
		WorktreePath: t.TempDir(),
		Branch:       "global-branch",
		Status:       WorktreeStatusActive,
		CreatedAt:    time.Now().Add(-5 * time.Hour),
		TTL:          0, // No per-assignment TTL
	}

	ctx := context.Background()
	stats, err := r.TriggerReconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if stats.TTLExpired != 1 {
		t.Errorf("TTLExpired = %d, want 1", stats.TTLExpired)
	}

	if svc.worktrees.assns["wt-global"].Status != WorktreeStatusOrphaned {
		t.Errorf("wt-global status = %q, want orphaned", svc.worktrees.assns["wt-global"].Status)
	}
}

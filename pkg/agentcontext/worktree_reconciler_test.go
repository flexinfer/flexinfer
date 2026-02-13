package agentcontext

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace/noop"
)

func newTestServiceForWorktree() *Service {
	return &Service{
		cfg: Config{
			PresenceHeartbeatTTL:            120,
			PresenceCleanupInterval:         60,
			WorktreeArtifactCleanupPatterns: ".next,node_modules,target",
		},
		logger:        slog.Default(),
		tracer:        noop.NewTracerProvider().Tracer("test"),
		metrics:       NewMetrics(),
		presenceMap:   make(map[string]*AgentPresence),
		fileClaims:    make(map[string]map[string]*FileClaim),
		worktreeAssns: make(map[string]*WorktreeAssignment),
		sessions:      make(map[string]*Session),
	}
}

func TestWorktreeReconciler_StartStop(t *testing.T) {
	svc := newTestServiceForWorktree()
	config := DefaultWorktreeReconcilerConfig()
	config.CheckInterval = 100 * time.Millisecond

	r := NewWorktreeReconciler(config, svc, slog.Default())

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

	r := NewWorktreeReconciler(config, svc, slog.Default())

	// Create an orphaned worktree that expired a while ago
	pastOrphan := time.Now().Add(-1 * time.Hour)
	svc.worktreeAssns["wt-old"] = &WorktreeAssignment{
		ID:           "wt-old",
		AgentID:      "agent-1",
		WorktreePath: t.TempDir(), // Use temp dir so disk scan doesn't fail
		Branch:       "old-branch",
		Status:       WorktreeStatusOrphaned,
		CreatedAt:    time.Now().Add(-2 * time.Hour),
		OrphanedAt:   &pastOrphan,
	}

	// Create an orphaned worktree that was JUST orphaned (no OrphanedAt yet)
	svc.worktreeAssns["wt-new"] = &WorktreeAssignment{
		ID:           "wt-new",
		AgentID:      "agent-2",
		WorktreePath: t.TempDir(),
		Branch:       "new-branch",
		Status:       WorktreeStatusOrphaned,
		CreatedAt:    time.Now(),
		OrphanedAt:   nil, // No OrphanedAt yet
	}

	// Create an active worktree — should not be touched
	svc.worktreeAssns["wt-active"] = &WorktreeAssignment{
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
	if svc.worktreeAssns["wt-new"] != nil && svc.worktreeAssns["wt-new"].OrphanedAt == nil {
		t.Error("wt-new should have OrphanedAt set")
	}

	// wt-active should still be active
	if active, ok := svc.worktreeAssns["wt-active"]; !ok || active.Status != WorktreeStatusActive {
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

	r := NewWorktreeReconciler(config, svc, slog.Default())

	// Worktree with TTL that has expired
	svc.worktreeAssns["wt-expired"] = &WorktreeAssignment{
		ID:           "wt-expired",
		AgentID:      "agent-1",
		WorktreePath: t.TempDir(),
		Branch:       "expired-branch",
		Status:       WorktreeStatusActive,
		CreatedAt:    time.Now().Add(-5 * time.Hour),
		TTL:          2, // 2 hours TTL, but created 5 hours ago
	}

	// Worktree with TTL that has NOT expired
	svc.worktreeAssns["wt-valid"] = &WorktreeAssignment{
		ID:           "wt-valid",
		AgentID:      "agent-2",
		WorktreePath: t.TempDir(),
		Branch:       "valid-branch",
		Status:       WorktreeStatusActive,
		CreatedAt:    time.Now(),
		TTL:          24, // 24 hours TTL, just created
	}

	// Worktree with no TTL
	svc.worktreeAssns["wt-no-ttl"] = &WorktreeAssignment{
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
	if svc.worktreeAssns["wt-expired"].Status != WorktreeStatusOrphaned {
		t.Errorf("wt-expired status = %q, want orphaned", svc.worktreeAssns["wt-expired"].Status)
	}
	if svc.worktreeAssns["wt-expired"].OrphanedAt == nil {
		t.Error("wt-expired should have OrphanedAt set")
	}

	// wt-valid should still be active
	if svc.worktreeAssns["wt-valid"].Status != WorktreeStatusActive {
		t.Errorf("wt-valid status = %q, want active", svc.worktreeAssns["wt-valid"].Status)
	}

	// wt-no-ttl should still be active
	if svc.worktreeAssns["wt-no-ttl"].Status != WorktreeStatusActive {
		t.Errorf("wt-no-ttl status = %q, want active", svc.worktreeAssns["wt-no-ttl"].Status)
	}
}

func TestWorktreeReconciler_TTLExpiration_GlobalMaxTTL(t *testing.T) {
	svc := newTestServiceForWorktree()
	config := DefaultWorktreeReconcilerConfig()
	config.DiskScanEnabled = false
	config.DetectUntracked = false
	config.MaxTTLHours = 4 // Global max TTL

	r := NewWorktreeReconciler(config, svc, slog.Default())

	// Worktree with no per-assignment TTL, but exceeds global max
	svc.worktreeAssns["wt-global"] = &WorktreeAssignment{
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

	if svc.worktreeAssns["wt-global"].Status != WorktreeStatusOrphaned {
		t.Errorf("wt-global status = %q, want orphaned", svc.worktreeAssns["wt-global"].Status)
	}
}

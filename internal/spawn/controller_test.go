package spawn

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSpawn(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)

	ctx := context.Background()
	id, err := ctrl.Spawn(ctx, Request{
		AgentType:       "claude-code",
		TaskDescription: "fix the bug",
		Project:         "loom-core",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty spawn ID")
	}

	state, ok := ctrl.Get(id)
	if !ok {
		t.Fatalf("spawn %s not found after Spawn()", id)
	}
	if state.Status != StatusPending {
		t.Errorf("status: got %q, want %q", state.Status, StatusPending)
	}
	if state.AgentID == "" {
		t.Error("expected non-empty agent ID")
	}
	if state.Request.AgentType != "claude-code" {
		t.Errorf("agent type: got %q, want %q", state.Request.AgentType, "claude-code")
	}
}

func TestSpawnValidation(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	tests := []struct {
		name string
		req  Request
	}{
		{
			name: "unsupported agent type",
			req:  Request{AgentType: "gpt-5", TaskDescription: "x", Project: "p"},
		},
		{
			name: "missing task",
			req:  Request{AgentType: "claude-code", Project: "p"},
		},
		{
			name: "missing project",
			req:  Request{AgentType: "claude-code", TaskDescription: "x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ctrl.Spawn(ctx, tt.req)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestSpawnDefaultAgentType(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)

	id, err := ctrl.Spawn(context.Background(), Request{
		TaskDescription: "test task",
		Project:         "test-project",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	state, ok := ctrl.Get(id)
	if !ok {
		t.Fatal("spawn not found")
	}
	if state.Request.AgentType != "claude-code" {
		t.Errorf("default agent type: got %q, want %q", state.Request.AgentType, "claude-code")
	}
}

func TestStop(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	id, err := ctrl.Spawn(ctx, Request{
		AgentType:       "claude-code",
		TaskDescription: "test",
		Project:         "proj",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := ctrl.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	state, ok := ctrl.Get(id)
	if !ok {
		t.Fatal("spawn not found after stop")
	}
	if state.Status != StatusStopped {
		t.Errorf("status: got %q, want %q", state.Status, StatusStopped)
	}
	if state.EndedAt == nil {
		t.Error("expected EndedAt to be set")
	}
}

func TestStopNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)

	err := ctrl.Stop(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent spawn")
	}
}

func TestList(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, err := ctrl.Spawn(ctx, Request{
			AgentType:       "claude-code",
			TaskDescription: "task",
			Project:         "proj",
		})
		if err != nil {
			t.Fatalf("Spawn %d: %v", i, err)
		}
	}

	list := ctrl.List()
	if len(list) != 3 {
		t.Errorf("List: got %d, want 3", len(list))
	}
}

func TestActiveCount(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	id1, _ := ctrl.Spawn(ctx, Request{AgentType: "claude-code", TaskDescription: "t", Project: "p"})
	_, _ = ctrl.Spawn(ctx, Request{AgentType: "claude-code", TaskDescription: "t", Project: "p"})

	if got := ctrl.ActiveCount(); got != 2 {
		t.Errorf("ActiveCount: got %d, want 2", got)
	}

	_ = ctrl.Stop(ctx, id1)

	if got := ctrl.ActiveCount(); got != 1 {
		t.Errorf("ActiveCount after stop: got %d, want 1", got)
	}
}

func TestUpdateState(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	id, _ := ctrl.Spawn(ctx, Request{AgentType: "claude-code", TaskDescription: "t", Project: "p"})
	state, _ := ctrl.Get(id)
	state.Status = StatusRunning
	state.PodName = "spawn-pod-123"
	ctrl.UpdateState(ctx, state)

	updated, ok := ctrl.Get(id)
	if !ok {
		t.Fatal("spawn not found after update")
	}
	if updated.Status != StatusRunning {
		t.Errorf("status: got %q, want %q", updated.Status, StatusRunning)
	}
	if updated.PodName != "spawn-pod-123" {
		t.Errorf("pod name: got %q, want %q", updated.PodName, "spawn-pod-123")
	}
}

func TestSpawnWithStore(t *testing.T) {
	client := fake.NewSimpleClientset()
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctrl := NewK8sController(client, "devbox", store, nil)
	ctx := context.Background()

	id, err := ctrl.Spawn(ctx, Request{
		AgentType:       "claude-code",
		TaskDescription: "test",
		Project:         "proj",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Verify persisted.
	loaded, err := store.Load(ctx, id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected persisted state")
	}
	if loaded.SpawnID != id {
		t.Errorf("persisted SpawnID: got %q, want %q", loaded.SpawnID, id)
	}
}

func makePod(name, spawnID, agentID string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "devbox",
			Labels: map[string]string{
				ManagedByLabel: ManagedByValue,
				SpawnIDLabel:   spawnID,
				AgentIDLabel:   agentID,
			},
		},
		Status: corev1.PodStatus{
			Phase: phase,
		},
	}
}

func TestReconcileUpdatesStatus(t *testing.T) {
	pod := makePod("spawn-pod-1", "spawn-abc123", "agent-1", corev1.PodRunning)
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	// Pre-populate with a pending state.
	ctrl.mu.Lock()
	ctrl.spawns["spawn-abc123"] = &State{
		SpawnID: "spawn-abc123",
		AgentID: "agent-1",
		Status:  StatusPending,
	}
	ctrl.mu.Unlock()

	if err := ctrl.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	state, ok := ctrl.Get("spawn-abc123")
	if !ok {
		t.Fatal("spawn not found after reconcile")
	}
	if state.Status != StatusRunning {
		t.Errorf("status: got %q, want %q", state.Status, StatusRunning)
	}
	if state.PodName != "spawn-pod-1" {
		t.Errorf("pod name: got %q, want %q", state.PodName, "spawn-pod-1")
	}
}

func TestReconcileMarksMissingPodAsFailed(t *testing.T) {
	// No pods in the cluster.
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	ctrl.mu.Lock()
	ctrl.spawns["spawn-gone"] = &State{
		SpawnID: "spawn-gone",
		Status:  StatusRunning,
		PodName: "spawn-pod-gone",
	}
	ctrl.mu.Unlock()

	if err := ctrl.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	state, ok := ctrl.Get("spawn-gone")
	if !ok {
		t.Fatal("spawn not found")
	}
	if state.Status != StatusFailed {
		t.Errorf("status: got %q, want %q", state.Status, StatusFailed)
	}
	if state.Error == "" {
		t.Error("expected error message for missing pod")
	}
	if state.EndedAt == nil {
		t.Error("expected EndedAt to be set")
	}
}

func TestReconcileKeepsPreRuntimeSpawnWithoutPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	ctrl := NewK8sController(client, "devbox", nil, nil)
	ctx := context.Background()

	ctrl.mu.Lock()
	ctrl.spawns["spawn-building"] = &State{
		SpawnID: "spawn-building",
		Status:  StatusBuilding,
	}
	ctrl.mu.Unlock()

	if err := ctrl.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	state, ok := ctrl.Get("spawn-building")
	if !ok {
		t.Fatal("spawn not found")
	}
	if state.Status != StatusBuilding {
		t.Errorf("status: got %q, want %q", state.Status, StatusBuilding)
	}
	if state.Error != "" {
		t.Errorf("error: got %q, want empty", state.Error)
	}
	if state.EndedAt != nil {
		t.Error("expected EndedAt to remain nil")
	}
}

func TestReconcileDiscoversUntrackedPods(t *testing.T) {
	pod := makePod("spawn-pod-new", "spawn-new123", "agent-new", corev1.PodRunning)
	pod.Labels["loom.dev/agent-type"] = "codex"
	pod.Labels["loom.dev/project"] = "test-proj"
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	ctrl := NewK8sController(client, "devbox", nil, nil)

	if err := ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	state, ok := ctrl.Get("spawn-new123")
	if !ok {
		t.Fatal("untracked pod not discovered")
	}
	if state.Status != StatusRunning {
		t.Errorf("status: got %q, want %q", state.Status, StatusRunning)
	}
	if state.AgentID != "agent-new" {
		t.Errorf("agent ID: got %q, want %q", state.AgentID, "agent-new")
	}
	if state.Request.AgentType != "codex" {
		t.Errorf("agent type: got %q, want %q", state.Request.AgentType, "codex")
	}
	if state.Request.Project != "test-proj" {
		t.Errorf("project: got %q, want %q", state.Request.Project, "test-proj")
	}
}

func TestReconcilePreservesTerminalState(t *testing.T) {
	// Pod succeeded, but the state was already marked failed.
	pod := makePod("spawn-pod-x", "spawn-term", "agent-x", corev1.PodSucceeded)
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	ctrl := NewK8sController(client, "devbox", nil, nil)

	ctrl.mu.Lock()
	ctrl.spawns["spawn-term"] = &State{
		SpawnID: "spawn-term",
		Status:  StatusFailed,
		Error:   "manually failed",
	}
	ctrl.mu.Unlock()

	if err := ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	state, _ := ctrl.Get("spawn-term")
	if state.Status != StatusFailed {
		t.Errorf("terminal state was overwritten: got %q, want %q", state.Status, StatusFailed)
	}
}

func TestReconcileNilClient(t *testing.T) {
	// Controller with nil K8s client should skip reconciliation.
	ctrl := NewK8sController(nil, "", nil, nil)
	ctx := context.Background()

	// Pre-populate a running spawn.
	ctrl.mu.Lock()
	ctrl.spawns["spawn-nil"] = &State{
		SpawnID: "spawn-nil",
		Status:  StatusRunning,
	}
	ctrl.mu.Unlock()

	// Reconcile should not error and should not modify state.
	if err := ctrl.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile with nil client: %v", err)
	}

	state, ok := ctrl.Get("spawn-nil")
	if !ok {
		t.Fatal("spawn-nil not found")
	}
	if state.Status != StatusRunning {
		t.Errorf("status should be unchanged: got %q, want %q", state.Status, StatusRunning)
	}
}

func TestSetK8sClient(t *testing.T) {
	ctrl := NewK8sController(nil, "", nil, nil)

	// Initially nil client, reconcile is a no-op.
	if err := ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile with nil client: %v", err)
	}

	// Inject a fake client.
	pod := makePod("spawn-pod-set", "spawn-set", "agent-set", corev1.PodRunning)
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	ctrl.SetK8sClient(client, "devbox")

	// Now reconcile should discover the pod.
	if err := ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile after SetK8sClient: %v", err)
	}

	state, ok := ctrl.Get("spawn-set")
	if !ok {
		t.Fatal("spawn-set not discovered after SetK8sClient")
	}
	if state.Status != StatusRunning {
		t.Errorf("status: got %q, want %q", state.Status, StatusRunning)
	}
}

func TestNewSpawnID(t *testing.T) {
	id1 := NewSpawnID()
	id2 := NewSpawnID()
	if id1 == id2 {
		t.Error("expected unique spawn IDs")
	}
	if len(id1) < 10 {
		t.Errorf("spawn ID too short: %q", id1)
	}
}

// TestReconcileFiresTerminalHookOnNewlyFailedSpawn verifies the cleanup
// hook is invoked when Reconcile transitions a spawn into a terminal
// state from observing the pod's Failed phase. The hook receives the
// state by value so concurrent reconciles do not race the caller.
func TestReconcileFiresTerminalHookOnNewlyFailedSpawn(t *testing.T) {
	pod := makePod("spawn-pod-fail", "spawn-fail", "agent-fail", corev1.PodFailed)
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	ctrl := NewK8sController(client, "devbox", nil, nil)

	ctrl.mu.Lock()
	ctrl.spawns["spawn-fail"] = &State{
		SpawnID: "spawn-fail",
		AgentID: "agent-fail",
		Status:  StatusRunning,
	}
	ctrl.mu.Unlock()

	var hookCalls []State
	ctrl.SetTerminalHook(func(_ context.Context, st State) {
		hookCalls = append(hookCalls, st)
	})

	if err := ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if len(hookCalls) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(hookCalls))
	}
	if hookCalls[0].SpawnID != "spawn-fail" || hookCalls[0].AgentID != "agent-fail" {
		t.Fatalf("hook state mismatch: %+v", hookCalls[0])
	}
	state, _ := ctrl.Get("spawn-fail")
	if state.CleanupAt == nil {
		t.Fatal("CleanupAt should be stamped after hook fires")
	}
}

// TestReconcileTerminalHookIsIdempotent ensures CleanupAt gates the
// hook so a long-lived terminal state does not re-trigger cleanup on
// every reconcile tick — the symptom that filled namespace quota
// with stale spawn pods before the reaper landed.
func TestReconcileTerminalHookIsIdempotent(t *testing.T) {
	// Pod is still alive in the cluster even though the spawn's
	// state went terminal — exactly the orphan path that motivated
	// the reaper.
	pod := makePod("spawn-pod-orphan", "spawn-orphan", "agent-orphan", corev1.PodRunning)
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	ctrl := NewK8sController(client, "devbox", nil, nil)

	ctrl.mu.Lock()
	ctrl.spawns["spawn-orphan"] = &State{
		SpawnID: "spawn-orphan",
		AgentID: "agent-orphan",
		Status:  StatusFailed,
		PodName: "spawn-pod-orphan",
	}
	ctrl.mu.Unlock()

	var fires int
	ctrl.SetTerminalHook(func(_ context.Context, _ State) { fires++ })

	for range 3 {
		if err := ctrl.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	}

	if fires != 1 {
		t.Fatalf("hook fired %d times across 3 reconciles, want 1 (CleanupAt should gate)", fires)
	}
}

// TestReconcileTerminalHookFiresForDiscoveredOrphan covers the
// operator-restart recovery path: a pod observed for the first time
// already in PodFailed must trigger cleanup so it doesn't linger.
func TestReconcileTerminalHookFiresForDiscoveredOrphan(t *testing.T) {
	pod := makePod("spawn-pod-found", "spawn-found", "agent-found", corev1.PodFailed)
	client := fake.NewSimpleClientset([]runtime.Object{pod}...)
	ctrl := NewK8sController(client, "devbox", nil, nil)

	var fires int
	ctrl.SetTerminalHook(func(_ context.Context, _ State) { fires++ })

	if err := ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if fires != 1 {
		t.Fatalf("hook fired %d times for newly-discovered failed pod, want 1", fires)
	}
}

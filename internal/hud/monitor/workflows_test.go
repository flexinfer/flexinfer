package monitor

import (
	"sync"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestWorkflowMonitor_OnNewApprovalFiresOncePerTransition(t *testing.T) {
	m := NewWorkflowMonitor(nil, nil)

	var (
		mu        sync.Mutex
		fired     []bridge.WorkflowInfo
		callCount int
	)
	m.OnNewApproval(func(workflows []bridge.WorkflowInfo) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
		fired = append(fired, workflows...)
	})

	// First refresh: one workflow running, one waiting. Only the waiting
	// workflow should fire the callback.
	m.Update([]bridge.WorkflowInfo{
		{ID: "wf-1", Name: "First", Status: "running", CurrentStep: "step-a"},
		{ID: "wf-2", Name: "Second", Status: "waiting_approval", CurrentStep: "review"},
	})

	mu.Lock()
	if callCount != 1 {
		mu.Unlock()
		t.Fatalf("expected 1 callback invocation, got %d", callCount)
	}
	if len(fired) != 1 || fired[0].ID != "wf-2" {
		mu.Unlock()
		t.Fatalf("expected single fired workflow wf-2, got %#v", fired)
	}
	mu.Unlock()

	// Second refresh with same waiting state: callback must not re-fire.
	m.Update([]bridge.WorkflowInfo{
		{ID: "wf-1", Name: "First", Status: "running", CurrentStep: "step-a"},
		{ID: "wf-2", Name: "Second", Status: "waiting_approval", CurrentStep: "review"},
	})

	mu.Lock()
	if callCount != 1 {
		mu.Unlock()
		t.Fatalf("expected dedup on unchanged waiting state, callCount=%d", callCount)
	}
	mu.Unlock()

	// Third refresh: wf-2 step advances to a new waiting step — new transition
	// key, should fire again.
	m.Update([]bridge.WorkflowInfo{
		{ID: "wf-1", Name: "First", Status: "running", CurrentStep: "step-a"},
		{ID: "wf-2", Name: "Second", Status: "waiting_approval", CurrentStep: "deploy"},
	})

	mu.Lock()
	if callCount != 2 {
		mu.Unlock()
		t.Fatalf("expected new step to fire callback, callCount=%d", callCount)
	}
	if fired[1].CurrentStep != "deploy" {
		mu.Unlock()
		t.Fatalf("expected second fire on step=deploy, got %s", fired[1].CurrentStep)
	}
	mu.Unlock()
}

func TestWorkflowMonitor_OnNewApprovalRefiresAfterResolution(t *testing.T) {
	m := NewWorkflowMonitor(nil, nil)

	var (
		mu        sync.Mutex
		callCount int
	)
	m.OnNewApproval(func(_ []bridge.WorkflowInfo) {
		mu.Lock()
		defer mu.Unlock()
		callCount++
	})

	// Transition into waiting_approval.
	m.Update([]bridge.WorkflowInfo{
		{ID: "wf-1", Status: "waiting_approval", CurrentStep: "review"},
	})
	// Resolve (approved).
	m.Update([]bridge.WorkflowInfo{
		{ID: "wf-1", Status: "running", CurrentStep: "deploy"},
	})
	// Re-enter waiting_approval on the same step — should fire again because
	// the prior entry was pruned when it left the waiting state.
	m.Update([]bridge.WorkflowInfo{
		{ID: "wf-1", Status: "waiting_approval", CurrentStep: "review"},
	})

	mu.Lock()
	defer mu.Unlock()
	if callCount != 2 {
		t.Fatalf("expected 2 callbacks (initial + re-entry), got %d", callCount)
	}
}

func TestWorkflowMonitor_OnNewApprovalNoCallbackWhenNoneWaiting(t *testing.T) {
	m := NewWorkflowMonitor(nil, nil)

	var called bool
	m.OnNewApproval(func(_ []bridge.WorkflowInfo) {
		called = true
	})

	m.Update([]bridge.WorkflowInfo{
		{ID: "wf-1", Status: "running", CurrentStep: "step-a"},
		{ID: "wf-2", Status: "completed", CurrentStep: "done"},
	})

	if called {
		t.Fatal("expected no callback when no workflows are waiting_approval")
	}
}

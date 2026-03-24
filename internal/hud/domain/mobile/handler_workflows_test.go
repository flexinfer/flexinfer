package mobile

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestBuildMobileWorkflowsResponse_DeprecatesApprovalSurface(t *testing.T) {
	workflows := []bridge.WorkflowInfo{
		{
			ID:          "wf-active",
			Name:        "Build and test",
			Status:      "running",
			CurrentStep: "build",
			Progress:    0.5,
			CreatedAt:   "2026-03-23T19:59:00Z",
		},
		{
			ID:          "wf-approval",
			Name:        "Release gate",
			Status:      "waiting_approval",
			CurrentStep: "review",
			Progress:    0.2,
			CreatedAt:   "2026-03-23T20:00:00Z",
		},
	}

	resp := buildMobileWorkflowsResponse(workflows, 50, "", "", nil)
	if !resp.Deprecated {
		t.Fatal("expected workflows surface to be marked deprecated")
	}
	if resp.DeprecationMessage == "" {
		t.Fatal("expected a deprecation message")
	}
	if resp.PendingApprovals != 0 {
		t.Fatalf("expected active pending approvals to be de-emphasized, got %d", resp.PendingApprovals)
	}
	if resp.DeprecatedPendingApprovals != 1 {
		t.Fatalf("expected one deprecated approval, got %d", resp.DeprecatedPendingApprovals)
	}
	if resp.ActiveWorkflows != 1 {
		t.Fatalf("expected one active workflow, got %d", resp.ActiveWorkflows)
	}
	if len(resp.Workflows) != 2 {
		t.Fatalf("expected two workflow entries, got %#v", resp.Workflows)
	}

	var approval mobileWorkflowListItemDTO
	for _, workflow := range resp.Workflows {
		if workflow.ID == "wf-approval" {
			approval = workflow
			break
		}
	}
	if approval.ID != "wf-approval" {
		t.Fatalf("expected approval workflow to be present, got %#v", resp.Workflows)
	}
	if !approval.Deprecated {
		t.Fatalf("expected approval workflow to be marked deprecated, got %#v", approval)
	}
	if approval.DeprecationMessage == "" {
		t.Fatalf("expected approval workflow deprecation message, got %#v", approval)
	}
}

func TestBuildMobileWorkflowDetailResponse_DeprecatesResponse(t *testing.T) {
	detail := &bridge.WorkflowDetail{
		ID:          "wf-approval",
		Name:        "Release gate",
		Status:      "waiting_approval",
		CurrentStep: "review",
		Progress:    0.2,
		CreatedAt:   "2026-03-23T20:00:00Z",
		StartedAt:   "2026-03-23T20:01:00Z",
		CompletedAt: "",
		Error:       "",
		Steps: []bridge.WorkflowStep{
			{ID: "review", Name: "Review", Status: "waiting_approval"},
		},
		Events: []bridge.WorkflowEvent{
			{ID: "evt-1", EventType: "step.waiting", Timestamp: "2026-03-23T20:01:30Z", StepID: "review", Details: map[string]any{"message": "waiting on approval"}},
		},
	}

	resp := buildMobileWorkflowDetailResponse(detail)
	if !resp.Deprecated || resp.DeprecationMessage == "" {
		t.Fatalf("expected deprecated detail response, got %#v", resp)
	}
	if !resp.Workflow.Deprecated || resp.Workflow.DeprecationMessage == "" {
		t.Fatalf("expected workflow payload to carry deprecation metadata, got %#v", resp.Workflow)
	}
	if len(resp.Events) != 1 {
		t.Fatalf("expected one workflow event, got %#v", resp.Events)
	}
	if resp.Events[0].StepName != "Review" {
		t.Fatalf("expected step name to be resolved, got %#v", resp.Events[0])
	}

	mutation := mobileWorkflowMutationResponse("wf-approval", "review", "approved")
	if !mutation.Deprecated || mutation.DeprecationMessage == "" {
		t.Fatalf("expected deprecated mutation response, got %#v", mutation)
	}
	if mutation.Action != "approved" || mutation.WorkflowID != "wf-approval" {
		t.Fatalf("unexpected mutation payload: %#v", mutation)
	}
}

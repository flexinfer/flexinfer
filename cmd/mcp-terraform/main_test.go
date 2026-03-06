package main

import "testing"

func TestFormatWorkspace(t *testing.T) {
	input := map[string]any{
		"id": "ws-abc123",
		"attributes": map[string]any{
			"name":              "production",
			"description":       "prod workspace",
			"terraform-version": "1.5.0",
			"auto-apply":        true,
			"execution-mode":    "remote",
			"working-directory": "/infra",
			"locked":            false,
			"resource-count":    float64(42),
			"updated-at":        "2025-01-01T00:00:00Z",
		},
	}

	result := formatWorkspace(input)

	if result["id"] != "ws-abc123" {
		t.Errorf("expected id ws-abc123, got %v", result["id"])
	}
	if result["name"] != "production" {
		t.Errorf("expected name production, got %v", result["name"])
	}
	if result["terraform_version"] != "1.5.0" {
		t.Errorf("expected terraform_version 1.5.0, got %v", result["terraform_version"])
	}
	if result["auto_apply"] != true {
		t.Errorf("expected auto_apply true, got %v", result["auto_apply"])
	}
}

func TestFormatWorkspaceDetailed(t *testing.T) {
	input := map[string]any{
		"id": "ws-abc123",
		"attributes": map[string]any{
			"name":                  "staging",
			"description":           "staging workspace",
			"terraform-version":     "1.5.0",
			"auto-apply":            false,
			"execution-mode":        "remote",
			"working-directory":     "",
			"locked":                false,
			"resource-count":        float64(10),
			"updated-at":            "2025-06-01T00:00:00Z",
			"created-at":            "2025-01-01T00:00:00Z",
			"environment":           "staging",
			"file-triggers-enabled": true,
			"speculative-enabled":   true,
			"queue-all-runs":        false,
		},
	}

	result := formatWorkspaceDetailed(input)

	if result["name"] != "staging" {
		t.Errorf("expected name staging, got %v", result["name"])
	}
	if result["created_at"] != "2025-01-01T00:00:00Z" {
		t.Errorf("expected created_at, got %v", result["created_at"])
	}
	if result["environment"] != "staging" {
		t.Errorf("expected environment staging, got %v", result["environment"])
	}
}

func TestFormatStateVersion(t *testing.T) {
	input := map[string]any{
		"id": "sv-xyz789",
		"attributes": map[string]any{
			"created-at":          "2025-03-01T00:00:00Z",
			"serial":              float64(5),
			"state-version":       "4",
			"terraform-version":   "1.5.0",
			"resources-processed": float64(42),
		},
	}

	result := formatStateVersion(input)

	if result["id"] != "sv-xyz789" {
		t.Errorf("expected id sv-xyz789, got %v", result["id"])
	}
	if result["serial"] != float64(5) {
		t.Errorf("expected serial 5, got %v", result["serial"])
	}
}

func TestFormatRun(t *testing.T) {
	input := map[string]any{
		"id": "run-abc123",
		"attributes": map[string]any{
			"status":      "applied",
			"message":     "Apply complete",
			"source":      "tfe-ui",
			"created-at":  "2025-03-01T00:00:00Z",
			"has-changes": true,
			"is-destroy":  false,
			"auto-apply":  true,
		},
	}

	result := formatRun(input)

	if result["id"] != "run-abc123" {
		t.Errorf("expected id run-abc123, got %v", result["id"])
	}
	if result["status"] != "applied" {
		t.Errorf("expected status applied, got %v", result["status"])
	}
	if result["has_changes"] != true {
		t.Errorf("expected has_changes true, got %v", result["has_changes"])
	}
}

func TestFormatRunDetailed(t *testing.T) {
	input := map[string]any{
		"id": "run-abc123",
		"attributes": map[string]any{
			"status":         "applied",
			"message":        "Apply complete",
			"source":         "tfe-api",
			"created-at":     "2025-03-01T00:00:00Z",
			"has-changes":    true,
			"is-destroy":     false,
			"auto-apply":     true,
			"trigger-reason": "manual",
			"refresh":        true,
			"refresh-only":   false,
			"plan-only":      false,
			"status-timestamps": map[string]any{
				"planned-at": "2025-03-01T00:01:00Z",
				"applied-at": "2025-03-01T00:02:00Z",
			},
		},
	}

	result := formatRunDetailed(input)

	if result["trigger_reason"] != "manual" {
		t.Errorf("expected trigger_reason manual, got %v", result["trigger_reason"])
	}
	if result["timestamps"] == nil {
		t.Error("expected timestamps to be present")
	}
}

func TestToolDefinitions(t *testing.T) {
	expectedTools := []string{
		"tf_list_workspaces",
		"tf_get_workspace",
		"tf_current_state",
		"tf_state_resources",
		"tf_state_outputs",
		"tf_list_runs",
		"tf_get_run",
		"tf_run_plan",
		"tf_list_variables",
		"tf_list_varsets",
		"tf_get_varset",
		"tf_get_organization",
		"tf_list_policies",
		"tf_list_modules",
	}

	for _, name := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
	}

	if len(expectedTools) != 14 {
		t.Errorf("expected 14 tools, got %d", len(expectedTools))
	}
}

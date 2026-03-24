package alerting

import (
	"log/slog"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestEvaluate_PipelineFailed(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	pipelines := []bridge.PipelineInfo{
		{ID: 100, Project: "services/loom-core", Ref: "main", Status: "failed", WebURL: "https://example.com/100"},
	}

	engine.Evaluate(pipelines)

	alerts := engine.ListAlerts(10)
	if len(alerts) == 0 {
		t.Fatal("expected at least one alert for failed pipeline")
	}

	found := false
	for _, a := range alerts {
		if a.RuleID == "pipeline-failed" {
			found = true
			if a.Severity != "warning" {
				t.Errorf("expected severity=warning, got %s", a.Severity)
			}
			if a.Pipeline.ID != 100 {
				t.Errorf("expected pipeline ID=100, got %d", a.Pipeline.ID)
			}
			if a.Pipeline.Project != "services/loom-core" {
				t.Errorf("expected project=services/loom-core, got %s", a.Pipeline.Project)
			}
		}
	}
	if !found {
		t.Error("expected to find a pipeline-failed alert")
	}
}

func TestEvaluate_CooldownEnforced(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	pipelines := []bridge.PipelineInfo{
		{ID: 100, Project: "services/loom-core", Ref: "main", Status: "failed"},
	}

	// First evaluation fires.
	engine.Evaluate(pipelines)
	alerts1 := engine.ListAlerts(10)
	count1 := len(alerts1)
	if count1 == 0 {
		t.Fatal("expected alerts after first evaluation")
	}

	// Second evaluation within cooldown should not fire additional alerts.
	engine.Evaluate(pipelines)
	alerts2 := engine.ListAlerts(10)
	if len(alerts2) != count1 {
		t.Errorf("expected %d alerts (cooldown), got %d", count1, len(alerts2))
	}
}

func TestEvaluate_ConsecutiveFailures(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	// Set cooldown to 0 for testing.
	engine.mu.Lock()
	for i := range engine.rules {
		engine.rules[i].Cooldown = 0
	}
	engine.mu.Unlock()

	ref := "feat/test"
	project := "services/app"

	// Simulate 3 consecutive failures.
	for i := 0; i < 3; i++ {
		pipelines := []bridge.PipelineInfo{
			{ID: 200 + i, Project: project, Ref: ref, Status: "failed"},
		}
		engine.Evaluate(pipelines)
	}

	alerts := engine.ListAlerts(50)
	foundConsecutive := false
	for _, a := range alerts {
		if a.RuleID == "consecutive-failures" {
			foundConsecutive = true
			if a.Severity != "critical" {
				t.Errorf("expected severity=critical, got %s", a.Severity)
			}
		}
	}
	if !foundConsecutive {
		t.Error("expected a consecutive-failures alert after 3 failures")
	}
}

func TestEvaluate_ConsecutiveFailures_ResetOnSuccess(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	// Set cooldown to 0 for testing.
	engine.mu.Lock()
	for i := range engine.rules {
		engine.rules[i].Cooldown = 0
	}
	engine.mu.Unlock()

	ref := "feat/test"
	project := "services/app"

	// 2 failures.
	for i := 0; i < 2; i++ {
		engine.Evaluate([]bridge.PipelineInfo{
			{ID: 300 + i, Project: project, Ref: ref, Status: "failed"},
		})
	}

	// Success resets counter.
	engine.Evaluate([]bridge.PipelineInfo{
		{ID: 310, Project: project, Ref: ref, Status: "success"},
	})

	// 2 more failures (should not trigger threshold of 3).
	for i := 0; i < 2; i++ {
		engine.Evaluate([]bridge.PipelineInfo{
			{ID: 320 + i, Project: project, Ref: ref, Status: "failed"},
		})
	}

	alerts := engine.ListAlerts(50)
	for _, a := range alerts {
		if a.RuleID == "consecutive-failures" {
			t.Error("should not have consecutive-failures alert after success reset")
		}
	}
}

func TestEvaluate_PipelineStuck(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	// Set cooldown to 0 and stuck duration to 1ms for fast testing.
	engine.mu.Lock()
	for i := range engine.rules {
		engine.rules[i].Cooldown = 0
		if engine.rules[i].ID == "pipeline-stuck" {
			engine.rules[i].Condition.Duration = 1 * time.Millisecond
		}
	}
	engine.mu.Unlock()

	pipelines := []bridge.PipelineInfo{
		{ID: 400, Project: "services/app", Ref: "main", Status: "running"},
	}

	// First evaluation: track start time.
	engine.Evaluate(pipelines)

	// Wait just past the threshold.
	time.Sleep(5 * time.Millisecond)

	// Second evaluation: should detect stuck.
	engine.Evaluate(pipelines)

	alerts := engine.ListAlerts(50)
	foundStuck := false
	for _, a := range alerts {
		if a.RuleID == "pipeline-stuck" {
			foundStuck = true
		}
	}
	if !foundStuck {
		t.Error("expected a pipeline-stuck alert")
	}
}

func TestAckAlert(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	engine.Evaluate([]bridge.PipelineInfo{
		{ID: 500, Project: "services/app", Ref: "main", Status: "failed"},
	})

	alerts := engine.ListAlerts(10)
	if len(alerts) == 0 {
		t.Fatal("expected alerts")
	}

	alertID := alerts[0].ID
	if err := engine.AckAlert(alertID, "test-user"); err != nil {
		t.Fatalf("ack failed: %v", err)
	}

	// Verify ack.
	alerts = engine.ListAlerts(10)
	for _, a := range alerts {
		if a.ID == alertID {
			if a.AckedAt == nil {
				t.Error("expected AckedAt to be set")
			}
			if a.AckedBy != "test-user" {
				t.Errorf("expected AckedBy=test-user, got %s", a.AckedBy)
			}
		}
	}

	// Ack non-existent alert.
	if err := engine.AckAlert("nonexistent", "user"); err == nil {
		t.Error("expected error for nonexistent alert")
	}
}

func TestAddRemoveRule(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	initialCount := len(engine.ListRules())

	engine.AddRule(AlertRule{
		ID:       "custom-rule",
		Name:     "Custom Rule",
		Enabled:  true,
		Severity: "info",
		Condition: AlertCondition{
			Type: "pipeline_failed",
		},
		Cooldown: time.Minute,
	})

	if got := len(engine.ListRules()); got != initialCount+1 {
		t.Errorf("expected %d rules after add, got %d", initialCount+1, got)
	}

	// Adding same ID replaces.
	engine.AddRule(AlertRule{
		ID:       "custom-rule",
		Name:     "Custom Rule v2",
		Enabled:  false,
		Severity: "critical",
		Condition: AlertCondition{
			Type: "pipeline_failed",
		},
	})

	if got := len(engine.ListRules()); got != initialCount+1 {
		t.Errorf("expected %d rules after replace, got %d", initialCount+1, got)
	}

	engine.RemoveRule("custom-rule")
	if got := len(engine.ListRules()); got != initialCount {
		t.Errorf("expected %d rules after remove, got %d", initialCount, got)
	}
}

func TestListAlerts_Limit(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	// Set cooldown to 0 for testing.
	engine.mu.Lock()
	for i := range engine.rules {
		engine.rules[i].Cooldown = 0
	}
	engine.mu.Unlock()

	// Generate multiple alerts.
	for i := 0; i < 10; i++ {
		engine.Evaluate([]bridge.PipelineInfo{
			{ID: 600 + i, Project: "services/app", Ref: "main", Status: "failed"},
		})
	}

	all := engine.ListAlerts(0)
	if len(all) == 0 {
		t.Fatal("expected alerts")
	}

	limited := engine.ListAlerts(3)
	if len(limited) > 3 {
		t.Errorf("expected at most 3 alerts, got %d", len(limited))
	}

	// Verify newest-first ordering.
	if len(limited) >= 2 {
		if limited[0].FiredAt.Before(limited[1].FiredAt) {
			t.Error("expected newest-first ordering")
		}
	}
}

func TestMatchesProject(t *testing.T) {
	tests := []struct {
		filter  []string
		project string
		want    bool
	}{
		{nil, "any-project", true},
		{[]string{}, "any-project", true},
		{[]string{"services/app"}, "services/app", true},
		{[]string{"services/app"}, "services/other", false},
		{[]string{"a", "b", "c"}, "b", true},
	}

	for _, tt := range tests {
		got := matchesProject(tt.filter, tt.project)
		if got != tt.want {
			t.Errorf("matchesProject(%v, %q) = %v, want %v", tt.filter, tt.project, got, tt.want)
		}
	}
}

func TestNormalizePipelineStatus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"running", "running"},
		{"success", "success"},
		{"passed", "success"},
		{"pending", "pending"},
		{"created", "pending"},
		{"failed", "failed"},
		{"canceled", "failed"},
		{"cancelled", "failed"},
		{"unknown", "failed"},
	}

	for _, tt := range tests {
		got := normalizePipelineStatus(tt.input)
		if got != tt.want {
			t.Errorf("normalizePipelineStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDisabledRule_NotFired(t *testing.T) {
	engine := NewAlertEngine(nil, slog.Default())

	// Disable all rules.
	engine.mu.Lock()
	for i := range engine.rules {
		engine.rules[i].Enabled = false
	}
	engine.mu.Unlock()

	engine.Evaluate([]bridge.PipelineInfo{
		{ID: 700, Project: "services/app", Ref: "main", Status: "failed"},
	})

	alerts := engine.ListAlerts(10)
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts with all rules disabled, got %d", len(alerts))
	}
}

package hive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fixtureV1 = `
version: 1
budgets:
  council:
    max_usd_per_run: 15
    max_usd_per_day: 50
  pipeline:
    max_usd_per_run: 5
    max_usd_per_day: 75
    max_concurrent_runs: 4
    max_runs_per_day: 20
council:
  schedule_cron: "0 5 * * *"
  triggers:
    on_roadmap_change: true
    on_incident: true
    on_merge_drift_hours: 48
  ensemble:
    editor: { model: claude-opus-4-7, backend: claude-code }
    reviewers:
      - { name: security,    model: gpt-5-codex,       backend: codex }
      - { name: tech-debt,   model: qwen3.5-9b,        backend: flexinfer }
      - { name: user-impact, model: claude-sonnet-4-6, backend: claude-code }
  artifacts_branch: "council/{date}"
  artifacts_merge_strategy: "fast-merge-loom-only"
pipeline:
  default_template: hive-default-pipeline
  per_label_overrides:
    - { label: docs,     auto_merge: true,  human_review: false }
    - { label: debt,     auto_merge: true,  human_review: false }
    - { label: security, auto_merge: false, human_review: true  }
  protected_paths:
    - "platform/gitops/**"
    - "cmd/loomd/**"
    - "**/*auth*.go"
  retry:
    max_attempts: 3
    cooldown_seconds: 300
human_handoff:
  on_escalation_create_handoff: true
  on_escalation_create_issue:   true
  notify_agent_id: "claude-code"
`

func TestParsePolicy_Valid(t *testing.T) {
	p, err := ParsePolicy([]byte(fixtureV1))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Version != 1 {
		t.Errorf("version: %d", p.Version)
	}
	if !p.IsEnabled() {
		t.Errorf("expected default-enabled when 'enabled' is omitted")
	}
	if p.Budgets.Pipeline.MaxConcurrentRuns != 4 {
		t.Errorf("concurrent: %d", p.Budgets.Pipeline.MaxConcurrentRuns)
	}
	if len(p.Council.Ensemble.Reviewers) != 3 {
		t.Errorf("reviewers: %d", len(p.Council.Ensemble.Reviewers))
	}
	if p.Pipeline.Retry.CooldownDuration() != 5*time.Minute {
		t.Errorf("cooldown: %v", p.Pipeline.Retry.CooldownDuration())
	}
}

func TestPolicy_Validate_Errors(t *testing.T) {
	cases := []struct {
		name  string
		patch func(*Policy)
		want  string
	}{
		{
			name:  "version mismatch",
			patch: func(p *Policy) { p.Version = 99 },
			want:  "unsupported policy version",
		},
		{
			name:  "negative budget",
			patch: func(p *Policy) { p.Budgets.Council.MaxUSDPerRun = -1 },
			want:  "max_usd_per_run must be >= 0",
		},
		{
			name:  "per-run > per-day",
			patch: func(p *Policy) { p.Budgets.Pipeline.MaxUSDPerRun = 200 },
			want:  "exceeds max_usd_per_day",
		},
		{
			name:  "bad merge strategy",
			patch: func(p *Policy) { p.Council.ArtifactsMergeStrategy = "yolo" },
			want:  "must be 'fast-merge-loom-only' or 'always-mr'",
		},
		{
			name:  "unlabeled override",
			patch: func(p *Policy) { p.Pipeline.PerLabelOverrides[0].Label = "" },
			want:  "label is empty",
		},
		{
			name:  "reviewer missing backend",
			patch: func(p *Policy) { p.Council.Ensemble.Reviewers[0].Backend = "" },
			want:  "requires both model and backend",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParsePolicy([]byte(fixtureV1))
			if err != nil {
				t.Fatalf("setup parse: %v", err)
			}
			tc.patch(p)
			err = p.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestPolicy_LabelOverrideFor(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))

	if ov, ok := p.LabelOverrideFor([]string{"docs", "frontend"}); !ok || !ov.AutoMerge {
		t.Errorf("docs auto_merge: ov=%+v ok=%v", ov, ok)
	}
	if ov, ok := p.LabelOverrideFor([]string{"security"}); !ok || ov.AutoMerge || !ov.HumanReview {
		t.Errorf("security override: %+v ok=%v", ov, ok)
	}
	if _, ok := p.LabelOverrideFor([]string{"random-label"}); ok {
		t.Errorf("expected no match for random-label")
	}
	// Mixed-case label still matches.
	if _, ok := p.LabelOverrideFor([]string{"  Debt  "}); !ok {
		t.Errorf("normalised lookup failed")
	}
}

func TestPolicy_ProtectedPathsHit(t *testing.T) {
	p, _ := ParsePolicy([]byte(fixtureV1))
	hits := p.ProtectedPathsHit([]string{
		"platform/gitops/k3s/foo.yaml",
		"internal/hud/spawn.go",
		"cmd/loomd/main.go",
		"pkg/auth/middleware_auth.go",
	})
	want := map[string]bool{
		"platform/gitops/k3s/foo.yaml": true,
		"cmd/loomd/main.go":            true,
		"pkg/auth/middleware_auth.go":  true, // matches **/*auth*.go
	}
	if len(hits) != len(want) {
		t.Errorf("hits: got %v want %v", hits, want)
	}
	for _, h := range hits {
		if !want[h] {
			t.Errorf("unexpected hit %q", h)
		}
	}
}

func TestPolicy_KillSwitch(t *testing.T) {
	p, err := ParsePolicy([]byte(`version: 1
enabled: false
budgets:
  council:  { max_usd_per_run: 1, max_usd_per_day: 1 }
  pipeline: { max_usd_per_run: 1, max_usd_per_day: 1 }
pipeline:
  retry: { max_attempts: 1, cooldown_seconds: 0 }
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.IsEnabled() {
		t.Errorf("explicit enabled:false should disable")
	}
}

func TestPolicyManager_HotReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(path, []byte(fixtureV1), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var reloadErr atomic.Value
	mgr, err := NewPolicyManager(ctx, path, PolicyManagerOptions{
		OnError: func(e error) { reloadErr.Store(e) },
	})
	if err != nil {
		t.Fatalf("new mgr: %v", err)
	}
	defer mgr.Close()

	if mgr.Current().Budgets.Pipeline.MaxConcurrentRuns != 4 {
		t.Errorf("initial cap: %d", mgr.Current().Budgets.Pipeline.MaxConcurrentRuns)
	}

	// fsnotify can be flaky on macOS — fall back to manual Reload after a
	// short fs-watch attempt. The semantics under test are atomic swap and
	// validation, not the OS notification path.
	updated := strings.Replace(fixtureV1, "max_concurrent_runs: 4", "max_concurrent_runs: 8", 1)
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatalf("write update: %v", err)
	}

	// Subscribe so we can wait for a notification.
	notified := make(chan struct{}, 1)
	mgr.Subscribe(func(_, _ *Policy) {
		select {
		case notified <- struct{}{}:
		default:
		}
	})

	deadline := time.After(2 * time.Second)
	for mgr.Current().Budgets.Pipeline.MaxConcurrentRuns != 8 {
		select {
		case <-notified:
		case <-deadline:
			// Notification didn't arrive — invoke Reload manually.
			if err := mgr.Reload(); err != nil {
				t.Fatalf("manual reload: %v", err)
			}
		}
	}

	if mgr.Current().Budgets.Pipeline.MaxConcurrentRuns != 8 {
		t.Errorf("after reload: %d", mgr.Current().Budgets.Pipeline.MaxConcurrentRuns)
	}
	if v := reloadErr.Load(); v != nil {
		t.Errorf("unexpected reload error: %v", v)
	}
}

func TestPolicyManager_BadReloadKeepsOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(path, []byte(fixtureV1), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mgr, err := NewPolicyManager(context.Background(), path, PolicyManagerOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("new mgr: %v", err)
	}
	defer mgr.Close()
	originalRuns := mgr.Current().Budgets.Pipeline.MaxConcurrentRuns

	if err := os.WriteFile(path, []byte("version: 99\nbudgets: {}\n"), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if err := mgr.Reload(); err == nil {
		t.Errorf("expected validation error on bad policy")
	}
	if mgr.Current().Budgets.Pipeline.MaxConcurrentRuns != originalRuns {
		t.Errorf("bad reload clobbered policy: now %d", mgr.Current().Budgets.Pipeline.MaxConcurrentRuns)
	}
}

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

// TestPolicy_V1ForwardCompat proves a v1-shaped YAML (no version field,
// no v2 sections) still parses on a v2 binary, and that v2-only helpers
// report safe defaults so the operator doesn't accidentally turn on a
// feature the operator never opted into.
func TestPolicy_V1ForwardCompat(t *testing.T) {
	v1Body := `
budgets:
  council:  { max_usd_per_run: 15, max_usd_per_day: 50 }
  pipeline: { max_usd_per_run: 5, max_usd_per_day: 75 }
pipeline:
  retry: { max_attempts: 3, cooldown_seconds: 300 }
`
	p, err := ParsePolicy([]byte(v1Body))
	if err != nil {
		t.Fatalf("v1 forward-compat parse: %v", err)
	}
	if !p.IsEnabled() {
		t.Errorf("default enabled when omitted")
	}
	if p.SquadsEnabled() {
		t.Errorf("squads must default off for v1 YAML")
	}
	if p.AuditEnabled() {
		t.Errorf("audit must default off when YAML omits the section")
	}
	if !p.AuditAdvisoryOnly() {
		t.Errorf("audit advisory_only must default true (fail-safe)")
	}
}

// TestPolicy_V2Roundtrip proves a v2-shaped YAML parses, validates, and
// the helpers return the YAML's values verbatim.
func TestPolicy_V2Roundtrip(t *testing.T) {
	v2Body := `
version: 2
budgets:
  council:  { max_usd_per_run: 15, max_usd_per_day: 50 }
  pipeline: { max_usd_per_run: 5, max_usd_per_day: 75 }
pipeline:
  retry: { max_attempts: 3, cooldown_seconds: 300 }
squads:
  enabled: true
  routing:
    min_confidence: 0.7
    fallback: _default
audit:
  enabled: true
  advisory_only: false
  survival_threshold: 0.85
  daily_budget_usd: 12.0
  pool_default:
    - { backend: flexinfer, model: llama-4-70b-instruct }
    - { backend: flexinfer, model: qwen-3-32b }
cross_repo:
  enabled: true
  per_repo_timeout_minutes: 60
  revert_strategy: all_or_revert
debate:
  enabled:
    cron: false
    incident: true
    manual: true
  max_usd: 8.0
  max_rounds: 3
recursion:
  enabled: true
  max_depth: 2
  subrun_max_budget_share: 0.6
adaptive_policy:
  enabled: true
  auto_apply: false
  relax_path_denylist:
    - "platform/gitops/**"
  revert_window_hours: 24
`
	p, err := ParsePolicy([]byte(v2Body))
	if err != nil {
		t.Fatalf("v2 parse: %v", err)
	}
	if p.Version != 2 {
		t.Errorf("version: got %d want 2", p.Version)
	}
	if !p.SquadsEnabled() {
		t.Errorf("squads.enabled true must surface via helper")
	}
	if p.Squads.Routing.MinConfidence != 0.7 {
		t.Errorf("routing.min_confidence: got %v want 0.7", p.Squads.Routing.MinConfidence)
	}
	if !p.AuditEnabled() {
		t.Errorf("audit.enabled true must surface via helper")
	}
	if p.AuditAdvisoryOnly() {
		t.Errorf("audit.advisory_only false must surface via helper")
	}
	if p.Audit.SurvivalThreshold != 0.85 {
		t.Errorf("survival_threshold: got %v want 0.85", p.Audit.SurvivalThreshold)
	}
	if len(p.Audit.PoolDefault) != 2 {
		t.Errorf("pool_default: got %d members want 2", len(p.Audit.PoolDefault))
	}
	if !p.CrossRepo.Enabled || p.CrossRepo.PerRepoTimeoutMinutes != 60 {
		t.Errorf("cross_repo: %+v", p.CrossRepo)
	}
	if !p.Debate.Enabled.Incident || p.Debate.Enabled.Cron {
		t.Errorf("debate triggers: %+v", p.Debate.Enabled)
	}
	// AllowedFor mirrors store.CouncilTrigger strings to the
	// per-trigger flags (and returns false on unknown strings).
	if !p.Debate.Enabled.AllowedFor("incident") {
		t.Error("debate.AllowedFor(incident) must be true when triggers.incident is true")
	}
	if p.Debate.Enabled.AllowedFor("cron") {
		t.Error("debate.AllowedFor(cron) must be false when triggers.cron is false")
	}
	if p.Debate.Enabled.AllowedFor("roadmap") {
		t.Error("debate.AllowedFor(roadmap) must be false when triggers.roadmap_change is unset")
	}
	if !p.Debate.Enabled.AllowedFor("manual") {
		t.Error("debate.AllowedFor(manual) must be true when triggers.manual is true")
	}
	if p.Debate.Enabled.AllowedFor("not-a-real-trigger") {
		t.Error("debate.AllowedFor must return false on unknown trigger strings")
	}
	if !p.Recursion.Enabled || p.Recursion.MaxDepth != 2 {
		t.Errorf("recursion: %+v", p.Recursion)
	}
	if !p.AdaptivePolicy.Enabled || p.AdaptivePolicy.AutoApply {
		t.Errorf("adaptive_policy: %+v", p.AdaptivePolicy)
	}
	if len(p.AdaptivePolicy.RelaxPathDenylist) != 1 {
		t.Errorf("relax_path_denylist: %v", p.AdaptivePolicy.RelaxPathDenylist)
	}
}

// TestPolicy_EmptyYAMLMatchesDefault proves that parsing an essentially
// empty policy (only the required sections to clear validation) yields
// the same gating defaults as Default() — so v2 helpers fail closed
// when the operator forgets to fill the YAML.
func TestPolicy_EmptyYAMLMatchesDefault(t *testing.T) {
	body := `
budgets:
  council:  { max_usd_per_run: 0, max_usd_per_day: 0 }
  pipeline: { max_usd_per_run: 0, max_usd_per_day: 0 }
pipeline:
  retry: { max_attempts: 0, cooldown_seconds: 0 }
`
	p, err := ParsePolicy([]byte(body))
	if err != nil {
		t.Fatalf("empty parse: %v", err)
	}
	// SquadsEnabled is the only helper Default() also reports false for
	// in spirit — Default() *does* turn squads on/off via the explicit
	// false. Both must agree.
	d := Default()
	if p.SquadsEnabled() != d.Squads.Enabled {
		t.Errorf("squads default mismatch: parsed=%v default=%v",
			p.SquadsEnabled(), d.Squads.Enabled)
	}
	if !p.AuditAdvisoryOnly() {
		t.Errorf("audit advisory_only must default true even on empty YAML")
	}
}

// TestPolicy_V2HelpersNilSafe codifies the fail-closed contract: every
// helper must return a safe value on a nil receiver so a misconfigured
// caller never accidentally enables a v2 feature.
func TestPolicy_V2HelpersNilSafe(t *testing.T) {
	var p *Policy
	if p.SquadsEnabled() {
		t.Errorf("nil policy must report squads disabled")
	}
	if p.AuditEnabled() {
		t.Errorf("nil policy must report audit disabled")
	}
	if !p.AuditAdvisoryOnly() {
		t.Errorf("nil policy must report audit advisory_only true (fail-safe)")
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

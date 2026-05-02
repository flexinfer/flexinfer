// Package hive is the meta-orchestration layer above weaver/spawn/mentatlab.
// It owns the planning council and the deterministic execution pipeline that
// together turn ongoing intent (roadmap, telemetry, alerts) into a continuous
// flow of merged changes — "CI above CI for agents".
//
// This file is the policy contract: the YAML-loadable, validate-on-startup,
// hot-reloadable rule set that bounds every council and pipeline run.
package hive

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// Policy is the full rule set the operator consults at runtime.
//
// Schema version evolution:
//   - v1: Phase 1-6 fields (Budgets, Council, Pipeline, HumanHandoff).
//   - v2: Adds Squads, Audit, CrossRepo, Debate, Recursion, AdaptivePolicy
//     for the Hive v2 hierarchical-swarm rollout (Phases 7-8). v2 is a
//     superset of v1 — v1 YAML still parses; v2-only sections default
//     to "off" so v1 behavior is preserved when those keys are omitted.
//
// Defaults policy (.loom/93-product-spec-hive-v2-…2026-05-02.md, §"Policy
// file additions"):
//   - Squads off — flip on per Phase 8 default-on rollout.
//   - Audit on but advisory_only=true; flip to blocking in v2.1 once
//     audit_survival_rate proves low-noise (>0.85 over 100 runs).
//   - CrossRepo off — operator opts in after 4 weeks of dogfooding.
//   - Debate off (incident-only opt-in); Recursion off; AdaptivePolicy off.
type Policy struct {
	Version        int                `yaml:"version,omitempty"`
	Enabled        *bool              `yaml:"enabled,omitempty"` // nil treated as enabled
	Budgets        Budgets            `yaml:"budgets"`
	Council        CouncilPolicy      `yaml:"council"`
	Pipeline       PipelinePolicy     `yaml:"pipeline"`
	HumanHandoff   HumanHandoffPolicy `yaml:"human_handoff"`
	Squads         SquadsPolicy       `yaml:"squads,omitempty"`
	Audit          AuditPolicy        `yaml:"audit,omitempty"`
	CrossRepo      CrossRepoPolicy    `yaml:"cross_repo,omitempty"`
	Debate         DebatePolicy       `yaml:"debate,omitempty"`
	Recursion      RecursionPolicy    `yaml:"recursion,omitempty"`
	AdaptivePolicy AdaptivePolicy     `yaml:"adaptive_policy,omitempty"`
}

// Budgets holds per-tier spend limits.
type Budgets struct {
	Council  BudgetLimits `yaml:"council"`
	Pipeline BudgetLimits `yaml:"pipeline"`
}

// BudgetLimits captures one tier's caps. Zero values disable that specific cap.
type BudgetLimits struct {
	MaxUSDPerRun      float64 `yaml:"max_usd_per_run"`
	MaxUSDPerDay      float64 `yaml:"max_usd_per_day"`
	MaxConcurrentRuns int     `yaml:"max_concurrent_runs,omitempty"`
	MaxRunsPerDay     int     `yaml:"max_runs_per_day,omitempty"`
}

// CouncilPolicy bounds the planning tier.
type CouncilPolicy struct {
	ScheduleCron           string          `yaml:"schedule_cron"`
	Triggers               CouncilTriggers `yaml:"triggers"`
	Ensemble               CouncilEnsemble `yaml:"ensemble"`
	ArtifactsBranch        string          `yaml:"artifacts_branch"`
	ArtifactsMergeStrategy string          `yaml:"artifacts_merge_strategy"`
}

// CouncilTriggers controls when a council run is initiated.
type CouncilTriggers struct {
	OnRoadmapChange   bool `yaml:"on_roadmap_change"`
	OnIncident        bool `yaml:"on_incident"`
	OnMergeDriftHours int  `yaml:"on_merge_drift_hours,omitempty"`
}

// CouncilEnsemble names the editor + reviewer + judge agents the council uses.
type CouncilEnsemble struct {
	Editor    CouncilAgent   `yaml:"editor"`
	Reviewers []CouncilAgent `yaml:"reviewers"`
	Judge     CouncilAgent   `yaml:"judge,omitempty"`
}

// CouncilAgent identifies one council participant.
type CouncilAgent struct {
	Name    string `yaml:"name,omitempty"`
	Model   string `yaml:"model"`
	Backend string `yaml:"backend"`
}

// PipelinePolicy bounds the execution tier.
type PipelinePolicy struct {
	DefaultTemplate        string          `yaml:"default_template"`
	PerLabelOverrides      []LabelOverride `yaml:"per_label_overrides,omitempty"`
	ProtectedPaths         []string        `yaml:"protected_paths,omitempty"`
	Retry                  RetryPolicy     `yaml:"retry"`
	AutoRevertOnRegression bool            `yaml:"auto_revert_on_regression,omitempty"`
}

// LabelOverride lets a label flip auto_merge / human_review for a specific
// class of work without rewriting the global policy.
type LabelOverride struct {
	Label       string `yaml:"label"`
	AutoMerge   bool   `yaml:"auto_merge"`
	HumanReview bool   `yaml:"human_review"`
}

// RetryPolicy controls how the pipeline retries failed stages.
type RetryPolicy struct {
	MaxAttempts     int `yaml:"max_attempts"`
	CooldownSeconds int `yaml:"cooldown_seconds"`
}

// HumanHandoffPolicy controls escalation behavior.
type HumanHandoffPolicy struct {
	OnEscalationCreateHandoff bool   `yaml:"on_escalation_create_handoff"`
	OnEscalationCreateIssue   bool   `yaml:"on_escalation_create_issue"`
	NotifyAgentID             string `yaml:"notify_agent_id"`
}

// SquadsPolicy gates and tunes the v2 squad-routing layer. When Enabled
// is false the reconciler bypasses the router and the v1 generic path
// runs. Routing.MinConfidence mirrors squads.Router.MinConfidence — the
// router still enforces its own internal default if this is zero.
type SquadsPolicy struct {
	Enabled bool                `yaml:"enabled,omitempty"`
	Routing SquadsRoutingPolicy `yaml:"routing,omitempty"`
}

// SquadsRoutingPolicy bounds router behavior. Fallback is the squad
// name to route to when no squad clears MinConfidence; empty means the
// router's compiled-in FallbackName ("_default") is used.
type SquadsRoutingPolicy struct {
	MinConfidence float64 `yaml:"min_confidence,omitempty"`
	Fallback      string  `yaml:"fallback,omitempty"`
}

// AuditPolicy controls the adversarial audit swarm. AdvisoryOnly is the
// v2.0 default: audit findings open follow-up issues + score in the HUD
// but never block merges. v2.1 flips it to blocking once the survival
// rate clears the SurvivalThreshold (default 0.6).
//
// AdvisoryOnly is a *bool so an omitted YAML key defaults to true (the
// spec's v2.0 fail-safe). An explicit `advisory_only: false` lets the
// operator opt into blocking mode once survival rates prove low-noise.
type AuditPolicy struct {
	Enabled           bool        `yaml:"enabled,omitempty"`
	PoolDefault       []AuditPool `yaml:"pool_default,omitempty"`
	PoolEscalation    []AuditPool `yaml:"pool_escalation,omitempty"`
	DailyBudgetUSD    float64     `yaml:"daily_budget_usd,omitempty"`
	AdvisoryOnly      *bool       `yaml:"advisory_only,omitempty"`
	SurvivalThreshold float64     `yaml:"survival_threshold,omitempty"`
}

// AuditPool names one auditor model. Backend is the spawn-side driver
// ("flexinfer" or "spawn"); Model is the model identifier; Driver is
// the spawn driver name when Backend == "spawn".
type AuditPool struct {
	Backend string `yaml:"backend,omitempty"`
	Model   string `yaml:"model,omitempty"`
	Driver  string `yaml:"driver,omitempty"`
}

// CrossRepoPolicy controls atomic-merge runs that span multiple repos.
// Disabled in v2.0; flipped after 4 weeks of dogfooding per V2-D4.
type CrossRepoPolicy struct {
	Enabled               bool   `yaml:"enabled,omitempty"`
	PerRepoTimeoutMinutes int    `yaml:"per_repo_timeout_minutes,omitempty"`
	RevertStrategy        string `yaml:"revert_strategy,omitempty"`
}

// DebatePolicy controls the v2 council debate rounds. Disabled by
// default for cron + roadmap_change triggers; enabled for incident
// triggers per V2-D5.
type DebatePolicy struct {
	Enabled            DebateTriggers `yaml:"enabled,omitempty"`
	MaxUSD             float64        `yaml:"max_usd,omitempty"`
	MaxRounds          int            `yaml:"max_rounds,omitempty"`
	EarlyExitThreshold float64        `yaml:"early_exit_threshold,omitempty"`
}

// DebateTriggers controls which trigger sources kick off a debate run.
type DebateTriggers struct {
	Cron          bool `yaml:"cron,omitempty"`
	RoadmapChange bool `yaml:"roadmap_change,omitempty"`
	Incident      bool `yaml:"incident,omitempty"`
	Manual        bool `yaml:"manual,omitempty"`
}

// RecursionPolicy controls bounded sub-runs in the pipeline. Disabled
// by default; opt-in per-squad via the squad manifest's
// default_ensemble.recursion: true (V2-D6).
type RecursionPolicy struct {
	Enabled              bool    `yaml:"enabled,omitempty"`
	MaxDepth             int     `yaml:"max_depth,omitempty"`
	SubrunMaxBudgetShare float64 `yaml:"subrun_max_budget_share,omitempty"`
}

// AdaptivePolicy controls the policy-proposal engine. Disabled by
// default; v2.0 ships with auto_apply=false so all proposals require
// human edit before applying.
type AdaptivePolicy struct {
	Enabled           bool     `yaml:"enabled,omitempty"`
	AutoApply         bool     `yaml:"auto_apply,omitempty"`
	RelaxPathDenylist []string `yaml:"relax_path_denylist,omitempty"`
	RevertWindowHours int      `yaml:"revert_window_hours,omitempty"`
}

// Default returns a baseline policy suitable for local development. Production
// deployments override via the ConfigMap-mounted policy.yaml. Enabled is true
// because phase 6 has shipped (slice 6.6 default-on flip, 2026-05-02). The
// kill switch is `enabled: false` in the YAML; nil treats as enabled per
// IsEnabled.
//
// Schema version is 2 (the Hive v2 hierarchical-swarm rollout). v2-only
// sections default to "off" so a v1 deployment that hot-reloads onto a
// v2 binary keeps its v1 behavior until the operator opts in.
func Default() *Policy {
	enabled := true
	return &Policy{
		Version: 2,
		Enabled: &enabled,
		Budgets: Budgets{
			Council:  BudgetLimits{MaxUSDPerRun: 15, MaxUSDPerDay: 50},
			Pipeline: BudgetLimits{MaxUSDPerRun: 5, MaxUSDPerDay: 75, MaxConcurrentRuns: 4, MaxRunsPerDay: 20},
		},
		Council: CouncilPolicy{
			ScheduleCron:           "0 5 * * *",
			ArtifactsBranch:        "council/{date}",
			ArtifactsMergeStrategy: "fast-merge-loom-only",
			Triggers:               CouncilTriggers{OnRoadmapChange: true, OnIncident: true, OnMergeDriftHours: 48},
		},
		Pipeline: PipelinePolicy{
			DefaultTemplate: "hive-default-pipeline",
			Retry:           RetryPolicy{MaxAttempts: 3, CooldownSeconds: 300},
			ProtectedPaths: []string{
				"platform/gitops/**",
				"cmd/loomd/**",
				"**/*auth*.go",
				"**/secret*.yaml",
			},
		},
		HumanHandoff: HumanHandoffPolicy{
			OnEscalationCreateHandoff: true,
			OnEscalationCreateIssue:   true,
		},
		// v2 defaults: squads off, audit advisory-on, everything else off.
		// Operator flips per Phase 8 default-on rollout (one feature, one
		// week soak, repeat).
		Squads: SquadsPolicy{
			Enabled: false,
			Routing: SquadsRoutingPolicy{
				MinConfidence: 0.6,
				Fallback:      "_default",
			},
		},
		Audit: AuditPolicy{
			Enabled:           true,
			AdvisoryOnly:      boolPtr(true),
			SurvivalThreshold: 0.6,
		},
		CrossRepo: CrossRepoPolicy{Enabled: false},
		Debate:    DebatePolicy{},
		Recursion: RecursionPolicy{Enabled: false},
		AdaptivePolicy: AdaptivePolicy{
			Enabled:   false,
			AutoApply: false,
		},
	}
}

// LoadPolicy reads, parses, and validates a policy YAML file.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hive: read policy %s: %w", path, err)
	}
	return ParsePolicy(data)
}

// ParsePolicy parses + validates a policy from raw YAML.
func ParsePolicy(data []byte) (*Policy, error) {
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("hive: parse policy: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("hive: validate policy: %w", err)
	}
	return &p, nil
}

// Validate enforces the rules a malformed policy must trip on.
//
// Version handling: 0 (omitted) is treated as legacy v1 — pre-Phase-7
// YAML had no `version` key in many test fixtures and dev configs, and
// silently bumping those to "unsupported" would break hot-reload. v1
// and v2 are both valid; anything else is rejected.
func (p *Policy) Validate() error {
	if p == nil {
		return errors.New("policy is nil")
	}
	switch p.Version {
	case 0, 1, 2:
		// 0 = omitted; treat as legacy v1.
	default:
		return fmt.Errorf("unsupported policy version %d (supported: 1, 2)", p.Version)
	}
	if err := validateBudget("council", p.Budgets.Council); err != nil {
		return err
	}
	if err := validateBudget("pipeline", p.Budgets.Pipeline); err != nil {
		return err
	}
	if p.Council.ArtifactsMergeStrategy != "" {
		switch p.Council.ArtifactsMergeStrategy {
		case "fast-merge-loom-only", "always-mr":
		default:
			return fmt.Errorf("council.artifacts_merge_strategy: must be 'fast-merge-loom-only' or 'always-mr', got %q", p.Council.ArtifactsMergeStrategy)
		}
	}
	if p.Pipeline.Retry.MaxAttempts < 0 {
		return errors.New("pipeline.retry.max_attempts must be >= 0")
	}
	if p.Pipeline.Retry.CooldownSeconds < 0 {
		return errors.New("pipeline.retry.cooldown_seconds must be >= 0")
	}
	for i, ov := range p.Pipeline.PerLabelOverrides {
		if ov.Label == "" {
			return fmt.Errorf("pipeline.per_label_overrides[%d].label is empty", i)
		}
	}
	for i, gp := range p.Pipeline.ProtectedPaths {
		if !doublestar.ValidatePattern(gp) {
			return fmt.Errorf("pipeline.protected_paths[%d] %q is not a valid glob", i, gp)
		}
	}
	if p.Council.Ensemble.Editor.Model != "" && p.Council.Ensemble.Editor.Backend == "" {
		return errors.New("council.ensemble.editor.backend is required when editor.model is set")
	}
	for i, r := range p.Council.Ensemble.Reviewers {
		if r.Model == "" || r.Backend == "" {
			return fmt.Errorf("council.ensemble.reviewers[%d] requires both model and backend", i)
		}
	}
	return nil
}

func validateBudget(tier string, b BudgetLimits) error {
	if b.MaxUSDPerRun < 0 {
		return fmt.Errorf("budgets.%s.max_usd_per_run must be >= 0", tier)
	}
	if b.MaxUSDPerDay < 0 {
		return fmt.Errorf("budgets.%s.max_usd_per_day must be >= 0", tier)
	}
	if b.MaxUSDPerRun > 0 && b.MaxUSDPerDay > 0 && b.MaxUSDPerRun > b.MaxUSDPerDay {
		return fmt.Errorf("budgets.%s.max_usd_per_run (%v) exceeds max_usd_per_day (%v)", tier, b.MaxUSDPerRun, b.MaxUSDPerDay)
	}
	if b.MaxConcurrentRuns < 0 {
		return fmt.Errorf("budgets.%s.max_concurrent_runs must be >= 0", tier)
	}
	if b.MaxRunsPerDay < 0 {
		return fmt.Errorf("budgets.%s.max_runs_per_day must be >= 0", tier)
	}
	return nil
}

// IsEnabled reports whether the hive should act. The kill switch defaults to
// enabled; an explicit `enabled: false` in YAML freezes everything.
func (p *Policy) IsEnabled() bool {
	if p == nil {
		return false
	}
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

// SquadsEnabled reports whether v2 squad routing is on. Nil-safe and
// defaults to false so a v1-policy hot-reloaded onto a v2 binary keeps
// the v1 generic path. Mirrors the spec's `policy.squads.enabled` flag.
func (p *Policy) SquadsEnabled() bool {
	if p == nil {
		return false
	}
	return p.Squads.Enabled
}

// AuditEnabled reports whether the v2 adversarial audit swarm should
// run. Nil-safe and defaults to false. Per the v2.0 spec audit defaults
// to enabled in Default(), but a missing/empty YAML section yields
// false here so the trigger gate fails closed.
func (p *Policy) AuditEnabled() bool {
	if p == nil {
		return false
	}
	return p.Audit.Enabled
}

// AuditAdvisoryOnly reports whether audit findings should never block
// merges. The v2.0 default is true; v2.1 flips it once survival rates
// prove low-noise. Nil-safe and YAML-omission-safe; returns true on a
// nil receiver or when `advisory_only` is omitted, so downstream code
// that forgets to wire the policy never accidentally blocks a merge.
func (p *Policy) AuditAdvisoryOnly() bool {
	if p == nil {
		return true
	}
	if p.Audit.AdvisoryOnly == nil {
		return true
	}
	return *p.Audit.AdvisoryOnly
}

// boolPtr is a tiny convenience for constructing the *bool fields on
// the policy struct from literal values. Defined here so callers
// outside the package don't need to introduce their own helper.
func boolPtr(b bool) *bool { return &b }

// LabelOverrideFor returns the per-label override matching the given labels in
// declaration order. The first match wins. If no override matches, ok=false.
func (p *Policy) LabelOverrideFor(labels []string) (LabelOverride, bool) {
	if p == nil {
		return LabelOverride{}, false
	}
	want := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		want[strings.ToLower(strings.TrimSpace(l))] = struct{}{}
	}
	for _, ov := range p.Pipeline.PerLabelOverrides {
		if _, ok := want[strings.ToLower(ov.Label)]; ok {
			return ov, true
		}
	}
	return LabelOverride{}, false
}

// ProtectedPathsHit returns the subset of input paths that match any pattern
// in pipeline.protected_paths. Used by the path-policy gate to decide whether
// an item must require human review regardless of its label policy.
func (p *Policy) ProtectedPathsHit(paths []string) []string {
	if p == nil || len(p.Pipeline.ProtectedPaths) == 0 || len(paths) == 0 {
		return nil
	}
	var hits []string
	for _, path := range paths {
		for _, pat := range p.Pipeline.ProtectedPaths {
			ok, err := doublestar.Match(pat, path)
			if err == nil && ok {
				hits = append(hits, path)
				break
			}
		}
	}
	return hits
}

// CooldownDuration is a typed accessor around RetryPolicy.CooldownSeconds.
func (r RetryPolicy) CooldownDuration() time.Duration {
	if r.CooldownSeconds <= 0 {
		return 0
	}
	return time.Duration(r.CooldownSeconds) * time.Second
}

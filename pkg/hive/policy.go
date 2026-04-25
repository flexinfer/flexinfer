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
type Policy struct {
	Version      int                `yaml:"version"`
	Enabled      *bool              `yaml:"enabled,omitempty"` // nil treated as enabled
	Budgets      Budgets            `yaml:"budgets"`
	Council      CouncilPolicy      `yaml:"council"`
	Pipeline     PipelinePolicy     `yaml:"pipeline"`
	HumanHandoff HumanHandoffPolicy `yaml:"human_handoff"`
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

// Default returns a baseline policy suitable for local development. Production
// deployments override via the ConfigMap-mounted policy.yaml.
func Default() *Policy {
	enabled := false // start disabled; flipped on by phase 6
	return &Policy{
		Version: 1,
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
func (p *Policy) Validate() error {
	if p == nil {
		return errors.New("policy is nil")
	}
	if p.Version != 1 {
		return fmt.Errorf("unsupported policy version %d (only 1 is recognized)", p.Version)
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

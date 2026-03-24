// Package alerting provides pipeline alert types, rule evaluation, and
// dispatch for the HUD alert engine.
package alerting

import (
	"time"
)

// AlertRule defines a condition under which an alert fires.
type AlertRule struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Enabled   bool           `json:"enabled"`
	Condition AlertCondition `json:"condition"`
	Severity  string         `json:"severity"` // critical, warning, info
	Cooldown  time.Duration  `json:"cooldown"`
	LastFired time.Time      `json:"last_fired"`
}

// AlertCondition specifies the trigger parameters for a rule.
type AlertCondition struct {
	Type      string        `json:"type"`               // "pipeline_failed", "pipeline_stuck", "consecutive_failures"
	Threshold int           `json:"threshold"`          // e.g., 3 consecutive failures
	Duration  time.Duration `json:"duration,omitempty"` // for "stuck" alerts
	Projects  []string      `json:"projects,omitempty"` // filter to specific projects
}

// Alert represents a fired alert instance.
type Alert struct {
	ID         string      `json:"id"`
	RuleID     string      `json:"rule_id"`
	RuleName   string      `json:"rule_name"`
	Severity   string      `json:"severity"`
	Title      string      `json:"title"`
	Message    string      `json:"message"`
	Pipeline   PipelineRef `json:"pipeline"`
	FiredAt    time.Time   `json:"fired_at"`
	AckedAt    *time.Time  `json:"acked_at,omitempty"`
	AckedBy    string      `json:"acked_by,omitempty"`
	ResolvedAt *time.Time  `json:"resolved_at,omitempty"`
	AutoFixID  string      `json:"autofix_id,omitempty"`
}

// PipelineRef is a lightweight pipeline reference embedded in alerts.
type PipelineRef struct {
	ID      int    `json:"id"`
	Project string `json:"project"`
	Ref     string `json:"ref"`
	Status  string `json:"status"`
	URL     string `json:"url,omitempty"`
}

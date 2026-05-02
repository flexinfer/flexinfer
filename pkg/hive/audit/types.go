// Package audit is the Hive v2 adversarial audit swarm. An independent,
// lateral ensemble that scores Council artifacts and merged Pipeline diffs
// against a fixed rubric the Council editor never sees. Findings are
// advisory in v2.0 (a low survival_score opens a follow-up issue / MR
// for human triage); blocking enforcement is deferred to v2.1.
//
// The package owns three concerns: (1) a versioned rubric loaded from
// embedded markdown assets, (2) a Dispatcher that runs the configured
// reviewer pool concurrently and aggregates by median, and (3) — in
// follow-up slices — triggers that listen for council/merge events and a
// followup writer that opens advisory issues. This file is the type
// contract everything else in the package consumes.
//
// See `.loom/93-product-spec-hive-v2-hierarchical-swarm-2026-05-02.md`
// §"Audit swarm flow" + §"Audit swarm — what it must do" for design.
package audit

import (
	"context"
	"errors"
	"strings"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// RubricID is the canonical identifier for the v1 audit rubric. The
// Dispatcher stamps it on every AuditFinding row so future rubric
// rotations leave audit history queryable by version.
const RubricID = "audit_v1"

// PoolMember names one auditor in the dispatcher's reviewer pool. The
// production wiring fills Backend with "flexinfer" or "spawn" (v2.1)
// and Model with the configured model id; the Dispatcher matches Backend
// to a registered Reviewer and forwards the prompt + model + max-cost.
type PoolMember struct {
	// Backend selects which Reviewer implementation handles this
	// member's call ("flexinfer" satisfied by *clients.FlexInferClient
	// today; "spawn" reserved for v2.1's headless adapter).
	Backend string

	// Model is the backend-specific model id (e.g.
	// "llama-4-70b-instruct", "qwen-3-32b", "claude-opus", "codex-gpt5").
	Model string

	// MaxCostUSD is a per-call soft cap surfaced through context to the
	// underlying Reviewer; the budget enforcer is the load-bearing
	// guardrail. Zero is "no member-level cap".
	MaxCostUSD float64
}

// Request bundles every input the Dispatcher needs for one audit run.
type Request struct {
	// SubjectKind names what the artifact is. Routed to either the
	// council or the pipeline rubric variant.
	SubjectKind store.AuditSubjectKind

	// SubjectID is the council_runs.id or pipeline_runs.id the
	// Dispatcher will reference back from AuditFinding.SubjectID. Empty
	// is rejected — the audit row is unreachable without it.
	SubjectID string

	// Artifact is the raw text the auditor reads. For council artifacts
	// the spec recommends artifact = the markdown doc + sidecar JSON
	// concatenation; for pipeline merges it's the merged diff (unified
	// patch). Auditors never see Council prompts or operator state by
	// design (independence — see §"Audit swarm — what it must do" #2).
	Artifact string

	// Pool is the bulk reviewer set; runs concurrently. Required.
	Pool []PoolMember

	// EscalationPool runs only when the bulk median lands in the
	// configured escalation band ([0.4, 0.7] by default). Empty disables
	// escalation entirely — the dispatcher returns the bulk median.
	EscalationPool []PoolMember
}

// Validate returns an error when the Request would not produce a
// well-formed AuditFinding. Called by Dispatcher.Run before any reviewer
// dispatch so a bad request fails fast and cheap.
func (r *Request) Validate() error {
	if r == nil {
		return errors.New("audit: request nil")
	}
	switch r.SubjectKind {
	case store.AuditSubjectCouncilArtifact, store.AuditSubjectPipelineMerge:
	default:
		return errors.New(`audit: subject_kind must be "council_artifact" or "pipeline_merge"`)
	}
	if strings.TrimSpace(r.SubjectID) == "" {
		return errors.New("audit: subject_id required")
	}
	if strings.TrimSpace(r.Artifact) == "" {
		return errors.New("audit: artifact text empty")
	}
	if len(r.Pool) == 0 {
		return errors.New("audit: pool must have at least one member")
	}
	for i, m := range r.Pool {
		if strings.TrimSpace(m.Backend) == "" || strings.TrimSpace(m.Model) == "" {
			return errors.New("audit: pool[" + itoa(i) + "] missing backend or model")
		}
	}
	for i, m := range r.EscalationPool {
		if strings.TrimSpace(m.Backend) == "" || strings.TrimSpace(m.Model) == "" {
			return errors.New("audit: escalation_pool[" + itoa(i) + "] missing backend or model")
		}
	}
	return nil
}

// FindingItem is one structured note an auditor returned. The Dispatcher
// consolidates findings across pool members into a deduplicated list
// stored on the persisted AuditFinding row.
type FindingItem struct {
	ID       string              `json:"id"`
	Title    string              `json:"title"`
	Severity store.AuditSeverity `json:"severity"`
	Detail   string              `json:"detail,omitempty"`
}

// MemberOutput captures one auditor's raw response. The Dispatcher keeps
// these in Result.Members so the operator can inspect divergence; only
// the survival_score + deduplicated findings are persisted to the row.
type MemberOutput struct {
	Member        PoolMember
	SurvivalScore float64
	Findings      []FindingItem
	CostUSD       float64
	Raw           string
	ParseErr      error
}

// Result is the Dispatcher's full output. Persist Finding via
// store.AuditDAO.RecordFinding; the per-member breakdown lives in
// Members for diagnostics + escalation analysis.
type Result struct {
	// Finding is the row ready for the canonical store. SurvivalScore is
	// the median (or escalation-aggregated) value; Severity is computed
	// from the survival score banding (see Dispatcher.severity).
	Finding *store.AuditFinding

	// Members is the per-pool-member raw outputs. Empty for members the
	// Reviewer rejected (e.g. unsupported backend) — those errors are
	// folded into Members[i].ParseErr / Result.SkippedMembers.
	Members []MemberOutput

	// Escalated reports whether the bulk median triggered the frontier
	// escalation pool. False when the bulk median was clearly above /
	// below the band, or when EscalationPool was empty.
	Escalated bool

	// SkippedMembers counts pool entries the Dispatcher could not run
	// (no Reviewer registered for the backend, persistent error). The
	// caller can decide whether to alert; the audit row is still
	// emitted using the surviving members' median.
	SkippedMembers int
}

// Reviewer is the contract every audit backend satisfies. Implementations
// live alongside the production clients (see *clients.FlexInferClient).
// The Dispatcher selects a Reviewer by Backend name; the prompt the
// Reviewer receives is already fully rendered (rubric + artifact) so the
// Reviewer is a thin "prompt → text + cost" bridge.
type Reviewer interface {
	// Backend names the PoolMember.Backend value this reviewer handles.
	Backend() string

	// Review issues a single completion. Implementations return content
	// (the auditor's structured response) + cost_usd. ctx cancellation
	// must abort the request promptly.
	Review(ctx context.Context, model, prompt string, maxCostUSD float64) (string, float64, error)
}

// itoa is a small strconv.Itoa replacement so this file stays free of
// strconv (kept symmetric with other audit files; we don't need the
// extra import surface for one Validate path).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

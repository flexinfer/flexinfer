package audit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// DefaultFollowupThreshold is the survival score below which the
// Followup writer creates an advisory issue. Mirrors spec §"Audit swarm
// flow" #5: "open MR (council artifact) / open issue (pipeline merge)
// when survival_score < 0.6". v2.0 uses GitLab issues for both subjects;
// promoting council follow-ups to MRs lands in v2.1 once the operator
// plumbs the council branch ref onto the audit row.
const DefaultFollowupThreshold = 0.6

// Issuer is the surface Followup depends on. Production satisfies it
// with *clients.GitLabClient (its existing CreateIssue method).
type Issuer interface {
	CreateIssue(ctx context.Context, req pipeline.IssueRequest) (pipeline.IssueResponse, error)
}

// Followup writes advisory GitLab issues when an audit row's survival
// score crosses below Threshold. It is wired to QueueWorker.OnRecorded
// in the operator so every persisted finding gets considered without
// the dispatcher needing to know about issue creation.
//
// v2.0 is intentionally non-blocking: a low score yields an issue, not
// a merge revert / artifact rewrite. Operators triage the issue and
// decide what to do. The HUD audit panel surfaces the issue link
// inline once slice 3.5 ships.
type Followup struct {
	// Issuer creates the advisory issue. nil disables the writer (the
	// operator boots without GitLab; OnRecorded becomes a no-op).
	Issuer Issuer

	// Threshold is the survival_score boundary. Findings with
	// SurvivalScore < Threshold trigger an issue; anything ≥ Threshold
	// is silently dropped. Zero falls back to DefaultFollowupThreshold.
	Threshold float64

	// Logger surfaces issue URLs + per-finding skip reasons. nil
	// discards.
	Logger *slog.Logger

	// Clock is injected for deterministic timestamps in the issue body.
	// Defaults to time.Now.
	Clock func() time.Time
}

// NewFollowup constructs a Followup writer with sensible defaults. nil
// Issuer is permitted; the resulting writer's OnRecorded is a no-op
// (logs once at boot via the operator's wiring path).
func NewFollowup(issuer Issuer) *Followup {
	return &Followup{
		Issuer:    issuer,
		Threshold: DefaultFollowupThreshold,
	}
}

// OnRecorded inspects a freshly-recorded finding. When SurvivalScore is
// strictly below Threshold (and the Issuer is wired), a GitLab issue
// is opened with the audit context. Returns nil even on Issuer error
// so the QueueWorker keeps draining; errors are logged for triage.
//
// Idempotency is NOT enforced today: the same finding fired through
// OnRecorded twice will create two issues. v2.1 should dedup by
// (subject_kind, subject_id, finding_id). Documented as a known
// limitation; the worker only fires OnRecorded once per finding under
// normal operation, so this is a reset-after-restart scenario in
// practice.
func (f *Followup) OnRecorded(ctx context.Context, finding *store.AuditFinding) error {
	if f == nil || finding == nil {
		return nil
	}
	threshold := f.Threshold
	if threshold <= 0 {
		threshold = DefaultFollowupThreshold
	}
	if finding.SurvivalScore >= threshold {
		return nil // above threshold; nothing to do
	}
	if f.Issuer == nil {
		f.warn("audit/followup: skipping issue (no Issuer wired)",
			"subject_kind", string(finding.SubjectKind),
			"subject_id", finding.SubjectID,
			"survival", finding.SurvivalScore,
		)
		return nil
	}

	req := pipeline.IssueRequest{
		Title:       f.title(finding),
		Description: f.body(finding),
		Labels:      f.labels(finding),
	}
	resp, err := f.Issuer.CreateIssue(ctx, req)
	if err != nil {
		f.warn("audit/followup: create issue failed",
			"subject_kind", string(finding.SubjectKind),
			"subject_id", finding.SubjectID,
			"error", err,
		)
		// Best-effort: never surface to the QueueWorker.
		return nil
	}
	f.info("audit/followup: issue opened",
		"subject_kind", string(finding.SubjectKind),
		"subject_id", finding.SubjectID,
		"survival", finding.SurvivalScore,
		"iid", resp.IID,
		"url", resp.URL,
	)
	return nil
}

// title formats the issue title. Stable + scannable in a triage list:
// the subject id is the longest variable element so we keep it last
// for prefix-matching searches.
func (f *Followup) title(finding *store.AuditFinding) string {
	return fmt.Sprintf("Audit follow-up [%s] %s — survival %.2f",
		strings.ToLower(string(finding.SubjectKind)),
		finding.SubjectID,
		finding.SurvivalScore,
	)
}

// body composes the markdown description. Links the auditor pool +
// every Finding so the triage queue sees the full picture without
// needing the audit drawer.
func (f *Followup) body(finding *store.AuditFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Audit rubric:** `%s`  \n", finding.RubricID)
	fmt.Fprintf(&b, "**Survival score:** `%.3f` (threshold `%.2f`)  \n", finding.SurvivalScore, f.threshold())
	fmt.Fprintf(&b, "**Severity:** `%s`  \n", finding.Severity)
	fmt.Fprintf(&b, "**Subject:** `%s` `%s`  \n", finding.SubjectKind, finding.SubjectID)
	fmt.Fprintf(&b, "**Cost:** `$%.4f`  \n", finding.CostUSD)
	fmt.Fprintf(&b, "**Recorded at:** `%s`\n\n", f.now().UTC().Format(time.RFC3339))

	if len(finding.AuditorPool) > 0 {
		b.WriteString("## Auditor pool\n\n")
		for _, m := range finding.AuditorPool {
			role, _ := m["role"].(string)
			backend, _ := m["backend"].(string)
			model, _ := m["model"].(string)
			fmt.Fprintf(&b, "- `%s` / `%s` (role: %s)\n", backend, model, role)
		}
		b.WriteString("\n")
	}

	if len(finding.Findings) > 0 {
		b.WriteString("## Findings\n\n")
		for _, item := range finding.Findings {
			id, _ := item["id"].(string)
			title, _ := item["title"].(string)
			severity, _ := item["severity"].(string)
			detail, _ := item["detail"].(string)
			fmt.Fprintf(&b, "### `%s` — %s (`%s`)\n", id, title, severity)
			if detail != "" {
				fmt.Fprintf(&b, "%s\n\n", detail)
			} else {
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString("## Findings\n\n_No structured findings — see the survival score._\n\n")
	}

	b.WriteString("---\n")
	b.WriteString("_Opened automatically by the Loom Mills audit subsystem (advisory; v2.0 does not block on this)._\n")
	return b.String()
}

// labels assemble the triage label set. We always emit `audit-followup`
// + a severity-prefixed label so triage queues can filter by severity
// without parsing the title; subject-kind variants help split the
// queue between council artifacts and pipeline merges.
func (f *Followup) labels(finding *store.AuditFinding) []string {
	out := []string{
		"audit-followup",
		"severity-" + strings.ToLower(string(finding.Severity)),
	}
	switch finding.SubjectKind {
	case store.AuditSubjectCouncilArtifact:
		out = append(out, "council-artifact")
	case store.AuditSubjectPipelineMerge:
		out = append(out, "pipeline-merge")
	}
	return out
}

func (f *Followup) threshold() float64 {
	if f == nil || f.Threshold <= 0 {
		return DefaultFollowupThreshold
	}
	return f.Threshold
}

func (f *Followup) now() time.Time {
	if f != nil && f.Clock != nil {
		return f.Clock()
	}
	return time.Now()
}

func (f *Followup) warn(msg string, kv ...any) {
	if f == nil || f.Logger == nil {
		return
	}
	f.Logger.Warn(msg, kv...)
}

func (f *Followup) info(msg string, kv ...any) {
	if f == nil || f.Logger == nil {
		return
	}
	f.Logger.Info(msg, kv...)
}

// Compile-time guard that *Followup satisfies the QueueWorker.OnRecorded
// callback shape. Catches signature drift before runtime.
var _ func(context.Context, *store.AuditFinding) error = (&Followup{}).OnRecorded

// errInvalidIssuer is reserved for a future builder that surfaces
// configuration errors at construction time. Not used today; here as
// a placeholder so test files can reference it without a follow-up
// import churn when Followup gets a stricter constructor.
var errInvalidIssuer = errors.New("audit/followup: invalid Issuer configuration")

// _ keeps errInvalidIssuer referenced so `go vet` doesn't flag it as
// unused while the constructor stays simple.
var _ = errInvalidIssuer

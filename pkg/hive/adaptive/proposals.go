// Package adaptive holds the Sunday adaptive-policy job (Hive v2 Phase 7).
//
// The ProposalsBuilder reads kpi_snapshots, eval_scores, audit_findings, and
// gate_outcomes from the canonical store, applies a small set of deterministic
// rules to spot policy knobs that look too tight or too loose, and writes one
// or more PolicyProposal rows (state=pending) plus a human-facing markdown
// digest at .loom/hive/policy_proposals/<date>.md.
//
// v2.0 proposals are advisory — a human (or the v2.1 auto-applier) reads the
// markdown, picks one, and runs `loom hive policy apply`. The rules below are
// intentionally conservative so that "no proposals" is the steady-state output
// on a healthy week.
package adaptive

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	hiveeval "github.com/crb2nu/loom/pkg/hive/eval"
	"github.com/crb2nu/loom/pkg/hive/store"
)

const (
	// ProposalsWindow is how far back the builder looks. 14 days is the
	// shortest window that still captures multiple Sunday cron cycles +
	// enough pipeline merges for the medians to be stable.
	ProposalsWindow = 14 * 24 * time.Hour

	// MarkdownDir is the on-disk location for the human-facing digest. The
	// builder will create it relative to the configured DocsRoot. Made a
	// constant so tests can assert the exact filename.
	MarkdownDir = ".loom/hive/policy_proposals"

	// JudgedBy is the attribution stamped on emitted rationale (not used by
	// the DAO, but logged so retrospective audits can find the source).
	JudgedBy = "phase7_adaptive_v1"

	// --- Relax rule thresholds (all must be satisfied) -----------------
	// relaxMergeRateMin: 14d pipeline merge rate must be >= this for a
	// relax to fire. 0.95 keeps the rule from firing on weeks where any
	// pipeline failed for non-policy reasons (CI flake, etc.).
	relaxMergeRateMin = 0.95
	// relaxGateFailRateMax: <5% gate-fail rate window-wide.
	relaxGateFailRateMax = 0.05
	// relaxBumpFraction: the diff produced bumps the target +20%.
	relaxBumpFraction = 0.20

	// --- Tighten rule thresholds (any one fires) -----------------------
	// tightenMedianBelow: 14d median pipeline_outcome score below this
	// triggers a tighten.
	tightenMedianBelow = 0.70
	// tightenShrinkFraction: the diff produced cuts the target -20%.
	tightenShrinkFraction = 0.20

	// --- Rotate ensemble rule ------------------------------------------
	// rotateMedianMaxQuartile: ensemble's slot median must be at-or-below
	// the second quartile of the rolling distribution (i.e., bottom-half).
	rotateMedianMaxQuartile = 0.50

	// rotateMinSamples: minimum council_run rows in the window for the
	// rotate rule to consider firing — avoids flapping on a single bad
	// council run.
	rotateMinSamples = 4
)

// Inputs aggregates the four read streams the builder consumes. Exposed as
// a struct so tests can build minimal fixtures without touching the store.
//
// Actual production callers use BuildFromStore which fills these by querying
// the DAOs.
type Inputs struct {
	WindowStart time.Time
	WindowEnd   time.Time

	// Pipeline runs that started in the window (any state — terminal +
	// non-terminal, the rules count merge-vs-other separately).
	PipelineRuns []*store.PipelineRun

	// Gate outcomes within the window. The builder doesn't need the full
	// rows, only the (gate_name, outcome) tuple — but we keep the row
	// shape so future rules can cite specific runs.
	GateOutcomes []*store.GateOutcome

	// Eval scores in the window. Builder filters by Rubric/SubjectKind.
	EvalScores []*store.EvalScore

	// Audit findings in the window — the rule cares about severity counts.
	AuditFindings []*store.AuditFinding

	// CouncilRuns in the window — used to attribute eval scores to slots
	// (cron / roadmap / incident) for the rotate rule.
	CouncilRuns []*store.CouncilRun

	// CurrentMaxUSDPerRun is the policy.budgets.pipeline.max_usd_per_run
	// at the moment the builder runs. The relax/tighten diff strings cite
	// the absolute target so the operator can see the proposed value
	// without re-reading policy. Zero is acceptable — the rule then emits
	// a relative diff (e.g., "+20%").
	CurrentMaxUSDPerRun float64
}

// ProposalsBuilder runs the Sunday adaptive-policy rules. Stateless — safe to
// construct per-tick.
type ProposalsBuilder struct {
	Store *store.Store

	// DocsRoot is the workspace root where the markdown digest gets
	// written. Defaults to "." (current working directory). Set to
	// t.TempDir() in tests.
	DocsRoot string

	// CurrentMaxUSDPerRun mirrors Inputs.CurrentMaxUSDPerRun for the
	// store-backed BuildFromStore path. Unused when callers pass
	// pre-built Inputs.
	CurrentMaxUSDPerRun float64

	// Now is injected for deterministic tests. Defaults to time.Now.
	Now func() time.Time

	Logger *slog.Logger
}

// Result is the aggregate return from one Run. Returned for tests + scheduler
// logging. The canonical record is the policy_proposals rows.
type Result struct {
	WindowStart  time.Time
	WindowEnd    time.Time
	ProposalDate string
	Proposals    []*store.PolicyProposal
	MarkdownPath string
}

// Run is the production entry point: it pulls inputs from the store, applies
// the rules, persists proposals, and writes the markdown digest. Errors from
// individual checks don't abort the others — the job is best-effort.
func (b *ProposalsBuilder) Run(ctx context.Context) (Result, error) {
	if b == nil || b.Store == nil {
		return Result{}, fmt.Errorf("adaptive: store required")
	}
	now := b.now().UTC()
	in, err := b.collectInputs(ctx, now)
	if err != nil {
		return Result{}, fmt.Errorf("collect inputs: %w", err)
	}
	return b.runWithInputs(ctx, in)
}

// runWithInputs is the unit-test seam: callers pass deterministic Inputs and
// receive the same persistence + markdown side-effects as Run.
func (b *ProposalsBuilder) runWithInputs(ctx context.Context, in Inputs) (Result, error) {
	res := Result{
		WindowStart:  in.WindowStart,
		WindowEnd:    in.WindowEnd,
		ProposalDate: in.WindowEnd.Format("2006-01-02"),
	}

	if p := b.evalRelax(in); p != nil {
		res.Proposals = append(res.Proposals, p)
	}
	if p := b.evalTighten(in); p != nil {
		res.Proposals = append(res.Proposals, p)
	}
	res.Proposals = append(res.Proposals, b.evalRotate(in)...)

	for _, p := range res.Proposals {
		p.ProposalDate = res.ProposalDate
		if err := b.Store.PolicyProposals.Create(ctx, p); err != nil {
			b.warn("create proposal", err)
		}
	}

	if path, err := b.writeMarkdown(res); err != nil {
		b.warn("write markdown", err)
	} else {
		res.MarkdownPath = path
	}

	if b.Logger != nil {
		b.Logger.Info("adaptive proposals",
			"window", res.WindowStart.Format(time.RFC3339)+".."+res.WindowEnd.Format(time.RFC3339),
			"emitted", len(res.Proposals),
		)
	}
	return res, nil
}

// BuildFromStore is exposed so handlers and ad-hoc tooling can replay the rule
// set with the same inputs the scheduler uses, without persisting a row. It
// is unused inside this package today; kept on the public surface so the v2.1
// auto-applier can consume it.
func (b *ProposalsBuilder) BuildFromStore(ctx context.Context) (Inputs, []*store.PolicyProposal, error) {
	if b == nil || b.Store == nil {
		return Inputs{}, nil, fmt.Errorf("adaptive: store required")
	}
	now := b.now().UTC()
	in, err := b.collectInputs(ctx, now)
	if err != nil {
		return Inputs{}, nil, err
	}
	var props []*store.PolicyProposal
	if p := b.evalRelax(in); p != nil {
		props = append(props, p)
	}
	if p := b.evalTighten(in); p != nil {
		props = append(props, p)
	}
	props = append(props, b.evalRotate(in)...)
	return in, props, nil
}

func (b *ProposalsBuilder) collectInputs(ctx context.Context, now time.Time) (Inputs, error) {
	windowStart := now.Add(-ProposalsWindow)
	in := Inputs{
		WindowStart:         windowStart,
		WindowEnd:           now,
		CurrentMaxUSDPerRun: b.CurrentMaxUSDPerRun,
	}

	// Pipeline runs in window — walk every state because the rules want
	// merged-vs-other counts. O(100) rows per week; cheap.
	for _, st := range allPipelineStates() {
		runs, err := b.Store.Pipeline.ListByState(ctx, st)
		if err != nil {
			return in, fmt.Errorf("list pipeline state %s: %w", st, err)
		}
		for _, r := range runs {
			if r.StartedAt.Before(windowStart) {
				continue
			}
			in.PipelineRuns = append(in.PipelineRuns, r)
		}
	}

	// Gate outcomes — there's no time-window query on the DAO, so we
	// fan out by run id from the pipeline_runs we just collected.
	for _, r := range in.PipelineRuns {
		gates, err := b.Store.Pipeline.ListGates(ctx, r.ID)
		if err != nil {
			return in, fmt.Errorf("list gates for %s: %w", r.ID, err)
		}
		for _, g := range gates {
			if g.EvaluatedAt.Before(windowStart) {
				continue
			}
			in.GateOutcomes = append(in.GateOutcomes, g)
		}
	}

	// Eval scores — DAO has a window-aware helper, use it directly. The
	// 5000 limit is a safety cap; a healthy week stays well under that.
	scores, err := b.Store.Eval.ListSince(ctx, windowStart, 5000)
	if err != nil {
		return in, fmt.Errorf("list eval scores: %w", err)
	}
	in.EvalScores = scores

	// Audit findings — same shape.
	findings, err := b.Store.Audit.ListSince(ctx, windowStart, 5000)
	if err != nil {
		return in, fmt.Errorf("list audit findings: %w", err)
	}
	in.AuditFindings = findings

	// Council runs — needed to map council_run eval rows to a slot. The
	// council DAO doesn't expose a window-aware helper either; the
	// rotate rule is tolerant of missing context (it just won't rotate
	// without enough samples).
	runs, err := b.Store.Council.List(ctx, 200)
	if err != nil {
		return in, fmt.Errorf("list council runs: %w", err)
	}
	for _, r := range runs {
		if r.StartedAt.Before(windowStart) {
			continue
		}
		in.CouncilRuns = append(in.CouncilRuns, r)
	}

	return in, nil
}

// ----- Rules ---------------------------------------------------------------

// evalRelax fires when all three healthy-week conditions hold:
//  1. 14d pipeline merge rate >= relaxMergeRateMin (95%)
//  2. zero `critical` audit findings in window
//  3. gate-fail rate < relaxGateFailRateMax (5%)
//
// The proposal targets policy.budgets.pipeline.max_usd_per_run with a +20%
// bump. The diff is rendered as "X.XX -> Y.YY" when the current value is
// known, otherwise as a percentage diff.
func (b *ProposalsBuilder) evalRelax(in Inputs) *store.PolicyProposal {
	mergeRate, mergeN, total := pipelineMergeRate(in.PipelineRuns)
	if total == 0 || mergeRate < relaxMergeRateMin {
		return nil
	}
	criticals := countAuditSeverity(in.AuditFindings, store.AuditSeverityCritical)
	if criticals > 0 {
		return nil
	}
	gateFailRate, failN, gateTotal := gateFailRate(in.GateOutcomes)
	if gateTotal == 0 || gateFailRate >= relaxGateFailRateMax {
		return nil
	}

	target := "policy.budgets.pipeline.max_usd_per_run"
	diff := relaxBumpDiff(in.CurrentMaxUSDPerRun, relaxBumpFraction)
	rationale := fmt.Sprintf(`Healthy steady-state observed in window %s..%s:

- Pipeline merge rate: %.1f%% (%d merged of %d total runs)
- Critical audit findings: 0 (window-wide)
- Gate fail rate: %.1f%% (%d failed of %d evaluated)

Recommendation: bump %s by +%.0f%% to give pipeline runs more headroom — the
gates and audits show no reason for the current cap. Revert if any audit
finding goes critical or merge rate dips below %.0f%% next week.`,
		formatDate(in.WindowStart), formatDate(in.WindowEnd),
		mergeRate*100, mergeN, total,
		gateFailRate*100, failN, gateTotal,
		target, relaxBumpFraction*100,
		relaxMergeRateMin*100,
	)
	return &store.PolicyProposal{
		Kind:      store.PolicyProposalRelax,
		Target:    target,
		Diff:      diff,
		Rationale: rationale,
		State:     store.PolicyProposalPending,
	}
}

// evalTighten fires when either signal goes red:
//  1. 14d median pipeline_outcome score below tightenMedianBelow (0.70), or
//  2. >=1 critical audit finding in window.
//
// First condition emits a budget-shrink proposal; second emits an
// audit-advisory-off proposal so the audit gate becomes blocking.
func (b *ProposalsBuilder) evalTighten(in Inputs) *store.PolicyProposal {
	median, mSamples := pipelineOutcomeMedian(in.EvalScores)
	criticals := countAuditSeverity(in.AuditFindings, store.AuditSeverityCritical)

	medianRed := mSamples > 0 && median < tightenMedianBelow
	auditRed := criticals > 0
	if !medianRed && !auditRed {
		return nil
	}

	// Audit-criticals get priority — flipping advisory_only is a sharper
	// recovery than shaving the budget cap.
	if auditRed {
		target := "policy.audit.advisory_only"
		diff := "true -> false"
		rationale := fmt.Sprintf(`Critical audit findings observed in window %s..%s:

- Critical audit findings: %d
- Pipeline merge median (pipeline_outcome_v1): %s
- Median sample size: %d eval rows

Recommendation: flip %s to false so the audit gate becomes blocking. The
critical findings indicate the v2.0 advisory mode missed something that
should have stopped a merge.`,
			formatDate(in.WindowStart), formatDate(in.WindowEnd),
			criticals, formatScore(median, mSamples), mSamples,
			target,
		)
		return &store.PolicyProposal{
			Kind:      store.PolicyProposalTighten,
			Target:    target,
			Diff:      diff,
			Rationale: rationale,
			State:     store.PolicyProposalPending,
		}
	}

	// Median red -> shrink the budget cap.
	target := "policy.budgets.pipeline.max_usd_per_run"
	diff := tightenShrinkDiff(in.CurrentMaxUSDPerRun, tightenShrinkFraction)
	rationale := fmt.Sprintf(`Pipeline outcome median dropped below %.2f in window %s..%s:

- Pipeline outcome median (pipeline_outcome_v1): %.3f over %d eval rows
- Critical audit findings: %d

Recommendation: shrink %s by %.0f%% — pipelines that score below the median
threshold tend to spend more on retries; tightening the cap forces the
escalation path earlier.`,
		tightenMedianBelow,
		formatDate(in.WindowStart), formatDate(in.WindowEnd),
		median, mSamples,
		criticals,
		target, tightenShrinkFraction*100,
	)
	return &store.PolicyProposal{
		Kind:      store.PolicyProposalTighten,
		Target:    target,
		Diff:      diff,
		Rationale: rationale,
		State:     store.PolicyProposalPending,
	}
}

// evalRotate fires once per slot (cron / roadmap / incident) whose median
// council ROI score is at-or-below the second quartile of the
// rolling distribution. Returns 0..3 proposals.
func (b *ProposalsBuilder) evalRotate(in Inputs) []*store.PolicyProposal {
	// Map council_run subject_id -> trigger slot.
	slotByID := map[string]store.CouncilTrigger{}
	for _, cr := range in.CouncilRuns {
		slotByID[cr.ID] = cr.Trigger
	}

	// Gather council_roi scores per slot.
	bySlot := map[store.CouncilTrigger][]float64{}
	for _, sc := range in.EvalScores {
		if sc.SubjectKind != store.EvalSubjectCouncilRun {
			continue
		}
		if sc.Rubric != hiveeval.CouncilROIRubric {
			continue
		}
		slot, ok := slotByID[sc.SubjectID]
		if !ok {
			// No matching council_run in window — skip rather than guess.
			continue
		}
		bySlot[slot] = append(bySlot[slot], sc.Score)
	}

	if len(bySlot) == 0 {
		return nil
	}

	// Compute rolling distribution of all scores so the per-slot median
	// has something to compare against. The "2nd quartile" is the median
	// of the combined distribution; a slot whose own median is at-or-
	// below that line is in the bottom half.
	all := make([]float64, 0)
	for _, xs := range bySlot {
		all = append(all, xs...)
	}
	if len(all) < rotateMinSamples {
		return nil
	}
	rollingMedian := median(all)

	// Stable order: cron, roadmap, incident.
	slots := []store.CouncilTrigger{
		store.CouncilTriggerCron,
		store.CouncilTriggerRoadmap,
		store.CouncilTriggerIncident,
	}
	var out []*store.PolicyProposal
	for _, slot := range slots {
		xs, ok := bySlot[slot]
		if !ok || len(xs) == 0 {
			continue
		}
		slotMedian := median(xs)
		if slotMedian > rollingMedian*rotateMaxScale() {
			continue
		}
		// Slot median is in the bottom half — propose rotation.
		target := fmt.Sprintf("policy.council.ensembles.%s", slot)
		diff := fmt.Sprintf("rotate ensemble (slot=%s, median=%.3f <= rolling=%.3f)",
			slot, slotMedian, rollingMedian)
		rationale := fmt.Sprintf(`Council ROI median for slot %q is in the bottom half of the rolling distribution in window %s..%s:

- Slot %q council_roi_v1 median: %.3f over %d council runs
- Rolling council_roi_v1 median (all slots): %.3f over %d eval rows

Recommendation: rotate the ensemble for %s — its judges/reviewers are scoring
below the rolling median, suggesting the ensemble has drifted relative to
peers. Concrete swap-in candidates depend on FlexInfer availability; pick a
reviewer from a different model family.`,
			slot,
			formatDate(in.WindowStart), formatDate(in.WindowEnd),
			slot, slotMedian, len(xs),
			rollingMedian, len(all),
			slot,
		)
		out = append(out, &store.PolicyProposal{
			Kind:      store.PolicyProposalRotateEnsemble,
			Target:    target,
			Diff:      diff,
			Rationale: rationale,
			State:     store.PolicyProposalPending,
		})
	}
	return out
}

// rotateMaxScale is a tiny helper kept separate so future tuning (e.g.,
// "trigger when slot is below 90% of the rolling median") becomes a
// one-liner. v1 fires when the slot median is at-or-below the rolling
// median (rotateMedianMaxQuartile = 0.5 == "median == 2nd quartile").
func rotateMaxScale() float64 {
	// Convert quartile-style target into a multiplier on the rolling
	// median. Quartile 0.5 means "<= rolling median"; we keep the factor
	// at exactly 1.0 in v1 so the rule is dead-simple to reason about.
	if rotateMedianMaxQuartile <= 0 {
		return 0
	}
	return 1.0
}

// ----- Markdown digest -----------------------------------------------------

func (b *ProposalsBuilder) writeMarkdown(res Result) (string, error) {
	root := b.DocsRoot
	if root == "" {
		root = "."
	}
	dir := filepath.Join(root, MarkdownDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	name := res.ProposalDate + ".md"
	path := filepath.Join(dir, name)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Policy proposals — %s\n\n", res.ProposalDate)
	fmt.Fprintf(&sb, "_Window: %s .. %s_\n\n",
		formatDate(res.WindowStart), formatDate(res.WindowEnd))
	if len(res.Proposals) == 0 {
		sb.WriteString("_No proposals — steady state observed._\n")
	}
	for i, p := range res.Proposals {
		fmt.Fprintf(&sb, "## %d. %s — `%s`\n\n", i+1, titleCase(string(p.Kind)), p.Target)
		fmt.Fprintf(&sb, "**Diff:** `%s`\n\n", p.Diff)
		fmt.Fprintf(&sb, "**Rationale:**\n\n%s\n\n", p.Rationale)
	}

	if err := writeFileAtomic(path, []byte(sb.String()), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// writeFileAtomic writes data via a same-directory tempfile + rename. Mirrors
// pkg/skills/fileops.go (unexported there); keeping the duplicate inside this
// package avoids a fragile import path when the skill helper moves. See memory
// note "Atomic File Writes for Watched Files" for the upstream reasoning.
func writeFileAtomic(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".policy-proposal-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ----- Helpers -------------------------------------------------------------

func (b *ProposalsBuilder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *ProposalsBuilder) warn(msg string, err error) {
	if b.Logger != nil {
		b.Logger.Warn("adaptive: "+msg, "error", err)
	}
}

// pipelineMergeRate computes (rate, merged, total) over runs that ended in
// a terminal state. Non-terminal runs are excluded so an in-flight pipeline
// doesn't mask a regression.
func pipelineMergeRate(runs []*store.PipelineRun) (float64, int, int) {
	merged := 0
	total := 0
	for _, r := range runs {
		switch r.State {
		case store.PipelineDone:
			merged++
			total++
		case store.PipelineEscalated, store.PipelinePaused:
			total++
		default:
			// non-terminal — ignore
		}
	}
	if total == 0 {
		return 0, 0, 0
	}
	return float64(merged) / float64(total), merged, total
}

func gateFailRate(gates []*store.GateOutcome) (float64, int, int) {
	failN := 0
	total := 0
	for _, g := range gates {
		switch g.Outcome {
		case store.GateOutcomePass:
			total++
		case store.GateOutcomeFail:
			failN++
			total++
		case store.GateOutcomeSkip:
			// skipped gates aren't relevant — they didn't run
		}
	}
	if total == 0 {
		return 0, 0, 0
	}
	return float64(failN) / float64(total), failN, total
}

func countAuditSeverity(findings []*store.AuditFinding, sev store.AuditSeverity) int {
	n := 0
	for _, f := range findings {
		if f.Severity == sev {
			n++
		}
	}
	return n
}

// pipelineOutcomeMedian returns the median pipeline_outcome_v1 score across
// every EvalScore row in the window with that rubric.
func pipelineOutcomeMedian(scores []*store.EvalScore) (float64, int) {
	xs := make([]float64, 0)
	for _, sc := range scores {
		if sc.SubjectKind != store.EvalSubjectPipelineRun {
			continue
		}
		if sc.Rubric != hiveeval.PipelineOutcomeRubric {
			continue
		}
		xs = append(xs, sc.Score)
	}
	if len(xs) == 0 {
		return 0, 0
	}
	return median(xs), len(xs)
}

// median returns the middle element after sorting (interpolated for even N).
// Defensive against NaN/Inf — caller filters those out.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	cp := make([]float64, len(xs))
	copy(cp, xs)
	sort.Float64s(cp)
	n := len(cp)
	mid := n / 2
	if n%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}

func relaxBumpDiff(current, frac float64) string {
	if current <= 0 {
		return fmt.Sprintf("+%.0f%%", frac*100)
	}
	bumped := round2(current * (1 + frac))
	return fmt.Sprintf("%s -> %s", formatUSD(current), formatUSD(bumped))
}

func tightenShrinkDiff(current, frac float64) string {
	if current <= 0 {
		return fmt.Sprintf("-%.0f%%", frac*100)
	}
	shrunk := round2(current * (1 - frac))
	return fmt.Sprintf("%s -> %s", formatUSD(current), formatUSD(shrunk))
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func formatUSD(x float64) string { return fmt.Sprintf("$%.2f", x) }

func formatDate(t time.Time) string { return t.UTC().Format("2006-01-02") }

func formatScore(x float64, n int) string {
	if n == 0 {
		return "n/a (0 samples)"
	}
	return fmt.Sprintf("%.3f", x)
}

func allPipelineStates() []store.PipelineState {
	return []store.PipelineState{
		store.PipelineQueued, store.PipelinePlanning, store.PipelineSlicing,
		store.PipelineImplementing, store.PipelineTesting, store.PipelineReviewing,
		store.PipelineMR, store.PipelineCI, store.PipelineMerging,
		store.PipelineDone, store.PipelineEscalated, store.PipelinePaused,
	}
}

// titleCase upper-cases the first ASCII rune of s. Replacement for the
// deprecated strings.Title — the Kind enum values (relax/tighten/rotate_ensemble)
// are pure ASCII so unicode-correct casing is overkill.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
}

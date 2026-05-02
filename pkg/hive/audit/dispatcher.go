package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// EscalationLowerBound is the bulk-median threshold below which a Result
// is judged "clearly unsafe" without needing a frontier escalation pass.
// Mirrors spec §"Audit swarm flow" (escalation band [0.4, 0.7]).
const EscalationLowerBound = 0.40

// EscalationUpperBound is the bulk-median threshold above which a Result
// is judged "clearly safe" without escalating.
const EscalationUpperBound = 0.70

// SeverityCritical / SeverityWarn boundaries map a numeric survival
// score onto the categorical AuditSeverity stored on the row. The
// thresholds intentionally match the rubric's own banding so the prompt
// instructions and the dispatcher's classification stay aligned.
const (
	severityCriticalAt = 0.40 // < this → critical
	severityWarnAt     = 0.85 // < this → warn, ≥ this → info
)

// Dispatcher runs the configured reviewer pool concurrently, aggregates
// the results, and emits a persistable AuditFinding plus the per-member
// breakdown. Production wiring constructs one Dispatcher per operator
// boot; the rubric is loaded once and shared.
//
// The Dispatcher itself never persists. The caller (slice 3.3 triggers
// or slice 3.4 admin endpoint) decides whether to RecordFinding the
// returned Result and what to do with low-survival rows (slice 3.6
// follow-up issues).
type Dispatcher struct {
	// Reviewers is the registry mapping PoolMember.Backend →
	// implementation. Production callers register a "flexinfer" entry
	// backed by *clients.FlexInferClient (via FlexInferReviewer adapter)
	// and — in v2.1 — a "spawn" entry wrapping the headless spawn
	// controller. Unknown backends are skipped with a warn log; the
	// Result.SkippedMembers counter surfaces them.
	Reviewers map[string]Reviewer

	// Rubric is the prompt template set. Empty defaults to MustLoadRubric.
	Rubric *Rubric

	// Logger surfaces per-member parse failures, escalation triggers,
	// and reviewer errors. nil discards.
	Logger *slog.Logger

	// Clock is used for AuditFinding.CreatedAt; tests inject a fixed
	// time.
	Clock func() time.Time

	// LowerBand / UpperBand override the [0.40, 0.70] defaults for
	// tests that want to exercise escalation triggers without seeding
	// rubric outputs at exact boundaries.
	LowerBand float64
	UpperBand float64
}

// New returns a Dispatcher with sensible defaults. nil reviewers are
// permitted at construction; the caller registers them via
// dispatcher.Reviewers[backend] = impl before calling Run.
func New(reviewers map[string]Reviewer, r *Rubric) *Dispatcher {
	if reviewers == nil {
		reviewers = map[string]Reviewer{}
	}
	if r == nil {
		r = MustLoadRubric()
	}
	return &Dispatcher{
		Reviewers: reviewers,
		Rubric:    r,
		Clock:     time.Now,
		LowerBand: EscalationLowerBound,
		UpperBand: EscalationUpperBound,
	}
}

// Run executes one audit. The flow:
//  1. Validate the request.
//  2. Render the rubric prompt (council or pipeline).
//  3. Dispatch the bulk pool in parallel; collect MemberOutputs.
//  4. Compute the bulk median survival score.
//  5. If bulk median falls in [LowerBand, UpperBand] AND
//     EscalationPool is non-empty, dispatch the frontier pool too.
//  6. Aggregate findings, derive severity, build Result.
//
// Returns an error only for top-level failures (validation, rubric
// render). Per-member errors are folded into Members[i].ParseErr +
// Result.SkippedMembers so a degraded reviewer doesn't fail the audit.
func (d *Dispatcher) Run(ctx context.Context, req *Request) (*Result, error) {
	if d == nil {
		return nil, errors.New("audit: dispatcher nil")
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	rubric := d.Rubric
	if rubric == nil {
		rubric = MustLoadRubric()
	}
	prompt, err := rubric.Render(req, "")
	if err != nil {
		return nil, err
	}

	bulk, bulkSkipped := d.runPool(ctx, prompt, req.Pool)
	median := medianSurvival(bulk)
	cost := sumCost(bulk)

	escalated := false
	members := bulk
	skipped := bulkSkipped
	if d.shouldEscalate(median, len(req.EscalationPool)) {
		escalated = true
		extra, extraSkipped := d.runPool(ctx, prompt, req.EscalationPool)
		members = append(members, extra...)
		skipped += extraSkipped
		// Final score: max of bulk median and frontier median, mirroring
		// spec §"Audit swarm flow" #5 — frontier inference is the more
		// expensive opinion, so we let it overrule a borderline bulk
		// without dragging a low frontier score up via averaging.
		median = maxFloat(median, medianSurvival(extra))
		cost += sumCost(extra)
	}

	finding := &store.AuditFinding{
		SubjectKind:   req.SubjectKind,
		SubjectID:     req.SubjectID,
		Severity:      d.severity(median),
		RubricID:      rubric.ID(),
		SurvivalScore: median,
		Findings:      consolidateFindings(members),
		AuditorPool:   poolForRow(req.Pool, req.EscalationPool, escalated),
		CostUSD:       cost,
		CreatedAt:     d.now(),
	}
	res := &Result{
		Finding:        finding,
		Members:        members,
		Escalated:      escalated,
		SkippedMembers: skipped,
	}
	d.info("audit complete",
		"subject_kind", string(req.SubjectKind),
		"subject_id", req.SubjectID,
		"survival", median,
		"escalated", escalated,
		"members_run", len(members),
		"members_skipped", skipped,
	)
	return res, nil
}

// runPool dispatches every member in `pool` concurrently and gathers
// MemberOutputs. Members whose Backend is unregistered are folded into
// `skipped`; reviewer errors fold into the per-member ParseErr.
func (d *Dispatcher) runPool(ctx context.Context, prompt string, pool []PoolMember) ([]MemberOutput, int) {
	if len(pool) == 0 {
		return nil, 0
	}
	out := make([]MemberOutput, len(pool))
	var skipped int
	var skippedMu sync.Mutex
	var wg sync.WaitGroup
	for i, m := range pool {
		i, m := i, m
		reviewer, ok := d.Reviewers[m.Backend]
		if !ok || reviewer == nil {
			d.warn("audit: reviewer not registered", "backend", m.Backend, "model", m.Model)
			skippedMu.Lock()
			skipped++
			skippedMu.Unlock()
			out[i] = MemberOutput{Member: m, ParseErr: fmt.Errorf("audit: backend %q not registered", m.Backend)}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			content, cost, err := reviewer.Review(ctx, m.Model, prompt, m.MaxCostUSD)
			if err != nil {
				d.warn("audit: reviewer call failed",
					"backend", m.Backend, "model", m.Model, "error", err)
				out[i] = MemberOutput{Member: m, CostUSD: cost, ParseErr: err}
				return
			}
			score, findings, parseErr := parseRubricResponse(content)
			out[i] = MemberOutput{
				Member:        m,
				SurvivalScore: score,
				Findings:      findings,
				CostUSD:       cost,
				Raw:           content,
				ParseErr:      parseErr,
			}
		}()
	}
	wg.Wait()
	return out, skipped
}

// shouldEscalate reports whether the bulk median lands in the
// configured escalation band AND we have a frontier pool to call. Both
// conditions must hold — an empty escalation pool means "trust the
// bulk", not "escalate to nobody".
func (d *Dispatcher) shouldEscalate(bulkMedian float64, escalationSize int) bool {
	if escalationSize == 0 {
		return false
	}
	low := d.LowerBand
	if low <= 0 {
		low = EscalationLowerBound
	}
	high := d.UpperBand
	if high <= 0 {
		high = EscalationUpperBound
	}
	return bulkMedian > low && bulkMedian < high
}

// severity maps a numeric survival score onto the categorical
// AuditSeverity column. Boundaries match the rubric's own banding.
func (d *Dispatcher) severity(score float64) store.AuditSeverity {
	switch {
	case score < severityCriticalAt:
		return store.AuditSeverityCritical
	case score < severityWarnAt:
		return store.AuditSeverityWarn
	default:
		return store.AuditSeverityInfo
	}
}

func (d *Dispatcher) now() time.Time {
	if d.Clock != nil {
		return d.Clock()
	}
	return time.Now().UTC()
}

func (d *Dispatcher) warn(msg string, kv ...any) {
	if d == nil || d.Logger == nil {
		return
	}
	d.Logger.Warn(msg, kv...)
}

func (d *Dispatcher) info(msg string, kv ...any) {
	if d == nil || d.Logger == nil {
		return
	}
	d.Logger.Info(msg, kv...)
}

// parseRubricResponse decodes the auditor's JSON output. Be tolerant:
// some models wrap JSON in markdown fences or trailing prose. We extract
// the first balanced JSON object and parse it; on failure, return the
// "artifact unreadable" critical-severity fallback so a corrupt
// reviewer can't be mistaken for a clean pass.
func parseRubricResponse(content string) (float64, []FindingItem, error) {
	body := extractFirstJSON(content)
	if body == "" {
		return 0, nil, errors.New("audit: no JSON object in response")
	}
	var parsed struct {
		SurvivalScore float64 `json:"survival_score"`
		Severity      string  `json:"severity"`
		Findings      []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			Severity string `json:"severity"`
			Detail   string `json:"detail"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return 0, nil, fmt.Errorf("audit: parse rubric response: %w", err)
	}
	score := parsed.SurvivalScore
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	out := make([]FindingItem, 0, len(parsed.Findings))
	for _, f := range parsed.Findings {
		out = append(out, FindingItem{
			ID:       f.ID,
			Title:    f.Title,
			Severity: store.AuditSeverity(strings.ToLower(strings.TrimSpace(f.Severity))),
			Detail:   f.Detail,
		})
	}
	return score, out, nil
}

// extractFirstJSON returns the first balanced top-level JSON object in
// `s` or "" if none is present. Tolerant of markdown fences and prose
// before/after the object.
func extractFirstJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// medianSurvival returns the median survival_score across MemberOutputs
// that returned without a parse error. Empty input → 0 (treated as
// critical, which is the right default when nobody could score).
func medianSurvival(members []MemberOutput) float64 {
	scores := make([]float64, 0, len(members))
	for _, m := range members {
		if m.ParseErr != nil {
			continue
		}
		scores = append(scores, m.SurvivalScore)
	}
	if len(scores) == 0 {
		return 0
	}
	sort.Float64s(scores)
	n := len(scores)
	if n%2 == 1 {
		return scores[n/2]
	}
	return (scores[n/2-1] + scores[n/2]) / 2
}

// sumCost adds CostUSD across every member, including those that errored
// (a reviewer that errored may still have charged tokens).
func sumCost(members []MemberOutput) float64 {
	var total float64
	for _, m := range members {
		total += m.CostUSD
	}
	return total
}

// consolidateFindings deduplicates findings across the pool by Title.
// When two members surface the same Title, the higher-severity entry
// wins; ties are broken by detail length (favoring the more informative
// note).
func consolidateFindings(members []MemberOutput) []map[string]any {
	seen := map[string]FindingItem{}
	for _, m := range members {
		if m.ParseErr != nil {
			continue
		}
		for _, f := range m.Findings {
			key := strings.ToLower(strings.TrimSpace(f.Title))
			if key == "" {
				continue
			}
			cur, ok := seen[key]
			if !ok {
				seen[key] = f
				continue
			}
			if severityRank(f.Severity) > severityRank(cur.Severity) ||
				(severityRank(f.Severity) == severityRank(cur.Severity) && len(f.Detail) > len(cur.Detail)) {
				seen[key] = f
			}
		}
	}
	out := make([]map[string]any, 0, len(seen))
	for _, f := range seen {
		out = append(out, map[string]any{
			"id":       f.ID,
			"title":    f.Title,
			"severity": string(f.Severity),
			"detail":   f.Detail,
		})
	}
	// Stable order: severity desc, title asc.
	sort.Slice(out, func(i, j int) bool {
		si := severityRank(store.AuditSeverity(out[i]["severity"].(string)))
		sj := severityRank(store.AuditSeverity(out[j]["severity"].(string)))
		if si != sj {
			return si > sj
		}
		ti, _ := out[i]["title"].(string)
		tj, _ := out[j]["title"].(string)
		return ti < tj
	})
	return out
}

// severityRank yields a comparable integer for an AuditSeverity. Unknown
// values rank below "info" so a typo doesn't accidentally outrank a
// valid finding.
func severityRank(s store.AuditSeverity) int {
	switch s {
	case store.AuditSeverityCritical:
		return 3
	case store.AuditSeverityWarn:
		return 2
	case store.AuditSeverityInfo:
		return 1
	default:
		return 0
	}
}

// poolForRow encodes the pool that ran into the persisted JSON column
// (auditor_pool). When the run escalated, the EscalationPool entries
// are flagged so the operator's audit trail records WHY the second
// opinion was requested.
func poolForRow(bulk, escalation []PoolMember, escalated bool) []map[string]any {
	out := make([]map[string]any, 0, len(bulk)+len(escalation))
	for _, m := range bulk {
		out = append(out, map[string]any{
			"backend": m.Backend,
			"model":   m.Model,
			"role":    "bulk",
		})
	}
	if escalated {
		for _, m := range escalation {
			out = append(out, map[string]any{
				"backend": m.Backend,
				"model":   m.Model,
				"role":    "escalation",
			})
		}
	}
	return out
}

// maxFloat returns the larger of a, b. Tiny utility kept here so
// dispatcher.go doesn't pull math just for one Max call.
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

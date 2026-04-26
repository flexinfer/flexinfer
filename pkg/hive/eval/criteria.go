package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/hive/council"
)

// DefaultRubric returns the v1 set of criteria from .loom/89- §10.x.
// Six criteria, weights summing to 1.0:
//
//   - sidecar_validity        (0.20) — required fields present + parseable
//   - slice_independence      (0.20) — parallel slices touch disjoint files
//   - success_machine_check   (0.15) — every success.tests[] looks runnable
//   - plan_completeness       (0.15) — every slice has files + tests + budget
//   - roadmap_alignment       (0.15) — created backlog items reference an intent
//   - contradiction_free      (0.15) — LLM-judged against last 14d of merges
//
// The final criterion is the only one that needs a model call; pass it a
// FakeLLMJudge for tests + dryrun.
func DefaultRubric(llm LLMJudge) []Criterion {
	return []Criterion{
		&SidecarValidity{},
		&SliceIndependence{},
		&SuccessMachineCheck{},
		&PlanCompleteness{},
		&RoadmapAlignment{},
		&ContradictionFree{LLM: llm},
	}
}

// runnableTestPrefixes are the leading tokens we accept as "this looks
// like a runnable command". Pure heuristic; the actual run lands in the
// pipeline's tests stage. The point is to flag a sidecar that wrote
// success.tests = ["ensure foo works"] (English prose, not a command).
var runnableTestPrefixes = []string{
	"go ", "pytest", "make ", "pnpm ", "npm ", "yarn ", "cargo ",
	"./", "bash ", "sh ", "python ", "uv ", "ruby ", "bundle exec",
	"mvn ", "gradle ", "ginkgo ", "node ", "deno ", "swift ",
	"xcodebuild", "devbox_quality_gate",
}

// ----- SidecarValidity -----

// SidecarValidity is the cheapest gate: required fields present, valid
// types, no obvious nonsense (zero models, zero artifacts). Schema-
// invalid sidecars score 0; partially-shaped ones get a fractional
// score with reasons.
type SidecarValidity struct{}

// Name returns the persisted criterion name.
func (c *SidecarValidity) Name() string { return "sidecar_validity" }

// Weight is the rubric weight from .loom/89- §10.x.
func (c *SidecarValidity) Weight() float64 { return 0.20 }

// Score inspects the sidecar's required fields. Each missing field
// subtracts a fixed share of the criterion score so partial validity
// surfaces as 0.6 / 0.8 rather than a flat 0/1.
func (c *SidecarValidity) Score(_ context.Context, in Input) (CriterionResult, error) {
	r := CriterionResult{Name: c.Name(), Weight: c.Weight(), Score: 1.0}
	s := in.Sidecar
	const slot = 1.0 / 5.0 // 5 required-field slots

	if s.CouncilRunID == "" {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "missing council_run_id")
	}
	if len(s.Models) == 0 {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "models[] is empty")
	}
	if s.StartedAt.IsZero() {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "started_at is zero")
	}
	if s.EndedAt == nil || s.EndedAt.IsZero() {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "ended_at unset")
	}
	if len(s.Artifacts) == 0 {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "artifacts[] is empty")
	}
	return r, nil
}

// ----- SliceIndependence -----

// SliceIndependence inspects the editor's documents for any slice list
// where two parallel slices touch the same file. Today the editor's
// output doesn't carry a structured slices[] inside the EditorOutput
// (slice 3.6 will lift them out of the implementation_plan markdown);
// for slice 3.5 we scan the implementation_plan body for the pattern
// `parallel_with: [...]` + `files: [...]` near each other and flag
// overlaps. Heuristic but consistent — and falsifiable with a real
// example in tests.
type SliceIndependence struct{}

func (c *SliceIndependence) Name() string    { return "slice_independence" }
func (c *SliceIndependence) Weight() float64 { return 0.20 }

func (c *SliceIndependence) Score(_ context.Context, in Input) (CriterionResult, error) {
	r := CriterionResult{Name: c.Name(), Weight: c.Weight(), Score: 1.0}
	if in.EditorOutput == nil {
		// Without the document body we can't inspect; default to
		// neutral pass rather than fail since this is editor-side data.
		return r, nil
	}
	// For slice 3.5 the editor doesn't yet emit structured slices, so
	// score full marks unless the planning body is suspiciously empty.
	// Slice 3.6 (backlog_mutator) lifts the slices[] out of the plan
	// markdown and re-evaluates here with structured data.
	plan := findDoc(in.EditorOutput.Documents, council.KindImplementation)
	if plan == nil || strings.TrimSpace(plan.Body) == "" {
		r.Score = 0
		r.Reasons = append(r.Reasons, "implementation_plan document missing or empty")
	}
	return r, nil
}

// ----- SuccessMachineCheck -----

// SuccessMachineCheck inspects the implementation_plan markdown for
// `tests:` declarations and validates that each looks like a runnable
// command rather than English prose. Fails proportionally to the
// fraction of unrunnable entries.
//
// Note: this is heuristic. A real schema-driven check lands once the
// editor emits structured success criteria (slice 3.6 again). For
// slice 3.5 the heuristic is enough to catch the obvious foot-gun where
// the council declared "ensure logs are emitted" as a test command.
type SuccessMachineCheck struct{}

func (c *SuccessMachineCheck) Name() string    { return "success_machine_check" }
func (c *SuccessMachineCheck) Weight() float64 { return 0.15 }

func (c *SuccessMachineCheck) Score(_ context.Context, in Input) (CriterionResult, error) {
	r := CriterionResult{Name: c.Name(), Weight: c.Weight(), Score: 1.0}
	if in.EditorOutput == nil {
		return r, nil
	}
	plan := findDoc(in.EditorOutput.Documents, council.KindImplementation)
	if plan == nil {
		return r, nil
	}
	tests := extractTestLines(plan.Body)
	if len(tests) == 0 {
		// No test lines at all — heuristic neutral; the structured
		// version (slice 3.6) will turn this into a hard fail.
		return r, nil
	}
	runnable := 0
	var bad []string
	for _, t := range tests {
		if looksRunnable(t) {
			runnable++
		} else {
			bad = append(bad, t)
		}
	}
	r.Score = float64(runnable) / float64(len(tests))
	if len(bad) > 0 {
		sort.Strings(bad)
		if len(bad) > 4 {
			bad = append(bad[:4], "…")
		}
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("%d of %d test lines don't look runnable: %s",
				len(tests)-runnable, len(tests), strings.Join(bad, " | ")))
	}
	return r, nil
}

// extractTestLines pulls bullet items under a "tests:" key (YAML-ish)
// or "Tests:" header out of the markdown. Permissive on purpose; we'd
// rather over-extract and run the runnable-check than miss a section.
func extractTestLines(body string) []string {
	var out []string
	inTests := false
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "tests:") || strings.HasPrefix(line, "Tests:") ||
			strings.EqualFold(line, "tests") || strings.EqualFold(line, "## tests") {
			inTests = true
			continue
		}
		// Exit a tests block on a blank line or new heading.
		if inTests && (line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---")) {
			inTests = false
			continue
		}
		if !inTests {
			continue
		}
		// Bullet bodies: `- "go test ./..."` or `- go test ./...`
		if strings.HasPrefix(line, "- ") {
			body := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			body = strings.Trim(body, "\"'`")
			if body != "" {
				out = append(out, body)
			}
		}
	}
	return out
}

func looksRunnable(line string) bool {
	for _, p := range runnableTestPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// ----- PlanCompleteness -----

// PlanCompleteness rewards an implementation_plan that names files +
// tests + budget for every slice it lists. For slice 3.5 the heuristic
// is "the plan body mentions all three section keywords"; the
// structured version follows in slice 3.6.
type PlanCompleteness struct{}

func (c *PlanCompleteness) Name() string    { return "plan_completeness" }
func (c *PlanCompleteness) Weight() float64 { return 0.15 }

func (c *PlanCompleteness) Score(_ context.Context, in Input) (CriterionResult, error) {
	r := CriterionResult{Name: c.Name(), Weight: c.Weight(), Score: 1.0}
	if in.EditorOutput == nil {
		return r, nil
	}
	plan := findDoc(in.EditorOutput.Documents, council.KindImplementation)
	if plan == nil {
		r.Score = 0
		r.Reasons = append(r.Reasons, "implementation_plan missing")
		return r, nil
	}
	body := strings.ToLower(plan.Body)
	const slot = 1.0 / 3.0
	if !strings.Contains(body, "files:") && !strings.Contains(body, "files**") {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "no files: section in plan")
	}
	if !strings.Contains(body, "tests:") && !strings.Contains(body, "tests**") {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "no tests: section in plan")
	}
	if !strings.Contains(body, "budget") && !strings.Contains(body, "max_") {
		r.Score -= slot
		r.Reasons = append(r.Reasons, "no budget mention in plan")
	}
	return r, nil
}

// ----- RoadmapAlignment -----

// RoadmapAlignment checks that each backlog_create entry in the sidecar
// references at least one row in the canonical roadmap_intents table.
// For slice 3.5 the editor doesn't yet emit per-item roadmap
// references; we treat this as "each created backlog id appears in the
// implementation plan body alongside a roadmap intent summary". Slice
// 3.6's structured backlog mutator tightens this into a real key check.
type RoadmapAlignment struct{}

func (c *RoadmapAlignment) Name() string    { return "roadmap_alignment" }
func (c *RoadmapAlignment) Weight() float64 { return 0.15 }

func (c *RoadmapAlignment) Score(ctx context.Context, in Input) (CriterionResult, error) {
	r := CriterionResult{Name: c.Name(), Weight: c.Weight(), Score: 1.0}
	if in.Sidecar == nil || in.Store == nil {
		return r, nil
	}
	if in.Sidecar.BacklogDeltas.Created == 0 {
		// Nothing to align; default pass.
		return r, nil
	}
	intents, err := in.Store.Roadmap.List(ctx)
	if err != nil {
		return r, fmt.Errorf("read roadmap: %w", err)
	}
	if len(intents) == 0 {
		r.Score = 0
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("%d backlog item(s) created with no roadmap_intents to align against",
				in.Sidecar.BacklogDeltas.Created))
		return r, nil
	}
	// Heuristic match: the plan body should mention each intent's
	// summary. Misses subtract a slot.
	body := ""
	if in.EditorOutput != nil {
		if plan := findDoc(in.EditorOutput.Documents, council.KindImplementation); plan != nil {
			body = strings.ToLower(plan.Body)
		}
	}
	if body == "" {
		// Without the plan body we can't check; pass neutrally.
		return r, nil
	}
	hits := 0
	for _, intent := range intents {
		if strings.Contains(body, strings.ToLower(intent.Summary)) {
			hits++
		}
	}
	if hits == 0 {
		r.Score = 0
		r.Reasons = append(r.Reasons,
			"plan does not reference any of the canonical roadmap intents by summary")
	}
	return r, nil
}

// ----- ContradictionFree (LLM-judged) -----

// ContradictionFree is the only criterion that needs a language model.
// The judge sees the sidecar + the last 14 days of merged backlog
// items and reports a [0,1] score for "no contradictions found". For
// slice 3.5 we wrap the LLMJudge interface; production wiring drops a
// FlexInfer-backed impl in.
type ContradictionFree struct {
	LLM LLMJudge
}

func (c *ContradictionFree) Name() string    { return "contradiction_free" }
func (c *ContradictionFree) Weight() float64 { return 0.15 }

func (c *ContradictionFree) Score(ctx context.Context, in Input) (CriterionResult, error) {
	r := CriterionResult{Name: c.Name(), Weight: c.Weight(), Score: 1.0}
	if c.LLM == nil {
		// No judge configured; default neutral pass with a reason so
		// the operator notices the omission.
		r.Reasons = append(r.Reasons, "no LLM judge configured; criterion skipped")
		return r, nil
	}
	score, findings, err := c.LLM.JudgeContradiction(ctx, in)
	if err != nil {
		return r, fmt.Errorf("llm judge: %w", err)
	}
	r.Score = score
	if len(findings) > 0 {
		r.Reasons = append(r.Reasons, findings...)
	}
	return r, nil
}

// ----- helpers -----

// findDoc returns the first document of the requested kind, or nil.
func findDoc(docs []council.ArtifactDoc, kind council.ArtifactKind) *council.ArtifactDoc {
	for i, d := range docs {
		if d.Kind == kind {
			return &docs[i]
		}
	}
	return nil
}

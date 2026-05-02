package squads

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/crb2nu/loom/pkg/hive/store"
)

//go:embed prompts/squad_planner.md
var defaultPlannerPrompt string

// PlanInput is everything the planner needs to refine a backlog item under
// the chosen squad's conventions and recent working memory. The reconciler
// builds this immediately after Router.Pick succeeds and hands the result
// off to the Pipeline runner.
type PlanInput struct {
	// Item is the canonical backlog item to refine. Must be non-nil; its
	// Slices + Budget are treated as the Council's starting proposal.
	Item *store.BacklogItem

	// Squad is the manifest the router selected. Must be non-nil; the
	// planner extracts paths, tests, gates, and conventions from it.
	Squad *Manifest

	// Memory is the squad's working-memory snapshot, already sorted by
	// importance descending. The planner truncates to a sensible cap so
	// the prompt stays under the spawn driver's context budget.
	Memory []*store.SquadMemory

	// Sidecar carries the raw Council sidecar (compact JSON in the
	// prompt). Pass-through only — the planner does not interpret it.
	Sidecar map[string]any
}

// PlanOutput is the refined plan the Pipeline runner consumes. Slices,
// Gates, and Budget are normalized — even on parse failure callers receive
// a well-formed structure (the Council's slices + the squad's manifest
// defaults) so the dispatcher never crashes on a bad model output.
type PlanOutput struct {
	// Slices is the refined slice list. Equal to Item.Slices on parse
	// failure. Files outside the squad's path scope are filtered out.
	Slices []store.Slice

	// Gates is the squad's gate policy as the planner refined it.
	Gates GateSpec

	// Budget is the per-item budget the spawner echoed back, falling
	// back to Item.Budget on parse failure.
	Budget store.Budget

	// Notes is a markdown rationale persisted to events log + the HUD's
	// routing trace. May include parse-error context on failure paths.
	Notes string

	// CostUSD echoes the spawner's reported cost so the budget enforcer
	// can charge the right tier (frontier vs. local).
	CostUSD float64
}

// GateSpec mirrors the squad manifest's `spec.gates` shape with `required`
// and `advisory` buckets resolved into a typed pair.
type GateSpec struct {
	Required []string
	Advisory []string
}

// Spawner is the contract the planner depends on. Production wiring
// satisfies it with HUDSpawnClient or FlexInferClient adapters in a
// follow-up reconciler-integration slice. Tests use a fake.
type Spawner interface {
	PlanSlices(ctx context.Context, prompt string, options SpawnerOptions) (SpawnerResult, error)
}

// SpawnerOptions tunes one PlanSlices invocation. Driver + cost cap are
// derived from the squad's manifest editor block.
type SpawnerOptions struct {
	// Driver names the spawn agent_type or model to dispatch (e.g.
	// "claude-opus" lifted from manifest's editor.driver).
	Driver string

	// MaxCostUSD bounds spend on this single planning call. Lifted from
	// the manifest's editor.max_cost_usd.
	MaxCostUSD float64

	// Project is the spawn target project (typically the loom-core
	// repo). Reconciler integration fills this in.
	Project string

	// Namespace is the spawn worktree namespace.
	Namespace string
}

// SpawnerResult is the model's structured output. JSONBody is the raw
// model JSON; the planner parses it (defensively) into PlanOutput. CostUSD
// is what the spawner actually billed.
type SpawnerResult struct {
	JSONBody string
	CostUSD  float64
}

// Planner refines a Council-emitted backlog item under a squad's local
// conventions. It is stateless beyond the prompt template; one Planner
// instance is safe for concurrent use.
type Planner struct {
	tmpl    *template.Template
	spawner Spawner
}

// NewPlanner returns a planner using the embedded default prompt template.
// Callers wishing to override the template can use NewPlannerWithTemplate.
func NewPlanner(spawner Spawner) (*Planner, error) {
	return NewPlannerWithTemplate(spawner, defaultPlannerPrompt)
}

// NewPlannerWithTemplate is the override constructor — useful for tests
// that want a tiny prompt or for operators experimenting with planner
// prompt variants without rebuilding the binary.
func NewPlannerWithTemplate(spawner Spawner, tmplBody string) (*Planner, error) {
	if spawner == nil {
		return nil, errors.New("squads/planner: spawner required")
	}
	if strings.TrimSpace(tmplBody) == "" {
		return nil, errors.New("squads/planner: prompt template required")
	}
	t, err := template.New("squad_planner").Parse(tmplBody)
	if err != nil {
		return nil, fmt.Errorf("squads/planner: parse template: %w", err)
	}
	return &Planner{tmpl: t, spawner: spawner}, nil
}

// Plan refines `in` and returns a PlanOutput the Pipeline runner can hand
// to its dispatcher. It never returns a partial output: on parse failure
// the manifest defaults take over and `Notes` records the failure.
func (p *Planner) Plan(ctx context.Context, in PlanInput) (PlanOutput, error) {
	if p == nil {
		return PlanOutput{}, errors.New("squads/planner: nil planner")
	}
	if err := validatePlanInput(in); err != nil {
		return PlanOutput{}, err
	}

	prompt, err := p.renderPrompt(in)
	if err != nil {
		return PlanOutput{}, fmt.Errorf("squads/planner: render prompt: %w", err)
	}

	driver, maxCost := extractEditorDriver(in.Squad)
	res, err := p.spawner.PlanSlices(ctx, prompt, SpawnerOptions{
		Driver:     driver,
		MaxCostUSD: maxCost,
	})
	if err != nil {
		return PlanOutput{}, fmt.Errorf("squads/planner: spawner: %w", err)
	}

	// Always succeed at producing a structured PlanOutput, even when the
	// model output is unparseable. The fallback path keeps the pipeline
	// progressing rather than hard-failing on a one-off bad LLM turn.
	out := parsePlanOutput(res.JSONBody, in)
	out.CostUSD = res.CostUSD

	// Filter slices that touch files outside the squad's path scope.
	out.Slices, out.Notes = enforcePathScope(out.Slices, in.Squad, out.Notes)
	return out, nil
}

func validatePlanInput(in PlanInput) error {
	if in.Item == nil {
		return errors.New("squads/planner: PlanInput.Item required")
	}
	if in.Squad == nil {
		return errors.New("squads/planner: PlanInput.Squad required")
	}
	return nil
}

// renderPrompt fills the template's placeholder tags using values lifted
// out of the manifest + the input. The template keys are stable, ASCII,
// and PascalCased so the prompt asset stays grep-friendly.
func (p *Planner) renderPrompt(in PlanInput) (string, error) {
	required, advisory := manifestGates(in.Squad)
	conventions := manifestConventions(in.Squad)

	sidecarBytes, _ := json.Marshal(safeMap(in.Sidecar))
	sliceBytes, _ := json.Marshal(in.Item.Slices)

	data := map[string]any{
		"SquadName":     in.Squad.Metadata.Name,
		"Paths":         strings.Join(in.Squad.Spec.Paths, "\n"),
		"Tests":         strings.Join(in.Squad.Spec.Tests, "\n"),
		"RequiredGates": strings.Join(required, ", "),
		"AdvisoryGates": strings.Join(advisory, ", "),
		"Conventions":   conventions,
		"Memory":        renderMemory(in.Memory),
		"ItemTitle":     in.Item.Title,
		"SpecDoc":       in.Item.SpecDoc,
		"Priority":      string(in.Item.Priority),
		"SidecarSlices": string(sliceBytes),
		"SidecarJSON":   string(sidecarBytes),
	}
	var buf bytes.Buffer
	if err := p.tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// renderMemory turns the typed memory slice into a markdown bullet list.
// Empty input yields a "(no working memory)" placeholder so the prompt
// renders cleanly on a fresh squad.
func renderMemory(memory []*store.SquadMemory) string {
	if len(memory) == 0 {
		return "_(no working memory entries)_"
	}
	var b strings.Builder
	for _, m := range memory {
		if m == nil {
			continue
		}
		fmt.Fprintf(&b, "- **%s** (importance %.2f, kind=%s): %s\n",
			m.Title, m.Importance, m.Kind, oneLine(m.Body))
	}
	if b.Len() == 0 {
		return "_(no working memory entries)_"
	}
	return strings.TrimRight(b.String(), "\n")
}

// oneLine collapses whitespace + line breaks for prompt-friendly memory
// rendering. Long bodies stay readable as single bullets.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 280 {
		s = s[:277] + "..."
	}
	return s
}

func manifestGates(m *Manifest) (required, advisory []string) {
	if m == nil {
		return nil, nil
	}
	required = append(required, m.Spec.Gates["required"]...)
	advisory = append(advisory, m.Spec.Gates["advisory"]...)
	return required, advisory
}

// manifestConventions extracts free-form convention notes from the
// manifest's ensemble block. The on-disk schema does not have an explicit
// "conventions" field today, so this function looks at well-known shapes
// (ensemble.editor.notes, ensemble.conventions) and falls back to a
// terse summary so the prompt always renders.
func manifestConventions(m *Manifest) string {
	if m == nil {
		return ""
	}
	if c, ok := m.Spec.Ensemble["conventions"].(string); ok && strings.TrimSpace(c) != "" {
		return c
	}
	editor, _ := m.Spec.Ensemble["editor"].(map[string]any)
	if editor != nil {
		if n, ok := editor["notes"].(string); ok && strings.TrimSpace(n) != "" {
			return n
		}
	}
	if len(m.Spec.Tests) > 0 {
		return fmt.Sprintf("Run tests: %s. Recursion %s.",
			strings.Join(m.Spec.Tests, ", "),
			recursionLabel(m.Spec.RecursionEnabled))
	}
	return fmt.Sprintf("Recursion %s.", recursionLabel(m.Spec.RecursionEnabled))
}

func recursionLabel(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}

// extractEditorDriver lifts the editor driver + cost cap out of the
// manifest's ensemble block. Empty values are returned when the manifest
// does not specify an editor — the spawner is expected to apply its own
// default.
func extractEditorDriver(m *Manifest) (driver string, maxCost float64) {
	if m == nil {
		return "", 0
	}
	editor, _ := m.Spec.Ensemble["editor"].(map[string]any)
	if editor == nil {
		return "", 0
	}
	if d, ok := editor["driver"].(string); ok {
		driver = d
	}
	switch v := editor["max_cost_usd"].(type) {
	case float64:
		maxCost = v
	case int:
		maxCost = float64(v)
	case int64:
		maxCost = float64(v)
	}
	return driver, maxCost
}

// modelPlan mirrors the JSON contract documented in
// prompts/squad_planner.md. Any field may be missing; parsePlanOutput
// composes a fallback from the input when so.
type modelPlan struct {
	Slices []store.Slice `json:"slices"`
	Gates  *struct {
		Required []string `json:"required"`
		Advisory []string `json:"advisory"`
	} `json:"gates"`
	Budget *store.Budget `json:"budget"`
	Notes  string        `json:"notes"`
}

// parsePlanOutput is the defensive parser. On any failure the function
// returns a PlanOutput equal to the manifest defaults + the original
// item slices, with `Notes` carrying the parse-failure summary. CostUSD
// is filled by the caller (the planner echoes the spawner's value back
// so the budget enforcer charges the right tier).
func parsePlanOutput(body string, in PlanInput) PlanOutput {
	required, advisory := manifestGates(in.Squad)
	fallback := PlanOutput{
		Slices: cloneSlices(in.Item.Slices),
		Gates:  GateSpec{Required: required, Advisory: advisory},
		Budget: in.Item.Budget,
	}

	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		fallback.Notes = "planner fallback: empty model output; using manifest defaults"
		return fallback
	}

	var plan modelPlan
	if err := json.Unmarshal([]byte(trimmed), &plan); err != nil {
		fallback.Notes = fmt.Sprintf(
			"planner fallback: parse error %q; using manifest defaults",
			err.Error())
		return fallback
	}

	out := PlanOutput{Notes: plan.Notes}
	if len(plan.Slices) > 0 {
		out.Slices = plan.Slices
	} else {
		out.Slices = cloneSlices(in.Item.Slices)
	}
	if plan.Gates != nil && (len(plan.Gates.Required) > 0 || len(plan.Gates.Advisory) > 0) {
		out.Gates = GateSpec{
			Required: append([]string(nil), plan.Gates.Required...),
			Advisory: append([]string(nil), plan.Gates.Advisory...),
		}
	} else {
		out.Gates = GateSpec{Required: required, Advisory: advisory}
	}
	if plan.Budget != nil {
		out.Budget = *plan.Budget
	} else {
		out.Budget = in.Item.Budget
	}
	return out
}

// enforcePathScope drops slices whose files fall outside the squad's
// declared path globs and appends a one-liner to Notes when at least one
// slice or file was filtered. The original slice survives if at least one
// file remains in scope.
func enforcePathScope(slices []store.Slice, m *Manifest, notes string) ([]store.Slice, string) {
	if m == nil || len(m.Spec.Paths) == 0 {
		return slices, notes
	}
	var (
		kept             []store.Slice
		droppedSlices    []string
		droppedFileCount int
	)
	for _, s := range slices {
		var inScope []string
		for _, f := range s.Files {
			if m.MatchesPath(f) != "" {
				inScope = append(inScope, f)
			} else {
				droppedFileCount++
			}
		}
		if len(s.Files) > 0 && len(inScope) == 0 {
			droppedSlices = append(droppedSlices, s.Name)
			continue
		}
		s.Files = inScope
		kept = append(kept, s)
	}
	if len(droppedSlices) == 0 && droppedFileCount == 0 {
		return kept, notes
	}
	msg := fmt.Sprintf(
		"planner: dropped %d out-of-scope file(s) and %d slice(s) [%s] outside squad %q paths",
		droppedFileCount, len(droppedSlices),
		strings.Join(droppedSlices, ", "), m.Metadata.Name)
	if strings.TrimSpace(notes) == "" {
		return kept, msg
	}
	return kept, notes + "\n\n" + msg
}

// cloneSlices returns a defensive copy so callers cannot mutate the
// item's slices through a returned PlanOutput.
func cloneSlices(in []store.Slice) []store.Slice {
	if len(in) == 0 {
		return nil
	}
	out := make([]store.Slice, len(in))
	for i, s := range in {
		c := store.Slice{
			Name:         s.Name,
			Files:        append([]string(nil), s.Files...),
			Tests:        append([]string(nil), s.Tests...),
			ParallelWith: append([]string(nil), s.ParallelWith...),
		}
		out[i] = c
	}
	return out
}

func safeMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return in
}

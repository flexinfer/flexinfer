package squads

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// fakeSpawner records the prompt + options it received and returns a
// scripted JSONBody / CostUSD pair. Tests inspect both the recorded
// prompt (to assert template substitution) and the returned PlanOutput.
type fakeSpawner struct {
	JSONBody string
	CostUSD  float64
	Err      error

	GotPrompt  string
	GotOptions SpawnerOptions
	Calls      int
}

func (f *fakeSpawner) PlanSlices(ctx context.Context, prompt string, opts SpawnerOptions) (SpawnerResult, error) {
	f.Calls++
	f.GotPrompt = prompt
	f.GotOptions = opts
	if f.Err != nil {
		return SpawnerResult{}, f.Err
	}
	return SpawnerResult{JSONBody: f.JSONBody, CostUSD: f.CostUSD}, nil
}

// hudFrontendManifest is the typed fixture mirroring validHUDFrontend in
// loader_test.go. Inline here so planner tests don't take a fragile
// dependency on YAML parsing for fixture construction.
func hudFrontendManifest() *Manifest {
	return &Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		Metadata:   ManifestMeta{Name: "hud-frontend"},
		Spec: ManifestSpec{
			Paths: []string{"internal/hud/frontend/**"},
			Tests: []string{"pnpm-typecheck", "pnpm-vitest"},
			Gates: map[string][]string{
				"required": {"pr_self_review", "scope", "secret_scan"},
				"advisory": {"coverage"},
			},
			Ensemble: map[string]any{
				"editor": map[string]any{
					"backend":      "spawn",
					"driver":       "claude-opus",
					"max_cost_usd": 4.0,
				},
			},
			BudgetShare: 0.30,
		},
	}
}

func sampleItem() *store.BacklogItem {
	return &store.BacklogItem{
		ID:       "MILLS-PLAN-001",
		Title:    "Refresh routing trace UI",
		SpecDoc:  ".loom/93-product-spec.md",
		Priority: store.P2,
		Slices: []store.Slice{
			{
				Name:  "trace-panel",
				Files: []string{"internal/hud/frontend/src/RoutePanel.tsx"},
				Tests: []string{"pnpm-vitest"},
			},
		},
		Budget: store.Budget{MaxCostUSD: 12.0, MaxTurns: 60, MaxPipelineMinutes: 90},
	}
}

func sampleMemory() []*store.SquadMemory {
	return []*store.SquadMemory{
		{
			ID: 1, SquadName: "hud-frontend", Kind: store.SquadMemoryConvention,
			Title: "Always memoize SSE hooks", Body: "useMemo around SSE handler avoids reconnect storm",
			Importance: 0.9,
		},
		{
			ID: 2, SquadName: "hud-frontend", Kind: store.SquadMemoryMerge,
			Title: "Last week: routing badge added", Body: "Shipped MR !1234 adding /api/route trace.",
			Importance: 0.5,
		},
	}
}

func TestPlanner_HappyPath(t *testing.T) {
	want := map[string]any{
		"slices": []map[string]any{
			{
				"name":  "refined-panel",
				"files": []string{"internal/hud/frontend/src/RoutePanel.tsx"},
				"tests": []string{"pnpm-vitest"},
			},
		},
		"gates": map[string]any{
			"required": []string{"pr_self_review", "scope", "secret_scan"},
			"advisory": []string{"coverage"},
		},
		"budget": map[string]any{
			"max_cost_usd":         8.0,
			"max_turns":            40,
			"max_pipeline_minutes": 60,
		},
		"notes": "Tightened budget; cited memory entry on SSE hooks.",
	}
	bodyBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	spawner := &fakeSpawner{JSONBody: string(bodyBytes), CostUSD: 0.42}
	p, err := NewPlanner(spawner)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}

	out, err := p.Plan(context.Background(), PlanInput{
		Item:    sampleItem(),
		Squad:   hudFrontendManifest(),
		Memory:  sampleMemory(),
		Sidecar: map[string]any{"models": []string{"claude-opus"}},
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Spawner options carry the manifest's editor driver + cost cap.
	if spawner.GotOptions.Driver != "claude-opus" {
		t.Errorf("driver: want claude-opus, got %q", spawner.GotOptions.Driver)
	}
	if spawner.GotOptions.MaxCostUSD != 4.0 {
		t.Errorf("max cost: want 4.0, got %v", spawner.GotOptions.MaxCostUSD)
	}

	// Prompt embeds the squad name + memory + sidecar.
	if !strings.Contains(spawner.GotPrompt, "hud-frontend") {
		t.Errorf("prompt missing squad name; got: %q", truncate(spawner.GotPrompt, 200))
	}
	if !strings.Contains(spawner.GotPrompt, "Always memoize SSE hooks") {
		t.Error("prompt missing memory entry")
	}
	if !strings.Contains(spawner.GotPrompt, "internal/hud/frontend/**") {
		t.Error("prompt missing path scope")
	}
	if !strings.Contains(spawner.GotPrompt, "Refresh routing trace UI") {
		t.Error("prompt missing item title")
	}

	if len(out.Slices) != 1 || out.Slices[0].Name != "refined-panel" {
		t.Errorf("slices: %+v", out.Slices)
	}
	if out.Budget.MaxCostUSD != 8.0 || out.Budget.MaxTurns != 40 {
		t.Errorf("budget: %+v", out.Budget)
	}
	if out.CostUSD != 0.42 {
		t.Errorf("cost echo: want 0.42, got %v", out.CostUSD)
	}
	if !strings.Contains(out.Notes, "SSE hooks") {
		t.Errorf("notes: %q", out.Notes)
	}
	if len(out.Gates.Required) != 3 || out.Gates.Required[0] != "pr_self_review" {
		t.Errorf("gates required: %+v", out.Gates.Required)
	}
}

func TestPlanner_ParseFailure_Fallback(t *testing.T) {
	// Garbage non-JSON body forces the parse-failure path.
	spawner := &fakeSpawner{JSONBody: "not even close to JSON {{{}", CostUSD: 0.10}
	p, err := NewPlanner(spawner)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}

	item := sampleItem()
	out, err := p.Plan(context.Background(), PlanInput{
		Item:  item,
		Squad: hudFrontendManifest(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// Slices fall back to the item's slices.
	if len(out.Slices) != len(item.Slices) || out.Slices[0].Name != "trace-panel" {
		t.Errorf("expected fallback slices, got %+v", out.Slices)
	}
	// Budget falls back to the item's budget.
	if out.Budget != item.Budget {
		t.Errorf("budget fallback: want %+v, got %+v", item.Budget, out.Budget)
	}
	// Gates fall back to the manifest defaults.
	if len(out.Gates.Required) != 3 {
		t.Errorf("gate fallback: %+v", out.Gates)
	}
	// Notes mention the parse error.
	if !strings.Contains(out.Notes, "parse error") {
		t.Errorf("notes should mention parse error, got %q", out.Notes)
	}
	// Cost still propagates.
	if out.CostUSD != 0.10 {
		t.Errorf("cost echo: want 0.10, got %v", out.CostUSD)
	}
}

func TestPlanner_OutOfScope_Filtered(t *testing.T) {
	// Model proposes one in-scope slice + one out-of-scope slice; planner
	// drops the out-of-scope slice and notes the drop.
	body := `{
        "slices": [
            {"name":"in","files":["internal/hud/frontend/src/A.tsx"],"tests":["pnpm-vitest"]},
            {"name":"out","files":["pkg/mills/router.go"],"tests":["go-test"]}
        ],
        "gates": {"required":["pr_self_review","scope","secret_scan"],"advisory":["coverage"]},
        "budget": {"max_cost_usd": 6.0, "max_turns": 30, "max_pipeline_minutes": 45},
        "notes": "Two slices proposed."
    }`
	spawner := &fakeSpawner{JSONBody: body, CostUSD: 0.20}
	p, err := NewPlanner(spawner)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}

	out, err := p.Plan(context.Background(), PlanInput{
		Item:  sampleItem(),
		Squad: hudFrontendManifest(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(out.Slices) != 1 {
		t.Fatalf("want 1 slice after filter, got %d: %+v", len(out.Slices), out.Slices)
	}
	if out.Slices[0].Name != "in" {
		t.Errorf("kept slice: %+v", out.Slices[0])
	}
	if !strings.Contains(out.Notes, "out-of-scope") {
		t.Errorf("notes should mention out-of-scope drops; got %q", out.Notes)
	}
	if !strings.Contains(out.Notes, "out") {
		t.Errorf("notes should name the dropped slice; got %q", out.Notes)
	}
	// Original notes from the model are preserved.
	if !strings.Contains(out.Notes, "Two slices proposed") {
		t.Errorf("model notes should be preserved; got %q", out.Notes)
	}
}

func TestPlanner_EmptyMemory_NoCrash(t *testing.T) {
	body := `{
        "slices": [{"name":"x","files":["internal/hud/frontend/src/Z.tsx"],"tests":["pnpm-vitest"]}],
        "gates": {"required":["pr_self_review"],"advisory":[]},
        "budget": {"max_cost_usd": 3.0, "max_turns": 20, "max_pipeline_minutes": 30},
        "notes": "ok"
    }`
	spawner := &fakeSpawner{JSONBody: body, CostUSD: 0.05}
	p, err := NewPlanner(spawner)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}
	out, err := p.Plan(context.Background(), PlanInput{
		Item:   sampleItem(),
		Squad:  hudFrontendManifest(),
		Memory: nil,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(out.Slices) != 1 {
		t.Errorf("slices: %+v", out.Slices)
	}
	if !strings.Contains(spawner.GotPrompt, "no working memory") {
		t.Errorf("prompt should render placeholder for empty memory; got: %q", truncate(spawner.GotPrompt, 200))
	}
}

func TestPlanner_BudgetEcho(t *testing.T) {
	// Spawner reports 1.23 USD; planner must echo into PlanOutput.CostUSD.
	body := `{"slices":[],"gates":null,"budget":null,"notes":"empty"}`
	spawner := &fakeSpawner{JSONBody: body, CostUSD: 1.23}
	p, err := NewPlanner(spawner)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}
	out, err := p.Plan(context.Background(), PlanInput{
		Item:  sampleItem(),
		Squad: hudFrontendManifest(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if out.CostUSD != 1.23 {
		t.Errorf("CostUSD echo: want 1.23, got %v", out.CostUSD)
	}
}

func TestPlanner_SpawnerError_Propagates(t *testing.T) {
	spawner := &fakeSpawner{Err: errors.New("boom")}
	p, err := NewPlanner(spawner)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}
	_, err = p.Plan(context.Background(), PlanInput{
		Item: sampleItem(), Squad: hudFrontendManifest(),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "spawner") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error wrapping: %v", err)
	}
}

func TestPlanner_NilSpawner_Rejected(t *testing.T) {
	if _, err := NewPlanner(nil); err == nil {
		t.Fatal("expected error for nil spawner")
	}
}

func TestPlanner_EmptyTemplate_Rejected(t *testing.T) {
	if _, err := NewPlannerWithTemplate(&fakeSpawner{}, "   "); err == nil {
		t.Fatal("expected error for empty template")
	}
}

func TestPlanner_Validate_RequiresItemAndSquad(t *testing.T) {
	p, err := NewPlanner(&fakeSpawner{JSONBody: "{}"})
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}
	if _, err := p.Plan(context.Background(), PlanInput{Squad: hudFrontendManifest()}); err == nil {
		t.Error("expected error for nil item")
	}
	if _, err := p.Plan(context.Background(), PlanInput{Item: sampleItem()}); err == nil {
		t.Error("expected error for nil squad")
	}
}

func TestPlanner_PartialOutOfScope_KeepsInScopeFiles(t *testing.T) {
	// Slice has two files: one in scope, one out. Planner keeps the
	// slice but drops only the offending file.
	body := `{
        "slices": [
            {"name":"mixed","files":["internal/hud/frontend/src/A.tsx","cmd/something/main.go"],"tests":["pnpm-vitest"]}
        ],
        "gates": null,
        "budget": null,
        "notes": ""
    }`
	spawner := &fakeSpawner{JSONBody: body}
	p, err := NewPlanner(spawner)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}
	out, err := p.Plan(context.Background(), PlanInput{
		Item: sampleItem(), Squad: hudFrontendManifest(),
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(out.Slices) != 1 || len(out.Slices[0].Files) != 1 {
		t.Fatalf("expected one slice with one in-scope file; got %+v", out.Slices)
	}
	if out.Slices[0].Files[0] != "internal/hud/frontend/src/A.tsx" {
		t.Errorf("kept file: %q", out.Slices[0].Files[0])
	}
	if !strings.Contains(out.Notes, "out-of-scope") {
		t.Errorf("notes should mention drop; got %q", out.Notes)
	}
}

// truncate is a small test-only helper to keep error messages readable.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

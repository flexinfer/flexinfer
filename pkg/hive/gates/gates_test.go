package gates

import (
	"context"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// fixturePolicy yields the validated default policy with a few overrides
// the gate tests exercise (protected paths in particular).
func fixturePolicy(t *testing.T) *hive.Policy {
	t.Helper()
	p := hive.Default()
	// Default returns enabled=true (slice 6.6, 2026-05-02). Re-affirm
	// explicitly so the gates tests don't depend on the default flipping.
	on := true
	p.Enabled = &on
	if err := p.Validate(); err != nil {
		t.Fatalf("fixture policy: %v", err)
	}
	return p
}

func fixtureItem(slices ...store.Slice) *store.BacklogItem {
	return &store.BacklogItem{
		ID:        "HIVE-T",
		Title:     "test item",
		State:     store.BacklogQueued,
		Priority:  store.P2,
		Slices:    slices,
		CreatedBy: "test",
	}
}

// ---------- DiffSize ----------

func TestDiffSize_PassUnderCap(t *testing.T) {
	g := &DiffSize{}
	out, err := g.Evaluate(context.Background(), StageInput{LinesAdded: 100, LinesRemoved: 50})
	if err != nil || !out.Pass {
		t.Errorf("expected pass, got %+v err=%v", out, err)
	}
}

func TestDiffSize_FailOverCap(t *testing.T) {
	g := &DiffSize{MaxLines: 100}
	out, _ := g.Evaluate(context.Background(), StageInput{LinesAdded: 80, LinesRemoved: 50})
	if out.Pass {
		t.Errorf("expected fail, got pass: %+v", out)
	}
	if len(out.Reasons) == 0 || !strings.Contains(out.Reasons[0], "130") {
		t.Errorf("reason should report total lines: %v", out.Reasons)
	}
}

// ---------- Scope ----------

func TestScope_NoFilesChangedPasses(t *testing.T) {
	g := &Scope{}
	out, _ := g.Evaluate(context.Background(), StageInput{Item: fixtureItem()})
	if !out.Pass {
		t.Errorf("expected pass with no diff, got %+v", out)
	}
}

func TestScope_AllInScopePasses(t *testing.T) {
	g := &Scope{}
	in := StageInput{
		Item: fixtureItem(store.Slice{
			Name:  "core",
			Files: []string{"pkg/auth/login.go"},
			Tests: []string{"pkg/auth/login_test.go"},
		}),
		FilesChanged: []string{"pkg/auth/login.go", "pkg/auth/login_test.go"},
	}
	out, _ := g.Evaluate(context.Background(), in)
	if !out.Pass {
		t.Errorf("expected pass, got %+v", out)
	}
}

func TestScope_OutOfScopeFails(t *testing.T) {
	g := &Scope{}
	in := StageInput{
		Item: fixtureItem(store.Slice{
			Name:  "core",
			Files: []string{"pkg/auth/login.go"},
		}),
		FilesChanged: []string{"pkg/auth/login.go", "internal/billing/charge.go"},
	}
	out, _ := g.Evaluate(context.Background(), in)
	if out.Pass {
		t.Errorf("expected fail, got pass: %+v", out)
	}
	if !strings.Contains(out.Reasons[0], "internal/billing/charge.go") {
		t.Errorf("violation should be named: %v", out.Reasons)
	}
}

func TestScope_TestFilesAlwaysAllowed(t *testing.T) {
	g := &Scope{}
	in := StageInput{
		Item: fixtureItem(store.Slice{
			Name:  "core",
			Files: []string{"pkg/auth/login.go"},
		}),
		// Test file not in the slice's tests[] list — should still be
		// allowed because looksLikeTestFile catches it.
		FilesChanged: []string{"pkg/auth/login.go", "pkg/auth/edge_test.go"},
	}
	out, _ := g.Evaluate(context.Background(), in)
	if !out.Pass {
		t.Errorf("test file should be allowed: %+v", out)
	}
}

func TestScope_NoSlicesFails(t *testing.T) {
	g := &Scope{}
	in := StageInput{
		Item:         fixtureItem(),
		FilesChanged: []string{"pkg/auth/login.go"},
	}
	out, _ := g.Evaluate(context.Background(), in)
	if out.Pass {
		t.Errorf("expected fail when item has no slices, got pass: %+v", out)
	}
}

// ---------- PathPolicy ----------

func TestPathPolicy_NoTouchPasses(t *testing.T) {
	g := &PathPolicy{}
	in := StageInput{
		Policy:       fixturePolicy(t),
		FilesChanged: []string{"internal/hud/spawn.go", "pkg/skills/fileops.go"},
	}
	out, _ := g.Evaluate(context.Background(), in)
	if !out.Pass {
		t.Errorf("expected pass, got %+v", out)
	}
}

func TestPathPolicy_UndeclaredTouchFails(t *testing.T) {
	g := &PathPolicy{}
	in := StageInput{
		Policy: fixturePolicy(t),
		Item:   fixtureItem(),
		FilesChanged: []string{
			"platform/gitops/k3s/devbox/code-server.yaml",
		},
	}
	out, _ := g.Evaluate(context.Background(), in)
	if out.Pass {
		t.Errorf("expected fail for undeclared protected touch, got %+v", out)
	}
	if !strings.Contains(out.Reasons[0], "platform/gitops") {
		t.Errorf("reason should name the path: %v", out.Reasons)
	}
}

func TestPathPolicy_DeclaredTouchPasses(t *testing.T) {
	g := &PathPolicy{}
	item := fixtureItem()
	item.Policy.ProtectedPathsTouched = []string{"platform/gitops/k3s/devbox/code-server.yaml"}
	in := StageInput{
		Policy:       fixturePolicy(t),
		Item:         item,
		FilesChanged: []string{"platform/gitops/k3s/devbox/code-server.yaml"},
	}
	out, _ := g.Evaluate(context.Background(), in)
	if !out.Pass {
		t.Errorf("declared touch should pass, got %+v", out)
	}
}

func TestPathPolicy_MissingPolicyFails(t *testing.T) {
	g := &PathPolicy{}
	out, _ := g.Evaluate(context.Background(), StageInput{FilesChanged: []string{"x"}})
	if out.Pass {
		t.Errorf("expected fail with no policy, got pass")
	}
}

// ---------- SecretScan ----------

func TestSecretScan_CleanDiffPasses(t *testing.T) {
	g := &SecretScan{}
	out, _ := g.Evaluate(context.Background(), StageInput{
		DiffPatch: []byte("+func login() {}\n-func old() {}\n"),
	})
	if !out.Pass {
		t.Errorf("expected pass, got %+v", out)
	}
}

func TestSecretScan_CatchesAWSKey(t *testing.T) {
	g := &SecretScan{}
	out, _ := g.Evaluate(context.Background(), StageInput{
		DiffPatch: []byte("+const key = \"AKIAIOSFODNN7EXAMPLE\"\n"),
	})
	if out.Pass {
		t.Errorf("expected fail, got %+v", out)
	}
	if !strings.Contains(out.Reasons[0], "AWS access key") {
		t.Errorf("reason should name the matched pattern: %v", out.Reasons)
	}
}

func TestSecretScan_CatchesAnthropicKey(t *testing.T) {
	g := &SecretScan{}
	out, _ := g.Evaluate(context.Background(), StageInput{
		DiffPatch: []byte("+ANTHROPIC_API_KEY=sk-ant-abcdefghijklmnopqrstuvwxyz123\n"),
	})
	if out.Pass {
		t.Errorf("expected fail, got %+v", out)
	}
}

func TestSecretScan_DeletionsIgnored(t *testing.T) {
	g := &SecretScan{}
	out, _ := g.Evaluate(context.Background(), StageInput{
		// `-` line: deletion of an existing key. Already a fix; don't
		// fail the diff that's removing it.
		DiffPatch: []byte("-const key = \"AKIAIOSFODNN7EXAMPLE\"\n"),
	})
	if !out.Pass {
		t.Errorf("deletion should pass, got %+v", out)
	}
}

func TestSecretScan_FilenameNotMatched(t *testing.T) {
	g := &SecretScan{}
	// `+++` header lines should be skipped so a filename that happens to
	// contain a JWT-shape doesn't trip the gate.
	out, _ := g.Evaluate(context.Background(), StageInput{
		DiffPatch: []byte("+++ b/eyJlbmNvZGVkIjoidGVzdCJ9.eyJjbGFpbSI6InZhbHVlIn0.signature/foo.go\n+func ok() {}\n"),
	})
	if !out.Pass {
		t.Errorf("filename should not match: %+v", out)
	}
}

// ---------- CommitFormat ----------

func TestCommitFormat_NoCommitsPasses(t *testing.T) {
	g := &CommitFormat{}
	out, _ := g.Evaluate(context.Background(), StageInput{})
	if !out.Pass {
		t.Errorf("expected pass with no commits, got %+v", out)
	}
}

func TestCommitFormat_ConventionalPasses(t *testing.T) {
	g := &CommitFormat{}
	out, _ := g.Evaluate(context.Background(), StageInput{
		CommitMessages: []string{
			"feat(hive): add diff_size gate",
			"fix: handle nil item",
			"refactor!: drop legacy path",
		},
	})
	if !out.Pass {
		t.Errorf("expected pass for conventional commits, got %+v", out)
	}
}

func TestCommitFormat_BadShapeFails(t *testing.T) {
	g := &CommitFormat{}
	out, _ := g.Evaluate(context.Background(), StageInput{
		CommitMessages: []string{
			"updated some stuff",
		},
	})
	if out.Pass {
		t.Errorf("expected fail, got %+v", out)
	}
}

func TestCommitFormat_UnknownTypeFails(t *testing.T) {
	g := &CommitFormat{}
	out, _ := g.Evaluate(context.Background(), StageInput{
		CommitMessages: []string{"wip: half-done"},
	})
	if out.Pass {
		t.Errorf("expected fail for unknown type, got %+v", out)
	}
	if !strings.Contains(out.Reasons[0], "wip") {
		t.Errorf("reason should name unknown type: %v", out.Reasons)
	}
}

func TestCommitFormat_LongSubjectFails(t *testing.T) {
	g := &CommitFormat{MaxSubjectLen: 30}
	out, _ := g.Evaluate(context.Background(), StageInput{
		CommitMessages: []string{
			"feat(hive): a really long subject that exceeds the cap",
		},
	})
	if out.Pass {
		t.Errorf("expected fail for long subject, got %+v", out)
	}
}

// ---------- Registry ----------

func TestDefault_HasAllCoreGates(t *testing.T) {
	r := Default()
	got := r.Names()
	want := []string{"commit_format", "diff_size", "path_policy", "scope", "secret_scan"}
	if len(got) != len(want) {
		t.Fatalf("default registry: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("registry[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r := NewRegistry()
	r.Register(&DiffSize{})
	r.Register(&DiffSize{})
}

func TestRegistry_GetUnknown(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Get("nope"); err == nil {
		t.Fatal("expected ErrUnknownGate")
	}
}

func TestEvaluateAll_AggregatesPass(t *testing.T) {
	r := Default()
	in := StageInput{
		Policy:       fixturePolicy(t),
		Item:         fixtureItem(store.Slice{Name: "x", Files: []string{"a.go"}}),
		FilesChanged: []string{"a.go"},
		LinesAdded:   10,
	}
	outcomes, allPass, err := r.EvaluateAll(context.Background(),
		[]string{"diff_size", "scope", "path_policy", "secret_scan", "commit_format"}, in)
	if err != nil {
		t.Fatalf("evaluate-all: %v", err)
	}
	if !allPass {
		t.Errorf("expected all pass; outcomes=%+v", outcomes)
	}
	if len(outcomes) != 5 {
		t.Errorf("expected 5 outcomes, got %d", len(outcomes))
	}
}

func TestEvaluateAll_UnknownGateFails(t *testing.T) {
	r := Default()
	outcomes, allPass, err := r.EvaluateAll(context.Background(),
		[]string{"diff_size", "nope"}, StageInput{LinesAdded: 1})
	if err != nil {
		t.Fatalf("evaluate-all should not error on unknown gate; got %v", err)
	}
	if allPass {
		t.Errorf("unknown gate should cause aggregate fail")
	}
	if len(outcomes) != 2 || outcomes[1].Outcome.Pass {
		t.Errorf("expected per-gate fail outcome for unknown gate; got %+v", outcomes)
	}
}

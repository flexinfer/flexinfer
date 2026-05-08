package aimodels

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDefaultResolver_RolesLookup(t *testing.T) {
	t.Parallel()
	r := DefaultResolver()

	// Every advertised role must resolve to something non-empty so a
	// fresh daemon never points at an absent model by accident.
	for _, role := range AllRoles() {
		got := r.Resolve(role)
		if got == "" {
			t.Errorf("Resolve(%q) returned empty, expected baked-in default", role)
		}
	}
}

func TestDefaultResolver_KnownPrimaries(t *testing.T) {
	t.Parallel()
	// Lock in the canonical defaults from MR-001 so we notice if
	// someone silently changes them.
	cases := map[Role]string{
		RoleWeaverRouter:       "qwen3-1p7b-tools-radeonvii",
		RoleWeaverSubagent:     "qwen3-8b",
		RoleMillsJudge:         "qwen3-8b",
		RoleMillsResearch:      "qwen3-8b",
		RoleCoordinatorDefault: "qwen3-8b",
		RoleAutofix:            "qwen3-8b",
	}
	r := DefaultResolver()
	for role, want := range cases {
		if got := r.Resolve(role); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", role, got, want)
		}
	}
}

func TestResolveWithFallbacks_ChainOrder(t *testing.T) {
	t.Parallel()
	r := DefaultResolver()
	got := r.ResolveWithFallbacks(RoleWeaverRouter)
	if len(got) < 2 {
		t.Fatalf("expected primary + fallbacks, got %v", got)
	}
	if got[0] != "qwen3-1p7b-tools-radeonvii" {
		t.Errorf("primary at index 0 = %q, want qwen3-1p7b-tools-radeonvii", got[0])
	}
	// Fallbacks must come after the primary; we don't assert the full
	// list to keep the test resilient to future fallback adjustments.
	if got[1] == got[0] {
		t.Errorf("fallback duplicates primary: %v", got)
	}
}

func TestResolveOrDefault_UnknownRole(t *testing.T) {
	t.Parallel()
	r := DefaultResolver()
	got := r.ResolveOrDefault(Role("never-defined"), "fallback-default")
	if got != "fallback-default" {
		t.Errorf("ResolveOrDefault unknown role = %q, want fallback-default", got)
	}
}

func TestLoadResolver_MissingFileNotAnError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.yaml")

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	r := LoadResolver(path, WithLogger(logger))
	if r == nil {
		t.Fatal("LoadResolver returned nil")
	}
	if got := r.Resolve(RoleWeaverRouter); got != "qwen3-1p7b-tools-radeonvii" {
		t.Errorf("primary after missing file = %q, want default", got)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no warning log for missing file, got: %s", buf.String())
	}
}

func TestLoadResolver_OverridePrimary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "roles.yaml")
	body := `
roles:
  weaver-router:
    primary: my-custom-router
    fallbacks: [a, b, c]
  mills-judge:
    primary: my-judge
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := LoadResolver(path)
	if got := r.Resolve(RoleWeaverRouter); got != "my-custom-router" {
		t.Errorf("Resolve weaver-router after override = %q, want my-custom-router", got)
	}
	chain := r.ResolveWithFallbacks(RoleWeaverRouter)
	if len(chain) != 4 || chain[0] != "my-custom-router" || chain[1] != "a" {
		t.Errorf("override fallback chain = %v, want [my-custom-router a b c]", chain)
	}
	// Untouched roles keep defaults.
	if got := r.Resolve(RoleAutofix); got != "qwen3-8b" {
		t.Errorf("Resolve autofix = %q, want qwen3-8b (unchanged)", got)
	}
	// Override that omits fallbacks keeps the role's existing fallbacks.
	judge := r.ResolveWithFallbacks(RoleMillsJudge)
	if len(judge) < 2 || judge[0] != "my-judge" {
		t.Errorf("mills-judge chain = %v, want primary=my-judge with default fallbacks", judge)
	}
}

func TestLoadResolver_MalformedFile_LogsAndKeepsDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("roles: {weaver-router: not-a-spec"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	r := LoadResolver(path, WithLogger(logger))
	if got := r.Resolve(RoleWeaverRouter); got != "qwen3-1p7b-tools-radeonvii" {
		t.Errorf("primary after malformed parse = %q, want default", got)
	}
	if !strings.Contains(buf.String(), "parse roles YAML failed") {
		t.Errorf("expected parse failure warning, got: %s", buf.String())
	}
}

func TestLoadResolver_UnknownRole_LogsAndIgnores(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "roles.yaml")
	body := `
roles:
  weaver-router:
    primary: kept
  brand-new-role:
    primary: ignored
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	r := LoadResolver(path, WithLogger(logger))
	if got := r.Resolve(RoleWeaverRouter); got != "kept" {
		t.Errorf("Resolve weaver-router = %q, want kept", got)
	}
	if !strings.Contains(buf.String(), "unknown role") {
		t.Errorf("expected unknown-role warning, got: %s", buf.String())
	}
}

func TestSetSpec_UnknownRoleErrors(t *testing.T) {
	t.Parallel()
	r := DefaultResolver()
	if err := r.SetSpec(Role("not-a-role"), RoleSpec{Primary: "x"}); err == nil {
		t.Error("SetSpec on unknown role: want error, got nil")
	}
	if err := r.SetSpec(RoleAutofix, RoleSpec{Primary: "new-autofix"}); err != nil {
		t.Errorf("SetSpec on known role: unexpected error %v", err)
	}
	if got := r.Resolve(RoleAutofix); got != "new-autofix" {
		t.Errorf("Resolve after SetSpec = %q, want new-autofix", got)
	}
}

func TestRoles_SnapshotIsCopy(t *testing.T) {
	t.Parallel()
	r := DefaultResolver()
	snap := r.Roles()
	// Mutating the snapshot must not affect the resolver.
	snap[RoleAutofix] = RoleSpec{Primary: "tampered"}
	if got := r.Resolve(RoleAutofix); got == "tampered" {
		t.Error("Roles() returned a live reference; resolver was mutated externally")
	}
	// Snapshot fallback slices must be independent copies.
	chain := r.ResolveWithFallbacks(RoleWeaverRouter)
	if len(chain) > 1 {
		snapSpec := snap[RoleWeaverRouter]
		if len(snapSpec.Fallbacks) > 0 {
			snapSpec.Fallbacks[0] = "tampered"
		}
		again := r.ResolveWithFallbacks(RoleWeaverRouter)
		if again[1] == "tampered" {
			t.Error("Roles() snapshot shares fallback slice with resolver")
		}
	}
}

func TestResolutionMetrics_IncrementsOnResolve(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	r := DefaultResolver(WithMetrics(m))

	r.Resolve(RoleWeaverRouter)
	r.Resolve(RoleWeaverRouter)
	r.ResolveOrDefault(RoleAutofix, "x")

	got := testutil.ToFloat64(m.ResolutionTotal.WithLabelValues(
		string(RoleWeaverRouter), "qwen3-1p7b-tools-radeonvii", "false"))
	if got != 2 {
		t.Errorf("router counter = %v, want 2", got)
	}
	got = testutil.ToFloat64(m.ResolutionTotal.WithLabelValues(
		string(RoleAutofix), "qwen3-8b", "false"))
	if got != 1 {
		t.Errorf("autofix counter = %v, want 1", got)
	}
}

func TestDefaultPath_NonEmpty(t *testing.T) {
	// Ensures the conventional path is computed; runs in any env that
	// has a HOME (every CI we use).
	if p := DefaultPath(); p == "" {
		t.Skip("no $HOME available; DefaultPath returned empty")
	} else if !strings.HasSuffix(p, filepath.Join(".config", "loom", "aimodel-roles.yaml")) {
		t.Errorf("DefaultPath = %q, want suffix .config/loom/aimodel-roles.yaml", p)
	}
}

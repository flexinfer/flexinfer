package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
)

func TestBuildRegistryCandidates_IncludesWorkspaceFallback(t *testing.T) {
	candidates := buildRegistryCandidates(
		"/tmp/work/services/loom-core",
		"/tmp/home",
		"/tmp/work",
	)

	if len(candidates) != 6 {
		t.Fatalf("len(candidates) = %d, want 6", len(candidates))
	}
	if got := candidates[0].Label; got != "cwd:mcp/context/registry.yaml" {
		t.Fatalf("first label = %q, want cwd candidate", got)
	}
	last := candidates[len(candidates)-1]
	if got := filepath.Clean(last.Path); got != "/tmp/work/platform/gitops/mcp/context/registry.yaml" {
		t.Fatalf("last path = %q, want workspace fallback path", got)
	}
}

func TestExtractTemplateReferences(t *testing.T) {
	input := "a=${env:FOO} b=${env:BAR:-default} c=${keychain:KC} d=${secret:SEC}"
	refs := extractTemplateReferences(input)
	if len(refs) != 4 {
		t.Fatalf("len(refs) = %d, want 4", len(refs))
	}

	if refs[0].Kind != "env" || refs[0].Key != "FOO" || refs[0].HasDefault {
		t.Fatalf("unexpected first ref: %#v", refs[0])
	}
	if refs[1].Kind != "env" || refs[1].Key != "BAR" || !refs[1].HasDefault {
		t.Fatalf("unexpected second ref: %#v", refs[1])
	}
	if refs[2].Kind != "keychain" || refs[2].Key != "KC" {
		t.Fatalf("unexpected third ref: %#v", refs[2])
	}
	if refs[3].Kind != "secret" || refs[3].Key != "SEC" {
		t.Fatalf("unexpected fourth ref: %#v", refs[3])
	}
}

func TestCollectTemplateDiagnostics_ReportsUnresolvedByProfile(t *testing.T) {
	t.Setenv("LOOM_UNITTEST_TEMPLATE_PRESENT_A", "ok")
	os.Unsetenv("LOOM_UNITTEST_TEMPLATE_MISSING_A")
	os.Unsetenv("LOOM_UNITTEST_TEMPLATE_MISSING_B")

	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "demo",
				Common: &registry.TargetSpec{
					Env: map[string]string{
						"PRESENT": "${env:LOOM_UNITTEST_TEMPLATE_PRESENT_A}",
						"MISSING": "${env:LOOM_UNITTEST_TEMPLATE_MISSING_A}",
					},
					Args: []any{
						"--endpoint=${env:LOOM_UNITTEST_TEMPLATE_MISSING_B}",
						"--ok=${env:LOOM_UNITTEST_TEMPLATE_PRESENT_A}",
					},
				},
			},
		},
	}

	diags := collectTemplateDiagnostics(reg, []string{"codex"})
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1", len(diags))
	}
	if diags[0].OK {
		t.Fatalf("diag should be non-OK for unresolved refs: %#v", diags[0])
	}
	if diags[0].Count != 2 {
		t.Fatalf("diag count = %d, want 2", diags[0].Count)
	}
}

func TestCollectEnvConventionWarnings(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "alpha",
				Common: &registry.TargetSpec{
					Env: map[string]string{
						"bad-key":              "1",
						"MCP_TIMEOUT":          "90",
						"GOOD_TIMEOUT_SECONDS": "30",
					},
				},
			},
		},
	}

	warnings := collectEnvConventionWarnings(reg)
	if len(warnings) != 2 {
		t.Fatalf("len(warnings) = %d, want 2", len(warnings))
	}

	got := map[string]envConventionWarning{}
	for _, w := range warnings {
		got[w.Key] = w
	}

	if w, ok := got["bad-key"]; !ok || w.Suggestion != "BAD_KEY" {
		t.Fatalf("missing bad-key warning or wrong suggestion: %#v", w)
	}
	if w, ok := got["MCP_TIMEOUT"]; !ok || w.Suggestion != "MCP_TIMEOUT_SECONDS" {
		t.Fatalf("missing MCP_TIMEOUT warning or wrong suggestion: %#v", w)
	}
}

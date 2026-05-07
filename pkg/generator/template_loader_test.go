package generator

import (
	"strings"
	"testing"
)

// TestRenderHookTemplate_NilProfile confirms the no-op path: when the
// profile is nil or has no Hooks.Template, the renderer returns ok=false
// without error so the caller falls through to the legacy Go-builder
// switch.
func TestRenderHookTemplate_NilProfile(t *testing.T) {
	config, ok, err := renderHookTemplate(nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for nil profile, got true (config=%v)", config)
	}
}

func TestRenderHookTemplate_NoTemplateField(t *testing.T) {
	profile, err := GetPlatformProfile("claude")
	if err != nil {
		t.Fatalf("get claude profile: %v", err)
	}
	// Claude has no hooks.template set — template path should be skipped.
	config, ok, err := renderHookTemplate(nil, profile, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false when profile.Hooks.Template is empty, got true (config=%v)", config)
	}
}

// TestRenderHookTemplate_GenericTemplate exercises the end-to-end
// template path against the embedded templates/hooks/generic.json.tmpl.
// Confirms that a profile with Template="hooks/generic.json.tmpl" produces
// a valid map[string]any with a "hooks" key.
func TestRenderHookTemplate_GenericTemplate(t *testing.T) {
	profile, err := GetPlatformProfile("claude")
	if err != nil {
		t.Fatalf("get claude profile: %v", err)
	}
	// Clone the profile and point it at the generic template. Don't mutate
	// the cached registry copy.
	cloned := *profile
	clonedHooks := cloned.Hooks
	clonedHooks.Template = "hooks/generic.json.tmpl"
	cloned.Hooks = clonedHooks

	config, ok, err := renderHookTemplate(testRegistry(), &cloned, "/usr/local/bin/loom")
	if err != nil {
		t.Fatalf("render generic template: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok=true, got false")
	}
	if config == nil {
		t.Fatalf("expected non-nil config map")
	}
	hooks, ok := config["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("expected config.hooks to be map[string]any, got %T", config["hooks"])
	}
	if len(hooks) == 0 {
		t.Fatalf("expected hooks map to be non-empty (buildHooks should produce SessionStart, etc.)")
	}
}

// TestRenderHookTemplate_BadTemplate confirms that a missing template
// path produces a clear error with the path included.
func TestRenderHookTemplate_BadTemplate(t *testing.T) {
	profile, err := GetPlatformProfile("claude")
	if err != nil {
		t.Fatalf("get claude profile: %v", err)
	}
	cloned := *profile
	clonedHooks := cloned.Hooks
	clonedHooks.Template = "hooks/does-not-exist.json.tmpl"
	cloned.Hooks = clonedHooks

	_, _, err = renderHookTemplate(nil, &cloned, "")
	if err == nil {
		t.Fatalf("expected error for missing template, got nil")
	}
	if !strings.Contains(err.Error(), "does-not-exist.json.tmpl") {
		t.Fatalf("expected error to mention template path, got: %v", err)
	}
}

// TestHookTemplateFuncs_JSONFunc spot-checks the json funcmap helper since
// it's the most-used helper in templates and must produce compact JSON.
func TestHookTemplateFuncs_JSONFunc(t *testing.T) {
	funcs := hookTemplateFuncs(nil, nil, "")
	jsonFn, ok := funcs["json"].(func(any) (string, error))
	if !ok {
		t.Fatalf("expected funcs[\"json\"] to be func(any) (string, error), got %T", funcs["json"])
	}
	out, err := jsonFn(map[string]any{"a": 1, "b": "two"})
	if err != nil {
		t.Fatalf("json func errored: %v", err)
	}
	// json.Marshal sorts map keys alphabetically: a comes before b.
	wantPrefix := `{"a":1,`
	if !strings.HasPrefix(out, wantPrefix) {
		t.Fatalf("expected output to start with %q, got %q", wantPrefix, out)
	}
}

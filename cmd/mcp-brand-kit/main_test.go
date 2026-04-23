package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeCLI(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "banner-kit")
	script := "#!/usr/bin/env bash\nset -euo pipefail\n" + body
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake cli: %v", err)
	}
	return path
}

func makeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, repo := range []string{"alpha", "beta"} {
		dir := filepath.Join(root, "libs", repo)
		if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
			t.Fatalf("mkdir repo: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+repo+"\n"), 0o644); err != nil {
			t.Fatalf("write readme: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "libs", "alpha", "assets", "banner.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write banner: %v", err)
	}
	return root
}

func resultJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, text)
	}
	return parsed
}

func TestHandleListReposUsesWorkspaceKindBucket(t *testing.T) {
	root := makeWorkspace(t)
	t.Setenv("BRAND_KIT_DEFAULT_ROOT", root)

	result, err := handleListRepos(context.Background(), map[string]any{
		"kind": "library",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	data := resultJSON(t, result.Content[0].Text)
	if data["count"].(float64) != 2 {
		t.Fatalf("expected two repos, got %v", data["count"])
	}
	if !strings.HasSuffix(data["root"].(string), "/libs") {
		t.Fatalf("expected libs root, got %s", data["root"])
	}
}

func TestHandleInspectReturnsLocalBrandingSummary(t *testing.T) {
	root := makeWorkspace(t)
	t.Setenv("BRAND_KIT_DEFAULT_ROOT", root)
	t.Setenv("BRAND_KIT_CLI", "/tmp/banner-kit")

	result, err := handleInspect(context.Background(), map[string]any{
		"kind": "library",
		"repo": "alpha",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"has_banner":true`) {
		t.Fatalf("expected banner summary, got %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"planned_cli_command"`) {
		t.Fatalf("expected planned command, got %s", result.Content[0].Text)
	}
}

func TestHandleLintTreatsExitOneJSONAsSuccess(t *testing.T) {
	root := makeWorkspace(t)
	cli := writeFakeCLI(t, `printf '{"ok":false,"findings":[{"repo":"alpha"}]}' && exit 1`)
	t.Setenv("BRAND_KIT_DEFAULT_ROOT", root)
	t.Setenv("BRAND_KIT_CLI", cli)

	result, err := handleLint(context.Background(), map[string]any{
		"kind":   "library",
		"repos":  []any{"alpha"},
		"verify": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("lint findings with JSON should be successful payload: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"exit_code":1`) {
		t.Fatalf("expected exit code in payload, got %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"findings"`) {
		t.Fatalf("expected parsed JSON payload, got %s", result.Content[0].Text)
	}
}

func TestHandleFixRejectsImplicitWorkspaceWideMutation(t *testing.T) {
	root := makeWorkspace(t)
	t.Setenv("BRAND_KIT_DEFAULT_ROOT", root)

	result, err := handleFix(context.Background(), map[string]any{
		"kind": "library",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected missing selector to be rejected")
	}
	if !strings.Contains(result.Content[0].Text, "repos or explicit all=true") {
		t.Fatalf("unexpected error text: %s", result.Content[0].Text)
	}
}

func TestHandleRenderBuildsHeaderCommand(t *testing.T) {
	root := makeWorkspace(t)
	cli := writeFakeCLI(t, `printf '{"rendered":true,"args":['; first=1; for arg in "$@"; do if [[ $first -eq 0 ]]; then printf ','; fi; first=0; printf '"%s"' "$arg"; done; printf ']}'`)
	t.Setenv("BRAND_KIT_DEFAULT_ROOT", root)
	t.Setenv("BRAND_KIT_CLI", cli)

	result, err := handleRender(context.Background(), map[string]any{
		"kind":  "library",
		"asset": "header",
		"repos": []any{"alpha"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"header"`) {
		t.Fatalf("expected header command in payload, got %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, `"alpha"`) {
		t.Fatalf("expected repo selector in payload, got %s", result.Content[0].Text)
	}
}

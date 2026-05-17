package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- shared helpers ------------------------------------------------------

func decodeResult(t *testing.T, text string, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(text), dst); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, text)
	}
}

// --- icc_format_slack_paste ---------------------------------------------

func TestFormatSlackPaste_EmptyText(t *testing.T) {
	result, err := handleFormatSlackPaste(context.Background(), map[string]any{
		"text":         "   \n  ",
		"project_slug": "vendor-x",
		"channel":      "general",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for empty text, got %s", result.Content[0].Text)
	}
}

func TestFormatSlackPaste_SingleMessage(t *testing.T) {
	paste := "alice  10:14 AM\nHey team, this is one message.\n"
	result, err := handleFormatSlackPaste(context.Background(), map[string]any{
		"text":         paste,
		"project_slug": "vendor-x",
		"channel":      "general",
		"captured_at":  "2026-05-14T10:14:00-04:00",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatSlackPasteResult
	decodeResult(t, result.Content[0].Text, &out)
	if !strings.Contains(out.Markdown, "### alice · 10:14 AM") {
		t.Fatalf("expected message header in markdown, got:\n%s", out.Markdown)
	}
	if strings.Count(out.Markdown, "### ") != 1 {
		t.Fatalf("expected exactly one message header, got:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, `participants: ["alice"]`) {
		t.Fatalf("expected single-participant frontmatter, got:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "project: vendor-x") {
		t.Fatalf("missing project field in frontmatter:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "classification: possible_phi") {
		t.Fatalf("missing classification floor in frontmatter:\n%s", out.Markdown)
	}
	if out.SuggestedFilename == "" || !strings.HasPrefix(out.SuggestedFilename, "2026-05-14-") {
		t.Fatalf("expected date-prefixed filename, got %q", out.SuggestedFilename)
	}
	if !strings.Contains(out.SuggestedPath, "/projects/vendor-x/slack/") {
		t.Fatalf("unexpected suggested path: %s", out.SuggestedPath)
	}
}

func TestFormatSlackPaste_MultiMessage(t *testing.T) {
	paste := strings.Join([]string{
		"alice  10:14 AM",
		"Hey team, anyone seen the cohere volume report?",
		"",
		"bob  10:15 AM",
		"yep, it's in #vendor-audits",
		"",
		"carol  10:17 AM",
		"link please",
	}, "\n")

	result, err := handleFormatSlackPaste(context.Background(), map[string]any{
		"text":         paste,
		"project_slug": "vendor-audits-zigna-cohere",
		"channel":      "pmt-integrity-tech",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatSlackPasteResult
	decodeResult(t, result.Content[0].Text, &out)

	if got := strings.Count(out.Markdown, "### "); got != 3 {
		t.Fatalf("expected 3 message headers, got %d:\n%s", got, out.Markdown)
	}
	for _, want := range []string{`"alice"`, `"bob"`, `"carol"`} {
		if !strings.Contains(out.Markdown, want) {
			t.Fatalf("expected participant %s in frontmatter:\n%s", want, out.Markdown)
		}
	}
	if !strings.Contains(out.Markdown, "channel: pmt-integrity-tech") {
		t.Fatalf("missing channel in frontmatter:\n%s", out.Markdown)
	}
}

func TestFormatSlackPaste_TopicDerivedFromFirstBody(t *testing.T) {
	paste := strings.Join([]string{
		"alice  10:14 AM",
		"Cohere volume report ready for review",
		"more body text",
	}, "\n")

	result, err := handleFormatSlackPaste(context.Background(), map[string]any{
		"text":         paste,
		"project_slug": "vendor-x",
		"channel":      "general",
		"captured_at":  "2026-05-14T10:14:00-04:00",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatSlackPasteResult
	decodeResult(t, result.Content[0].Text, &out)
	if !strings.HasPrefix(out.SuggestedFilename, "2026-05-14-cohere-volume-report") {
		t.Fatalf("expected topic-derived filename, got %q", out.SuggestedFilename)
	}
}

func TestFormatSlackPaste_ExplicitParticipantsWin(t *testing.T) {
	paste := strings.Join([]string{
		"alice  10:14 AM",
		"hi",
		"",
		"bob  10:15 AM",
		"hello",
	}, "\n")

	result, err := handleFormatSlackPaste(context.Background(), map[string]any{
		"text":         paste,
		"project_slug": "vendor-x",
		"channel":      "general",
		"participants": []any{"Alice External", "Outside Reviewer"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatSlackPasteResult
	decodeResult(t, result.Content[0].Text, &out)
	if !strings.Contains(out.Markdown, `participants: ["Alice External", "Outside Reviewer"]`) {
		t.Fatalf("explicit participants did not override inferred set:\n%s", out.Markdown)
	}
	if strings.Contains(out.Markdown, `"alice"`) || strings.Contains(out.Markdown, `"bob"`) {
		t.Fatalf("inferred participants leaked when explicit list was provided:\n%s", out.Markdown)
	}
}

// --- icc_lint_notes ------------------------------------------------------

// makeWorkspace builds a fake icc-project-workspaces tree under tmp.
// returns the workspace root path.
func makeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "projects", "vendor-x", "slack"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return root
}

func writeNote(t *testing.T, root, project, source, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, "projects", project, source)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	return p
}

func TestLintNotes_MissingFrontmatter(t *testing.T) {
	root := makeWorkspace(t)
	writeNote(t, root, "vendor-x", "slack", "2026-05-14-no-frontmatter.md",
		"# just a heading, no frontmatter\n")

	result, err := handleLintNotes(context.Background(), map[string]any{
		"workspace_root": root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out lintResult
	decodeResult(t, result.Content[0].Text, &out)
	if !findingRulePresent(out.Findings, "frontmatter_present") {
		t.Fatalf("expected frontmatter_present finding, got %+v", out.Findings)
	}
}

func TestLintNotes_SourceMismatch(t *testing.T) {
	root := makeWorkspace(t)
	content := strings.Join([]string{
		"---",
		"project: vendor-x",
		"source: email",
		"classification: possible_phi",
		"captured_at: 2026-05-14T10:00:00-04:00",
		"---",
		"",
		"# body",
	}, "\n")
	writeNote(t, root, "vendor-x", "slack", "2026-05-14-wrong-source.md", content)

	result, err := handleLintNotes(context.Background(), map[string]any{
		"workspace_root": root,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out lintResult
	decodeResult(t, result.Content[0].Text, &out)
	if !findingRulePresent(out.Findings, "source_matches_folder") {
		t.Fatalf("expected source_matches_folder finding, got %+v", out.Findings)
	}
}

func TestLintNotes_FixAddsClassificationDefault(t *testing.T) {
	root := makeWorkspace(t)
	content := strings.Join([]string{
		"---",
		"project: vendor-x",
		"source: slack",
		"captured_at: 2026-05-14T10:00:00-04:00",
		"---",
		"",
		"# body",
	}, "\n")
	path := writeNote(t, root, "vendor-x", "slack", "2026-05-14-no-class.md", content)

	result, err := handleLintNotes(context.Background(), map[string]any{
		"workspace_root": root,
		"fix":            true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out lintResult
	decodeResult(t, result.Content[0].Text, &out)
	if out.FilesFixed < 1 {
		t.Fatalf("expected at least one file fixed, got %+v", out)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "classification: possible_phi") {
		t.Fatalf("default classification not written:\n%s", string(data))
	}
}

func TestLintNotes_FixDoesNotRename(t *testing.T) {
	root := makeWorkspace(t)
	// Filename violates naming pattern (no YYYY-MM-DD prefix).
	content := strings.Join([]string{
		"---",
		"project: vendor-x",
		"source: slack",
		"classification: possible_phi",
		"captured_at: 2026-05-14T10:00:00-04:00",
		"---",
		"",
		"# body",
	}, "\n")
	path := writeNote(t, root, "vendor-x", "slack", "no-date-prefix.md", content)

	result, err := handleLintNotes(context.Background(), map[string]any{
		"workspace_root": root,
		"fix":            true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out lintResult
	decodeResult(t, result.Content[0].Text, &out)
	if !findingRulePresent(out.Findings, "naming_pattern") {
		t.Fatalf("expected naming_pattern finding, got %+v", out.Findings)
	}
	// Original filename must still exist (no auto-rename).
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file was renamed or removed: %v", err)
	}
}

func findingRulePresent(findings []lintFinding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

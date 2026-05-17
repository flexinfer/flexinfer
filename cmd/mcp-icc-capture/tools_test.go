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

// --- icc_format_email_extract -------------------------------------------

func TestFormatEmailExtract_MissingRequired(t *testing.T) {
	// project_slug is missing — validate.Required should catch it.
	result, err := handleFormatEmailExtract(context.Background(), map[string]any{
		"text": "From: alice@x.com\nSubject: hi\n\nbody",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing project_slug, got %s", result.Content[0].Text)
	}
}

func TestFormatEmailExtract_EmptyText(t *testing.T) {
	result, err := handleFormatEmailExtract(context.Background(), map[string]any{
		"text":         "   \n  ",
		"project_slug": "vendor-x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for empty text, got %s", result.Content[0].Text)
	}
}

func TestFormatEmailExtract_RFC822WithHeadersAndReply(t *testing.T) {
	text := strings.Join([]string{
		"From: alice@example.com",
		"To: bob@example.com",
		"Subject: Re: (Priority) Weekly Audit Inventory Requiring Action",
		"Date: Thu, 14 May 2026 09:15:00 -0400",
		"",
		"Yes, attached.",
		"",
		"On Wed, 13 May 2026 17:00:00 -0400, bob@example.com wrote:",
		"> Can you send the inventory?",
		"> Thanks",
	}, "\n")

	result, err := handleFormatEmailExtract(context.Background(), map[string]any{
		"text":         text,
		"project_slug": "vendor-audits",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}

	var out formatEmailExtractResult
	decodeResult(t, result.Content[0].Text, &out)

	// Frontmatter sanity.
	for _, want := range []string{
		"project: vendor-audits",
		"source: email",
		"classification: possible_phi",
		`subject: Re: (Priority) Weekly Audit Inventory Requiring Action`,
		`"alice@example.com"`,
		`"bob@example.com"`,
	} {
		if !strings.Contains(out.Markdown, want) {
			t.Fatalf("missing %q in markdown:\n%s", want, out.Markdown)
		}
	}

	// Two messages rendered: top reply and quoted parent.
	if got := strings.Count(out.Markdown, "### From "); got != 2 {
		t.Fatalf("expected 2 message headers, got %d:\n%s", got, out.Markdown)
	}
	if !strings.Contains(out.Markdown, "Can you send the inventory?") {
		t.Fatalf("expected unquoted reply body in markdown:\n%s", out.Markdown)
	}
	// Filename: date-prefixed and topic-slugged from subject.
	if !strings.HasPrefix(out.SuggestedFilename, "2026-05-14-") {
		t.Fatalf("expected 2026-05-14 prefix, got %q", out.SuggestedFilename)
	}
	if !strings.Contains(out.SuggestedPath, "/projects/vendor-audits/email/") {
		t.Fatalf("unexpected suggested path: %s", out.SuggestedPath)
	}
	if out.DetectedSubject == "" {
		t.Fatalf("expected detected_subject, got empty")
	}
	if len(out.DetectedFrom) == 0 {
		t.Fatalf("expected detected_from senders")
	}
}

func TestFormatEmailExtract_HeaderlessBodyWarns(t *testing.T) {
	// No header lines at all — formatter should treat the whole input
	// as body and surface a warning.
	result, err := handleFormatEmailExtract(context.Background(), map[string]any{
		"text":         "just some text\nno headers here",
		"project_slug": "vendor-x",
		"captured_at":  "2026-05-14T10:00:00-04:00",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatEmailExtractResult
	decodeResult(t, result.Content[0].Text, &out)
	if len(out.Warnings) == 0 {
		t.Fatalf("expected at least one warning when no headers are detected")
	}
	if !strings.Contains(out.Markdown, "just some text") {
		t.Fatalf("expected body preserved:\n%s", out.Markdown)
	}
}

// --- icc_format_meeting_notes -------------------------------------------

func TestFormatMeetingNotes_MissingParticipants(t *testing.T) {
	result, err := handleFormatMeetingNotes(context.Background(), map[string]any{
		"text":         "# Sync\nNotes go here",
		"project_slug": "vendor-x",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for missing participants, got %s", result.Content[0].Text)
	}
}

func TestFormatMeetingNotes_EmptyText(t *testing.T) {
	result, err := handleFormatMeetingNotes(context.Background(), map[string]any{
		"text":         "   ",
		"project_slug": "vendor-x",
		"participants": []any{"alice", "bob"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for empty text, got %s", result.Content[0].Text)
	}
}

func TestFormatMeetingNotes_GeminiStructure(t *testing.T) {
	text := strings.Join([]string{
		"# 1:1 — Cody & Nadia",
		"",
		"## Quick recap",
		"- Talked through Q3 priorities.",
		"",
		"## Action items",
		"- Cody: draft RFC by Friday.",
	}, "\n")

	result, err := handleFormatMeetingNotes(context.Background(), map[string]any{
		"text":         text,
		"project_slug": "vendor-x",
		"participants": []any{"Cody Blevins", "Nadia Patel"},
		"captured_at":  "2026-05-12T14:00:00-04:00",
		"topic":        "1on1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}

	var out formatMeetingNotesResult
	decodeResult(t, result.Content[0].Text, &out)

	for _, want := range []string{
		"project: vendor-x",
		"source: meeting",
		"classification: possible_phi",
		`participants: ["Cody Blevins", "Nadia Patel"]`,
		"# 1:1 — Cody & Nadia",
		"## Action items",
	} {
		if !strings.Contains(out.Markdown, want) {
			t.Fatalf("missing %q in markdown:\n%s", want, out.Markdown)
		}
	}

	// Filename pattern: YYYY-MM-DD-<participants-slug>-<topic>.md
	if out.SuggestedFilename != "2026-05-12-cody-nadia-1on1.md" {
		t.Fatalf("expected 2026-05-12-cody-nadia-1on1.md, got %q", out.SuggestedFilename)
	}
	if !strings.Contains(out.SuggestedPath, "/projects/vendor-x/meetings/") {
		t.Fatalf("unexpected suggested path: %s", out.SuggestedPath)
	}
}

func TestFormatMeetingNotes_FreeformGetsTopicHeading(t *testing.T) {
	// No H1 in body — formatter should synthesize one from the topic.
	result, err := handleFormatMeetingNotes(context.Background(), map[string]any{
		"text":         "discussion notes, no heading",
		"project_slug": "vendor-x",
		"participants": []any{"Cody", "Nadia"},
		"captured_at":  "2026-05-12T14:00:00-04:00",
		"topic":        "sprint-review",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatMeetingNotesResult
	decodeResult(t, result.Content[0].Text, &out)
	if !strings.Contains(out.Markdown, "# sprint review") {
		t.Fatalf("expected synthesized H1 from topic, got:\n%s", out.Markdown)
	}
}

// --- icc_format_standup -------------------------------------------------

func TestFormatStandup_EmptyText(t *testing.T) {
	result, err := handleFormatStandup(context.Background(), map[string]any{
		"text":         "   ",
		"project_slug": "_inbox",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result for empty text, got %s", result.Content[0].Text)
	}
}

func TestFormatStandup_PersonalPrepDefaultsToStandupPrep(t *testing.T) {
	result, err := handleFormatStandup(context.Background(), map[string]any{
		"text":         "Yesterday: shipped slice C-2.\nToday: slice D-1.\nBlocked: nothing.",
		"project_slug": "_inbox",
		"captured_at":  "2026-05-17T09:00:00-04:00",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatStandupResult
	decodeResult(t, result.Content[0].Text, &out)

	if out.SuggestedFilename != "2026-05-17-standup-prep.md" {
		t.Fatalf("expected 2026-05-17-standup-prep.md, got %q", out.SuggestedFilename)
	}
	// Personal prep still lands under research/ — same folder per
	// STRUCTURE.md (no standup/ source folder).
	if !strings.Contains(out.SuggestedPath, "/projects/_inbox/research/") {
		t.Fatalf("expected research/ folder under _inbox, got %s", out.SuggestedPath)
	}
	for _, want := range []string{
		"project: _inbox",
		"source: standup",
		"classification: possible_phi",
	} {
		if !strings.Contains(out.Markdown, want) {
			t.Fatalf("missing %q in markdown:\n%s", want, out.Markdown)
		}
	}
}

func TestFormatStandup_TeamStandupFilenameAndTitle(t *testing.T) {
	result, err := handleFormatStandup(context.Background(), map[string]any{
		"text":         "Updates from team standup.",
		"project_slug": "vendor-x",
		"team":         "PMT Integrity",
		"captured_at":  "2026-05-17T10:00:00-04:00",
		"participants": []any{"Alice", "Bob", "Carol"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got %s", result.Content[0].Text)
	}
	var out formatStandupResult
	decodeResult(t, result.Content[0].Text, &out)

	if out.SuggestedFilename != "2026-05-17-pmt-integrity-standup.md" {
		t.Fatalf("expected 2026-05-17-pmt-integrity-standup.md, got %q", out.SuggestedFilename)
	}
	if !strings.Contains(out.SuggestedPath, "/projects/vendor-x/research/") {
		t.Fatalf("expected research/ folder, got %s", out.SuggestedPath)
	}
	if !strings.Contains(out.Markdown, `participants: ["Alice", "Bob", "Carol"]`) {
		t.Fatalf("expected participants in frontmatter:\n%s", out.Markdown)
	}
	if !strings.Contains(out.Markdown, "# PMT Integrity standup") {
		t.Fatalf("expected synthesized team title heading:\n%s", out.Markdown)
	}
}

// --- shared allowlist assertion ----------------------------------------

func TestCaptureSourcesIncludesD1Sources(t *testing.T) {
	// Slice D-1 introduces the three new sources that the backend
	// already enumerates in CAPTURE_SOURCES. Pinning the allowlist
	// here guards against accidental shrinkage that would otherwise
	// only surface as a runtime 400 from /api/captures.
	required := []string{"email", "meeting", "standup"}
	for _, want := range required {
		if !contains(captureSources, want) {
			t.Fatalf("captureSources missing %q (got %v); keep it in lockstep with the ICC backend CAPTURE_SOURCES enum", want, captureSources)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- shared helpers for network-backed tools ----------------------------

// newTestICCServer spins an httptest server that the given handler
// drives, and returns a *iccClient already pointed at it. Use this in
// every test for a network-backed tool so the wiring stays one line.
// The returned cleanup is wired through t.Cleanup so callers don't have
// to manage server.Close() by hand.
func newTestICCServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *iccClient) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	icc := &iccClient{
		baseURL:    srv.URL,
		httpClient: srv.Client(),
		logger:     slog.Default(),
	}
	return srv, icc
}

// readBody is a small helper that drains an http.Request body into a
// JSON object so handlers can assert on the posted shape.
func readBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(raw) == 0 {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode body: %v\n%s", err, string(raw))
	}
	return out
}

// writeJSON is the mirror of readBody for test server handlers.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}

// --- icc_write_capture ---------------------------------------------------

func TestWriteCapture_HappyPathModeBoth(t *testing.T) {
	var seen map[string]any
	var seenHeaders http.Header
	var seenPath, seenMethod string

	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = readBody(t, r)
		seenHeaders = r.Header.Clone()
		seenPath = r.URL.Path
		seenMethod = r.Method
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code_ref":     map[string]any{"id": "cref_123", "path": "/ws/x.md"},
				"artifact":     map[string]any{"id": "art_456", "title": "X"},
				"path_written": "/ws/x.md",
			},
		})
	})

	handler := makeWriteCaptureHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"project_id":     "prj_abc",
		"source":         "slack",
		"markdown":       "---\nproject: x\n---\n\n# body",
		"suggested_path": "/ws/x.md",
		"mode":           "both",
		"title":          "X",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}

	// Validate request shape.
	if seenMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", seenMethod)
	}
	if seenPath != "/api/captures" {
		t.Fatalf("unexpected path: %s", seenPath)
	}
	for k, want := range map[string]string{
		"Content-Type":     "application/json",
		"X-Requested-With": "integration-command-center",
	} {
		if got := seenHeaders.Get(k); got != want {
			t.Fatalf("header %s = %q, want %q", k, got, want)
		}
	}
	if seenHeaders.Get("Origin") == "" {
		t.Fatalf("expected non-empty Origin header")
	}
	if seen["project_id"] != "prj_abc" || seen["source"] != "slack" || seen["mode"] != "both" {
		t.Fatalf("unexpected body: %+v", seen)
	}

	// Validate response unwrapping.
	var out writeCaptureResult
	decodeResult(t, result.Content[0].Text, &out)
	if out.PathWritten == nil || *out.PathWritten != "/ws/x.md" {
		t.Fatalf("expected path_written /ws/x.md, got %+v", out.PathWritten)
	}
	if len(out.CodeRef) == 0 || len(out.Artifact) == 0 {
		t.Fatalf("expected non-empty code_ref + artifact, got %+v", out)
	}
}

func TestWriteCapture_InvalidSource_ClientRefusal(t *testing.T) {
	called := false
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})

	handler := makeWriteCaptureHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"project_id":     "prj_abc",
		"source":         "twitter", // not in CAPTURE_SOURCES
		"markdown":       "x",
		"suggested_path": "/ws/x.md",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected client-side error, got success: %s", result.Content[0].Text)
	}
	if called {
		t.Fatalf("expected NO HTTP call for invalid source")
	}
}

func TestWriteCapture_InvalidMode_ClientRefusal(t *testing.T) {
	called := false
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})

	handler := makeWriteCaptureHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"project_id":     "prj_abc",
		"source":         "slack",
		"markdown":       "x",
		"suggested_path": "/ws/x.md",
		"mode":           "merge", // invalid
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected client-side error, got success: %s", result.Content[0].Text)
	}
	if called {
		t.Fatalf("expected NO HTTP call for invalid mode")
	}
}

func TestWriteCapture_ICC400_PropagatesErrorMessage(t *testing.T) {
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{
			"error": "path outside allowlist",
		})
	})

	handler := makeWriteCaptureHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"project_id":     "prj_abc",
		"source":         "slack",
		"markdown":       "x",
		"suggested_path": "/etc/passwd",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error result, got success: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "allowlist") {
		t.Fatalf("expected server error message bubbled up, got: %s", result.Content[0].Text)
	}
}

func TestWriteCapture_MissingBaseURL_ICCNotConfigured(t *testing.T) {
	icc := &iccClient{baseURL: "", httpClient: &http.Client{}, logger: slog.Default()}
	handler := makeWriteCaptureHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"project_id":     "prj_abc",
		"source":         "slack",
		"markdown":       "x",
		"suggested_path": "/ws/x.md",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected ICC-not-configured error, got success")
	}
	if !strings.Contains(result.Content[0].Text, "ICC_BASE_URL") {
		t.Fatalf("expected ICC_BASE_URL in error, got: %s", result.Content[0].Text)
	}
}

// --- icc_promote_to_artifact --------------------------------------------

func TestPromote_FreshPromotionReturnsFreshTrue(t *testing.T) {
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/code/refs/promote" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"already_promoted": false,
				"artifact":         map[string]any{"id": "art_1"},
				"code_ref":         map[string]any{"id": "cref_1"},
			},
		})
	})

	handler := makePromoteToArtifactHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"code_ref_id": "cref_1",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}

	var out promoteToolResult
	decodeResult(t, result.Content[0].Text, &out)
	if out.AlreadyPromoted {
		t.Fatalf("expected already_promoted=false")
	}
	if !out.Fresh {
		t.Fatalf("expected fresh=true")
	}
}

func TestPromote_IdempotentReturnsFreshFalse(t *testing.T) {
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"ok": true,
			"result": map[string]any{
				"already_promoted": true,
				"artifact":         map[string]any{"id": "art_existing"},
				"code_ref":         map[string]any{"id": "cref_1"},
			},
		})
	})

	handler := makePromoteToArtifactHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"code_ref_id": "cref_1",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}

	var out promoteToolResult
	decodeResult(t, result.Content[0].Text, &out)
	if !out.AlreadyPromoted {
		t.Fatalf("expected already_promoted=true")
	}
	if out.Fresh {
		t.Fatalf("expected fresh=false")
	}
}

func TestPromote_404PropagatesNotFound(t *testing.T) {
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"error": "code_ref not found",
		})
	})

	handler := makePromoteToArtifactHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"code_ref_id": "cref_missing",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "not found") {
		t.Fatalf("expected 'not found' in error, got: %s", result.Content[0].Text)
	}
}

func TestPromote_400FolderKindPropagatesServerMessage(t *testing.T) {
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{
			"error": "cannot promote folder ref",
		})
	})

	handler := makePromoteToArtifactHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"code_ref_id": "cref_folder",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error, got success: %s", result.Content[0].Text)
	}
	if !strings.Contains(result.Content[0].Text, "folder") {
		t.Fatalf("expected server message 'folder' in error, got: %s", result.Content[0].Text)
	}
}

// --- icc_demote_artifact ------------------------------------------------

func TestDemote_HappyPath_URLTemplatesIDAndForwardsKeepCodeRef(t *testing.T) {
	var seen map[string]any
	var seenPath string

	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = readBody(t, r)
		seenPath = r.URL.Path
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"artifact":          map[string]any{"id": "art_1", "deleted_at": "2026-05-17T10:00:00Z"},
				"kept_code_refs":    []map[string]any{{"id": "cref_1"}},
				"dropped_code_refs": []map[string]any{},
			},
		})
	})

	handler := makeDemoteArtifactHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"artifact_id":   "art_1",
		"reason":        "Promoted in error",
		"keep_code_ref": true,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}

	if seenPath != "/api/artifacts/art_1/demote" {
		t.Fatalf("expected URL to template artifact_id, got %s", seenPath)
	}
	if seen["keep_code_ref"] != true {
		t.Fatalf("expected keep_code_ref=true forwarded, got %+v", seen)
	}
	if seen["reason"] != "Promoted in error" {
		t.Fatalf("expected reason forwarded, got %+v", seen)
	}

	var out demoteToolResult
	decodeResult(t, result.Content[0].Text, &out)
	if !out.CodeRefUnlinked {
		t.Fatalf("expected code_ref_unlinked=true when kept_code_refs non-empty")
	}
	if out.CodeRefDeleted {
		t.Fatalf("expected code_ref_deleted=false when dropped_code_refs empty")
	}
}

func TestDemote_EmptyReason_ClientRefusal(t *testing.T) {
	called := false
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})

	handler := makeDemoteArtifactHandler(icc)
	// Whitespace-only reason
	result, err := handler(context.Background(), map[string]any{
		"artifact_id": "art_1",
		"reason":      "   ",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected client-side error for whitespace reason, got success")
	}
	if called {
		t.Fatalf("expected NO HTTP call for whitespace reason")
	}
}

func TestDemote_EmptyArtifactID_ClientRefusal(t *testing.T) {
	called := false
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})

	handler := makeDemoteArtifactHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"artifact_id": "",
		"reason":      "x",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected client-side error for empty artifact_id, got success")
	}
	if called {
		t.Fatalf("expected NO HTTP call for empty artifact_id")
	}
}

func TestDemote_404PropagatesNotFound(t *testing.T) {
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"error": "artifact not found",
		})
	})

	handler := makeDemoteArtifactHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"artifact_id": "art_missing",
		"reason":      "test",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected error, got success")
	}
	if !strings.Contains(result.Content[0].Text, "not found") {
		t.Fatalf("expected 'not found' in error, got: %s", result.Content[0].Text)
	}
}

// --- icc_capture_slack --------------------------------------------------

func TestCaptureSlack_FormatsAndPostsFrontmatter(t *testing.T) {
	var seen map[string]any

	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/captures" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seen = readBody(t, r)
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code_ref":     map[string]any{"id": "cref_1"},
				"artifact":     map[string]any{"id": "art_1"},
				"path_written": "/workspace/icc-project-workspaces/projects/vendor-x/slack/2026-05-17-hey-team.md",
			},
		})
	})

	handler := makeCaptureSlackHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         "alice  10:14 AM\nHey team, this is one message.\n",
		"project_id":   "prj_abc",
		"project_slug": "vendor-x",
		"channel":      "general",
		"captured_at":  "2026-05-17T10:14:00-04:00",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}

	// Posted body's markdown must include the frontmatter line for slack.
	md, _ := seen["markdown"].(string)
	if !strings.Contains(md, "source: slack") {
		t.Fatalf("expected slack frontmatter in posted markdown, got:\n%s", md)
	}
	if !strings.Contains(md, "project: vendor-x") {
		t.Fatalf("expected project slug in frontmatter, got:\n%s", md)
	}
	if seen["project_id"] != "prj_abc" {
		t.Fatalf("expected project_id=prj_abc in body, got %+v", seen["project_id"])
	}
	if seen["source"] != "slack" {
		t.Fatalf("expected source=slack hardcoded, got %+v", seen["source"])
	}

	// Tool result echoes suggested_filename for caller visibility.
	var out captureSlackResult
	decodeResult(t, result.Content[0].Text, &out)
	if !strings.HasSuffix(out.SuggestedPath, ".md") || !strings.Contains(out.SuggestedPath, "/vendor-x/slack/") {
		t.Fatalf("unexpected suggested_path: %s", out.SuggestedPath)
	}
	if out.SuggestedFilename == "" {
		t.Fatalf("expected suggested_filename echoed back")
	}
}

func TestCaptureSlack_ModeRawFlowsThrough(t *testing.T) {
	var seen map[string]any
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = readBody(t, r)
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code_ref":     map[string]any{"id": "cref_1"},
				"artifact":     nil,
				"path_written": "/ws/x.md",
			},
		})
	})

	handler := makeCaptureSlackHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         "alice  10:14 AM\nbody\n",
		"project_id":   "prj_abc",
		"project_slug": "vendor-x",
		"channel":      "general",
		"mode":         "raw",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}
	if seen["mode"] != "raw" {
		t.Fatalf("expected mode=raw forwarded, got %+v", seen["mode"])
	}
}

// --- icc_capture_email --------------------------------------------------

func TestCaptureEmail_FormatsAndPostsFrontmatter(t *testing.T) {
	var seen map[string]any

	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/captures" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seen = readBody(t, r)
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code_ref":     map[string]any{"id": "cref_1"},
				"artifact":     map[string]any{"id": "art_1"},
				"path_written": "/workspace/icc-project-workspaces/projects/vendor-x/email/2026-05-14-weekly-audit.md",
			},
		})
	})

	emailText := strings.Join([]string{
		"From: alice@example.com",
		"Subject: Weekly Audit Inventory",
		"Date: Thu, 14 May 2026 09:15:00 -0400",
		"",
		"body line one",
	}, "\n")

	handler := makeCaptureEmailHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         emailText,
		"project_id":   "prj_abc",
		"project_slug": "vendor-x",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}

	// Posted body's markdown must include email frontmatter.
	md, _ := seen["markdown"].(string)
	if !strings.Contains(md, "source: email") {
		t.Fatalf("expected email frontmatter in posted markdown, got:\n%s", md)
	}
	if !strings.Contains(md, "project: vendor-x") {
		t.Fatalf("expected project slug in frontmatter:\n%s", md)
	}
	if seen["project_id"] != "prj_abc" {
		t.Fatalf("expected project_id=prj_abc in body, got %+v", seen["project_id"])
	}
	if seen["source"] != "email" {
		t.Fatalf("expected source=email hardcoded, got %+v", seen["source"])
	}
	// Default mode = both for email.
	if seen["mode"] != "both" {
		t.Fatalf("expected default mode=both for email, got %+v", seen["mode"])
	}

	var out captureEmailResult
	decodeResult(t, result.Content[0].Text, &out)
	if !strings.Contains(out.SuggestedPath, "/vendor-x/email/") {
		t.Fatalf("unexpected suggested_path: %s", out.SuggestedPath)
	}
	if out.DetectedSubject == "" {
		t.Fatalf("expected detected_subject echoed back")
	}
}

func TestCaptureEmail_EmptyText_ClientRefusal(t *testing.T) {
	called := false
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})

	handler := makeCaptureEmailHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         "   ",
		"project_id":   "prj_abc",
		"project_slug": "vendor-x",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected client-side error for empty text, got success")
	}
	if called {
		t.Fatalf("expected NO HTTP call for empty text")
	}
}

// --- icc_capture_meeting -----------------------------------------------

func TestCaptureMeeting_FormatsAndPostsFrontmatter(t *testing.T) {
	var seen map[string]any

	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/captures" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seen = readBody(t, r)
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code_ref":     map[string]any{"id": "cref_1"},
				"artifact":     map[string]any{"id": "art_1"},
				"path_written": "/workspace/icc-project-workspaces/projects/vendor-x/meetings/2026-05-12-cody-nadia-1on1.md",
			},
		})
	})

	handler := makeCaptureMeetingHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         "# 1:1 — Cody & Nadia\n\nNotes",
		"project_id":   "prj_abc",
		"project_slug": "vendor-x",
		"participants": []any{"Cody Blevins", "Nadia Patel"},
		"captured_at":  "2026-05-12T14:00:00-04:00",
		"topic":        "1on1",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}

	md, _ := seen["markdown"].(string)
	if !strings.Contains(md, "source: meeting") {
		t.Fatalf("expected meeting frontmatter in posted markdown, got:\n%s", md)
	}
	if seen["source"] != "meeting" {
		t.Fatalf("expected source=meeting hardcoded, got %+v", seen["source"])
	}
	if seen["mode"] != "both" {
		t.Fatalf("expected default mode=both for meeting, got %+v", seen["mode"])
	}

	var out captureMeetingResult
	decodeResult(t, result.Content[0].Text, &out)
	if out.SuggestedFilename != "2026-05-12-cody-nadia-1on1.md" {
		t.Fatalf("expected meeting filename echoed back, got %q", out.SuggestedFilename)
	}
}

func TestCaptureMeeting_MissingParticipants_ClientRefusal(t *testing.T) {
	called := false
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
		writeJSON(t, w, http.StatusOK, map[string]any{})
	})

	handler := makeCaptureMeetingHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         "notes",
		"project_id":   "prj_abc",
		"project_slug": "vendor-x",
		// participants omitted
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected client-side error for missing participants, got success")
	}
	if called {
		t.Fatalf("expected NO HTTP call for missing participants")
	}
}

// --- icc_capture_standup -----------------------------------------------

func TestCaptureStandup_DefaultModeIsIngest(t *testing.T) {
	var seen map[string]any

	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/captures" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		seen = readBody(t, r)
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code_ref":     nil,
				"artifact":     map[string]any{"id": "art_1"},
				"path_written": nil,
			},
		})
	})

	handler := makeCaptureStandupHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         "Yesterday: x.\nToday: y.\nBlocked: z.",
		"project_id":   "prj_abc",
		"project_slug": "_inbox",
		"captured_at":  "2026-05-17T09:00:00-04:00",
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}

	if seen["source"] != "standup" {
		t.Fatalf("expected source=standup hardcoded, got %+v", seen["source"])
	}
	// Per-source default: standup is ephemeral, defaults to ingest-only.
	if seen["mode"] != "ingest" {
		t.Fatalf("expected default mode=ingest for standup, got %+v", seen["mode"])
	}
	md, _ := seen["markdown"].(string)
	if !strings.Contains(md, "source: standup") {
		t.Fatalf("expected standup frontmatter, got:\n%s", md)
	}

	var out captureStandupResult
	decodeResult(t, result.Content[0].Text, &out)
	if out.SuggestedFilename != "2026-05-17-standup-prep.md" {
		t.Fatalf("expected standup-prep filename for _inbox personal prep, got %q", out.SuggestedFilename)
	}
	// Standup files target research/ (no standup/ folder in STRUCTURE.md).
	if !strings.Contains(out.SuggestedPath, "/_inbox/research/") {
		t.Fatalf("expected research/ folder under _inbox, got %s", out.SuggestedPath)
	}
}

func TestCaptureStandup_ModeOverrideFlowsThrough(t *testing.T) {
	var seen map[string]any
	_, icc := newTestICCServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = readBody(t, r)
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"ok": true,
			"result": map[string]any{
				"code_ref":     map[string]any{"id": "cref_1"},
				"artifact":     map[string]any{"id": "art_1"},
				"path_written": "/ws/x.md",
			},
		})
	})

	handler := makeCaptureStandupHandler(icc)
	result, err := handler(context.Background(), map[string]any{
		"text":         "notes",
		"project_id":   "prj_abc",
		"project_slug": "vendor-x",
		"team":         "PMT",
		"mode":         "both", // override default
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got: %s", result.Content[0].Text)
	}
	if seen["mode"] != "both" {
		t.Fatalf("expected mode override=both, got %+v", seen["mode"])
	}
}

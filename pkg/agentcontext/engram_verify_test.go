package agentcontext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// detectProofKind
// ---------------------------------------------------------------------------

func TestDetectProofKind(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                                   "",
		"command: go test ./...":             "command",
		"line is\ncommand: go test\nbench:":  "command",
		"https://example.com":                "url",
		"see https://anthropic.com for docs": "url",
		"pkg/foo/bar.go":                     "file_ref",
		"pkg/foo/bar.go:42":                  "file_ref",
		"pkg/foo/bar.go:42-58":               "file_ref",
		"/abs/path.go:1-10":                  "file_ref",
		// Bare words are classified as file_ref candidates; checkFileRef
		// flags them stale when the path doesn't exist on disk. This keeps
		// detectProofKind purely lexical.
		"plainword":  "file_ref",
		"foo.bar":    "file_ref",
		"   ":        "",
		"  \n  \t  ": "",
	}
	for input, want := range cases {
		got := detectProofKind(input)
		if got != want {
			t.Errorf("detectProofKind(%q) = %q, want %q", input, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// parseFileRef
// ---------------------------------------------------------------------------

func TestParseFileRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in         string
		path       string
		start, end int
	}{
		{"pkg/foo/bar.go", "pkg/foo/bar.go", 0, 0},
		{"pkg/foo/bar.go:42", "pkg/foo/bar.go", 42, 0},
		{"pkg/foo/bar.go:42-58", "pkg/foo/bar.go", 42, 58},
		{"/abs/path.txt:1-10", "/abs/path.txt", 1, 10},
		{"path:notanumber", "path:notanumber", 0, 0},
		{"", "", 0, 0},
		{"a.go:5 ignored trailing", "a.go", 5, 0},
	}
	for _, tc := range cases {
		p, s, e := parseFileRef(tc.in)
		if p != tc.path || s != tc.start || e != tc.end {
			t.Errorf("parseFileRef(%q) = (%q,%d,%d), want (%q,%d,%d)",
				tc.in, p, s, e, tc.path, tc.start, tc.end)
		}
	}
}

// ---------------------------------------------------------------------------
// extractURL
// ---------------------------------------------------------------------------

func TestExtractURL(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"":                                     "",
		"https://example.com":                  "https://example.com",
		"see https://example.com/docs.":        "https://example.com/docs",
		"http://x.local:8080/foo":              "http://x.local:8080/foo",
		"no url here":                          "",
		"trailing https://example.com).":       "https://example.com",
		"first https://a.com second https://b": "https://a.com",
	}
	for input, want := range cases {
		got := extractURL(input)
		if got != want {
			t.Errorf("extractURL(%q) = %q, want %q", input, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// checkFileRef
// ---------------------------------------------------------------------------

func TestCheckFileRef_ExistingFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "ok.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, reason := checkFileRef("ok.txt", tmp)
	if !ok {
		t.Errorf("expected verified, got reason=%q", reason)
	}
}

func TestCheckFileRef_MissingFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	ok, reason := checkFileRef("missing.txt", tmp)
	if ok {
		t.Error("expected stale (file missing)")
	}
	if !strings.Contains(reason, "stat") {
		t.Errorf("reason should mention stat: %q", reason)
	}
}

func TestCheckFileRef_LineRangeOK(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "lines.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, reason := checkFileRef("lines.txt:2-4", tmp)
	if !ok {
		t.Errorf("expected verified, got reason=%q", reason)
	}
}

func TestCheckFileRef_LineRangeOutOfBounds(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "short.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, reason := checkFileRef("short.txt:1-99", tmp)
	if ok {
		t.Error("expected stale (line range out of bounds)")
	}
	if !strings.Contains(reason, "exceeds") {
		t.Errorf("reason should mention exceeds: %q", reason)
	}
}

func TestCheckFileRef_DirectoryRejected(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	subdir := filepath.Join(tmp, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	ok, reason := checkFileRef("subdir", tmp)
	if ok {
		t.Error("expected stale for directory ref")
	}
	if !strings.Contains(reason, "directory") {
		t.Errorf("reason should mention directory: %q", reason)
	}
}

// ---------------------------------------------------------------------------
// checkURL
// ---------------------------------------------------------------------------

func TestCheckURL_2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ok, reason := checkURL(context.Background(), srv.Client(), srv.URL)
	if !ok {
		t.Errorf("expected verified, reason=%q", reason)
	}
}

func TestCheckURL_404(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	ok, reason := checkURL(context.Background(), srv.Client(), srv.URL)
	if ok {
		t.Error("expected failing for 404")
	}
	if !strings.Contains(reason, "404") {
		t.Errorf("reason should mention 404: %q", reason)
	}
}

func TestCheckURL_EmptyURL(t *testing.T) {
	t.Parallel()
	ok, reason := checkURL(context.Background(), http.DefaultClient, "")
	if ok {
		t.Error("expected failing for empty URL")
	}
	if reason == "" {
		t.Error("reason should not be empty")
	}
}

// ---------------------------------------------------------------------------
// HandleEngramVerify (integration)
// ---------------------------------------------------------------------------

func TestHandleEngramVerify_FileRefVerified(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	// Create a real file the proof can point at.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "real.go")
	if err := os.WriteFile(path, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title":    "real-file engram",
		"problem":  "p",
		"solution": "s",
		"proof":    "real.go:1",
		"family":   "real-file",
		"slug":     "x",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	res, err := svc.HandleEngramVerify(ctx, map[string]any{
		"uri":       "engram://real-file/x",
		"repo":      "test-repo",
		"repo_root": tmp,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if res.IsError {
		t.Fatalf("error result: %+v", res)
	}
	payload := readResultJSON(t, res)
	results := payload["results"].([]any)
	first := results[0].(map[string]any)
	if first["status"] != "verified" {
		t.Errorf("expected verified, got %v reason=%v", first["status"], first["reason"])
	}

	// Confirm proof_status was flipped on the stored item.
	item, err := svc.lookupEngramByURI("engram://real-file/x")
	if err != nil || item == nil {
		t.Fatalf("lookup: %v", err)
	}
	if metadataString(item.Metadata, mdEngramProofStatus) != ProofStatusVerified {
		t.Errorf("status not persisted; got %v", item.Metadata[mdEngramProofStatus])
	}
	unlocked := metadataStringSlice(item.Metadata, mdEngramUnlockedIn)
	if !contains(unlocked, "test-repo") {
		t.Errorf("unlocked_in missing repo; got %v", unlocked)
	}
}

func TestHandleEngramVerify_FileRefStale(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()
	tmp := t.TempDir()

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title":    "missing-file engram",
		"problem":  "p",
		"solution": "s",
		"proof":    "absent.go:1",
		"family":   "missing-file",
		"slug":     "x",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	res, err := svc.HandleEngramVerify(ctx, map[string]any{
		"uri":       "engram://missing-file/x",
		"repo_root": tmp,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	payload := readResultJSON(t, res)
	first := payload["results"].([]any)[0].(map[string]any)
	if first["status"] != "stale" {
		t.Errorf("expected stale, got %v", first["status"])
	}

	item, _ := svc.lookupEngramByURI("engram://missing-file/x")
	if metadataString(item.Metadata, mdEngramProofStatus) != ProofStatusStale {
		t.Errorf("expected proof_status=stale, got %v", item.Metadata[mdEngramProofStatus])
	}
	if contains(metadataStringSlice(item.Metadata, mdEngramUnlockedIn), "any") {
		t.Error("stale verify should not append to unlocked_in")
	}
}

func TestHandleEngramVerify_URLVerified(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := newEngramTestService()
	ctx := context.Background()
	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title":    "url engram",
		"problem":  "p",
		"solution": "s",
		"proof":    srv.URL,
		"family":   "url-engram",
		"slug":     "x",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Override the HTTP client by piping through the service indirectly:
	// HandleEngramVerify uses default client unless we mutate VerifyOptions
	// through the args path. For tests, the default 5s timeout against an
	// httptest server is fine.
	res, err := svc.HandleEngramVerify(ctx, map[string]any{
		"uri": "engram://url-engram/x",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	payload := readResultJSON(t, res)
	first := payload["results"].([]any)[0].(map[string]any)
	if first["status"] != "verified" {
		t.Errorf("expected verified, got %v reason=%v", first["status"], first["reason"])
	}
}

func TestHandleEngramVerify_CommandSkipped(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()

	if _, err := svc.HandleEngramAdd(ctx, map[string]any{
		"title":    "tier-2 engram",
		"problem":  "p",
		"solution": "s",
		"proof":    "command: go test ./pkg/foo",
		"tier":     2,
		"family":   "cmd-engram",
		"slug":     "x",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	res, err := svc.HandleEngramVerify(ctx, map[string]any{
		"uri": "engram://cmd-engram/x",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	payload := readResultJSON(t, res)
	first := payload["results"].([]any)[0].(map[string]any)
	if first["status"] != "skipped" {
		t.Errorf("expected skipped, got %v", first["status"])
	}
	if first["proof_kind"] != "command" {
		t.Errorf("expected proof_kind=command, got %v", first["proof_kind"])
	}

	// Skipped should NOT downgrade an existing status.
	item, _ := svc.lookupEngramByURI("engram://cmd-engram/x")
	if metadataString(item.Metadata, mdEngramProofStatus) != ProofStatusUnverified {
		t.Errorf("skipped verify should leave status unchanged; got %v", item.Metadata[mdEngramProofStatus])
	}
}

func TestHandleEngramVerify_AllAggregates(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	ctx := context.Background()
	tmp := t.TempDir()
	good := filepath.Join(tmp, "g.go")
	_ = os.WriteFile(good, []byte("package x\n"), 0o644)

	cases := []map[string]any{
		{"title": "g", "problem": "p", "solution": "s", "proof": "g.go:1", "family": "all-good", "slug": "x"},
		{"title": "b", "problem": "p", "solution": "s", "proof": "missing.go:1", "family": "all-bad", "slug": "x"},
	}
	for _, args := range cases {
		if _, err := svc.HandleEngramAdd(ctx, args); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	res, err := svc.HandleEngramVerify(ctx, map[string]any{
		"all":       true,
		"repo_root": tmp,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	payload := readResultJSON(t, res)
	if payload["count"].(float64) != 2 {
		t.Errorf("expected count=2, got %v", payload["count"])
	}
	summary := payload["summary"].(map[string]any)
	if summary["verified"].(float64) != 1 {
		t.Errorf("expected 1 verified, got %v", summary["verified"])
	}
	if summary["stale"].(float64) != 1 {
		t.Errorf("expected 1 stale, got %v", summary["stale"])
	}
}

func TestHandleEngramVerify_RequiresURIOrAll(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	res, err := svc.HandleEngramVerify(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.IsError {
		t.Error("expected error when neither uri nor all is set")
	}
}

func TestHandleEngramVerify_UnknownURI(t *testing.T) {
	t.Parallel()
	svc := newEngramTestService()
	res, err := svc.HandleEngramVerify(context.Background(), map[string]any{
		"uri": "engram://does-not-exist/x",
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !res.IsError {
		t.Error("expected error for unknown URI")
	}
}

// ---------------------------------------------------------------------------
// inferRepoName
// ---------------------------------------------------------------------------

func TestInferRepoName(t *testing.T) {
	t.Parallel()
	// Invariant: never returns empty (cwd basename or "" if Getwd fails).
	got := inferRepoName()
	if got == "" {
		t.Error("inferRepoName returned empty for a normal cwd")
	}
}

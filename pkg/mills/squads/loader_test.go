package squads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

const validHUDFrontend = `
apiVersion: mills.loom.dev/v1
kind: Squad
metadata:
  name: hud-frontend
spec:
  paths:
    - "internal/hud/frontend/**"
    - "mcp/skills/hud-*"
  tests:
    - pnpm-typecheck
    - pnpm-vitest
  gates:
    required: [pr_self_review, scope, secret_scan]
    advisory: [coverage]
  ensemble:
    editor:
      backend: spawn
      driver: claude-opus
      max_cost_usd: 4.0
    reviewers:
      - backend: flexinfer
        model: llama-4-70b-instruct
        lens: ux
  budget_share: 0.30
  recursion_enabled: false
`

const validGitops = `
apiVersion: mills.loom.dev/v1
kind: Squad
metadata:
  name: gitops
spec:
  paths:
    - "platform/gitops/**"
  tests:
    - kustomize-build
  gates:
    required: [diff_size, scope, path_policy, secret_scan, pr_self_review]
  ensemble:
    editor:
      backend: spawn
      driver: codex-gpt5
  budget_share: 0.20
  recursion_enabled: false
`

const validDisabled = `
apiVersion: mills.loom.dev/v1
kind: Squad
metadata:
  name: legacy-paused
spec:
  paths: ["legacy/**"]
  enabled: false
`

const invalidBadAPIVersion = `
apiVersion: mills.loom.dev/v0
kind: Squad
metadata:
  name: oops
spec:
  paths: ["foo/**"]
`

const invalidUnknownGateBucket = `
apiVersion: mills.loom.dev/v1
kind: Squad
metadata:
  name: bad-gates
spec:
  paths: ["foo/**"]
  gates:
    not-a-bucket: [coverage]
`

const invalidBadGlob = `
apiVersion: mills.loom.dev/v1
kind: Squad
metadata:
  name: bad-glob
spec:
  paths: ["[unterminated"]
`

func writeManifest(t *testing.T, dir, file, body string) string {
	t.Helper()
	path := filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	return path
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{
		Path: filepath.Join(dir, "mills.db"),
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestLoader_LoadsValidManifests(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "hud-frontend.yaml", validHUDFrontend)
	writeManifest(t, dir, "gitops.yaml", validGitops)

	st := newTestStore(t)
	l, err := NewLoader(context.Background(), dir, st, LoaderOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	defer func() { _ = l.Close() }()

	got := l.Current()
	if len(got) != 2 {
		t.Fatalf("loaded count: got %d want 2 (%v)", len(got), keysOf(got))
	}
	if got["hud-frontend"] == nil || got["gitops"] == nil {
		t.Errorf("missing manifest names: %v", keysOf(got))
	}

	rows, err := st.Squads.ListSquads(context.Background())
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("rows: got %d want 2", len(rows))
	}
	for _, r := range rows {
		if r.LastLoadedSHA == "" {
			t.Errorf("squad %s missing LastLoadedSHA", r.Name)
		}
	}
}

func TestLoader_DisabledManifestStillReflects(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "legacy.yaml", validDisabled)
	st := newTestStore(t)

	l, err := NewLoader(context.Background(), dir, st, LoaderOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	defer func() { _ = l.Close() }()

	r, err := st.Squads.GetSquad(context.Background(), "legacy-paused")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if r.Enabled {
		t.Errorf("disabled manifest should reflect Enabled=false in store")
	}
}

func TestLoader_BadManifestIsolatedFromGood(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "hud-frontend.yaml", validHUDFrontend)
	writeManifest(t, dir, "broken.yaml", invalidBadAPIVersion)
	writeManifest(t, dir, "bad-gates.yaml", invalidUnknownGateBucket)
	writeManifest(t, dir, "bad-glob.yaml", invalidBadGlob)

	var collected []string
	st := newTestStore(t)
	l, err := NewLoader(context.Background(), dir, st, LoaderOptions{
		SkipWatch: true,
		OnError: func(e error) {
			collected = append(collected, e.Error())
		},
	})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	defer func() { _ = l.Close() }()

	got := l.Current()
	if len(got) != 1 || got["hud-frontend"] == nil {
		t.Errorf("expected only hud-frontend to load: %v", keysOf(got))
	}
	if len(collected) != 3 {
		t.Errorf("expected 3 OnError calls, got %d: %v", len(collected), collected)
	}
}

func TestLoader_HotReloadOnFileWrite(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "hud-frontend.yaml", validHUDFrontend)
	st := newTestStore(t)

	updated := make(chan map[string]*Manifest, 4)
	l, err := NewLoader(context.Background(), dir, st, LoaderOptions{})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	defer func() { _ = l.Close() }()
	l.Subscribe(func(m map[string]*Manifest) {
		// non-blocking — swallow extras under a tight test channel
		select {
		case updated <- m:
		default:
		}
	})

	// Add a new manifest; loader should pick it up.
	writeManifest(t, dir, "gitops.yaml", validGitops)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case snap := <-updated:
			if len(snap) == 2 {
				return // success
			}
		case <-deadline:
			got := l.Current()
			t.Fatalf("hot reload did not pick up new manifest within 3s: %v", keysOf(got))
		}
	}
}

func TestLoader_BadManifestKeepsLastGoodCache(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, "hud-frontend.yaml", validHUDFrontend)
	st := newTestStore(t)

	l, err := NewLoader(context.Background(), dir, st, LoaderOptions{SkipWatch: true})
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	defer func() { _ = l.Close() }()
	if len(l.Current()) != 1 {
		t.Fatalf("initial load count: %d", len(l.Current()))
	}

	// Corrupt the file. Sync should drop hud-frontend from the cache
	// because the file's bytes no longer parse — but the canonical-store
	// row from the first good load remains (we don't auto-delete).
	if err := os.WriteFile(filepath.Join(dir, "hud-frontend.yaml"),
		[]byte("not: yaml: at all: ["), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	var lastErr error
	l.opts.OnError = func(e error) { lastErr = e }
	if err := l.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(l.Current()) != 0 {
		t.Errorf("corrupt manifest should drop from cache; got %v", keysOf(l.Current()))
	}
	if lastErr == nil {
		t.Error("OnError should have fired on corrupt file")
	}
	// Store row from earlier good reload still present.
	if _, err := st.Squads.GetSquad(context.Background(), "hud-frontend"); err != nil {
		t.Errorf("store row should survive bad reload: %v", err)
	}
}

func TestLoader_RejectsMissingDir(t *testing.T) {
	st := newTestStore(t)
	_, err := NewLoader(context.Background(), filepath.Join(t.TempDir(), "nope"), st, LoaderOptions{SkipWatch: true})
	if err == nil {
		t.Error("expected error for missing dir")
	}
	if !strings.Contains(err.Error(), "read dir") {
		t.Errorf("error should reference read dir: %v", err)
	}
}

// keysOf returns the sorted keys of a manifest map for stable error logs.
func keysOf(m map[string]*Manifest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

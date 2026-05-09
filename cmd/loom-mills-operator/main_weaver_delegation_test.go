package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/clients"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// flexInferConfigForTest returns a stub FlexInfer client suitable for
// constructing a WeaverClient. attachWeaverDelegation never calls into
// the chat path, so a stub URL is enough.
func flexInferConfigForTest(t *testing.T) *clients.FlexInferClient {
	t.Helper()
	c, err := clients.NewFlexInferClient(clients.FlexInferConfig{ProxyURL: "http://stub"})
	if err != nil {
		t.Fatalf("flex client: %v", err)
	}
	return c
}

// openTestStore mints a real SQLite-backed store for the recorder
// branch. The recorder doesn't write at construction; we just need a
// non-nil store.PipelineDAO.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), store.Options{Path: filepath.Join(dir, "mills.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAttachWeaverDelegation_OnMode_WithURL_SetsDelegatorOnly(t *testing.T) {
	t.Setenv(clients.EnvResearchMode, "on")
	flex := flexInferConfigForTest(t)
	wc := clients.NewWeaverClient(flex)
	if wc.Mode != clients.ResearchModeOn {
		t.Fatalf("setup: mode = %q, want on", wc.Mode)
	}

	cfg := Config{WeaverURL: "http://mobile-hud.loom-hub.svc.cluster.local"}
	attachWeaverDelegation(wc, cfg, openTestStore(t), discardLogger())

	if wc.Delegator == nil {
		t.Error("expected Delegator to be set when URL configured")
	}
	if wc.DiffRecorder != nil {
		t.Error("expected DiffRecorder to remain nil in on mode (only shadow records)")
	}
}

func TestAttachWeaverDelegation_ShadowMode_WithURLAndStore_SetsBoth(t *testing.T) {
	t.Setenv(clients.EnvResearchMode, "shadow")
	flex := flexInferConfigForTest(t)
	wc := clients.NewWeaverClient(flex)
	if wc.Mode != clients.ResearchModeShadow {
		t.Fatalf("setup: mode = %q, want shadow", wc.Mode)
	}

	cfg := Config{WeaverURL: "http://mobile-hud.loom-hub.svc.cluster.local"}
	attachWeaverDelegation(wc, cfg, openTestStore(t), discardLogger())

	if wc.Delegator == nil {
		t.Error("expected Delegator to be set")
	}
	if wc.DiffRecorder == nil {
		t.Error("expected DiffRecorder to be set in shadow mode")
	}
}

func TestAttachWeaverDelegation_ShadowMode_NilStore_SkipsRecorder(t *testing.T) {
	t.Setenv(clients.EnvResearchMode, "shadow")
	flex := flexInferConfigForTest(t)
	wc := clients.NewWeaverClient(flex)

	cfg := Config{WeaverURL: "http://hud.example"}
	attachWeaverDelegation(wc, cfg, nil, discardLogger())

	if wc.Delegator == nil {
		t.Error("expected Delegator to be set even when store is nil")
	}
	if wc.DiffRecorder != nil {
		t.Error("expected DiffRecorder to stay nil when store is unavailable")
	}
}

func TestAttachWeaverDelegation_NoURL_SkipsDelegator(t *testing.T) {
	t.Setenv(clients.EnvResearchMode, "on")
	flex := flexInferConfigForTest(t)
	wc := clients.NewWeaverClient(flex)

	// Empty WeaverURL AND empty HUDBaseURL → delegator must not be set.
	attachWeaverDelegation(wc, Config{}, openTestStore(t), discardLogger())

	if wc.Delegator != nil {
		t.Error("expected Delegator to be nil when no URL configured")
	}
	if wc.DiffRecorder != nil {
		t.Error("expected DiffRecorder to be nil when delegator wiring is skipped")
	}
}

func TestAttachWeaverDelegation_WeaverURL_FallsBackToHUDBaseURL(t *testing.T) {
	t.Setenv(clients.EnvResearchMode, "on")
	flex := flexInferConfigForTest(t)
	wc := clients.NewWeaverClient(flex)

	// WeaverURL empty; HUDBaseURL set — delegator should be wired
	// (single loomd hosts both surfaces today).
	cfg := Config{HUDBaseURL: "http://mobile-hud.loom-hub.svc.cluster.local"}
	attachWeaverDelegation(wc, cfg, openTestStore(t), discardLogger())

	if wc.Delegator == nil {
		t.Error("expected Delegator to be wired from HUDBaseURL fallback")
	}
}

func TestAttachWeaverDelegation_WeaverURL_OverridesHUDBaseURL(t *testing.T) {
	t.Setenv(clients.EnvResearchMode, "on")
	flex := flexInferConfigForTest(t)
	wc := clients.NewWeaverClient(flex)

	cfg := Config{
		WeaverURL:  "http://weaver.example",
		HUDBaseURL: "http://hud.example",
	}
	attachWeaverDelegation(wc, cfg, openTestStore(t), discardLogger())

	if wc.Delegator == nil {
		t.Fatal("expected Delegator to be set")
	}
	// We can't easily peek at the stored URL on the delegator without
	// exporting an accessor; the wiring decision (set vs nil) is what
	// this test asserts. Per-URL plumbing is covered by the delegator
	// tests in pkg/mills/clients.
}

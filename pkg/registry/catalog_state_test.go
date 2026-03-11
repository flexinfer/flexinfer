package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCatalogState_EnableDisable(t *testing.T) {
	cs := &CatalogState{}

	if cs.IsDisabled("tavily") {
		t.Error("expected tavily to be enabled by default")
	}

	if !cs.Disable("tavily") {
		t.Error("expected Disable to return true for new entry")
	}
	if !cs.IsDisabled("tavily") {
		t.Error("expected tavily to be disabled after Disable")
	}

	if cs.Disable("tavily") {
		t.Error("expected Disable to return false for already-disabled entry")
	}

	if !cs.Enable("tavily") {
		t.Error("expected Enable to return true for disabled entry")
	}
	if cs.IsDisabled("tavily") {
		t.Error("expected tavily to be enabled after Enable")
	}

	if cs.Enable("tavily") {
		t.Error("expected Enable to return false for already-enabled entry")
	}
}

func TestCatalogState_DisabledListSorted(t *testing.T) {
	cs := &CatalogState{}
	cs.Disable("zep")
	cs.Disable("git")
	cs.Disable("alertmanager")

	want := []string{"alertmanager", "git", "zep"}
	if len(cs.DisabledServers) != len(want) {
		t.Fatalf("got %d disabled, want %d", len(cs.DisabledServers), len(want))
	}
	for i, name := range cs.DisabledServers {
		if name != want[i] {
			t.Errorf("index %d: got %q, want %q", i, name, want[i])
		}
	}
}

func TestCatalogState_EnabledServers(t *testing.T) {
	reg := &Registry{
		Servers: []*Server{
			{Name: "git"},
			{Name: "gitlab"},
			{Name: "tavily"},
		},
	}

	cs := &CatalogState{}
	cs.Disable("gitlab")

	enabled := cs.EnabledServers(reg)
	if len(enabled) != 2 {
		t.Fatalf("got %d enabled, want 2", len(enabled))
	}
	if enabled[0].Name != "git" {
		t.Errorf("expected git, got %s", enabled[0].Name)
	}
	if enabled[1].Name != "tavily" {
		t.Errorf("expected tavily, got %s", enabled[1].Name)
	}
}

func TestCatalogState_SaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer func() { _ = os.Setenv("HOME", origHome) }()

	// Ensure config dir exists
	configDir := filepath.Join(tmpDir, ".config", "loom")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cs := &CatalogState{}
	cs.Disable("tavily")
	cs.Disable("git")

	if err := cs.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadCatalogState()
	if err != nil {
		t.Fatalf("LoadCatalogState: %v", err)
	}

	if !loaded.IsDisabled("tavily") {
		t.Error("expected tavily to be disabled after load")
	}
	if !loaded.IsDisabled("git") {
		t.Error("expected git to be disabled after load")
	}
	if loaded.IsDisabled("gitlab") {
		t.Error("expected gitlab to be enabled after load")
	}
}

func TestCatalogState_LoadMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cs, err := LoadCatalogState()
	if err != nil {
		t.Fatalf("LoadCatalogState: %v", err)
	}
	if len(cs.DisabledServers) != 0 {
		t.Errorf("expected empty disabled list, got %v", cs.DisabledServers)
	}
}

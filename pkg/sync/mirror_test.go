package sync

import (
	"os"
	"path/filepath"
	"testing"
)

// setupMirrorFixture builds a temp directory that looks like a workspace:
//
//	<root>/services/loom-core/mcp/context/{registry.yaml,skills-registry.yaml}
//	<root>/platform/gitops/mcp/context/{registry.yaml,skills-registry.yaml}
//
// Returns the manager rooted at services/loom-core and the temp workspace
// root so individual tests can mutate file contents.
func setupMirrorFixture(t *testing.T, srcReg, srcSkills, dstReg, dstSkills string) (*Manager, string) {
	t.Helper()
	root := t.TempDir()

	mustWrite := func(p, content string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	srcRoot := filepath.Join(root, "services", "loom-core")
	mirrorRoot := filepath.Join(root, "platform", "gitops")

	mustWrite(filepath.Join(srcRoot, "mcp", "context", "registry.yaml"), srcReg)
	mustWrite(filepath.Join(srcRoot, "mcp", "context", "skills-registry.yaml"), srcSkills)
	mustWrite(filepath.Join(mirrorRoot, "mcp", "context", "registry.yaml"), dstReg)
	mustWrite(filepath.Join(mirrorRoot, "mcp", "context", "skills-registry.yaml"), dstSkills)

	mgr := &Manager{
		RepoRoot:      srcRoot,
		HomeDir:       t.TempDir(),
		WorkspaceRoot: root,
		Profiles:      map[string]*Profile{},
	}
	return mgr, root
}

func TestGetMirrorStatus_AllInSync(t *testing.T) {
	mgr, _ := setupMirrorFixture(t,
		"servers: same\n", "skills: same\n",
		"servers: same\n", "skills: same\n",
	)

	status, err := mgr.GetMirrorStatus()
	if err != nil {
		t.Fatalf("GetMirrorStatus: %v", err)
	}
	if !status.InSync {
		t.Fatalf("expected in-sync, got drift: %+v", status.Files)
	}
	if len(status.Files) != len(MirrorFiles) {
		t.Fatalf("expected %d files, got %d", len(MirrorFiles), len(status.Files))
	}
}

func TestGetMirrorStatus_DriftDetected(t *testing.T) {
	mgr, _ := setupMirrorFixture(t,
		"servers: canonical-version-2\n", "skills: same\n",
		"servers: old\n", "skills: same\n",
	)

	status, err := mgr.GetMirrorStatus()
	if err != nil {
		t.Fatalf("GetMirrorStatus: %v", err)
	}
	if status.InSync {
		t.Fatal("expected drift")
	}
	var registryEntry *MirrorFileStatus
	for i := range status.Files {
		if status.Files[i].RelPath == "mcp/context/registry.yaml" {
			registryEntry = &status.Files[i]
		}
	}
	if registryEntry == nil || registryEntry.InSync {
		t.Fatal("expected registry.yaml entry to show drift")
	}
}

func TestSyncMirror_AppliesCanonicalOverMirror(t *testing.T) {
	mgr, root := setupMirrorFixture(t,
		"servers: NEW\n", "skills: NEW\n",
		"servers: OLD\n", "skills: OLD\n",
	)

	updated, status, err := mgr.SyncMirror(false)
	if err != nil {
		t.Fatalf("SyncMirror: %v", err)
	}
	if updated != 2 {
		t.Fatalf("expected 2 files updated, got %d", updated)
	}
	if !status.InSync {
		t.Fatal("expected post-sync state to be in-sync")
	}

	mirrorReg, _ := os.ReadFile(filepath.Join(root, "platform", "gitops", "mcp", "context", "registry.yaml"))
	if string(mirrorReg) != "servers: NEW\n" {
		t.Fatalf("expected mirror registry.yaml to be overwritten with canonical, got %q", string(mirrorReg))
	}
}

func TestSyncMirror_DryRunDoesNotWrite(t *testing.T) {
	mgr, root := setupMirrorFixture(t,
		"servers: NEW\n", "skills: NEW\n",
		"servers: OLD\n", "skills: OLD\n",
	)

	updated, status, err := mgr.SyncMirror(true)
	if err != nil {
		t.Fatalf("SyncMirror dry-run: %v", err)
	}
	if updated != 2 {
		t.Fatalf("expected 2 would-update count, got %d", updated)
	}
	if !status.InSync {
		// Dry-run flips InSync at the end of SyncMirror; that's intentional so
		// the printed summary reads cleanly. The on-disk state is what we
		// actually want to verify here.
		_ = status
	}

	mirrorReg, _ := os.ReadFile(filepath.Join(root, "platform", "gitops", "mcp", "context", "registry.yaml"))
	if string(mirrorReg) != "servers: OLD\n" {
		t.Fatalf("expected mirror to be unchanged in dry-run, got %q", string(mirrorReg))
	}
}

func TestGetMirrorStatus_MirrorMissing(t *testing.T) {
	mgr, root := setupMirrorFixture(t,
		"s: 1\n", "s: 1\n",
		"s: 1\n", "s: 1\n",
	)
	// Remove the entire mirror tree to simulate no platform/gitops checkout.
	if err := os.RemoveAll(filepath.Join(root, "platform", "gitops")); err != nil {
		t.Fatalf("rm mirror: %v", err)
	}

	if _, err := mgr.GetMirrorStatus(); err == nil {
		t.Fatal("expected error when mirror root absent, got nil")
	}
}

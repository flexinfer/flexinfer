package main

import (
	"context"
	"testing"
)

// TestRunSelfCheck makes the binary's offline gate a go-test target too, so
// CI exercises the full client path (canned chat server, real tool exec,
// append-only assertion, header parse, path jail) without a cluster.
func TestRunSelfCheck(t *testing.T) {
	if err := runSelfCheck(); err != nil {
		t.Fatalf("self-check failed: %v", err)
	}
}

func TestFSToolsRejectsNonDir(t *testing.T) {
	if _, err := fsTools("/nonexistent-path-xyz"); err == nil {
		t.Fatal("fsTools should reject a missing workdir")
	}
}

func TestResolveInRootJail(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveInRoot(root, "../escape"); err == nil {
		t.Fatal("resolveInRoot should reject path escaping root")
	}
	got, err := resolveInRoot(root, "sub/file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatal("expected a resolved path")
	}
}

func TestListDirEmptyArgsDefaultsToRoot(t *testing.T) {
	root := t.TempDir()
	tool := listDirTool(root)
	// Empty arguments must not error — the model may call list_dir with "{}".
	out, err := tool.Invoke(context.Background(), "")
	if err != nil {
		t.Fatalf("list_dir with empty args errored: %v", err)
	}
	if out != "(empty directory)" {
		t.Fatalf("got %q want (empty directory)", out)
	}
}

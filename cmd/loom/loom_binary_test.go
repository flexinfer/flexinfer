package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsEphemeralGoRunBinary(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/var/folders/xx/T/go-build123/b001/exe/loom", want: true},
		{path: "/Users/cblevins/.local/bin/loom", want: false},
		{path: "/opt/homebrew/bin/loom", want: false},
		{path: "", want: false},
	}

	for _, tc := range tests {
		if got := isEphemeralGoRunBinary(tc.path); got != tc.want {
			t.Fatalf("isEphemeralGoRunBinary(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestResolveStableLoomBinary(t *testing.T) {
	oldExecutable := loomExecutablePath
	oldLookPath := loomLookPath
	oldHome := loomUserHomeDir
	defer func() {
		loomExecutablePath = oldExecutable
		loomLookPath = oldLookPath
		loomUserHomeDir = oldHome
	}()

	t.Run("explicit path wins", func(t *testing.T) {
		loomExecutablePath = func() (string, error) { return "/tmp/ignored", nil }
		loomLookPath = func(string) (string, error) { return "/tmp/ignored-too", nil }
		loomUserHomeDir = func() (string, error) { return t.TempDir(), nil }

		if got := resolveStableLoomBinary("/custom/loom"); got != "/custom/loom" {
			t.Fatalf("resolveStableLoomBinary(explicit) = %q, want /custom/loom", got)
		}
	})

	t.Run("stable executable path is used", func(t *testing.T) {
		loomExecutablePath = func() (string, error) { return "/opt/homebrew/bin/loom", nil }
		loomLookPath = func(string) (string, error) { return "/Users/test/.local/bin/loom", nil }
		loomUserHomeDir = func() (string, error) { return t.TempDir(), nil }

		if got := resolveStableLoomBinary(""); got != "/opt/homebrew/bin/loom" {
			t.Fatalf("resolveStableLoomBinary() = %q, want stable executable", got)
		}
	})

	t.Run("go-run executable falls back to PATH loom", func(t *testing.T) {
		loomExecutablePath = func() (string, error) { return "/var/folders/xx/T/go-build123/b001/exe/loom", nil }
		loomLookPath = func(string) (string, error) { return "/Users/test/.local/bin/loom", nil }
		loomUserHomeDir = func() (string, error) { return t.TempDir(), nil }

		if got := resolveStableLoomBinary(""); got != "/Users/test/.local/bin/loom" {
			t.Fatalf("resolveStableLoomBinary() = %q, want PATH loom", got)
		}
	})

	t.Run("go-run executable falls back to home binary when PATH misses", func(t *testing.T) {
		home := t.TempDir()
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatalf("mkdir home bin: %v", err)
		}
		candidate := filepath.Join(binDir, "loom")
		if err := os.WriteFile(candidate, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write fallback binary: %v", err)
		}

		loomExecutablePath = func() (string, error) { return "/var/folders/xx/T/go-build123/b001/exe/loom", nil }
		loomLookPath = func(string) (string, error) { return "", os.ErrNotExist }
		loomUserHomeDir = func() (string, error) { return home, nil }

		if got := resolveStableLoomBinary(""); got != candidate {
			t.Fatalf("resolveStableLoomBinary() = %q, want %q", got, candidate)
		}
	})
}

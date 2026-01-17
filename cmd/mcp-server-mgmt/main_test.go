package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHosts_FindsPlatformGitopsServersTomlUpwards(t *testing.T) {
	tmp := t.TempDir()
	workspaceRoot := filepath.Join(tmp, "workspace")

	serversTomlPath := filepath.Join(workspaceRoot, "platform", "gitops", "scripts", "mcp", "servers.toml")
	if err := os.MkdirAll(filepath.Dir(serversTomlPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(serversTomlPath, []byte(`[hosts]
cblevins-7900xtx = { host = "192.168.50.65", user = "cblevins" }
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	workDir := filepath.Join(workspaceRoot, "services", "loom-core")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll workDir: %v", err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	t.Setenv("LOOM_SERVER_MGMT_HOSTS_TOML", "")

	if err := loadHosts(); err != nil {
		t.Fatalf("loadHosts: %v", err)
	}

	h, err := getHost("cblevins-7900xtx")
	if err != nil {
		t.Fatalf("getHost: %v", err)
	}
	if h.Host != "192.168.50.65" {
		t.Fatalf("unexpected host: %q", h.Host)
	}
	assertSameFile(t, hostsConfigPath, serversTomlPath)
}

func TestLoadHosts_UsesEnvOverride(t *testing.T) {
	tmp := t.TempDir()

	serversTomlPath := filepath.Join(tmp, "servers.toml")
	if err := os.WriteFile(serversTomlPath, []byte(`[hosts]
test = { host = "1.2.3.4" }
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	workDir := filepath.Join(tmp, "somewhere", "else")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(workDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	t.Setenv("LOOM_SERVER_MGMT_HOSTS_TOML", serversTomlPath)

	if err := loadHosts(); err != nil {
		t.Fatalf("loadHosts: %v", err)
	}

	h, err := getHost("test")
	if err != nil {
		t.Fatalf("getHost: %v", err)
	}
	if h.Host != "1.2.3.4" {
		t.Fatalf("unexpected host: %q", h.Host)
	}
	assertSameFile(t, hostsConfigPath, serversTomlPath)
}

func assertSameFile(t *testing.T, a, b string) {
	t.Helper()

	aInfo, err := os.Stat(a)
	if err != nil {
		t.Fatalf("Stat(%q): %v", a, err)
	}

	bInfo, err := os.Stat(b)
	if err != nil {
		t.Fatalf("Stat(%q): %v", b, err)
	}

	if !os.SameFile(aInfo, bInfo) {
		t.Fatalf("paths do not refer to the same file: %q != %q", a, b)
	}
}

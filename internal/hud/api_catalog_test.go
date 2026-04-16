package hud

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleCatalogListUsesConfiguredRegistryPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	registryPath := filepath.Join(tmp, "mounted-registry.yaml")
	writeTestCatalogRegistry(t, registryPath, "mounted_gitlab", "Mounted GitLab server")

	app := &App{
		config: Config{RegistryPath: registryPath},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	got := requestCatalogList(t, app, "/api/catalog")
	if got.Count != 1 {
		t.Fatalf("count = %d, want 1", got.Count)
	}
	if got.RegistryPath != registryPath {
		t.Fatalf("registry_path = %q, want %q", got.RegistryPath, registryPath)
	}
	if got.Servers[0].Name != "mounted_gitlab" {
		t.Fatalf("server name = %q, want mounted_gitlab", got.Servers[0].Name)
	}
	if got.Servers[0].Description != "Mounted GitLab server" {
		t.Fatalf("description = %q, want mounted registry description", got.Servers[0].Description)
	}
}

func TestHandleCatalogListFallsBackToDiscoveredRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tmp := t.TempDir()
	t.Chdir(tmp)

	registryPath := filepath.Join(tmp, "mcp", "context", "registry.yaml")
	writeTestCatalogRegistry(t, registryPath, "cwd_git", "CWD Git server")

	app := &App{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	got := requestCatalogList(t, app, "/api/catalog")
	if got.Count != 1 {
		t.Fatalf("count = %d, want 1", got.Count)
	}
	if got.RegistryPath != registryPath {
		t.Fatalf("registry_path = %q, want %q", got.RegistryPath, registryPath)
	}
	if got.Servers[0].Name != "cwd_git" {
		t.Fatalf("server name = %q, want cwd_git", got.Servers[0].Name)
	}
}

func requestCatalogList(t *testing.T, app *App, target string) struct {
	Servers      []catalogAPIEntry `json:"servers"`
	Count        int               `json:"count"`
	RegistryPath string            `json:"registry_path"`
} {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	app.handleCatalogList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var got struct {
		Servers      []catalogAPIEntry `json:"servers"`
		Count        int               `json:"count"`
		RegistryPath string            `json:"registry_path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return got
}

func writeTestCatalogRegistry(t *testing.T, path, name, description string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir registry dir: %v", err)
	}

	content := "version: 1\nservers:\n" +
		"  - name: " + name + "\n" +
		"    categories: [test]\n" +
		"    common:\n" +
		"      description: " + description + "\n" +
		"      command: /bin/echo\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
}

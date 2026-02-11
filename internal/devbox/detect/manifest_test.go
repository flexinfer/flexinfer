package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest_NoFile(t *testing.T) {
	dir := t.TempDir()

	m, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest() returned error: %v", err)
	}
	if m != nil {
		t.Errorf("loadManifest() = %+v, want nil when no .devbox.yaml exists", m)
	}
}

func TestLoadManifest_ValidYAML(t *testing.T) {
	dir := t.TempDir()

	manifest := `base_image: ubuntu:24.04
system_deps:
  - git
  - curl
  - ffmpeg
setup:
  - apt-get update
  - apt-get install -y build-essential
env:
  APP_ENV: development
  LOG_LEVEL: debug
mounts:
  - host: /data/models
    container: /models
    read_only: true
  - host: /tmp/cache
    container: /cache
limits:
  memory_mb: 4096
  cpu: 2.0
network: true
`
	if err := os.WriteFile(filepath.Join(dir, ".devbox.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("failed to write .devbox.yaml: %v", err)
	}

	m, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest() returned error: %v", err)
	}
	if m == nil {
		t.Fatal("loadManifest() returned nil, want non-nil")
	}

	// base_image
	if m.BaseImage != "ubuntu:24.04" {
		t.Errorf("BaseImage = %q, want %q", m.BaseImage, "ubuntu:24.04")
	}

	// system_deps
	if len(m.SystemDeps) != 3 {
		t.Fatalf("len(SystemDeps) = %d, want 3", len(m.SystemDeps))
	}
	expectedDeps := []string{"git", "curl", "ffmpeg"}
	for i, dep := range expectedDeps {
		if m.SystemDeps[i] != dep {
			t.Errorf("SystemDeps[%d] = %q, want %q", i, m.SystemDeps[i], dep)
		}
	}

	// setup
	if len(m.Setup) != 2 {
		t.Fatalf("len(Setup) = %d, want 2", len(m.Setup))
	}
	if m.Setup[0] != "apt-get update" {
		t.Errorf("Setup[0] = %q, want %q", m.Setup[0], "apt-get update")
	}

	// env
	if len(m.Env) != 2 {
		t.Fatalf("len(Env) = %d, want 2", len(m.Env))
	}
	if m.Env["APP_ENV"] != "development" {
		t.Errorf("Env[APP_ENV] = %q, want %q", m.Env["APP_ENV"], "development")
	}
	if m.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("Env[LOG_LEVEL] = %q, want %q", m.Env["LOG_LEVEL"], "debug")
	}

	// mounts
	if len(m.Mounts) != 2 {
		t.Fatalf("len(Mounts) = %d, want 2", len(m.Mounts))
	}
	if m.Mounts[0].Host != "/data/models" {
		t.Errorf("Mounts[0].Host = %q, want %q", m.Mounts[0].Host, "/data/models")
	}
	if m.Mounts[0].Container != "/models" {
		t.Errorf("Mounts[0].Container = %q, want %q", m.Mounts[0].Container, "/models")
	}
	if !m.Mounts[0].ReadOnly {
		t.Error("Mounts[0].ReadOnly = false, want true")
	}
	if m.Mounts[1].ReadOnly {
		t.Error("Mounts[1].ReadOnly = true, want false")
	}

	// limits
	if m.Limits == nil {
		t.Fatal("Limits is nil, want non-nil")
	}
	if m.Limits.MemoryMB != 4096 {
		t.Errorf("Limits.MemoryMB = %d, want 4096", m.Limits.MemoryMB)
	}
	if m.Limits.CPU != 2.0 {
		t.Errorf("Limits.CPU = %f, want 2.0", m.Limits.CPU)
	}

	// network
	if m.Network == nil {
		t.Fatal("Network is nil, want non-nil")
	}
	if !*m.Network {
		t.Error("Network = false, want true")
	}
}

func TestLoadManifest_InvalidYAML(t *testing.T) {
	dir := t.TempDir()

	invalidYAML := `base_image: ubuntu:24.04
system_deps:
  - git
  this is not valid yaml: [
    broken
`
	if err := os.WriteFile(filepath.Join(dir, ".devbox.yaml"), []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write .devbox.yaml: %v", err)
	}

	m, err := loadManifest(dir)
	if err == nil {
		t.Fatalf("loadManifest() returned nil error for invalid YAML, got manifest: %+v", m)
	}
	if m != nil {
		t.Errorf("loadManifest() returned non-nil manifest for invalid YAML: %+v", m)
	}
}

func TestLoadManifest_EmptyFile(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ".devbox.yaml"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write .devbox.yaml: %v", err)
	}

	m, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest() returned error for empty file: %v", err)
	}
	// Empty YAML unmarshals to zero-value struct, which is returned as a pointer
	if m == nil {
		t.Fatal("loadManifest() returned nil for empty file, want zero-value ManifestOverride")
	}
}

func TestLoadManifest_MinimalFields(t *testing.T) {
	dir := t.TempDir()

	manifest := `base_image: alpine:3.20
`
	if err := os.WriteFile(filepath.Join(dir, ".devbox.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("failed to write .devbox.yaml: %v", err)
	}

	m, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest() returned error: %v", err)
	}
	if m == nil {
		t.Fatal("loadManifest() returned nil, want non-nil")
	}
	if m.BaseImage != "alpine:3.20" {
		t.Errorf("BaseImage = %q, want %q", m.BaseImage, "alpine:3.20")
	}
	if len(m.SystemDeps) != 0 {
		t.Errorf("SystemDeps = %v, want empty", m.SystemDeps)
	}
	if len(m.Setup) != 0 {
		t.Errorf("Setup = %v, want empty", m.Setup)
	}
	if m.Env != nil && len(m.Env) != 0 {
		t.Errorf("Env = %v, want nil or empty", m.Env)
	}
	if len(m.Mounts) != 0 {
		t.Errorf("Mounts = %v, want empty", m.Mounts)
	}
	if m.Limits != nil {
		t.Errorf("Limits = %+v, want nil", m.Limits)
	}
	if m.Network != nil {
		t.Errorf("Network = %v, want nil", m.Network)
	}
}

func TestLoadManifest_NetworkFalse(t *testing.T) {
	dir := t.TempDir()

	manifest := `network: false
`
	if err := os.WriteFile(filepath.Join(dir, ".devbox.yaml"), []byte(manifest), 0644); err != nil {
		t.Fatalf("failed to write .devbox.yaml: %v", err)
	}

	m, err := loadManifest(dir)
	if err != nil {
		t.Fatalf("loadManifest() returned error: %v", err)
	}
	if m == nil {
		t.Fatal("loadManifest() returned nil")
	}
	if m.Network == nil {
		t.Fatal("Network is nil, want non-nil false")
	}
	if *m.Network {
		t.Error("Network = true, want false")
	}
}

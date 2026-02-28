package backend

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractReplacePath(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		// Relative local paths.
		{"gitlab.flexinfer.ai/libs/mcp-go => ../../libs/mcp-go", "../../libs/mcp-go"},
		{"gitlab.flexinfer.ai/libs/fi-accel/go/fiaccel => ../../libs/fi-accel/go/fiaccel", "../../libs/fi-accel/go/fiaccel"},
		{"module/path v1.0.0 => ../local/dir", "../local/dir"},
		{"module/path => ./local", "./local"},

		// Absolute paths.
		{"module/path => /absolute/path", "/absolute/path"},

		// Remote replacements (should return "").
		{"module/path => other/module v1.2.3", ""},
		{"module/path v1.0.0 => other/module v2.0.0", ""},

		// Empty / malformed.
		{"no arrow here", ""},
		{"module/path =>", ""},
		{"=> ../../libs/mcp-go", "../../libs/mcp-go"},
	}

	for _, tt := range tests {
		got := extractReplacePath(tt.line)
		if got != tt.want {
			t.Errorf("extractReplacePath(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestParseGoModReplaceDirs(t *testing.T) {
	dir := t.TempDir()

	gomod := `module github.com/example/project

go 1.22

require (
	gitlab.flexinfer.ai/libs/mcp-go v0.0.0
	gitlab.flexinfer.ai/libs/fi-mcp-kit v0.0.0
)

replace (
	gitlab.flexinfer.ai/libs/fi-accel/go/fiaccel => ../../libs/fi-accel/go/fiaccel
	gitlab.flexinfer.ai/libs/fi-mcp-kit => ../../libs/fi-mcp-kit
	gitlab.flexinfer.ai/libs/mcp-go => ../../libs/mcp-go
)
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatal(err)
	}

	dirs, err := parseGoModReplaceDirs(dir)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"../../libs/fi-accel/go/fiaccel",
		"../../libs/fi-mcp-kit",
		"../../libs/mcp-go",
	}

	if len(dirs) != len(want) {
		t.Fatalf("got %d dirs, want %d: %v", len(dirs), len(want), dirs)
	}
	for i, d := range dirs {
		if d != want[i] {
			t.Errorf("dirs[%d] = %q, want %q", i, d, want[i])
		}
	}
}

func TestParseGoModReplaceDirs_SingleLine(t *testing.T) {
	dir := t.TempDir()

	gomod := `module github.com/example/simple

go 1.22

replace gitlab.flexinfer.ai/libs/mcp-go => ../mcp-go
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatal(err)
	}

	dirs, err := parseGoModReplaceDirs(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(dirs) != 1 || dirs[0] != "../mcp-go" {
		t.Errorf("got %v, want [../mcp-go]", dirs)
	}
}

func TestParseGoModReplaceDirs_NoGoMod(t *testing.T) {
	dir := t.TempDir()

	dirs, err := parseGoModReplaceDirs(dir)
	if err == nil {
		t.Error("expected error for missing go.mod")
	}
	if dirs != nil {
		t.Errorf("expected nil dirs, got %v", dirs)
	}
}

func TestDiscoverDeps(t *testing.T) {
	// Create workspace layout:
	// workspace/
	//   services/project/ (go.mod with replace directive)
	//   libs/mcp-go/
	//   libs/fi-mcp-kit/
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "services", "project")
	libMcpGo := filepath.Join(workspace, "libs", "mcp-go")
	libFiMcpKit := filepath.Join(workspace, "libs", "fi-mcp-kit")

	for _, d := range []string{projectDir, libMcpGo, libFiMcpKit} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	gomod := `module github.com/example/project

go 1.22

replace (
	gitlab.flexinfer.ai/libs/fi-mcp-kit => ../../libs/fi-mcp-kit
	gitlab.flexinfer.ai/libs/mcp-go => ../../libs/mcp-go
)
`
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatal(err)
	}

	dirs, err := DiscoverDeps(projectDir, workspace)
	if err != nil {
		t.Fatal(err)
	}

	// Should have project + 2 deps = 3 entries.
	if len(dirs) != 3 {
		t.Fatalf("got %d dirs, want 3: %v", len(dirs), dirs)
	}

	// First entry: the project itself.
	if dirs[0].LocalPath != projectDir {
		t.Errorf("dirs[0].LocalPath = %q, want %q", dirs[0].LocalPath, projectDir)
	}
	if dirs[0].RemotePath != "/workspace/services/project" {
		t.Errorf("dirs[0].RemotePath = %q, want /workspace/services/project", dirs[0].RemotePath)
	}

	// Check that deps are discovered (order may vary for deps).
	depPaths := map[string]string{}
	for _, d := range dirs[1:] {
		depPaths[d.LocalPath] = d.RemotePath
	}

	if rp, ok := depPaths[libMcpGo]; !ok || rp != "/workspace/libs/mcp-go" {
		t.Errorf("missing or wrong mcp-go dep: %v", depPaths)
	}
	if rp, ok := depPaths[libFiMcpKit]; !ok || rp != "/workspace/libs/fi-mcp-kit" {
		t.Errorf("missing or wrong fi-mcp-kit dep: %v", depPaths)
	}
}

func TestDiscoverDeps_NoGoMod(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "services", "pyproject")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	dirs, err := DiscoverDeps(projectDir, workspace)
	if err != nil {
		t.Fatal(err)
	}

	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1", len(dirs))
	}
	if dirs[0].LocalPath != projectDir {
		t.Errorf("dirs[0].LocalPath = %q, want %q", dirs[0].LocalPath, projectDir)
	}
}

func TestDiscoverDeps_MissingDepDir(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "services", "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Replace directive points to non-existent dir.
	gomod := `module github.com/example/project

go 1.22

replace gitlab.flexinfer.ai/libs/mcp-go => ../../libs/mcp-go
`
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte(gomod), 0644); err != nil {
		t.Fatal(err)
	}

	dirs, err := DiscoverDeps(projectDir, workspace)
	if err != nil {
		t.Fatal(err)
	}

	// Missing dep should be silently skipped.
	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1 (missing dep should be skipped)", len(dirs))
	}
}

func TestDiscoverDeps_OutsideWorkspace(t *testing.T) {
	projectDir := t.TempDir()
	workspace := t.TempDir()

	dirs, err := DiscoverDeps(projectDir, workspace)
	if err != nil {
		t.Fatal(err)
	}

	if len(dirs) != 1 {
		t.Fatalf("got %d dirs, want 1", len(dirs))
	}
	if dirs[0].RemotePath != "/workspace" {
		t.Errorf("outside-workspace project should map to /workspace, got %q", dirs[0].RemotePath)
	}
}

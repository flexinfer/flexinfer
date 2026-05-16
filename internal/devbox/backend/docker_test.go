package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeDocker(t *testing.T) (dockerPath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "docker.log")
	dockerPath = filepath.Join(dir, "docker")

	script := `#!/bin/sh
set -eu
echo "$@" >> "$DOCKER_LOG_PATH"
case "${1:-}" in
  build)
    echo "STEP 1/1: CACHED"
    ;;
  run)
    echo "fake-container-id-123"
    ;;
  inspect)
    echo "running"
    ;;
  *)
    ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker script: %v", err)
	}
	t.Setenv("DOCKER_LOG_PATH", logPath)
	return dockerPath, logPath
}

func readLogLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read docker log: %v", err)
	}
	raw := strings.TrimSpace(string(b))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}

func TestDockerBuild_MonorepoContextPathPassedToCLI(t *testing.T) {
	dockerPath, logPath := writeFakeDocker(t)
	d := &DockerBackend{dockerPath: dockerPath}

	workspace := t.TempDir()
	contextDir := filepath.Join(workspace, "services", "loom-core")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatalf("mkdir context dir: %v", err)
	}

	res, err := d.Build(context.Background(), BuildOpts{
		Tag:        "mcp/devbox/loom-core:abc1234",
		Dockerfile: []byte("FROM scratch\n"),
		ContextDir: contextDir,
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if res.ImageTag != "mcp/devbox/loom-core:abc1234" {
		t.Fatalf("ImageTag=%q", res.ImageTag)
	}
	if !res.Cached {
		t.Fatal("expected Cached=true when build output contains CACHED")
	}

	lines := readLogLines(t, logPath)
	if len(lines) != 1 {
		t.Fatalf("expected 1 docker invocation, got %d (%v)", len(lines), lines)
	}
	line := lines[0]
	if !strings.HasPrefix(line, "build -t mcp/devbox/loom-core:abc1234 -f ") {
		t.Fatalf("unexpected docker build args: %q", line)
	}
	if !strings.HasSuffix(line, " "+contextDir) {
		t.Fatalf("expected monorepo context dir %q in build args: %q", contextDir, line)
	}
}

func TestDockerStart_PassesMountsAndNetworkFlags(t *testing.T) {
	dockerPath, logPath := writeFakeDocker(t)
	d := &DockerBackend{dockerPath: dockerPath}

	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "services", "loom-core")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	res, err := d.Start(context.Background(), StartOpts{
		Name:     "devbox-loom-core",
		ImageTag: "mcp/devbox/loom-core:abc1234",
		WorkDir:  "/workspace/services/loom-core",
		Mounts: []Mount{
			{Host: workspace, Container: "/workspace"},
			{Host: projectDir, Container: "/workspace/services/loom-core", ReadOnly: true},
		},
		Env:     map[string]string{"GOFLAGS": "-mod=mod"},
		Network: false,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if res.ContainerID != "fake-container-id-123" {
		t.Fatalf("ContainerID=%q", res.ContainerID)
	}

	lines := readLogLines(t, logPath)
	if len(lines) < 3 {
		t.Fatalf("expected stop+rm+run calls, got %d (%v)", len(lines), lines)
	}
	runLine := lines[len(lines)-1]
	if !strings.HasPrefix(runLine, "run -d --name devbox-loom-core ") {
		t.Fatalf("unexpected docker run args: %q", runLine)
	}
	if !strings.Contains(runLine, "-v "+workspace+":/workspace") {
		t.Fatalf("missing workspace mount in args: %q", runLine)
	}
	if !strings.Contains(runLine, "-v "+projectDir+":/workspace/services/loom-core:ro") {
		t.Fatalf("missing project read-only mount in args: %q", runLine)
	}
	if !strings.Contains(runLine, "--network none") {
		t.Fatalf("missing --network none in args: %q", runLine)
	}
}

func TestDockerStart_DefaultManagedByLabel(t *testing.T) {
	dockerPath, logPath := writeFakeDocker(t)
	d := &DockerBackend{dockerPath: dockerPath}

	_, err := d.Start(context.Background(), StartOpts{
		Name:     "devbox-test",
		ImageTag: "test:latest",
		Network:  true,
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	lines := readLogLines(t, logPath)
	runLine := lines[len(lines)-1]
	if !strings.Contains(runLine, "--label app.kubernetes.io/managed-by=mcp-devbox") {
		t.Fatalf("missing default managed-by label in args: %q", runLine)
	}
	if !strings.Contains(runLine, "--label devbox/project=devbox-test") {
		t.Fatalf("missing devbox/project label in args: %q", runLine)
	}
}

func TestDockerStart_ManagedByOverrideAndExtraLabels(t *testing.T) {
	dockerPath, logPath := writeFakeDocker(t)
	d := &DockerBackend{dockerPath: dockerPath}

	_, err := d.Start(context.Background(), StartOpts{
		Name:              "spawn-abc123",
		ImageTag:          "test:latest",
		Network:           true,
		AgentID:           "agent-1",
		ManagedByOverride: "loom-spawn",
		ExtraLabels: map[string]string{
			"loom.dev/spawn-id": "spawn-abc123",
			"loom.dev/agent-id": "agent-1",
		},
	})
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	lines := readLogLines(t, logPath)
	runLine := lines[len(lines)-1]
	if !strings.Contains(runLine, "--label app.kubernetes.io/managed-by=loom-spawn") {
		t.Fatalf("missing overridden managed-by label in args: %q", runLine)
	}
	if !strings.Contains(runLine, "--label devbox/agent-id=agent-1") {
		t.Fatalf("missing agent-id label in args: %q", runLine)
	}
	if !strings.Contains(runLine, "--label loom.dev/spawn-id=spawn-abc123") {
		t.Fatalf("missing spawn-id extra label in args: %q", runLine)
	}
	if !strings.Contains(runLine, "--label loom.dev/agent-id=agent-1") {
		t.Fatalf("missing agent-id extra label in args: %q", runLine)
	}
}

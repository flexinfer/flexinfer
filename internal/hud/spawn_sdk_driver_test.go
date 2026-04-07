package hud

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/devbox/backend"
)

// recordingBackend is a minimal backend.Backend implementation that captures
// every Exec invocation for assertions. The other Backend methods return
// ErrNotSupported because the tests in this file only exercise control-file
// helpers (which only call Exec).
type recordingBackend struct {
	execCalls []backend.ExecOpts
	execErr   error
}

func (r *recordingBackend) Build(_ context.Context, _ backend.BuildOpts) (*backend.BuildResult, error) {
	return nil, backend.ErrNotSupported
}
func (r *recordingBackend) Start(_ context.Context, _ backend.StartOpts) (*backend.StartResult, error) {
	return nil, backend.ErrNotSupported
}
func (r *recordingBackend) Exec(_ context.Context, opts backend.ExecOpts) (*backend.ExecResult, error) {
	r.execCalls = append(r.execCalls, opts)
	if r.execErr != nil {
		return nil, r.execErr
	}
	return &backend.ExecResult{ExitCode: 0}, nil
}
func (r *recordingBackend) Stop(_ context.Context, _ string) error { return backend.ErrNotSupported }
func (r *recordingBackend) Status(_ context.Context, _ string) (*backend.StatusResult, error) {
	return nil, backend.ErrNotSupported
}
func (r *recordingBackend) Health(_ context.Context) error           { return nil }
func (r *recordingBackend) Pause(_ context.Context, _ string) error  { return backend.ErrNotSupported }
func (r *recordingBackend) Resume(_ context.Context, _ string) error { return backend.ErrNotSupported }
func (r *recordingBackend) ReadFile(_ context.Context, _, _ string) ([]byte, error) {
	return nil, backend.ErrNotSupported
}
func (r *recordingBackend) WriteFile(_ context.Context, _, _ string, _ []byte, _ string) error {
	return backend.ErrNotSupported
}
func (r *recordingBackend) CleanupBuilds(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}

// TestBuildSDKDriverCommand_SingleShot verifies the legacy single-shot flag
// surface stays unchanged when controlFilePath is empty (slice 7c parity).
func TestBuildSDKDriverCommand_SingleShot(t *testing.T) {
	cmd := buildSDKDriverCommand(
		"claude-code",
		"do the thing",
		"agent-1",
		"spawn-abc",
		"/workspace/loom-core",
		"", // controlFilePath
		50, // maxTurns
		1.5,
	)

	want := []string{
		"--agent-type 'claude-code'",
		"--task 'do the thing'",
		"--agent-id 'agent-1'",
		"--spawn-id 'spawn-abc'",
		"--working-dir '/workspace/loom-core'",
		"--max-turns 50",
		"--max-cost-usd 1.5000",
	}
	for _, w := range want {
		if !strings.Contains(cmd, w) {
			t.Errorf("expected command to contain %q, got: %s", w, cmd)
		}
	}
	if strings.Contains(cmd, "--control-file") {
		t.Errorf("single-shot command should not include --control-file, got: %s", cmd)
	}
	if !strings.HasPrefix(cmd, "node "+spawnDriverPodPath) {
		t.Errorf("expected command to start with `node %s`, got: %s", spawnDriverPodPath, cmd)
	}
}

// TestBuildSDKDriverCommand_MultiTurn verifies the new --control-file flag
// is appended (and shell-quoted) when a control path is supplied.
func TestBuildSDKDriverCommand_MultiTurn(t *testing.T) {
	path := "/opt/loom/control/spawn-control-spawn-xyz.jsonl"
	cmd := buildSDKDriverCommand(
		"codex",
		"hello",
		"agent-2",
		"spawn-xyz",
		"/workspace/loom-core",
		path,
		0,
		0,
	)
	wantFlag := "--control-file '" + path + "'"
	if !strings.Contains(cmd, wantFlag) {
		t.Errorf("expected command to contain %q, got: %s", wantFlag, cmd)
	}
	// Budget flags should be omitted when zero (matches single-shot behavior).
	if strings.Contains(cmd, "--max-turns") {
		t.Errorf("expected no --max-turns when maxTurns=0, got: %s", cmd)
	}
	if strings.Contains(cmd, "--max-cost-usd") {
		t.Errorf("expected no --max-cost-usd when maxCostUSD=0, got: %s", cmd)
	}
}

// TestBuildSDKDriverCommand_ShellQuoting ensures embedded single quotes in
// task text are escaped via the standard '\” splice so the bundled driver
// receives the original string intact.
func TestBuildSDKDriverCommand_ShellQuoting(t *testing.T) {
	cmd := buildSDKDriverCommand(
		"claude-code",
		"don't break it",
		"agent-3",
		"spawn-3",
		"",
		"",
		0,
		0,
	)
	// shellQuote splices embedded `'` as `'\''` so the resulting argv stays
	// inside one shell word.
	want := `--task 'don'\''t break it'`
	if !strings.Contains(cmd, want) {
		t.Errorf("expected escaped task in command, got: %s", cmd)
	}
}

// TestControlFilePathForSpawn verifies the in-pod path format and that the
// sanitizer rejects shell metacharacters and path traversal attempts.
func TestControlFilePathForSpawn(t *testing.T) {
	cases := []struct {
		name    string
		spawnID string
		want    string
	}{
		{
			name:    "canonical id",
			spawnID: "spawn-abc123",
			want:    "/opt/loom/control/spawn-control-spawn-abc123.jsonl",
		},
		{
			name:    "underscores allowed",
			spawnID: "spawn_test_1",
			want:    "/opt/loom/control/spawn-control-spawn_test_1.jsonl",
		},
		{
			name:    "path traversal sanitized",
			spawnID: "../../etc/passwd",
			want:    "/opt/loom/control/spawn-control-______etc_passwd.jsonl",
		},
		{
			name:    "shell metas sanitized",
			spawnID: "spawn;rm -rf",
			want:    "/opt/loom/control/spawn-control-spawn_rm_-rf.jsonl",
		},
		{
			name:    "empty fallback",
			spawnID: "",
			want:    "/opt/loom/control/spawn-control-unknown.jsonl",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := controlFilePathForSpawn(tc.spawnID)
			if got != tc.want {
				t.Errorf("controlFilePathForSpawn(%q) = %q, want %q", tc.spawnID, got, tc.want)
			}
		})
	}
}

// TestInjectControlFile verifies the orchestrator pre-creates the control
// file with `: > path` so the driver's fs.watch fires immediately on the
// first append.
func TestInjectControlFile(t *testing.T) {
	rb := &recordingBackend{}
	o := &SpawnOrchestrator{backend: rb}

	if err := o.injectControlFile(context.Background(), "container-1", "spawn-abc"); err != nil {
		t.Fatalf("injectControlFile: %v", err)
	}
	if len(rb.execCalls) != 1 {
		t.Fatalf("expected 1 Exec call, got %d", len(rb.execCalls))
	}
	call := rb.execCalls[0]
	if call.ContainerID != "container-1" {
		t.Errorf("ContainerID = %q, want container-1", call.ContainerID)
	}
	wantPath := "/opt/loom/control/spawn-control-spawn-abc.jsonl"
	if !strings.Contains(call.Command, wantPath) {
		t.Errorf("expected command to reference %q, got: %s", wantPath, call.Command)
	}
	if !strings.Contains(call.Command, "mkdir -p /opt/loom/control") {
		t.Errorf("expected mkdir of control dir, got: %s", call.Command)
	}
	if !strings.Contains(call.Command, ": > "+wantPath) {
		t.Errorf("expected `: > %s` to truncate/create the file, got: %s", wantPath, call.Command)
	}
}

// TestInjectControlMessage_Message verifies a `message` command is JSON
// serialized, base64-encoded for shell safety, and appended (>>) to the
// per-spawn control file.
func TestInjectControlMessage_Message(t *testing.T) {
	rb := &recordingBackend{}
	o := &SpawnOrchestrator{backend: rb}

	cmd := SpawnControlCommand{Type: "message", Text: "follow up please"}
	if err := o.injectControlMessage(context.Background(), "container-2", "spawn-xyz", cmd); err != nil {
		t.Fatalf("injectControlMessage: %v", err)
	}
	if len(rb.execCalls) != 1 {
		t.Fatalf("expected 1 Exec call, got %d", len(rb.execCalls))
	}
	got := rb.execCalls[0].Command
	wantPath := "/opt/loom/control/spawn-control-spawn-xyz.jsonl"
	if !strings.Contains(got, ">> "+wantPath) {
		t.Errorf("expected append (`>> %s`), got: %s", wantPath, got)
	}
	if !strings.Contains(got, "base64 -d") {
		t.Errorf("expected base64 decode pipeline, got: %s", got)
	}
	// Sanity-check the encoded payload by extracting the base64 token and
	// decoding it back to JSON.
	payload := extractBase64Payload(t, got)
	if payload != `{"type":"message","text":"follow up please"}`+"\n" {
		t.Errorf("decoded payload = %q, want JSONL message", payload)
	}
}

// TestInjectControlMessage_Interrupt covers the no-payload variant.
func TestInjectControlMessage_Interrupt(t *testing.T) {
	rb := &recordingBackend{}
	o := &SpawnOrchestrator{backend: rb}

	if err := o.injectControlMessage(context.Background(), "container-3", "spawn-int", SpawnControlCommand{Type: "interrupt"}); err != nil {
		t.Fatalf("injectControlMessage: %v", err)
	}
	if len(rb.execCalls) != 1 {
		t.Fatalf("expected 1 Exec call, got %d", len(rb.execCalls))
	}
	payload := extractBase64Payload(t, rb.execCalls[0].Command)
	if payload != `{"type":"interrupt"}`+"\n" {
		t.Errorf("decoded payload = %q, want interrupt JSONL", payload)
	}
}

// TestInjectControlMessage_RequiresType ensures empty-type commands are
// rejected before any Exec call hits the backend.
func TestInjectControlMessage_RequiresType(t *testing.T) {
	rb := &recordingBackend{}
	o := &SpawnOrchestrator{backend: rb}
	err := o.injectControlMessage(context.Background(), "container-4", "spawn-bad", SpawnControlCommand{})
	if err == nil {
		t.Fatal("expected error for empty type, got nil")
	}
	if len(rb.execCalls) != 0 {
		t.Errorf("expected 0 Exec calls when validation fails, got %d", len(rb.execCalls))
	}
}

// TestInjectControlMessage_BackendError surfaces the underlying Exec error
// to the caller so the REST handler in slice 8c can return a 5xx instead of
// silently swallowing pod failures.
func TestInjectControlMessage_BackendError(t *testing.T) {
	rb := &recordingBackend{execErr: errors.New("boom")}
	o := &SpawnOrchestrator{backend: rb}
	err := o.injectControlMessage(context.Background(), "container-5", "spawn-err", SpawnControlCommand{Type: "shutdown"})
	if err == nil {
		t.Fatal("expected propagated error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected wrapped error to contain 'boom', got: %v", err)
	}
}

// extractBase64Payload pulls the single-quoted base64 token out of a
// `echo '<b64>' | base64 -d ...` command and returns the decoded text.
func extractBase64Payload(t *testing.T, shellCmd string) string {
	t.Helper()
	// Find `echo '` and the closing `'`.
	const prefix = "echo '"
	start := strings.Index(shellCmd, prefix)
	if start < 0 {
		t.Fatalf("no `echo '` in command: %s", shellCmd)
	}
	rest := shellCmd[start+len(prefix):]
	end := strings.Index(rest, "'")
	if end < 0 {
		t.Fatalf("unterminated `echo '` in command: %s", shellCmd)
	}
	encoded := rest[:end]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode %q: %v", encoded, err)
	}
	return string(decoded)
}

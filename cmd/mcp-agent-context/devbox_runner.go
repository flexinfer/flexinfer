package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/agentcontext"
	"github.com/crb2nu/loom/pkg/toolexec"
)

// inferProjectName resolves the devbox project name from the cwd, matching
// the verifier's inferRepoName so engram and runner agree on "this repo".
// Linked-worktree paths under <repo>/.claude/worktrees/<name>/ or
// <repo>/.worktrees/<branch>/ resolve back to the canonical repo name.
func inferProjectName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for _, marker := range []string{"/.claude/worktrees/", "/.worktrees/"} {
		if idx := strings.Index(cwd, marker); idx > 0 {
			return filepath.Base(cwd[:idx])
		}
	}
	return filepath.Base(cwd)
}

// devboxRunner adapts the daemon-routed devbox MCP server into an
// agentcontext.CommandRunner. It is constructed once per mcp-agent-context
// process when LOOM_SOCKET is set, and is wired onto the service via
// SetCommandRunner so engram `command:` proofs can be verified inside the
// devbox sandbox associated with `project`.
//
// Only daemon-failure conditions surface as TransportErr; a non-zero exit
// code from the actual command is reported as-is so the verifier can flip
// the engram's proof_status to failing.
type devboxRunner struct {
	client  *toolexec.Client
	project string
}

func newDevboxRunner(client *toolexec.Client, project string) *devboxRunner {
	return &devboxRunner{client: client, project: project}
}

// RunCommand satisfies agentcontext.CommandRunner. The timeout is forwarded
// to devbox as a Go duration string (devbox accepts e.g. "120s" / "2m").
func (r *devboxRunner) RunCommand(ctx context.Context, cmd string, timeout time.Duration) agentcontext.CommandRunResult {
	if r == nil || r.client == nil {
		return agentcontext.CommandRunResult{
			TransportErr: fmt.Errorf("devbox runner not initialized"),
		}
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	args := map[string]any{
		"project":   r.project,
		"command":   cmd,
		"timeout":   timeout.String(),
		"agent_id":  "engram-verify",
		"max_lines": 50,
	}

	// Give the daemon a small buffer past the inner timeout so devbox can
	// surface its own "command timed out" result instead of us cancelling
	// the whole exec round trip first.
	callCtx, cancel := context.WithTimeout(ctx, timeout+15*time.Second)
	defer cancel()

	resp, err := r.client.Execute(callCtx, "devbox", "devbox_exec", args)
	if err != nil {
		return agentcontext.CommandRunResult{TransportErr: err}
	}

	res := agentcontext.CommandRunResult{
		ExitCode:   intFromAny(resp["exit_code"]),
		StdoutTail: stringFromAny(resp["stdout_tail"]),
		StderrTail: stringFromAny(resp["stderr_tail"]),
		DurationMs: int64FromAny(resp["duration_ms"]),
	}
	// devbox exposes timeouts as a non-zero exit (typically 124) plus a
	// `truncated` / OOM flag. Treat exit==124 as TimedOut so the verifier
	// reports the more useful reason.
	if res.ExitCode == 124 {
		res.TimedOut = true
	}
	return res
}

func intFromAny(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

func int64FromAny(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	}
	return 0
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

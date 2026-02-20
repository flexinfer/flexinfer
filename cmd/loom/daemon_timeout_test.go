package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDaemonRPCTimeout_Defaults(t *testing.T) {
	t.Setenv("LOOM_DAEMON_CONTROL_TIMEOUT", "")
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "")

	if got := daemonRPCTimeout("loom/status"); got != defaultDaemonControlTimeout {
		t.Fatalf("daemonRPCTimeout(control) = %v, want %v", got, defaultDaemonControlTimeout)
	}
	if got := daemonRPCTimeout("tools/call"); got != defaultDaemonToolTimeout {
		t.Fatalf("daemonRPCTimeout(tools/call) = %v, want %v", got, defaultDaemonToolTimeout)
	}
}

func TestDaemonRPCTimeout_EnvOverride(t *testing.T) {
	t.Setenv("LOOM_DAEMON_CONTROL_TIMEOUT", "45s")
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "75s")

	if got := daemonRPCTimeout("loom/status"); got != 45*time.Second {
		t.Fatalf("daemonRPCTimeout(control) = %v, want 45s", got)
	}
	if got := daemonRPCTimeout("tools/call"); got != 75*time.Second {
		t.Fatalf("daemonRPCTimeout(tools/call) = %v, want 75s", got)
	}
}

func TestDaemonRPCTimeout_RejectsNonPositive(t *testing.T) {
	t.Setenv("LOOM_DAEMON_CONTROL_TIMEOUT", "0s")
	t.Setenv("LOOM_DAEMON_TOOL_TIMEOUT", "-1s")

	if got := daemonRPCTimeout("loom/status"); got != defaultDaemonControlTimeout {
		t.Fatalf("daemonRPCTimeout(control) = %v, want %v", got, defaultDaemonControlTimeout)
	}
	if got := daemonRPCTimeout("tools/call"); got != defaultDaemonToolTimeout {
		t.Fatalf("daemonRPCTimeout(tools/call) = %v, want %v", got, defaultDaemonToolTimeout)
	}
}

func TestDaemonRPCPhaseError_TimeoutIncludesRecoverability(t *testing.T) {
	err := daemonRPCPhaseError("tools/call", "recv", 12*time.Second, context.DeadlineExceeded)
	msg := err.Error()

	if !strings.Contains(msg, "tools/call timeout during recv after 12s") {
		t.Fatalf("missing timeout phase details in %q", msg)
	}
	if !strings.Contains(msg, "recoverable: retry the command") {
		t.Fatalf("missing recoverability hint in %q", msg)
	}
}

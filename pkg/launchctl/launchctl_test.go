package launchctl

import (
	"context"
	"testing"
)

// TestLoadCancelledContext verifies that Load respects context cancellation.
func TestLoadCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := Load(ctx, "/tmp/fake.plist")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

// TestUnloadCancelledContext verifies that Unload respects context cancellation.
func TestUnloadCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Unload(ctx, "/tmp/fake.plist")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

// TestStartCancelledContext verifies that Start respects context cancellation.
func TestStartCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Start(ctx, "com.test.service")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

// TestStopCancelledContext verifies that Stop respects context cancellation.
func TestStopCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Stop(ctx, "com.test.service")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

// TestKillCancelledContext verifies that Kill respects context cancellation.
func TestKillCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Kill(ctx, "-TERM", "some-pattern")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

// TestFindProcessByPortCancelledContext verifies that FindProcessByPort
// respects context cancellation.
func TestFindProcessByPortCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pid, err := FindProcessByPort(ctx, "3333")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
	if pid != "" {
		t.Errorf("expected empty pid, got %q", pid)
	}
}

// TestKillPIDCancelledContext verifies that KillPID respects context cancellation.
func TestKillPIDCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := KillPID(ctx, "12345")
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
}

// TestFindProcessByPortNoProcess verifies that FindProcessByPort returns an
// error when lsof finds no process. We use a port that is almost certainly
// not in use.
func TestFindProcessByPortNoProcess(t *testing.T) {
	// lsof will exit non-zero when no process is found on this port.
	pid, err := FindProcessByPort(context.Background(), "19")
	if err == nil {
		t.Errorf("expected error for unused port, got pid=%q", pid)
	}
}

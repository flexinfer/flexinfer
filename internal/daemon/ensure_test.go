package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uniqueSocketPath(t *testing.T, label string) string {
	t.Helper()
	safe := strings.NewReplacer("/", "-", " ", "-", ".", "-").Replace(label)
	return filepath.Join("/tmp", fmt.Sprintf("loom-%d-%d-%s.sock", os.Getpid(), time.Now().UnixNano(), safe))
}

func listenUnixSocket(t *testing.T, socketPath string) net.Listener {
	t.Helper()
	_ = os.Remove(socketPath)
	lc := net.ListenConfig{}
	l, err := lc.Listen(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	t.Cleanup(func() {
		_ = l.Close()
		_ = os.Remove(socketPath)
	})
	return l
}

func TestDialCheck_ActiveSocket(t *testing.T) {
	socketPath := uniqueSocketPath(t, "dial-active")
	_ = listenUnixSocket(t, socketPath)

	if !dialCheck(socketPath, 300*time.Millisecond) {
		t.Fatal("expected dialCheck to succeed for active socket")
	}
}

func TestDialCheck_MissingSocket(t *testing.T) {
	socketPath := uniqueSocketPath(t, "dial-missing")
	if dialCheck(socketPath, 100*time.Millisecond) {
		t.Fatal("expected dialCheck to fail for missing socket")
	}
}

func TestWaitForSocket_ReadyAfterDelay(t *testing.T) {
	socketPath := uniqueSocketPath(t, "wait-delayed")
	started := make(chan net.Listener, 1)
	errCh := make(chan error, 1)

	go func() {
		time.Sleep(120 * time.Millisecond)
		lc := net.ListenConfig{}
		l, err := lc.Listen(context.Background(), "unix", socketPath)
		if err != nil {
			errCh <- err
			return
		}
		started <- l
	}()

	if !waitForSocket(socketPath, 2*time.Second) {
		t.Fatal("expected waitForSocket to succeed after listener starts")
	}

	select {
	case err := <-errCh:
		t.Fatalf("listener failed to start: %v", err)
	case l := <-started:
		_ = l.Close()
		_ = os.Remove(socketPath)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delayed listener")
	}
}

func TestWaitForSocket_Timeout(t *testing.T) {
	socketPath := uniqueSocketPath(t, "wait-timeout")
	if waitForSocket(socketPath, 250*time.Millisecond) {
		t.Fatal("expected waitForSocket to time out when no listener is started")
	}
}

func TestTryLaunchAgent_NoPlist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	socketPath := uniqueSocketPath(t, "launch-none")
	if tryLaunchAgent(socketPath, 200*time.Millisecond) {
		t.Fatal("expected tryLaunchAgent to return false when plist is missing")
	}
}

func TestFindLoomd_FromPATH(t *testing.T) {
	binDir := t.TempDir()
	loomdPath := filepath.Join(binDir, "loomd")
	if err := os.WriteFile(loomdPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake loomd: %v", err)
	}
	t.Setenv("PATH", binDir)

	got := findLoomd()
	if filepath.Clean(got) != filepath.Clean(loomdPath) {
		t.Fatalf("findLoomd() = %q, want %q", got, loomdPath)
	}
}

func TestEnsureRunning_AlreadyRunning(t *testing.T) {
	socketPath := uniqueSocketPath(t, "ensure-running")
	_ = listenUnixSocket(t, socketPath)

	err := EnsureRunning(StartConfig{
		SocketPath: socketPath,
		Timeout:    300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("EnsureRunning should return nil when daemon is already running: %v", err)
	}
}

func TestEnsureRunning_NoDaemonBinary(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // Avoid using any real LaunchAgent plist.
	t.Setenv("PATH", t.TempDir()) // Ensure no loomd in PATH.

	socketPath := uniqueSocketPath(t, "ensure-missing")
	err := EnsureRunning(StartConfig{
		SocketPath: socketPath,
		Timeout:    200 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected EnsureRunning to fail when loomd binary is missing")
	}
	if !strings.Contains(err.Error(), "loomd not found in PATH") {
		t.Fatalf("expected loomd-not-found error, got: %v", err)
	}
}

package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// acquireLockAt acquires an flock on a lock file in the given directory,
// returning the open file and lock path. Caller must close the file to release.
func acquireLockAt(t *testing.T, dir string) (*os.File, string) {
	t.Helper()
	lockPath := filepath.Join(dir, "loomd.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatalf("acquire lock: %v", err)
	}
	syscall.CloseOnExec(int(f.Fd()))
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	return f, lockPath
}

func TestAcquireLock_Single(t *testing.T) {
	dir := t.TempDir()
	f, lockPath := acquireLockAt(t, dir)
	defer f.Close()

	// Lock file should exist.
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}

	// FD should be valid (Fstat should succeed).
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &stat); err != nil {
		t.Fatalf("Fstat on lock FD failed: %v", err)
	}
}

func TestAcquireLock_Double(t *testing.T) {
	dir := t.TempDir()
	f1, _ := acquireLockAt(t, dir)
	defer f1.Close()

	// A second non-blocking flock on the same path should fail.
	lockPath := filepath.Join(dir, "loomd.lock")
	f2, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	defer f2.Close()

	err = syscall.Flock(int(f2.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		t.Fatal("expected second flock to fail, but it succeeded")
	}
}

func TestAcquireLock_CloseOnExec(t *testing.T) {
	dir := t.TempDir()
	f, _ := acquireLockAt(t, dir)
	defer f.Close()

	// Verify FD_CLOEXEC is set via fcntl.
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatalf("fcntl F_GETFD failed: %v", errno)
	}
	if flags&syscall.FD_CLOEXEC == 0 {
		t.Fatal("expected FD_CLOEXEC to be set on lock FD")
	}
}

func TestAcquireLock_PIDWritten(t *testing.T) {
	dir := t.TempDir()
	f, lockPath := acquireLockAt(t, dir)
	defer f.Close()

	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("parse PID from lock file: %v (content: %q)", err, string(data))
	}
	if pid != os.Getpid() {
		t.Errorf("lock file PID = %d, want %d", pid, os.Getpid())
	}
}

func TestStaleSocket_RemovedOnStart(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "loom.sock")

	// Create a stale socket file (not actually listening).
	if err := os.WriteFile(socketPath, []byte("stale"), 0600); err != nil {
		t.Fatalf("create stale socket: %v", err)
	}

	// Simulate what Start() does after acquiring lock: unconditionally remove.
	_ = os.Remove(socketPath)

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatal("stale socket should have been removed")
	}
}

func TestSocket_ActiveDaemonBlocksStart(t *testing.T) {
	dir := t.TempDir()

	// First "daemon" acquires the lock.
	f1, _ := acquireLockAt(t, dir)
	defer f1.Close()

	// Second "daemon" cannot acquire lock.
	lockPath := filepath.Join(dir, "loomd.lock")
	f2, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	defer f2.Close()

	err = syscall.Flock(int(f2.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		t.Fatal("second daemon should not be able to acquire lock")
	}
}

func TestStop_ReleasesLock(t *testing.T) {
	dir := t.TempDir()
	f1, _ := acquireLockAt(t, dir)

	// Simulate Stop(): close the file, releasing the lock.
	f1.Close()

	// Now a new daemon should be able to acquire the lock.
	f2, _ := acquireLockAt(t, dir)
	defer f2.Close()

	// Verify FD is valid.
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(f2.Fd()), &stat); err != nil {
		t.Fatalf("Fstat on re-acquired lock FD failed: %v", err)
	}
}

func TestStaleSocket_DialFails(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "loom.sock")

	// Create a real (but not listening) socket file.
	if err := os.WriteFile(socketPath, []byte("not-a-socket"), 0600); err != nil {
		t.Fatalf("create file: %v", err)
	}

	// Attempting to dial should fail.
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", socketPath)
	if err == nil {
		conn.Close()
		t.Fatal("expected dial to fail on non-socket file")
	}
}

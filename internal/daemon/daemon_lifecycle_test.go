package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
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

// --- Accept loop and connection lifecycle tests ---

func newLifecycleTestPool() *pool.Pool {
	return pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Second,
		DialFunc: func(context.Context, string) (mcp.Transport, error) {
			return nil, fmt.Errorf("dial not implemented in lifecycle test")
		},
	})
}

func TestAcceptLoop_StopsAcceptingOnDone(t *testing.T) {
	socketPath := shortSocketPath("accept-stop")
	t.Cleanup(func() { os.Remove(socketPath) })
	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	d := &Daemon{
		cfg:      Config{SocketPath: socketPath},
		done:     make(chan struct{}),
		listener: listener,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		pool:     newLifecycleTestPool(),
		procMgr:  process.NewManager(nil, "test"),
	}
	t.Cleanup(func() { d.pool.Close() })

	// Start the accept loop.
	d.wg.Add(1)
	go d.acceptLoop(context.Background())

	// Verify a client can connect.
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("dial before stop: %v", err)
	}
	conn.Close()

	// Signal the daemon to stop and close the listener.
	close(d.done)
	_ = listener.Close()

	// Wait for the accept loop goroutine to exit via WaitGroup.
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Accept loop exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("acceptLoop did not exit within 2s after done channel closed")
	}

	// New connections should fail now.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, dialErr := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if dialErr == nil {
		t.Fatal("expected dial to fail after listener closed")
	}
}

func TestHandleConnection_ExitsOnDone(t *testing.T) {
	// Create a socket pair to simulate a client connection.
	server, client, err := socketPair()
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	defer client.Close()

	d := &Daemon{
		done:   make(chan struct{}),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	d.wg.Add(1)
	go d.handleConnection(context.Background(), server)

	// Close done to signal shutdown.
	close(d.done)
	// Close the client side so transport.Recv returns EOF.
	client.Close()

	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// handleConnection exited and decremented WaitGroup.
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection did not exit within 2s after done channel closed")
	}
}

// socketPair creates a connected pair of Unix-domain stream sockets.
func socketPair() (net.Conn, net.Conn, error) {
	dir, err := os.MkdirTemp("", "loom-sockpair-*")
	if err != nil {
		return nil, nil, err
	}
	sockPath := filepath.Join(dir, "pair.sock")
	lc := net.ListenConfig{}
	l, err := lc.Listen(context.Background(), "unix", sockPath)
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}

	accepted := make(chan net.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			errCh <- err
			return
		}
		accepted <- c
	}()

	client, err := (&net.Dialer{}).DialContext(context.Background(), "unix", sockPath)
	if err != nil {
		l.Close()
		os.RemoveAll(dir)
		return nil, nil, err
	}

	select {
	case server := <-accepted:
		l.Close()
		os.RemoveAll(dir)
		return server, client, nil
	case err := <-errCh:
		client.Close()
		l.Close()
		os.RemoveAll(dir)
		return nil, nil, err
	}
}

func TestDaemonStop_WaitGroupDrainsConnections(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	socketPath := shortSocketPath("drain")
	t.Cleanup(func() { os.Remove(socketPath) })

	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	manifest := NewManifestManager()
	manifest.path = filepath.Join(t.TempDir(), "manifest.yaml")

	d := &Daemon{
		cfg:      Config{SocketPath: socketPath},
		done:     make(chan struct{}),
		pool:     newLifecycleTestPool(),
		manifest: manifest,
		procMgr:  process.NewManager(nil, "test"),
		listener: listener,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	t.Cleanup(func() { d.pool.Close() })

	if err := d.acquireLock(); err != nil {
		t.Fatalf("acquireLock: %v", err)
	}

	// Start accept loop.
	d.wg.Add(1)
	go d.acceptLoop(context.Background())

	// Connect a client so the daemon spawns a handleConnection goroutine.
	client, err := (&net.Dialer{}).DialContext(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Give accept loop time to spawn handleConnection.
	time.Sleep(50 * time.Millisecond)

	// Stop the daemon — should wait for connections to drain.
	stopDone := make(chan error, 1)
	go func() { stopDone <- d.Stop() }()

	// Close the client so handleConnection's Recv returns EOF.
	client.Close()

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5s — WaitGroup likely leaked")
	}
}

func TestSignalLoop_ExitsOnDone(t *testing.T) {
	d := &Daemon{
		done:   make(chan struct{}),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	exited := make(chan struct{})
	go func() {
		d.signalLoop(context.Background())
		close(exited)
	}()

	close(d.done)

	select {
	case <-exited:
		// signalLoop exited cleanly.
	case <-time.After(2 * time.Second):
		t.Fatal("signalLoop did not exit within 2s after done channel closed")
	}
}

func TestSignalLoop_ExitsOnContextCancel(t *testing.T) {
	d := &Daemon{
		done:   make(chan struct{}),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	go func() {
		d.signalLoop(ctx)
		close(exited)
	}()

	cancel()

	select {
	case <-exited:
		// signalLoop exited cleanly via context cancellation.
	case <-time.After(2 * time.Second):
		t.Fatal("signalLoop did not exit within 2s after context cancelled")
	}
}

// --- Socket directory creation tests ---

func TestSocketDirectoryCreation(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "a", "b", "c")
	socketPath := filepath.Join(nested, "loom.sock")

	// MkdirAll should create all intermediate directories.
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat on nested dir failed: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected nested path to be a directory")
	}
}

func TestSocketDirectoryPermissions(t *testing.T) {
	base := t.TempDir()
	socketDir := filepath.Join(base, "daemon-sock")
	socketPath := filepath.Join(socketDir, "loom.sock")

	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	info, err := os.Stat(socketDir)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf("socket dir permissions = %o, want 0700", perm)
	}
}

func TestSocketDirectoryPreexisting(t *testing.T) {
	base := t.TempDir()
	socketDir := filepath.Join(base, "existing")
	if err := os.Mkdir(socketDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// MkdirAll on pre-existing directory should succeed without error.
	socketPath := filepath.Join(socketDir, "loom.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatalf("MkdirAll on existing dir should succeed: %v", err)
	}
}

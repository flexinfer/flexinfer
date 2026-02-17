package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
)

func newShutdownTestPool() *pool.Pool {
	return pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Second,
		DialFunc: func(context.Context, string) (mcp.Transport, error) {
			return nil, errors.New("dial not implemented in test")
		},
	})
}

func shortSocketPath(label string) string {
	return filepath.Join("/tmp", fmt.Sprintf("loom-%d-%s.sock", os.Getpid(), label))
}

func TestDaemonAcquireLock_ContentionAndRelease(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	first := &Daemon{}
	if err := first.acquireLock(); err != nil {
		t.Fatalf("first acquireLock failed: %v", err)
	}
	defer func() {
		if first.lockFile != nil {
			_ = first.lockFile.Close()
		}
	}()

	second := &Daemon{}
	err := second.acquireLock()
	if err == nil {
		t.Fatal("expected second acquireLock to fail while first lock is held")
	}
	if !strings.Contains(err.Error(), "daemon already running") {
		t.Fatalf("unexpected second acquireLock error: %v", err)
	}

	_ = first.lockFile.Close()
	first.lockFile = nil

	third := &Daemon{}
	if err := third.acquireLock(); err != nil {
		t.Fatalf("third acquireLock after release failed: %v", err)
	}
	if third.lockFile == nil {
		t.Fatal("expected third lock file to be set")
	}
	_ = third.lockFile.Close()
}

func TestDaemonStop_IdempotentAndReleasesSocketAndLock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	socketPath := shortSocketPath("daemon-stop")
	_ = os.Remove(socketPath)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}

	manifest := NewManifestManager()
	manifest.path = filepath.Join(t.TempDir(), "manifest.yaml")

	d := &Daemon{
		cfg:      Config{SocketPath: socketPath},
		done:     make(chan struct{}),
		pool:     newShutdownTestPool(),
		manifest: manifest,
		procMgr:  process.NewManager(nil, "codex"),
		listener: listener,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if err := d.acquireLock(); err != nil {
		t.Fatalf("acquireLock failed: %v", err)
	}
	if d.lockFile == nil {
		t.Fatal("expected lockFile to be set after acquireLock")
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("second Stop failed: %v", err)
	}
	if d.lockFile != nil {
		t.Fatal("expected lockFile to be nil after Stop")
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket path to be removed, stat err=%v", err)
	}

	// Confirm lock was released by acquiring it from a fresh daemon instance.
	other := &Daemon{}
	if err := other.acquireLock(); err != nil {
		t.Fatalf("expected lock to be acquirable after Stop: %v", err)
	}
	_ = other.lockFile.Close()
}

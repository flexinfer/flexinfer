package daemon

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/pkg/registry"
)

func newReaperTestDaemon(t *testing.T, serverName string) *Daemon {
	t.Helper()

	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: serverName,
				Common: &registry.TargetSpec{
					Command: "cat",
				},
			},
		},
	}

	procMgr := process.NewManager(reg, "common")
	t.Cleanup(func() {
		procMgr.StopAll()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := procMgr.Start(ctx, serverName); err != nil {
		t.Fatalf("start %s: %v", serverName, err)
	}

	proc, ok := procMgr.Get(serverName)
	if !ok {
		t.Fatalf("expected process %s to be running", serverName)
	}
	proc.LastActivity = time.Now().Add(-time.Hour)

	return &Daemon{
		procMgr: procMgr,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestReapIdleServers_ReapsUnlockedIdleServer(t *testing.T) {
	d := newReaperTestDaemon(t, "idle_srv")

	reaped := d.reapIdleServers(5 * time.Minute)
	if len(reaped) != 1 || reaped[0] != "idle_srv" {
		t.Fatalf("reaped = %v, want [idle_srv]", reaped)
	}
	if d.procMgr.Count() != 0 {
		t.Fatalf("proc count = %d, want 0", d.procMgr.Count())
	}
}

func TestReapIdleServers_SkipsServerWithInFlightCallLock(t *testing.T) {
	d := newReaperTestDaemon(t, "busy_srv")

	mu := d.callLock("busy_srv")
	mu.Lock()

	reaped := d.reapIdleServers(5 * time.Minute)
	if len(reaped) != 0 {
		t.Fatalf("reaped = %v, want empty", reaped)
	}
	if d.procMgr.Count() != 1 {
		t.Fatalf("proc count = %d, want 1", d.procMgr.Count())
	}

	mu.Unlock()

	reaped = d.reapIdleServers(5 * time.Minute)
	if len(reaped) != 1 || reaped[0] != "busy_srv" {
		t.Fatalf("reaped after unlock = %v, want [busy_srv]", reaped)
	}
}

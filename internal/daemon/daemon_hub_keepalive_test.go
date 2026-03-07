package daemon

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/pkg/registry"
)

func TestHubKeepaliveLoop_ExitsOnDone(t *testing.T) {
	done := make(chan struct{})
	d := &Daemon{
		done:   done,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		fileCfg: FileConfig{
			Hub: HubConfig{PingIntervalSeconds: 1}, // fast tick for test
		},
	}

	d.wg.Add(1)
	go d.hubKeepaliveLoop()

	// Close done immediately; loop should exit cleanly.
	close(done)
	d.wg.Wait()
}

func TestHubKeepalivePing_NoIdleSkips(t *testing.T) {
	// When there are no idle connections in the hub pool, the ping should be a no-op.
	hubPool := pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc:    nil, // won't be called
	})
	defer hubPool.Close()

	d := &Daemon{
		hubPool: hubPool,
		logger:  slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}

	// Should not panic or attempt any dial.
	d.hubKeepalivePing()
}

func TestPickHubServer_NilRegistry(t *testing.T) {
	d := &Daemon{}
	if name := d.pickHubServer(); name != "" {
		t.Fatalf("expected empty server name with nil registry, got %q", name)
	}
}

func TestPickHubServer_ReturnsFirstHubCapable(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{Name: "local-only", Categories: []string{"local-only"}},
			{Name: "hub-capable", Categories: []string{"hub"}},
		},
	}

	d := &Daemon{registry: reg}
	name := d.pickHubServer()

	// The local-only server has "local-only" category, so IsLocalOnly() returns true.
	// The hub-capable server has "hub" category, so IsLocalOnly() returns false.
	if name != "hub-capable" {
		t.Fatalf("expected 'hub-capable', got %q", name)
	}
}

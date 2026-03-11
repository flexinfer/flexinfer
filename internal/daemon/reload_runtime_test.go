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

func TestReloadInvalidationReason(t *testing.T) {
	t.Run("static spec unchanged", func(t *testing.T) {
		spec := &registry.TargetSpec{
			Command: "cat",
			Args:    []any{"--help"},
			Env:     map[string]string{"FOO": "bar"},
		}
		if got := reloadInvalidationReason(spec, spec); got != "" {
			t.Fatalf("reloadInvalidationReason() = %q, want empty", got)
		}
	})

	t.Run("launch config changed", func(t *testing.T) {
		oldSpec := &registry.TargetSpec{
			Command: "cat",
			Env:     map[string]string{"FOO": "bar"},
		}
		newSpec := &registry.TargetSpec{
			Command: "cat",
			Env:     map[string]string{"FOO": "baz"},
		}
		if got := reloadInvalidationReason(oldSpec, newSpec); got != "launch_config_changed" {
			t.Fatalf("reloadInvalidationReason() = %q, want launch_config_changed", got)
		}
	})

	t.Run("runtime template present", func(t *testing.T) {
		spec := &registry.TargetSpec{
			Command: "cat",
			Env:     map[string]string{"TOKEN": "${secret:API_TOKEN}"},
		}
		if got := reloadInvalidationReason(spec, spec); got != "runtime_templates_present" {
			t.Fatalf("reloadInvalidationReason() = %q, want runtime_templates_present", got)
		}
	})
}

func TestInvalidateServersForReloadStopsAffectedProcesses(t *testing.T) {
	oldReg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "dynamic",
				Common: &registry.TargetSpec{
					Command: "cat",
					Env:     map[string]string{"TOKEN": "${secret:API_TOKEN}"},
				},
			},
			{
				Name: "static",
				Common: &registry.TargetSpec{
					Command: "cat",
					Env:     map[string]string{"FOO": "bar"},
				},
			},
		},
	}
	newReg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "dynamic",
				Common: &registry.TargetSpec{
					Command: "cat",
					Env:     map[string]string{"TOKEN": "${secret:API_TOKEN}"},
				},
			},
			{
				Name: "static",
				Common: &registry.TargetSpec{
					Command: "cat",
					Env:     map[string]string{"FOO": "bar"},
				},
			},
		},
	}

	procMgr := process.NewManager(oldReg, "codex")
	t.Cleanup(procMgr.StopAll)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := procMgr.Start(ctx, "dynamic"); err != nil {
		t.Fatalf("start dynamic: %v", err)
	}
	if _, err := procMgr.Start(ctx, "static"); err != nil {
		t.Fatalf("start static: %v", err)
	}

	d := &Daemon{
		cfg:     Config{Target: "codex"},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		pool:    newLoopsTestPool(),
		procMgr: procMgr,
	}
	t.Cleanup(func() { _ = d.pool.Close() })
	d.runningServers.Store("dynamic", true)
	d.runningServers.Store("static", true)

	procMgr.SetRegistry(newReg)
	invalidated := d.invalidateServersForReload(oldReg, newReg)
	if len(invalidated) != 1 || invalidated[0] != "dynamic" {
		t.Fatalf("invalidated = %v, want [dynamic]", invalidated)
	}

	running := procMgr.List()
	if len(running) != 1 || !containsStringValue(running, "static") {
		t.Fatalf("running processes = %v, want [static]", running)
	}
	if _, ok := d.runningServers.Load("dynamic"); ok {
		t.Fatal("expected dynamic server to be removed from runningServers")
	}
	if _, ok := d.runningServers.Load("static"); !ok {
		t.Fatal("expected static server to remain in runningServers")
	}
}

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

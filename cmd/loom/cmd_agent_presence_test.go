package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewAgentKeepaliveCmdHasKeepaliveWrapAlias(t *testing.T) {
	cmd := newAgentKeepaliveCmd()

	found := false
	for _, alias := range cmd.Aliases {
		if alias == "keepalive-wrap" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected keepalive command to expose keepalive-wrap alias, got %v", cmd.Aliases)
	}
}

func TestRunKeepaliveLoopSendsHeartbeatsWhileChildRuns(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	var heartbeats atomic.Int32
	var deregisters atomic.Int32
	childStarted := make(chan struct{})

	err := runKeepaliveLoop(context.Background(), keepaliveLoopOptions{
		agentID:     "codex-test",
		interval:    10 * time.Millisecond,
		maxLifetime: 0,
		quiet:       true,
	}, keepaliveLoopDeps{
		sendHeartbeat: func() error {
			heartbeats.Add(1)
			return nil
		},
		deregister: func() {
			deregisters.Add(1)
		},
		runChild: func(ctx context.Context) error {
			close(childStarted)
			select {
			case <-time.After(40 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	if err != nil {
		t.Fatalf("runKeepaliveLoop() error: %v", err)
	}
	if got := heartbeats.Load(); got < 2 {
		t.Fatalf("expected at least 2 heartbeats while child ran, got %d", got)
	}
	if got := deregisters.Load(); got != 1 {
		t.Fatalf("expected one deregister on child exit, got %d", got)
	}
	select {
	case <-childStarted:
	default:
		t.Fatal("expected child runner to start")
	}
}

func TestRunKeepaliveLoopCancelsChildCleanly(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var deregisters atomic.Int32
	childStarted := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- runKeepaliveLoop(ctx, keepaliveLoopOptions{
			agentID:     "codex-test",
			interval:    10 * time.Millisecond,
			maxLifetime: 0,
			quiet:       true,
		}, keepaliveLoopDeps{
			sendHeartbeat: func() error { return nil },
			deregister: func() {
				deregisters.Add(1)
			},
			runChild: func(childCtx context.Context) error {
				close(childStarted)
				<-childCtx.Done()
				return nil
			},
		})
	}()

	select {
	case <-childStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("child runner did not start")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runKeepaliveLoop() after cancel = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("keepalive loop did not exit after cancel")
	}

	if got := deregisters.Load(); got != 1 {
		t.Fatalf("expected one deregister on shutdown, got %d", got)
	}
}

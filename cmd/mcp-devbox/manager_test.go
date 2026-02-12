package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestCheckBackendHealth_Timeout(t *testing.T) {
	orig := backendHealthTimeout
	backendHealthTimeout = 50 * time.Millisecond
	defer func() {
		backendHealthTimeout = orig
	}()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := time.Now()

	checkBackendHealth(context.Background(), logger, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("health check took too long: %v", elapsed)
	}
}

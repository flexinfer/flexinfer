package main

import (
	"context"
	"testing"
	"time"
)

func TestParseDurationWithDays(t *testing.T) {
	t.Parallel()

	d, err := parseDurationWithDays("7d")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if d != 7*24*time.Hour {
		t.Fatalf("expected 7d, got %s", d)
	}

	if _, err := parseDurationWithDays("1.5d"); err == nil {
		t.Fatalf("expected error for non-integer days")
	}
}

func TestHandleWaitRespectsDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	_, err := handleWait(ctx, map[string]any{"duration": "200ms"})
	if err == nil {
		t.Fatalf("expected deadline-related error")
	}
}

package main

import (
	"context"
	"testing"
	"time"
)

func newTestHelm() *helmServer {
	return &helmServer{
		namespace: "default",
		timeout:   10 * time.Second,
	}
}

// ---------------------------------------------------------------------------
// Validation error tests
// ---------------------------------------------------------------------------

func TestHandleStatus_MissingRelease(t *testing.T) {
	h := newTestHelm()
	result, err := h.handleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing release")
	}
}

func TestHandleValues_MissingRelease(t *testing.T) {
	h := newTestHelm()
	result, err := h.handleValues(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing release")
	}
}

func TestHandleHistory_MissingRelease(t *testing.T) {
	h := newTestHelm()
	result, err := h.handleHistory(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing release")
	}
}

func TestHandleSearch_MissingKeyword(t *testing.T) {
	h := newTestHelm()
	result, err := h.handleSearch(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing keyword")
	}
}

func TestHandleShow_MissingChart(t *testing.T) {
	h := newTestHelm()
	result, err := h.handleShow(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing chart")
	}
}

func TestHandleTemplate_MissingParams(t *testing.T) {
	h := newTestHelm()

	t.Run("missing both", func(t *testing.T) {
		result, err := h.handleTemplate(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result")
		}
	})
	t.Run("missing release", func(t *testing.T) {
		result, err := h.handleTemplate(context.Background(), map[string]any{
			"chart": "stable/nginx",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing release")
		}
	})
	t.Run("missing chart", func(t *testing.T) {
		result, err := h.handleTemplate(context.Background(), map[string]any{
			"release": "my-release",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing chart")
		}
	})
}

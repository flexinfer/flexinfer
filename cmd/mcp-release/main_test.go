package main

import (
	"context"
	"testing"
)

func TestHandleValidate_MissingParams(t *testing.T) {
	result, err := handleValidate(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleChangelog_MissingParams(t *testing.T) {
	result, err := handleChangelog(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleStatus_MissingParams(t *testing.T) {
	result, err := handleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

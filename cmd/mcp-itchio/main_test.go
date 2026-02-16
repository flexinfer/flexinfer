package main

import (
	"context"
	"testing"
)

func TestHandleUpload_MissingParams(t *testing.T) {
	result, err := handleUpload(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleUpload_MissingFile(t *testing.T) {
	result, err := handleUpload(context.Background(), map[string]any{
		"project": "user/game",
		"channel": "linux-amd64",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing file")
	}
}

func TestHandleUpload_MissingProject(t *testing.T) {
	result, err := handleUpload(context.Background(), map[string]any{
		"file":    "/tmp/game.zip",
		"channel": "linux-amd64",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing project")
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

func TestHandleVersionHistory_MissingParams(t *testing.T) {
	result, err := handleVersionHistory(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

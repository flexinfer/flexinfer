package main

import (
	"context"
	"testing"
)

func TestHandleEditFile_MissingParams(t *testing.T) {
	result, err := handleEditFile(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleEditFile_MissingInstruction(t *testing.T) {
	result, err := handleEditFile(context.Background(), map[string]any{
		"path": "test.go",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing instruction")
	}
}

func TestHandleEditFile_MissingUpdate(t *testing.T) {
	result, err := handleEditFile(context.Background(), map[string]any{
		"path":        "test.go",
		"instruction": "fix the bug",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing update")
	}
}

package main

import (
	"context"
	"strings"
	"testing"
)

func TestHandleEmbed_MissingAPIKey(t *testing.T) {
	origKey := morphAPIKey
	morphAPIKey = ""
	defer func() { morphAPIKey = origKey }()

	result, err := handleEmbed(context.Background(), map[string]any{
		"input": "test text",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when API key is empty")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "MORPH_API_KEY") {
		t.Errorf("expected MORPH_API_KEY in error, got: %s", text)
	}
}

func TestHandleEmbed_MissingInput(t *testing.T) {
	result, err := handleEmbed(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing input")
	}
}

func TestHandleUpsert_MissingInput(t *testing.T) {
	result, err := handleUpsert(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing input")
	}
}

func TestHandleSearch_MissingQuery(t *testing.T) {
	result, err := handleSearch(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing query")
	}
}

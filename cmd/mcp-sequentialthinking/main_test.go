package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initTestState(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	state = &ThinkingState{
		Chains:      make(map[string]*ThoughtChain),
		persistPath: filepath.Join(tmpDir, "thinking.json"),
	}
}

func TestHandleStartThinking(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		initTestState(t)
		result, err := handleStartThinking(context.Background(), map[string]any{
			"title":           "Test chain",
			"initial_thought": "Let me think about this",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		if len(result.Content) == 0 {
			t.Fatal("expected content")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "chain_id") {
			t.Errorf("expected chain_id in result, got: %s", text)
		}
		if len(state.Chains) != 1 {
			t.Errorf("expected 1 chain, got %d", len(state.Chains))
		}
	})

	t.Run("without initial thought", func(t *testing.T) {
		initTestState(t)
		result, err := handleStartThinking(context.Background(), map[string]any{
			"title": "Empty chain",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		// Chain should have 0 steps
		for _, c := range state.Chains {
			if len(c.Steps) != 0 {
				t.Errorf("expected 0 steps, got %d", len(c.Steps))
			}
		}
	})

	t.Run("persist to disk", func(t *testing.T) {
		initTestState(t)
		_, err := handleStartThinking(context.Background(), map[string]any{
			"title": "Persisted chain",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Verify file was created
		if _, err := os.Stat(state.persistPath); os.IsNotExist(err) {
			t.Error("expected persist file to be created")
		}
	})
}

func TestHandleAddThought(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		initTestState(t)
		// First create a chain
		handleStartThinking(context.Background(), map[string]any{"title": "Test"})

		result, err := handleAddThought(context.Background(), map[string]any{
			"thought":      "This is a thought",
			"thought_type": "observation",
			"confidence":   0.8,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "step_id") {
			t.Errorf("expected step_id in result, got: %s", text)
		}
	})

	t.Run("missing thought param", func(t *testing.T) {
		initTestState(t)
		handleStartThinking(context.Background(), map[string]any{"title": "Test"})

		result, err := handleAddThought(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing thought")
		}
	})

	t.Run("no active chain", func(t *testing.T) {
		initTestState(t)
		result, err := handleAddThought(context.Background(), map[string]any{
			"thought": "orphan thought",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for no active chain")
		}
		if !strings.Contains(result.Content[0].Text, "no active chain") {
			t.Errorf("error should mention no active chain, got: %s", result.Content[0].Text)
		}
	})
}

func TestHandleGetChain(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		initTestState(t)
		handleStartThinking(context.Background(), map[string]any{
			"title":           "Test",
			"initial_thought": "first thought",
		})

		result, err := handleGetChain(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "Test") {
			t.Errorf("expected chain title in result, got: %s", text)
		}
	})

	t.Run("no active chain", func(t *testing.T) {
		initTestState(t)
		result, err := handleGetChain(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for no active chain")
		}
	})

	t.Run("nonexistent chain", func(t *testing.T) {
		initTestState(t)
		result, err := handleGetChain(context.Background(), map[string]any{
			"chain_id": "nonexistent",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for nonexistent chain")
		}
	})
}

func TestHandleListChains(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		initTestState(t)
		result, err := handleListChains(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		text := result.Content[0].Text
		if !strings.Contains(text, "total") {
			t.Errorf("expected total field, got: %s", text)
		}
	})

	t.Run("with chains", func(t *testing.T) {
		initTestState(t)
		handleStartThinking(context.Background(), map[string]any{"title": "Chain 1"})
		handleStartThinking(context.Background(), map[string]any{"title": "Chain 2"})

		result, err := handleListChains(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		initTestState(t)
		handleStartThinking(context.Background(), map[string]any{"title": "Active chain"})

		result, err := handleListChains(context.Background(), map[string]any{
			"status": "active",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
	})
}

func TestHandleSetActiveChain(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		initTestState(t)
		handleStartThinking(context.Background(), map[string]any{"title": "Chain 1"})
		chainID1 := state.ActiveChain
		handleStartThinking(context.Background(), map[string]any{"title": "Chain 2"})

		result, err := handleSetActiveChain(context.Background(), map[string]any{
			"chain_id": chainID1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		if state.ActiveChain != chainID1 {
			t.Errorf("active chain = %q, want %q", state.ActiveChain, chainID1)
		}
	})

	t.Run("missing chain_id", func(t *testing.T) {
		initTestState(t)
		result, err := handleSetActiveChain(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing chain_id")
		}
	})

	t.Run("nonexistent chain", func(t *testing.T) {
		initTestState(t)
		result, err := handleSetActiveChain(context.Background(), map[string]any{
			"chain_id": "nonexistent",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for nonexistent chain")
		}
	})
}

func TestHandleCompleteChain(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		initTestState(t)
		handleStartThinking(context.Background(), map[string]any{"title": "Test"})

		result, err := handleCompleteChain(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success, got error: %v", result.Content)
		}
	})

	t.Run("no active chain", func(t *testing.T) {
		initTestState(t)
		result, err := handleCompleteChain(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for no active chain")
		}
	})
}

func TestHandleDeleteChain(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		initTestState(t)
		handleStartThinking(context.Background(), map[string]any{"title": "To delete"})
		chainID := state.ActiveChain

		result, err := handleDeleteChain(context.Background(), map[string]any{
			"chain_id": chainID,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success")
		}
		if _, exists := state.Chains[chainID]; exists {
			t.Error("expected chain to be deleted")
		}
	})

	t.Run("missing chain_id", func(t *testing.T) {
		initTestState(t)
		result, err := handleDeleteChain(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing chain_id")
		}
	})
}

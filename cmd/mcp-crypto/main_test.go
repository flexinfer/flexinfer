package main

import (
	"context"
	"strings"
	"testing"
)

func TestHandleRandomString_Default(t *testing.T) {
	result, err := handleRandomString(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "result") {
		t.Errorf("expected 'result' key in output, got: %s", text)
	}
	if !strings.Contains(text, "length") {
		t.Errorf("expected 'length' key in output, got: %s", text)
	}
}

func TestHandleRandomString_CustomLength(t *testing.T) {
	result, err := handleRandomString(context.Background(), map[string]any{
		"length":  float64(32),
		"charset": "hex",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "hex") {
		t.Errorf("expected 'hex' charset in output, got: %s", text)
	}
}

func TestHandleRandomString_InvalidLength(t *testing.T) {
	result, err := handleRandomString(context.Background(), map[string]any{
		"length": float64(-5),
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for negative length")
	}
}

func TestHandleUUID_Format(t *testing.T) {
	result, err := handleUUID(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "uuid") {
		t.Errorf("expected 'uuid' key in output, got: %s", text)
	}
	// UUID v4 has dashes: 8-4-4-4-12
	if !strings.Contains(text, "-") {
		t.Errorf("expected UUID with dashes, got: %s", text)
	}
}

func TestHandleUUID_Uniqueness(t *testing.T) {
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		result, err := handleUUID(context.Background(), map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i, err)
		}
		results[i] = result.Content[0].Text
	}
	seen := make(map[string]bool)
	for _, r := range results {
		if seen[r] {
			t.Errorf("duplicate UUID output: %s", r)
		}
		seen[r] = true
	}
}

func TestHandleHashString_SHA256(t *testing.T) {
	result, err := handleHashString(context.Background(), map[string]any{
		"text": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if !strings.Contains(text, want) {
		t.Errorf("expected SHA256 hash of 'hello' in output, got: %s", text)
	}
}

func TestHandleHashString_MD5(t *testing.T) {
	result, err := handleHashString(context.Background(), map[string]any{
		"text":      "hello",
		"algorithm": "md5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	want := "5d41402abc4b2a76b9719d911017c592"
	if !strings.Contains(text, want) {
		t.Errorf("expected MD5 hash of 'hello' in output, got: %s", text)
	}
}

func TestHandleHashString_MissingText(t *testing.T) {
	result, err := handleHashString(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing text")
	}
}

func TestHandleBase64Encode_HappyPath(t *testing.T) {
	result, err := handleBase64Encode(context.Background(), map[string]any{
		"text": "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "aGVsbG8gd29ybGQ=") {
		t.Errorf("expected base64 of 'hello world' in output, got: %s", text)
	}
}

func TestHandleBase64Encode_MissingText(t *testing.T) {
	result, err := handleBase64Encode(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing text")
	}
}

func TestHandleBase64Decode_HappyPath(t *testing.T) {
	result, err := handleBase64Decode(context.Background(), map[string]any{
		"text": "aGVsbG8gd29ybGQ=",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "hello world") {
		t.Errorf("expected 'hello world' in decoded output, got: %s", text)
	}
}

func TestHandleBase64Decode_InvalidInput(t *testing.T) {
	result, err := handleBase64Decode(context.Background(), map[string]any{
		"text": "not-valid-base64!!!",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid base64")
	}
}

func TestHandleBase64Decode_MissingText(t *testing.T) {
	result, err := handleBase64Decode(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing text")
	}
}

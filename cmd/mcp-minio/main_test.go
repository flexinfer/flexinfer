package main

import (
	"context"
	"strings"
	"testing"
)

func TestHandleListBuckets_MissingCreds(t *testing.T) {
	origAccess := accessKey
	origSecret := secretKey
	accessKey = ""
	secretKey = ""
	minioClient = nil // force re-init
	defer func() {
		accessKey = origAccess
		secretKey = origSecret
		minioClient = nil
	}()

	result, err := handleListBuckets(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when credentials are empty")
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "MINIO_ACCESS_KEY") {
		t.Errorf("expected MINIO_ACCESS_KEY in error, got: %s", text)
	}
}

func TestHandleListObjects_MissingBucket(t *testing.T) {
	result, err := handleListObjects(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing bucket")
	}
}

func TestHandleGetObjectText_MissingParams(t *testing.T) {
	result, err := handleGetObjectText(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandleStatObject_MissingParams(t *testing.T) {
	result, err := handleStatObject(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandlePresignGet_MissingParams(t *testing.T) {
	result, err := handlePresignGet(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

func TestHandlePresignPut_MissingParams(t *testing.T) {
	result, err := handlePresignPut(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing required fields")
	}
}

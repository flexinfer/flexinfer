package main

import (
	"context"
	"testing"
)

func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain ID", "dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"watch URL", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"short URL", "https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"embed URL", "https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"shorts URL", "https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"unknown format", "not-a-url", "not-a-url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractVideoID(tt.input)
			if got != tt.want {
				t.Errorf("extractVideoID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHandleGetTranscript_MissingURL(t *testing.T) {
	result, err := handleGetTranscript(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing url")
	}
}

func TestHandleGetVideoInfo_MissingURL(t *testing.T) {
	result, err := handleGetVideoInfo(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing url")
	}
}

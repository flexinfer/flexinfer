package main

import (
	"context"
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestNormalizeImageDataURL_RawBase64(t *testing.T) {
	got, err := normalizeImageDataURL("image/png", "AQID")
	if err != nil {
		t.Fatalf("normalizeImageDataURL returned error: %v", err)
	}
	want := "data:image/png;base64,AQID"
	if got != want {
		t.Fatalf("unexpected data URL\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalizeImageDataURL_ExistingDataURL(t *testing.T) {
	got, err := normalizeImageDataURL("image/png", "data:image/jpeg;base64,AQID")
	if err != nil {
		t.Fatalf("normalizeImageDataURL returned error: %v", err)
	}
	want := "data:image/jpeg;base64,AQID"
	if got != want {
		t.Fatalf("unexpected data URL\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalizeImageDataURL_InvalidData(t *testing.T) {
	if _, err := normalizeImageDataURL("image/png", "not-base64@@@"); err == nil {
		t.Fatalf("expected error for invalid base64 payload")
	}
}

func TestNormalizeImageDataURL_InvalidDataURL(t *testing.T) {
	if _, err := normalizeImageDataURL("image/png", "data:image/png,abc"); err == nil {
		t.Fatalf("expected error for non-base64 data URL payload")
	}
}

func TestHandleScreenshot_ValidatesParams(t *testing.T) {
	t.Run("rejects unsupported scheme", func(t *testing.T) {
		res, err := handleScreenshot(context.Background(), map[string]any{
			"url": "javascript:alert(1)",
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if res == nil || !res.IsError {
			t.Fatalf("expected error result, got %+v", res)
		}
		if got := toolResultText(res); !strings.Contains(got, "invalid parameter 'url'") {
			t.Fatalf("expected url validation error, got: %q", got)
		}
	})

	t.Run("rejects invalid format", func(t *testing.T) {
		res, err := handleScreenshot(context.Background(), map[string]any{
			"url":    "https://example.com",
			"format": "gif",
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if res == nil || !res.IsError {
			t.Fatalf("expected error result, got %+v", res)
		}
		if got := toolResultText(res); !strings.Contains(got, "invalid parameter 'format'") {
			t.Fatalf("expected format validation error, got: %q", got)
		}
	})
}

func toolResultText(res *mcp.CallToolResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range res.Content {
		if c.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Text)
	}
	return b.String()
}

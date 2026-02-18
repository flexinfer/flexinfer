package main

import (
	"os"
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/daemon"
)

func TestTruncateCallToolResult_NoChangeWhenUnderLimit(t *testing.T) {
	r := &mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: "hello"}},
	}
	if truncateCallToolResult(r, 1024, 1024) {
		t.Fatalf("expected no truncation")
	}
	if got := r.Content[0].Text; got != "hello" {
		t.Fatalf("unexpected text: %q", got)
	}
}

func TestTruncateCallToolResult_TruncatesAndAddsSuffix(t *testing.T) {
	maxBytes := 2000
	huge := strings.Repeat("x", maxBytes*2)
	r := &mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: huge}},
	}

	if !truncateCallToolResult(r, maxBytes, 1024) {
		t.Fatalf("expected truncation")
	}
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(r.Content))
	}
	if len(r.Content[0].Text) > maxBytes {
		t.Fatalf("expected truncated text <= %d bytes, got %d", maxBytes, len(r.Content[0].Text))
	}
	if !strings.Contains(r.Content[0].Text, "[loom] output truncated") {
		t.Fatalf("expected truncation suffix, got: %q", r.Content[0].Text)
	}
}

func TestTruncateCallToolResult_DropsExtraContentAndHonorsBudget(t *testing.T) {
	maxBytes := 1024
	r := &mcp.CallToolResult{
		Content: []mcp.Content{
			{Type: "text", Text: strings.Repeat("a", maxBytes-10)},
			{Type: "text", Text: strings.Repeat("b", maxBytes)},
		},
	}

	if !truncateCallToolResult(r, maxBytes, 1024) {
		t.Fatalf("expected truncation")
	}

	total := 0
	for _, c := range r.Content {
		total += len(c.Text) + len(c.Data)
	}
	if total > maxBytes {
		t.Fatalf("expected total <= %d bytes, got %d", maxBytes, total)
	}
	if len(r.Content) != 2 {
		t.Fatalf("expected second content item to remain (truncated), got %d items", len(r.Content))
	}
}

func TestTruncateCallToolResult_ImageNeverTruncatesBase64(t *testing.T) {
	maxText := 512
	maxImg := 1024
	hugeImg := "data:image/png;base64," + strings.Repeat("A", maxImg*2)

	r := &mcp.CallToolResult{
		Content: []mcp.Content{
			{Type: "image", MimeType: "image/png", Data: hugeImg},
			{Type: "text", Text: "after"},
		},
	}

	if !truncateCallToolResult(r, maxText, maxImg) {
		t.Fatalf("expected truncation due to oversized image")
	}
	// Image should be dropped, not truncated.
	for _, c := range r.Content {
		if c.Type == "image" {
			t.Fatalf("expected image to be omitted, got image content")
		}
		if strings.Contains(c.Data, "data:image") {
			t.Fatalf("expected no image data URL remnants in truncated output")
		}
	}
	// Ensure we still keep the trailing text content.
	foundAfter := false
	for _, c := range r.Content {
		if c.Type == "text" && c.Text == "after" {
			foundAfter = true
		}
	}
	if !foundAfter {
		t.Fatalf("expected to keep trailing text content")
	}
}

func TestResolveProxyLimit_EnvOverridesConfig(t *testing.T) {
	os.Setenv("TEST_PROXY_LIMIT", "99999")
	defer os.Unsetenv("TEST_PROXY_LIMIT")

	got := resolveProxyLimit("TEST_PROXY_LIMIT", 50000, 48000, 1024)
	if got != 99999 {
		t.Errorf("resolveProxyLimit = %d, want 99999 (env)", got)
	}
}

func TestResolveProxyLimit_ConfigFallback(t *testing.T) {
	os.Unsetenv("TEST_PROXY_LIMIT_2")

	got := resolveProxyLimit("TEST_PROXY_LIMIT_2", 60000, 48000, 1024)
	if got != 60000 {
		t.Errorf("resolveProxyLimit = %d, want 60000 (config)", got)
	}
}

func TestResolveProxyLimit_HardcodedDefault(t *testing.T) {
	os.Unsetenv("TEST_PROXY_LIMIT_3")

	got := resolveProxyLimit("TEST_PROXY_LIMIT_3", 0, 48000, 1024)
	if got != 48000 {
		t.Errorf("resolveProxyLimit = %d, want 48000 (default)", got)
	}
}

func TestResolveProxyLimit_MinBound(t *testing.T) {
	os.Setenv("TEST_PROXY_LIMIT_4", "500")
	defer os.Unsetenv("TEST_PROXY_LIMIT_4")

	got := resolveProxyLimit("TEST_PROXY_LIMIT_4", 0, 48000, 1024)
	if got != 1024 {
		t.Errorf("resolveProxyLimit = %d, want 1024 (min bound)", got)
	}
}

func TestProxyMaxToolResultBytes_ConfigFallback(t *testing.T) {
	os.Unsetenv(loomProxyMaxToolResultBytesEnv)
	saved := proxyConfigGlobal
	defer func() { proxyConfigGlobal = saved }()

	proxyConfigGlobal = daemon.ProxyConfig{MaxToolResultBytes: 70000}
	got := proxyMaxToolResultBytes()
	if got != 70000 {
		t.Errorf("proxyMaxToolResultBytes() = %d, want 70000", got)
	}
}

func TestProxyMaxToolResultBytes_EnvOverridesConfig(t *testing.T) {
	os.Setenv(loomProxyMaxToolResultBytesEnv, "80000")
	defer os.Unsetenv(loomProxyMaxToolResultBytesEnv)
	saved := proxyConfigGlobal
	defer func() { proxyConfigGlobal = saved }()

	proxyConfigGlobal = daemon.ProxyConfig{MaxToolResultBytes: 70000}
	got := proxyMaxToolResultBytes()
	if got != 80000 {
		t.Errorf("proxyMaxToolResultBytes() = %d, want 80000 (env)", got)
	}
}

func TestProxyToolPageSize_DefaultAndClamp(t *testing.T) {
	os.Unsetenv(loomProxyToolPageSizeEnv)
	if got := proxyToolPageSize(); got != defaultToolPageSize {
		t.Errorf("proxyToolPageSize() = %d, want %d", got, defaultToolPageSize)
	}

	os.Setenv(loomProxyToolPageSizeEnv, "1")
	if got := proxyToolPageSize(); got != minToolPageSize {
		t.Errorf("proxyToolPageSize() = %d, want %d", got, minToolPageSize)
	}

	os.Setenv(loomProxyToolPageSizeEnv, "9999")
	if got := proxyToolPageSize(); got != maxToolPageSize {
		t.Errorf("proxyToolPageSize() = %d, want %d", got, maxToolPageSize)
	}
	os.Unsetenv(loomProxyToolPageSizeEnv)
}

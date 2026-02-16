package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/daemon"
)

const (
	loomProxyMaxToolResultBytesEnv = "LOOM_PROXY_MAX_TOOL_RESULT_BYTES"
	defaultMaxToolResultBytes      = 48_000

	loomProxyMaxImageResultBytesEnv = "LOOM_PROXY_MAX_IMAGE_RESULT_BYTES"
	defaultMaxImageResultBytes      = 1_500_000

	loomProxyMaxResourceBytesEnv = "LOOM_PROXY_MAX_RESOURCE_BYTES"
	defaultMaxResourceBytes      = 64_000
)

// proxyConfigGlobal holds the loaded file config for proxy-side settings.
// It is loaded once at proxy startup.
var proxyConfigGlobal daemon.ProxyConfig

func proxyMaxToolResultBytes() int {
	return resolveProxyLimit(
		loomProxyMaxToolResultBytesEnv,
		proxyConfigGlobal.MaxToolResultBytes,
		defaultMaxToolResultBytes,
		1024,
	)
}

func proxyMaxImageResultBytes() int {
	return resolveProxyLimit(
		loomProxyMaxImageResultBytesEnv,
		proxyConfigGlobal.MaxImageResultBytes,
		defaultMaxImageResultBytes,
		16_384,
	)
}

func proxyMaxResourceBytes() int {
	return resolveProxyLimit(
		loomProxyMaxResourceBytesEnv,
		proxyConfigGlobal.MaxResourceBytes,
		defaultMaxResourceBytes,
		1024,
	)
}

// resolveProxyLimit resolves a proxy limit with precedence: env var > config file > hardcoded default.
func resolveProxyLimit(envKey string, configValue, hardcodedDefault, minValue int) int {
	// Env var takes highest precedence.
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			if n < minValue {
				return minValue
			}
			return n
		}
	}
	// Config file is next.
	if configValue > 0 {
		if configValue < minValue {
			return minValue
		}
		return configValue
	}
	// Hardcoded default.
	return hardcodedDefault
}

func truncateResourceText(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	suffix := fmt.Sprintf("\n\n[loom] resource truncated to %d bytes (set %s to increase)\n", maxBytes, loomProxyMaxResourceBytesEnv)
	allowed := maxBytes - len(suffix)
	if allowed < 0 {
		allowed = 0
		suffix = ""
	}
	return truncateUTF8Bytes(text, allowed) + suffix
}

func truncateCallToolResult(result *mcp.CallToolResult, maxTextBytes int, maxImageBytes int) bool {
	if result == nil || maxTextBytes <= 0 || len(result.Content) == 0 {
		return false
	}
	if maxImageBytes <= 0 {
		maxImageBytes = 0
	}

	remainingText := maxTextBytes
	remainingImage := maxImageBytes
	changed := false
	out := make([]mcp.Content, 0, len(result.Content))

	for _, c := range result.Content {
		// Avoid emitting partial base64 data URLs (breaks clients like Codex/OpenAI).
		// Treat image payloads as atomic: keep whole if within image budget, else drop.
		if c.Type == "image" || strings.HasPrefix(strings.ToLower(c.MimeType), "image/") || strings.HasPrefix(strings.ToLower(c.Data), "data:image/") {
			size := len(c.Data)
			if size == 0 {
				out = append(out, c)
				continue
			}
			if remainingImage >= size {
				out = append(out, c)
				remainingImage -= size
				continue
			}

			// Not enough budget: drop the image rather than truncating it.
			note := mcp.Content{
				Type: "text",
				Text: fmt.Sprintf("[loom] image output omitted (%d bytes). Reduce screenshot size/quality or increase %s.\n", size, loomProxyMaxImageResultBytesEnv),
			}
			// Best-effort fit note into text budget.
			if len(note.Text) > remainingText && remainingText > 0 {
				note.Text = truncateUTF8Bytes(note.Text, remainingText)
			}
			if remainingText > 0 {
				remainingText -= min(remainingText, len(note.Text))
				out = append(out, note)
			}
			changed = true
			continue
		}

		// Count both text and data since either can blow up message sizes.
		size := len(c.Text) + len(c.Data)
		if size == 0 {
			out = append(out, c)
			continue
		}

		if size <= remainingText {
			out = append(out, c)
			remainingText -= size
			continue
		}

		// Truncate this content item and stop; later items are dropped to keep total size bounded.
		suffix := fmt.Sprintf("\n\n[loom] output truncated to %d bytes (set %s to increase)\n", maxTextBytes, loomProxyMaxToolResultBytesEnv)

		if remainingText <= 0 {
			changed = true
			break
		}

		if len(suffix) >= remainingText {
			// Not enough room to include the suffix; prioritize returning some data.
			suffix = ""
		}

		allowed := remainingText - len(suffix)
		if allowed < 0 {
			allowed = 0
		}

		if c.Text != "" {
			c.Text = truncateUTF8Bytes(c.Text, allowed) + suffix
		} else if c.Data != "" {
			c.Data = truncateUTF8Bytes(c.Data, allowed) + suffix
		}

		out = append(out, c)
		changed = true
		break
	}

	if changed {
		result.Content = out
	}
	return changed
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}

	b := []byte(s)
	b = b[:maxBytes]
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

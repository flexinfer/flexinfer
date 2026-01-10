package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	loomProxyMaxToolResultBytesEnv = "LOOM_PROXY_MAX_TOOL_RESULT_BYTES"
	defaultMaxToolResultBytes      = 48_000
)

func proxyMaxToolResultBytes() int {
	v := strings.TrimSpace(os.Getenv(loomProxyMaxToolResultBytesEnv))
	if v == "" {
		return defaultMaxToolResultBytes
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultMaxToolResultBytes
	}
	// Keep a sane lower bound to avoid zero-length results.
	if n < 1024 {
		return 1024
	}
	return n
}

func truncateCallToolResult(result *mcp.CallToolResult, maxBytes int) bool {
	if result == nil || maxBytes <= 0 || len(result.Content) == 0 {
		return false
	}

	remaining := maxBytes
	changed := false
	out := make([]mcp.Content, 0, len(result.Content))

	for _, c := range result.Content {
		// Count both text and data since either can blow up message sizes.
		size := len(c.Text) + len(c.Data)
		if size == 0 {
			out = append(out, c)
			continue
		}

		if size <= remaining {
			out = append(out, c)
			remaining -= size
			continue
		}

		// Truncate this content item and stop; later items are dropped to keep total size bounded.
		suffix := fmt.Sprintf("\n\n[loom] output truncated to %d bytes (set %s to increase)\n", maxBytes, loomProxyMaxToolResultBytesEnv)

		if remaining <= 0 {
			changed = true
			break
		}

		if len(suffix) >= remaining {
			// Not enough room to include the suffix; prioritize returning some data.
			suffix = ""
		}

		allowed := remaining - len(suffix)
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

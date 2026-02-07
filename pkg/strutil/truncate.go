// Package strutil provides string utility functions for MCP servers.
package strutil

import "strings"

// Truncate truncates a string to maxLen characters, adding "..." if truncated.
// The returned string will be at most maxLen characters (including the ellipsis).
// If maxLen <= 3, the string is truncated without ellipsis.
//
// Usage:
//
//	strutil.Truncate("hello world", 8) // "hello..."
//	strutil.Truncate("hi", 8)          // "hi"
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// TruncateNoEllipsis truncates a string to maxLen characters without adding ellipsis.
func TruncateNoEllipsis(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// TruncateSingleLine truncates a string to maxLen characters, replacing
// newlines with spaces before truncation and adding "..." if truncated.
// The returned string will be at most maxLen characters (including the ellipsis).
func TruncateSingleLine(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	return Truncate(s, maxLen)
}

// TruncateBytes truncates a string to at most maxBytes bytes, respecting
// UTF-8 boundaries. If truncation occurs, "..." is appended.
// This is useful when you need to fit a string within a byte limit.
func TruncateBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}

	ellipsis := "..."
	if maxBytes <= len(ellipsis) {
		return s[:maxBytes]
	}

	// Find the last valid UTF-8 boundary within maxBytes - len(ellipsis)
	target := maxBytes - len(ellipsis)
	for target > 0 && !isUTF8Start(s[target]) {
		target--
	}

	return s[:target] + ellipsis
}

// BodySnippet converts a byte slice (typically from io.ReadAll) to a truncated string.
// It trims whitespace, returns "<empty response body>" for empty input,
// and truncates to max bytes using Truncate.
// This is a convenience wrapper commonly used for HTTP response body error messages.
func BodySnippet(body []byte, max int) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "<empty response body>"
	}
	return Truncate(s, max)
}

// isUTF8Start returns true if b is the start of a UTF-8 character
// (not a continuation byte).
func isUTF8Start(b byte) bool {
	// Continuation bytes are 10xxxxxx (0x80-0xBF)
	return b&0xC0 != 0x80
}

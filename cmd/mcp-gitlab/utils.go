package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
)

func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	secs, err := strconv.Atoi(v)
	if err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	// Retry-After also supports HTTP date format.
	if t, parseErr := http.ParseTime(v); parseErr == nil {
		delay := time.Until(t)
		if delay <= 0 {
			return 0
		}
		return delay
	}
	return 0
}

func parsePaginationHeaders(headers http.Header) map[string]any {
	if headers == nil {
		return nil
	}
	out := map[string]any{}
	for _, kv := range []struct {
		key string
		dst string
	}{
		{"X-Page", "page"},
		{"X-Per-Page", "per_page"},
		{"X-Next-Page", "next_page"},
		{"X-Prev-Page", "prev_page"},
		{"X-Total-Pages", "total_pages"},
		{"X-Total", "total"},
	} {
		v := strings.TrimSpace(headers.Get(kv.key))
		if v == "" {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			out[kv.dst] = n
		} else {
			out[kv.dst] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func readTail(r io.Reader, maxBytes int) ([]byte, int, error) {
	if maxBytes <= 0 {
		return nil, 0, fmt.Errorf("maxBytes must be > 0")
	}

	ring := make([]byte, maxBytes)
	buf := make([]byte, 32*1024)
	pos := 0
	filled := 0
	total := 0

	for {
		n, err := r.Read(buf)
		if n > 0 {
			total += n
			if n >= maxBytes {
				copy(ring, buf[n-maxBytes:n])
				pos = 0
				filled = maxBytes
			} else {
				end := pos + n
				if end <= maxBytes {
					copy(ring[pos:end], buf[:n])
				} else {
					first := maxBytes - pos
					copy(ring[pos:], buf[:first])
					copy(ring[:end-maxBytes], buf[first:n])
				}
				pos = end % maxBytes
				if filled < maxBytes {
					filled += n
					if filled > maxBytes {
						filled = maxBytes
					}
				}
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, total, err
		}
	}

	if filled == 0 {
		return []byte{}, total, nil
	}

	if filled < maxBytes {
		return ring[:filled], total, nil
	}

	// pos is the start of the oldest data.
	out := make([]byte, 0, maxBytes)
	out = append(out, ring[pos:]...)
	out = append(out, ring[:pos]...)
	return out, total, nil
}

func encodeProject(project string) string {
	return url.PathEscape(project)
}

func encodeArtifactPath(artifactPath string) string {
	parts := strings.Split(strings.TrimPrefix(artifactPath, "/"), "/")
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return strings.Join(escaped, "/")
}

func validatePositiveIntParam(field string, value int) *mcp.CallToolResult {
	if value <= 0 {
		return mcp.ErrorResult(mcperror.InvalidParam(field, "must be greater than 0"))
	}
	return nil
}

func parseOptionalPositiveIntSliceArg(args map[string]any, field string) ([]int, error) {
	raw, ok := args[field]
	if !ok || raw == nil {
		return nil, nil
	}

	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of integers")
	}

	out := make([]int, 0, len(values))
	for i, item := range values {
		n, ok := toInt(item)
		if !ok {
			return nil, fmt.Errorf("item %d must be an integer", i)
		}
		if n <= 0 {
			return nil, fmt.Errorf("item %d must be greater than 0", i)
		}
		out = append(out, n)
	}

	return out, nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case float32:
		if n != float32(int(n)) {
			return 0, false
		}
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case uint:
		return int(n), true
	case uint64:
		return int(n), true
	case uint32:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func isTextContent(contentType string, data []byte) bool {
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	if strings.Contains(contentType, "json") ||
		strings.Contains(contentType, "xml") ||
		strings.Contains(contentType, "yaml") {
		return true
	}
	// Check first 512 bytes for text
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		b := data[i]
		// Allow printable ASCII, tabs, newlines, carriage returns
		if b < 32 && b != '\t' && b != '\n' && b != '\r' {
			return false
		}
		if b > 126 && b < 160 {
			return false
		}
	}
	return true
}

func encodeBase64(data []byte) string {
	const base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var result strings.Builder
	result.Grow((len(data)*4 + 2) / 3)

	for i := 0; i < len(data); i += 3 {
		var n uint32
		remaining := len(data) - i

		n = uint32(data[i]) << 16
		if remaining > 1 {
			n |= uint32(data[i+1]) << 8
		}
		if remaining > 2 {
			n |= uint32(data[i+2])
		}

		result.WriteByte(base64Chars[(n>>18)&0x3f])
		result.WriteByte(base64Chars[(n>>12)&0x3f])
		if remaining > 1 {
			result.WriteByte(base64Chars[(n>>6)&0x3f])
		} else {
			result.WriteByte('=')
		}
		if remaining > 2 {
			result.WriteByte(base64Chars[n&0x3f])
		} else {
			result.WriteByte('=')
		}
	}

	return result.String()
}

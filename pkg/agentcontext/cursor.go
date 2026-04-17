// cursor.go -- Opaque pagination cursors for delta-style recall.
//
// Cursors are base64url-encoded strings of the form "<sessionID>|<nanoseconds>".
// They are opaque to callers but deterministic and collision-free for a given
// (session_id, updated_at_ns) pair. No HMAC is applied — these cursors are not
// authenticated, they simply carry enough state to resume polling.
package agentcontext

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cursorSeparator delimits session id from nanosecond timestamp inside a cursor.
const cursorSeparator = "|"

// EncodeCursor returns an opaque, base64url-encoded cursor representing the
// position "(sessionID, updatedAt)" in a recall stream. It never returns an
// error: callers that need a non-empty cursor should pass a non-zero time.
func EncodeCursor(sessionID string, updatedAt time.Time) string {
	raw := sessionID + cursorSeparator + strconv.FormatInt(updatedAt.UnixNano(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses a cursor previously produced by EncodeCursor. An empty
// input string decodes to (sessionID="", updatedAtNs=0, nil) — this represents
// "start from the beginning" and is a valid, cheap resume point.
//
// Malformed inputs return a non-nil error. The error is wrapped so callers can
// distinguish decode failures from other service errors.
func DecodeCursor(s string) (sessionID string, updatedAtNs int64, err error) {
	if s == "" {
		return "", 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", 0, fmt.Errorf("decode cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), cursorSeparator, 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("decode cursor: missing separator")
	}
	ns, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("decode cursor: parse nanos: %w", err)
	}
	return parts[0], ns, nil
}

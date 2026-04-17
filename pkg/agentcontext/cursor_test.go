package agentcontext

import (
	"strings"
	"testing"
	"time"
)

func TestCursor_RoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		sessionID string
		t         time.Time
	}{
		{"simple", "s1", time.Unix(1700000000, 123456789)},
		{"zero_time", "s2", time.Unix(0, 0)},
		{"session_with_dash", "sess-abc-123", time.Unix(1700000000, 0)},
		{"nanos_max", "s3", time.Unix(0, 999999999)},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cur := EncodeCursor(tc.sessionID, tc.t)
			if cur == "" {
				t.Fatal("expected non-empty cursor")
			}
			// Cursor must be base64url-safe: no '+', '/', '=' padding.
			if strings.ContainsAny(cur, "+/=") {
				t.Fatalf("cursor contains unsafe chars: %q", cur)
			}
			sid, ns, err := DecodeCursor(cur)
			if err != nil {
				t.Fatalf("DecodeCursor: %v", err)
			}
			if sid != tc.sessionID {
				t.Errorf("sessionID = %q, want %q", sid, tc.sessionID)
			}
			if ns != tc.t.UnixNano() {
				t.Errorf("nanos = %d, want %d", ns, tc.t.UnixNano())
			}
		})
	}
}

func TestCursor_EmptyDecodesToZero(t *testing.T) {
	t.Parallel()
	sid, ns, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("DecodeCursor(\"\"): %v", err)
	}
	if sid != "" {
		t.Errorf("sessionID = %q, want empty", sid)
	}
	if ns != 0 {
		t.Errorf("nanos = %d, want 0", ns)
	}
}

func TestCursor_RejectsMalformed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
	}{
		{"not_base64", "!!!not-valid-base64!!!"},
		{"base64_no_separator", "c2Vzc2lvbi1uby1zZXAtaGVyZQ"},    // "session-no-sep-here"
		{"base64_non_numeric_nanos", "c2Vzc3wtbm90LWEtbnVtYmVy"}, // "sess|-not-a-number"
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := DecodeCursor(tc.input)
			if err == nil {
				t.Fatalf("DecodeCursor(%q) expected error, got nil", tc.input)
			}
			if !strings.Contains(err.Error(), "decode cursor") {
				t.Errorf("error = %q, want prefix 'decode cursor'", err.Error())
			}
		})
	}
}

func TestCursor_Deterministic(t *testing.T) {
	t.Parallel()
	tm := time.Unix(1700000000, 42)
	a := EncodeCursor("s1", tm)
	b := EncodeCursor("s1", tm)
	if a != b {
		t.Errorf("EncodeCursor not deterministic: %q vs %q", a, b)
	}
}

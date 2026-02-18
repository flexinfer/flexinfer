package main

import (
	"testing"
	"time"
)

func TestHookStateFromSignals(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name               string
		lastHeartbeat      time.Time
		hasHeartbeat       bool
		heartbeatsInWindow int
		want               string
	}{
		{
			name:               "healthy when heartbeat is fresh",
			lastHeartbeat:      now.Add(-10 * time.Second),
			hasHeartbeat:       true,
			heartbeatsInWindow: 1,
			want:               "healthy",
		},
		{
			name:               "stale when heartbeat is recent but aging",
			lastHeartbeat:      now.Add(-2 * time.Minute),
			hasHeartbeat:       true,
			heartbeatsInWindow: 1,
			want:               "stale",
		},
		{
			name:               "missing when heartbeat is old",
			lastHeartbeat:      now.Add(-10 * time.Minute),
			hasHeartbeat:       true,
			heartbeatsInWindow: 0,
			want:               "missing",
		},
		{
			name:               "stale when only timeline events exist",
			hasHeartbeat:       false,
			heartbeatsInWindow: 2,
			want:               "stale",
		},
		{
			name:               "missing when no signals",
			hasHeartbeat:       false,
			heartbeatsInWindow: 0,
			want:               "missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hookStateFromSignals(now, tt.lastHeartbeat, tt.hasHeartbeat, tt.heartbeatsInWindow)
			if got != tt.want {
				t.Fatalf("hookStateFromSignals() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseHeartbeatTimestamp(t *testing.T) {
	rfc3339Nano := time.Now().UTC().Format(time.RFC3339Nano)
	if _, ok := parseHeartbeatTimestamp(rfc3339Nano); !ok {
		t.Fatal("expected RFC3339Nano timestamp to parse")
	}

	if _, ok := parseHeartbeatTimestamp("not-a-time"); ok {
		t.Fatal("expected invalid timestamp parse to fail")
	}
}

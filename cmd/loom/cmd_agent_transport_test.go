package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestIsTransientTransportError verifies the matcher recognises the dominant
// failure strings seen in loom-agent-hooks.log under heartbeat storms.
func TestIsTransientTransportError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantHit bool
	}{
		{"nil", nil, false},
		{
			name:    "local transport closed during recv",
			err:     errors.New(`agent tool agent_presence_heartbeat: daemon error (-32603): server unavailable: local: tools/call failed during recv: transport closed`),
			wantHit: true,
		},
		{
			name:    "hub websocket reset",
			err:     errors.New(`hub: tools/call failed during send: write message: use of closed network connection`),
			wantHit: true,
		},
		{
			name:    "recv timeout during tools/call",
			err:     errors.New(`local: tools/call timeout during recv after 30s (recoverable: daemon will reconnect upstream transport and retry on the next request): context deadline exceeded`),
			wantHit: true,
		},
		{
			name:    "auth failure - not transient",
			err:     errors.New(`HUD returned 401: unauthorized`),
			wantHit: false,
		},
		{
			name:    "unknown tool - not transient",
			err:     errors.New(`daemon error (-32602): unknown tool: agent_made_up`),
			wantHit: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientTransportError(tc.err); got != tc.wantHit {
				t.Errorf("isTransientTransportError(%q) = %v, want %v", tc.err, got, tc.wantHit)
			}
		})
	}
}

// TestWithAgentFallback_RetriesOnTransientDaemonError verifies that when HUD
// fails and the first daemon call returns a transient transport error, the
// second daemon call is invoked. Mirrors the heartbeat-storm scenario where
// a stale pool conn fails recv but the next pool.Get hands back a fresh
// connection.
func TestWithAgentFallback_RetriesOnTransientDaemonError(t *testing.T) {
	hudCalls := 0
	daemonCalls := 0
	hudCall := func() (json.RawMessage, error) {
		hudCalls++
		return nil, errors.New("HUD returned 502")
	}
	daemonCall := func() (json.RawMessage, error) {
		daemonCalls++
		if daemonCalls == 1 {
			return nil, errors.New("tools/call failed during recv: transport closed")
		}
		return json.RawMessage(`{"ok":true}`), nil
	}

	result, err := withAgentFallback("test op", hudCall, daemonCall)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if !strings.Contains(string(result), `"ok":true`) {
		t.Errorf("unexpected result: %s", result)
	}
	if hudCalls != 1 {
		t.Errorf("hudCalls = %d, want 1", hudCalls)
	}
	if daemonCalls != 2 {
		t.Errorf("daemonCalls = %d, want 2 (initial + retry)", daemonCalls)
	}
}

// TestWithAgentFallback_NoRetryOnNonTransientError verifies that a
// non-transient failure (e.g. auth error, unknown tool) doesn't trigger
// the retry path. Retrying those wastes time and confuses error messages.
func TestWithAgentFallback_NoRetryOnNonTransientError(t *testing.T) {
	daemonCalls := 0
	hudCall := func() (json.RawMessage, error) {
		return nil, errors.New("HUD returned 502")
	}
	daemonCall := func() (json.RawMessage, error) {
		daemonCalls++
		return nil, errors.New("daemon error (-32602): unknown tool: bogus")
	}

	_, err := withAgentFallback("test op", hudCall, daemonCall)
	if err == nil {
		t.Fatal("expected error")
	}
	if daemonCalls != 1 {
		t.Errorf("daemonCalls = %d, want 1 (no retry for non-transient error)", daemonCalls)
	}
}

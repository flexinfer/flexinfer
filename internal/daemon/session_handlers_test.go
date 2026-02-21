package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func newTestDaemonWithSessions(t *testing.T) *Daemon {
	t.Helper()
	logger := slog.Default()
	d := &Daemon{
		daemonEpoch: 1,
		logger:      logger,
	}
	d.sessions = NewSessionManager(100, 10*time.Minute, d.daemonEpoch, logger)
	return d
}

func TestHandleSessionOpen(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "loom/session/open",
	}
	msg.Params, _ = json.Marshal(map[string]any{
		"agent_hint": "claude-code",
		"host_pid":   "12345",
		"version":    "0.1.0",
	})

	resp, err := d.handleSessionOpen(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleSessionOpen error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %s", resp.Error.Message)
	}

	var result sessionOpenResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.SessionID == "" {
		t.Fatal("expected non-empty session_id")
	}
	if result.DaemonEpoch != 1 {
		t.Fatalf("expected daemon_epoch 1, got %d", result.DaemonEpoch)
	}
	if result.LeaseSecs <= 0 {
		t.Fatalf("expected positive lease_seconds, got %d", result.LeaseSecs)
	}

	// Verify session exists in manager.
	if d.sessions.Count() != 1 {
		t.Fatalf("expected 1 session, got %d", d.sessions.Count())
	}
}

func TestHandleSessionOpen_WithPriorID(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "loom/session/open",
	}
	msg.Params, _ = json.Marshal(map[string]any{
		"prior_session_id": "old-abc",
	})

	resp, err := d.handleSessionOpen(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleSessionOpen error: %v", err)
	}

	var result sessionOpenResult
	json.Unmarshal(resp.Result, &result)

	// Verify prior_id was stored.
	sess, ok := d.sessions.Get(result.SessionID)
	if !ok {
		t.Fatal("session not found")
	}
	if sess.PriorID != "old-abc" {
		t.Fatalf("expected PriorID 'old-abc', got %q", sess.PriorID)
	}
}

func TestHandleSessionHeartbeat_Success(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	// Open a session first.
	sess := d.sessions.Open(SessionClientInfo{}, "")

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "loom/session/heartbeat",
	}
	msg.Params, _ = json.Marshal(map[string]any{
		"session_id":   sess.ID,
		"daemon_epoch": int64(1),
	})

	resp, err := d.handleSessionHeartbeat(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleSessionHeartbeat error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result sessionHeartbeatResult
	json.Unmarshal(resp.Result, &result)
	if result.State != "active" {
		t.Fatalf("expected state active, got %s", result.State)
	}
}

func TestHandleSessionHeartbeat_EpochMismatch(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	sess := d.sessions.Open(SessionClientInfo{}, "")

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "loom/session/heartbeat",
	}
	msg.Params, _ = json.Marshal(map[string]any{
		"session_id":   sess.ID,
		"daemon_epoch": int64(999),
	})

	resp, err := d.handleSessionHeartbeat(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleSessionHeartbeat error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected epoch mismatch error")
	}
	if resp.Error.Code != mcp.InvalidRequest {
		t.Fatalf("expected InvalidRequest code, got %d", resp.Error.Code)
	}
}

func TestHandleSessionStatus(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	d.sessions.Open(SessionClientInfo{}, "")
	d.sessions.Open(SessionClientInfo{}, "")

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "loom/session/status",
	}

	resp, err := d.handleSessionStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleSessionStatus error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result sessionStatusResult
	json.Unmarshal(resp.Result, &result)
	if result.DaemonEpoch != 1 {
		t.Fatalf("expected epoch 1, got %d", result.DaemonEpoch)
	}
	if result.ActiveSessions != 2 {
		t.Fatalf("expected 2 active sessions, got %d", result.ActiveSessions)
	}
	if result.TotalSessions != 2 {
		t.Fatalf("expected 2 total sessions, got %d", result.TotalSessions)
	}
}

func TestHandleSessionClose(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	sess := d.sessions.Open(SessionClientInfo{}, "")

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "loom/session/close",
	}
	msg.Params, _ = json.Marshal(map[string]any{
		"session_id": sess.ID,
	})

	resp, err := d.handleSessionClose(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleSessionClose error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	var result sessionCloseResult
	json.Unmarshal(resp.Result, &result)
	if !result.Closed {
		t.Fatal("expected closed=true")
	}

	if d.sessions.Count() != 0 {
		t.Fatalf("expected 0 sessions after close, got %d", d.sessions.Count())
	}
}

func TestHandleSessionClose_MissingID(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "loom/session/close",
	}
	msg.Params, _ = json.Marshal(map[string]any{})

	resp, err := d.handleSessionClose(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleSessionClose error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing session_id")
	}
	if resp.Error.Code != mcp.InvalidParams {
		t.Fatalf("expected InvalidParams code, got %d", resp.Error.Code)
	}
}

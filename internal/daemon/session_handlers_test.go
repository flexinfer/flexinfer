package daemon

import (
	"context"
	"encoding/json"
	"fmt"
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

// --- Integration tests for session lease deferred items ---

func TestEpochMismatchRecovery(t *testing.T) {
	t.Parallel()
	logger := slog.Default()

	// Epoch 1 daemon: open a session.
	d1 := &Daemon{daemonEpoch: 1, logger: logger}
	d1.sessions = NewSessionManager(100, 10*time.Minute, 1, logger)

	openMsg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "loom/session/open",
	}
	openMsg.Params, _ = json.Marshal(map[string]any{"agent_hint": "test"})

	resp, err := d1.handleSessionOpen(context.Background(), openMsg)
	if err != nil {
		t.Fatalf("open error: %v", err)
	}
	var openResult sessionOpenResult
	json.Unmarshal(resp.Result, &openResult)
	oldSessionID := openResult.SessionID

	// Simulate daemon restart: new manager with epoch 2.
	d2 := &Daemon{daemonEpoch: 2, logger: logger}
	d2.sessions = NewSessionManager(100, 10*time.Minute, 2, logger)

	// Heartbeat with old epoch → mismatch error.
	hbMsg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "loom/session/heartbeat",
	}
	hbMsg.Params, _ = json.Marshal(map[string]any{
		"session_id":   oldSessionID,
		"daemon_epoch": int64(1),
	})

	resp, err = d2.handleSessionHeartbeat(context.Background(), hbMsg)
	if err != nil {
		t.Fatalf("heartbeat error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected epoch mismatch error on heartbeat")
	}

	// Re-open with prior_session_id succeeds on new daemon.
	reopenMsg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "loom/session/open",
	}
	reopenMsg.Params, _ = json.Marshal(map[string]any{
		"agent_hint":       "test",
		"prior_session_id": oldSessionID,
	})

	resp, err = d2.handleSessionOpen(context.Background(), reopenMsg)
	if err != nil {
		t.Fatalf("reopen error: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error on reopen: %s", resp.Error.Message)
	}

	var reopenResult sessionOpenResult
	json.Unmarshal(resp.Result, &reopenResult)
	if reopenResult.DaemonEpoch != 2 {
		t.Fatalf("expected epoch 2, got %d", reopenResult.DaemonEpoch)
	}

	// Verify PriorID was stored.
	sess, ok := d2.sessions.Get(reopenResult.SessionID)
	if !ok {
		t.Fatal("session not found after reopen")
	}
	if sess.PriorID != oldSessionID {
		t.Fatalf("expected PriorID %q, got %q", oldSessionID, sess.PriorID)
	}
}

func TestDrainRejection_SessionStatus(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)
	d.sessions.Open(SessionClientInfo{}, "")

	// Before drain: status shows "none".
	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "loom/session/status",
	}
	resp, err := d.handleSessionStatus(context.Background(), msg)
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	var result sessionStatusResult
	json.Unmarshal(resp.Result, &result)
	if result.DrainState != "none" {
		t.Fatalf("expected drain_state 'none' before drain, got %q", result.DrainState)
	}

	// Set draining.
	d.SetDraining()

	// After drain: status shows "draining".
	msg2 := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "loom/session/status",
	}
	resp, err = d.handleSessionStatus(context.Background(), msg2)
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	json.Unmarshal(resp.Result, &result)
	if result.DrainState != "draining" {
		t.Fatalf("expected drain_state 'draining', got %q", result.DrainState)
	}
}

func TestDrainRejection_DaemonFlag(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	// Not draining initially.
	if d.IsDraining() {
		t.Fatal("expected IsDraining=false initially")
	}

	// Open a session so SetDraining has something to drain.
	d.sessions.Open(SessionClientInfo{}, "")
	if d.sessions.ActiveCount() != 1 {
		t.Fatalf("expected 1 active session, got %d", d.sessions.ActiveCount())
	}

	// Set draining.
	d.SetDraining()

	if !d.IsDraining() {
		t.Fatal("expected IsDraining=true after SetDraining")
	}

	// Sessions should be drained.
	if d.sessions.ActiveCount() != 0 {
		t.Fatalf("expected 0 active sessions after drain, got %d", d.sessions.ActiveCount())
	}

	// IsDraining on session manager should also report true.
	if !d.sessions.IsDraining() {
		t.Fatal("expected session manager IsDraining=true")
	}
}

func TestDrainRejection_CallGate(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)
	d.SetDraining()

	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
	}
	msg.Params, _ = json.Marshal(map[string]any{
		"name": "test__tool",
	})

	resp, err := d.handleCallWithOptions(context.Background(), msg, true)
	if err != nil {
		t.Fatalf("handleCall error: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error during drain")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Fatalf("expected InternalError code, got %d", resp.Error.Code)
	}

	// Verify pipeline error data is retryable.
	data, ok := resp.Error.Data.(*PipelineErrorData)
	if !ok {
		t.Fatalf("Data type = %T, want *PipelineErrorData", resp.Error.Data)
	}
	if data.Code != "DAEMON_DRAINING" {
		t.Fatalf("expected code DAEMON_DRAINING, got %q", data.Code)
	}
	if !data.Retryable {
		t.Fatal("expected retryable=true")
	}
}

func TestDrainRejection_ConcurrentCallsReturnQuickly(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)
	d.SetDraining()

	start := time.Now()
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			msg := &mcp.Message{
				JSONRPC: "2.0",
				ID:      json.RawMessage(`1`),
				Method:  "tools/call",
			}
			msg.Params, _ = json.Marshal(map[string]any{
				"name": "test__tool",
			})
			resp, err := d.handleCallWithOptions(context.Background(), msg, true)
			if err != nil {
				errs <- err
				return
			}
			if resp.Error == nil {
				errs <- fmt.Errorf("call %d: expected error during drain", id)
				return
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("concurrent drain rejections took %v, expected <100ms", elapsed)
	}
}

func TestSessionSurvivesViaPriorSessionID(t *testing.T) {
	t.Parallel()
	d := newTestDaemonWithSessions(t)

	// Open session.
	openMsg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "loom/session/open",
	}
	openMsg.Params, _ = json.Marshal(map[string]any{"agent_hint": "test"})
	resp, _ := d.handleSessionOpen(context.Background(), openMsg)
	var openResult sessionOpenResult
	json.Unmarshal(resp.Result, &openResult)
	firstID := openResult.SessionID

	// Close it.
	closeMsg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "loom/session/close",
	}
	closeMsg.Params, _ = json.Marshal(map[string]any{"session_id": firstID})
	d.handleSessionClose(context.Background(), closeMsg)

	// Re-open with prior_session_id.
	reopenMsg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "loom/session/open",
	}
	reopenMsg.Params, _ = json.Marshal(map[string]any{
		"prior_session_id": firstID,
	})
	resp, _ = d.handleSessionOpen(context.Background(), reopenMsg)
	var reopenResult sessionOpenResult
	json.Unmarshal(resp.Result, &reopenResult)

	if reopenResult.SessionID == firstID {
		t.Fatal("expected new session ID, got same as first")
	}

	sess, ok := d.sessions.Get(reopenResult.SessionID)
	if !ok {
		t.Fatal("new session not found")
	}
	if sess.PriorID != firstID {
		t.Fatalf("expected PriorID %q, got %q", firstID, sess.PriorID)
	}
}

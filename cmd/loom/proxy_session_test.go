package main

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// sessionStubTransport records calls and returns configurable responses.
type sessionStubTransport struct {
	sentMessages []*mcp.Message
	recvQueue    []*mcp.Message
	recvIndex    int
	sendErr      error
	recvErr      error
}

func (s *sessionStubTransport) Send(_ context.Context, msg *mcp.Message) error {
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sentMessages = append(s.sentMessages, msg)
	return nil
}

func (s *sessionStubTransport) Recv(_ context.Context) (*mcp.Message, error) {
	if s.recvErr != nil {
		return nil, s.recvErr
	}
	if s.recvIndex < len(s.recvQueue) {
		msg := s.recvQueue[s.recvIndex]
		s.recvIndex++
		return msg, nil
	}
	return &mcp.Message{JSONRPC: "2.0"}, nil
}

func (s *sessionStubTransport) Close() error {
	return nil
}

type serializedSessionTransport struct {
	sessionStubTransport
	recvEntered         chan struct{}
	releaseRecv         chan struct{}
	firstRecvHook       sync.Once
	sendDuringRecvCount atomic.Int32
	recvActive          atomic.Bool
}

func (s *serializedSessionTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if s.recvActive.Load() {
		s.sendDuringRecvCount.Add(1)
	}
	return s.sessionStubTransport.Send(ctx, msg)
}

func (s *serializedSessionTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	s.recvActive.Store(true)
	s.firstRecvHook.Do(func() {
		close(s.recvEntered)
	})
	select {
	case <-s.releaseRecv:
	case <-ctx.Done():
		s.recvActive.Store(false)
		return nil, ctx.Err()
	}
	resp, err := s.sessionStubTransport.Recv(ctx)
	s.recvActive.Store(false)
	return resp, err
}

func TestProxyOpenSession_Success(t *testing.T) {
	// Reset global state for this test.
	oldSessionID := proxySessionID
	oldEpoch := proxyDaemonEpoch
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxyDaemonEpoch = oldEpoch
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = ""
	proxyDaemonEpoch = 0
	proxySessionDisabled = false

	result, _ := json.Marshal(map[string]any{
		"session_id":    "test-sess-abc",
		"daemon_epoch":  int64(1),
		"lease_seconds": 1800,
	})
	resp, _ := mcp.NewResponse(json.RawMessage(`99`), json.RawMessage(result))

	transport := &sessionStubTransport{
		recvQueue: []*mcp.Message{resp},
	}

	proxyOpenSession(context.Background(), transport)

	if proxySessionID != "test-sess-abc" {
		t.Fatalf("expected proxySessionID 'test-sess-abc', got %q", proxySessionID)
	}
	if proxyDaemonEpoch != 1 {
		t.Fatalf("expected proxyDaemonEpoch 1, got %d", proxyDaemonEpoch)
	}

	// Verify the request was sent.
	if len(transport.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(transport.sentMessages))
	}
	if transport.sentMessages[0].Method != "loom/session/open" {
		t.Fatalf("expected method loom/session/open, got %q", transport.sentMessages[0].Method)
	}
}

func TestProxyOpenSession_MethodNotFound(t *testing.T) {
	// Older daemon returns method_not_found -- should be silently ignored.
	oldSessionID := proxySessionID
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = ""
	proxySessionDisabled = false

	errResp := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`99`),
		Error: &mcp.Error{
			Code:    mcp.MethodNotFound,
			Message: "unknown method: loom/session/open",
		},
	}

	transport := &sessionStubTransport{
		recvQueue: []*mcp.Message{errResp},
	}

	proxyOpenSession(context.Background(), transport)

	// Session ID should remain empty (graceful fallback).
	if proxySessionID != "" {
		t.Fatalf("expected empty proxySessionID on method_not_found, got %q", proxySessionID)
	}
}

func TestProxyOpenSession_Disabled(t *testing.T) {
	oldDisabled := proxySessionDisabled
	defer func() { proxySessionDisabled = oldDisabled }()

	proxySessionDisabled = true

	transport := &sessionStubTransport{}

	proxyOpenSession(context.Background(), transport)

	// No messages should be sent when disabled.
	if len(transport.sentMessages) != 0 {
		t.Fatalf("expected 0 sent messages when disabled, got %d", len(transport.sentMessages))
	}
}

func TestProxyOpenSession_SendTimeout(t *testing.T) {
	// Send failure should not panic or set session state.
	oldSessionID := proxySessionID
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = ""
	proxySessionDisabled = false

	transport := &sessionStubTransport{
		sendErr: context.DeadlineExceeded,
	}

	proxyOpenSession(context.Background(), transport)

	if proxySessionID != "" {
		t.Fatalf("expected empty proxySessionID on send failure, got %q", proxySessionID)
	}
}

// --- Session heartbeat tests ---

func TestProxySessionHeartbeat_ExtendsLease(t *testing.T) {
	oldSessionID := proxySessionID
	oldEpoch := proxyDaemonEpoch
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxyDaemonEpoch = oldEpoch
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = "sess-heartbeat-1"
	proxyDaemonEpoch = 1
	proxySessionDisabled = false

	// Daemon responds with success (no error).
	resp, _ := mcp.NewResponse(json.RawMessage(`97`), json.RawMessage(`{"ok":true}`))
	transport := &sessionStubTransport{
		recvQueue: []*mcp.Message{resp},
	}

	proxySessionHeartbeat(context.Background(), transport)

	// Session ID should be preserved after successful heartbeat.
	if proxySessionID != "sess-heartbeat-1" {
		t.Fatalf("expected proxySessionID preserved, got %q", proxySessionID)
	}

	// Verify the heartbeat request was sent with correct params.
	if len(transport.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(transport.sentMessages))
	}
	if transport.sentMessages[0].Method != "loom/session/heartbeat" {
		t.Fatalf("expected method loom/session/heartbeat, got %q", transport.sentMessages[0].Method)
	}
	var params map[string]any
	json.Unmarshal(transport.sentMessages[0].Params, &params)
	if params["session_id"] != "sess-heartbeat-1" {
		t.Fatalf("expected session_id 'sess-heartbeat-1', got %v", params["session_id"])
	}
}

func TestProxySessionHeartbeat_Rejected(t *testing.T) {
	oldSessionID := proxySessionID
	oldEpoch := proxyDaemonEpoch
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxyDaemonEpoch = oldEpoch
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = "sess-expired"
	proxyDaemonEpoch = 1
	proxySessionDisabled = false

	// Daemon responds with error (expired session).
	errResp := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`97`),
		Error: &mcp.Error{
			Code:    mcp.InvalidRequest,
			Message: "session not found or expired",
		},
	}
	transport := &sessionStubTransport{
		recvQueue: []*mcp.Message{errResp},
	}

	proxySessionHeartbeat(context.Background(), transport)

	// Session ID should be cleared so next request re-opens.
	if proxySessionID != "" {
		t.Fatalf("expected proxySessionID cleared on rejection, got %q", proxySessionID)
	}
}

func TestProxySessionHeartbeat_Disabled(t *testing.T) {
	oldSessionID := proxySessionID
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = "sess-123"
	proxySessionDisabled = true

	transport := &sessionStubTransport{}

	proxySessionHeartbeat(context.Background(), transport)

	// No messages should be sent when disabled.
	if len(transport.sentMessages) != 0 {
		t.Fatalf("expected 0 sent messages when disabled, got %d", len(transport.sentMessages))
	}
}

func TestProxySessionHeartbeat_NoSession(t *testing.T) {
	oldSessionID := proxySessionID
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = ""
	proxySessionDisabled = false

	transport := &sessionStubTransport{}

	proxySessionHeartbeat(context.Background(), transport)

	// No messages should be sent when no session is active.
	if len(transport.sentMessages) != 0 {
		t.Fatalf("expected 0 sent messages with no session, got %d", len(transport.sentMessages))
	}
}

func TestProxyOpenSession_PreservesPriorID(t *testing.T) {
	// When proxySessionID is already set, it should be sent as prior_session_id.
	oldSessionID := proxySessionID
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = "prior-123"
	proxySessionDisabled = false

	result, _ := json.Marshal(map[string]any{
		"session_id":   "new-sess-456",
		"daemon_epoch": int64(2),
	})
	resp, _ := mcp.NewResponse(json.RawMessage(`99`), json.RawMessage(result))

	transport := &sessionStubTransport{
		recvQueue: []*mcp.Message{resp},
	}

	proxyOpenSession(context.Background(), transport)

	if proxySessionID != "new-sess-456" {
		t.Fatalf("expected proxySessionID 'new-sess-456', got %q", proxySessionID)
	}

	// Verify prior_session_id was sent in the request params.
	if len(transport.sentMessages) != 1 {
		t.Fatal("expected 1 sent message")
	}
	var params map[string]any
	json.Unmarshal(transport.sentMessages[0].Params, &params)
	if params["prior_session_id"] != "prior-123" {
		t.Fatalf("expected prior_session_id 'prior-123', got %v", params["prior_session_id"])
	}
}

func TestProxySessionLifecycleRPCsSerializeWithForegroundCalls(t *testing.T) {
	oldSessionID := proxySessionID
	oldEpoch := proxyDaemonEpoch
	oldDisabled := proxySessionDisabled
	defer func() {
		proxySessionID = oldSessionID
		proxyDaemonEpoch = oldEpoch
		proxySessionDisabled = oldDisabled
	}()

	proxySessionID = ""
	proxyDaemonEpoch = 0
	proxySessionDisabled = false

	openResult, _ := json.Marshal(map[string]any{
		"session_id":   "sess-serial",
		"daemon_epoch": int64(1),
	})
	openResp, _ := mcp.NewResponse(json.RawMessage(`99`), json.RawMessage(openResult))
	toolResp, _ := mcp.NewResponse(json.RawMessage(`1`), json.RawMessage(`{"ok":true}`))

	transport := &serializedSessionTransport{
		sessionStubTransport: sessionStubTransport{
			recvQueue: []*mcp.Message{openResp, toolResp},
		},
		recvEntered: make(chan struct{}),
		releaseRecv: make(chan struct{}),
	}

	errCh := make(chan error, 1)
	go func() {
		proxyOpenSession(context.Background(), transport)
		errCh <- nil
	}()

	<-transport.recvEntered

	respCh := make(chan *mcp.Message, 1)
	go func() {
		req, _ := mcp.NewRequest(1, "loom/status", nil)
		resp, err := proxyDaemonRoundTrip(context.Background(), transport, req, "loom/status")
		if err != nil {
			t.Errorf("proxyDaemonRoundTrip error: %v", err)
			respCh <- nil
			return
		}
		respCh <- resp
	}()

	time.Sleep(50 * time.Millisecond)
	if got := transport.sendDuringRecvCount.Load(); got != 0 {
		t.Fatalf("expected no concurrent send during recv, got %d", got)
	}

	close(transport.releaseRecv)

	if err := <-errCh; err != nil {
		t.Fatalf("proxyOpenSession returned error: %v", err)
	}
	if got := <-respCh; got == nil {
		t.Fatal("expected foreground round trip response")
	}
	if transport.sendDuringRecvCount.Load() != 0 {
		t.Fatalf("expected serialized daemon RPCs, got %d concurrent sends during recv", transport.sendDuringRecvCount.Load())
	}
	if proxySessionID != "sess-serial" {
		t.Fatalf("expected proxySessionID to be set from open response, got %q", proxySessionID)
	}
}

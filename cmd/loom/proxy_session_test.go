package main

import (
	"context"
	"encoding/json"
	"testing"

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

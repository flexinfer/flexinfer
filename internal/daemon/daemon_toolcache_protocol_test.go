package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type recvStep struct {
	msg *mcp.Message
	err error
}

type scriptedTransport struct {
	sent      []*mcp.Message
	recvSteps []recvStep
	recvIndex int
}

func (s *scriptedTransport) Send(_ context.Context, msg *mcp.Message) error {
	s.sent = append(s.sent, msg)
	return nil
}

func (s *scriptedTransport) Recv(_ context.Context) (*mcp.Message, error) {
	if s.recvIndex >= len(s.recvSteps) {
		return &mcp.Message{JSONRPC: mcp.JSONRPCVersion}, nil
	}
	step := s.recvSteps[s.recvIndex]
	s.recvIndex++
	if step.err != nil {
		return nil, step.err
	}
	return step.msg, nil
}

func (s *scriptedTransport) Close() error { return nil }

func TestInitializeMCPTransport_FallsBackToLegacyProtocol(t *testing.T) {
	transport := &scriptedTransport{
		recvSteps: []recvStep{
			{
				msg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					Error:   &mcp.Error{Code: mcp.InvalidParams, Message: "unsupported protocol"},
				},
			},
			{
				msg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					Result:  json.RawMessage(`{}`),
				},
			},
		},
	}

	if err := initializeMCPTransport(context.Background(), transport); err != nil {
		t.Fatalf("initializeMCPTransport() error = %v", err)
	}

	if len(transport.sent) != 3 {
		t.Fatalf("sent message count = %d, want 3", len(transport.sent))
	}
	if transport.sent[0].Method != "initialize" {
		t.Fatalf("first message method = %q, want initialize", transport.sent[0].Method)
	}
	if transport.sent[1].Method != "initialize" {
		t.Fatalf("second message method = %q, want initialize", transport.sent[1].Method)
	}
	if transport.sent[2].Method != "notifications/initialized" {
		t.Fatalf("third message method = %q, want notifications/initialized", transport.sent[2].Method)
	}

	firstVersion := initProtocolVersion(t, transport.sent[0])
	secondVersion := initProtocolVersion(t, transport.sent[1])
	if firstVersion != mcp.ProtocolVersion20250618 {
		t.Fatalf("first init protocol = %q, want %q", firstVersion, mcp.ProtocolVersion20250618)
	}
	if secondVersion != mcp.ProtocolVersion {
		t.Fatalf("second init protocol = %q, want %q", secondVersion, mcp.ProtocolVersion)
	}
}

func TestInitializeMCPTransport_ReturnsLastProtocolError(t *testing.T) {
	transport := &scriptedTransport{
		recvSteps: []recvStep{
			{
				msg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					Error:   &mcp.Error{Code: mcp.InvalidParams, Message: "unsupported 2025-06-18"},
				},
			},
			{
				msg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					Error:   &mcp.Error{Code: mcp.InvalidParams, Message: "unsupported 2024-11-05"},
				},
			},
		},
	}

	err := initializeMCPTransport(context.Background(), transport)
	if err == nil {
		t.Fatal("expected error when all protocol attempts fail")
	}
	if !strings.Contains(err.Error(), "init error (2024-11-05)") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(transport.sent) != 2 {
		t.Fatalf("sent message count = %d, want 2", len(transport.sent))
	}
}

func initProtocolVersion(t *testing.T, msg *mcp.Message) string {
	t.Helper()
	var p mcp.InitializeParams
	if err := json.Unmarshal(msg.Params, &p); err != nil {
		t.Fatalf("unmarshal initialize params: %v", err)
	}
	return p.ProtocolVersion
}

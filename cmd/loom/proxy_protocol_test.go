package main

import (
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestNegotiateProxyProtocolVersion(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "defaults to modern when params missing",
			raw:  nil,
			want: mcp.ProtocolVersion20250618,
		},
		{
			name: "accepts modern protocol request",
			raw:  json.RawMessage(`{"protocolVersion":"2025-06-18"}`),
			want: mcp.ProtocolVersion20250618,
		},
		{
			name: "accepts legacy protocol request",
			raw:  json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
			want: mcp.ProtocolVersion,
		},
		{
			name: "falls back to modern for unknown protocol",
			raw:  json.RawMessage(`{"protocolVersion":"future"}`),
			want: mcp.ProtocolVersion20250618,
		},
		{
			name: "falls back to modern for invalid params",
			raw:  json.RawMessage(`{"protocolVersion"`),
			want: mcp.ProtocolVersion20250618,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := negotiateProxyProtocolVersion(tt.raw); got != tt.want {
				t.Fatalf("negotiateProxyProtocolVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHandleProxyInitialize_UsesNegotiatedProtocolVersion(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "legacy requested",
			raw:  json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
			want: mcp.ProtocolVersion,
		},
		{
			name: "unknown requested defaults modern",
			raw:  json.RawMessage(`{"protocolVersion":"future"}`),
			want: mcp.ProtocolVersion20250618,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &mcp.Message{
				JSONRPC: mcp.JSONRPCVersion,
				ID:      1,
				Method:  "initialize",
				Params:  tt.raw,
			}
			resp := handleProxyInitialize(msg)

			var result mcp.InitializeResult
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				t.Fatalf("unmarshal initialize result: %v", err)
			}
			if result.ProtocolVersion != tt.want {
				t.Fatalf("handleProxyInitialize() protocol = %q, want %q", result.ProtocolVersion, tt.want)
			}
		})
	}
}

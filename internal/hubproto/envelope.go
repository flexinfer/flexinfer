// Package hubproto defines the domain-multiplexed message envelope for the
// daemon-hub WebSocket channel. It allows the daemon to multiplex spawn
// commands, devbox operations, agent-context calls, and control messages
// over a single hub WebSocket connection.
package hubproto

import (
	"encoding/json"
	"time"
)

// Domain identifies the message domain for routing purposes.
type Domain string

const (
	// DomainMCP routes standard MCP JSON-RPC messages.
	DomainMCP Domain = "mcp"
	// DomainSpawn routes spawn-related commands.
	DomainSpawn Domain = "spawn"
	// DomainDevbox routes devbox operations.
	DomainDevbox Domain = "devbox"
	// DomainAgent routes agent-context calls.
	DomainAgent Domain = "agent"
	// DomainControl routes control messages (ping, pong, shutdown, etc.).
	DomainControl Domain = "control"
)

// Envelope is the top-level wire format for all messages on the hub WebSocket.
// Every message is wrapped in an Envelope so the receiver can route it to the
// correct domain handler without parsing the inner payload first.
type Envelope struct {
	// Domain identifies which subsystem should handle this message.
	Domain Domain `json:"domain"`
	// Method is the operation within the domain (e.g. "tools/call" for MCP,
	// "ping" for control).
	Method string `json:"method"`
	// RequestID correlates requests with responses. Empty for notifications.
	RequestID string `json:"request_id,omitempty"`
	// Payload is the domain-specific message body, kept as raw JSON to avoid
	// double-deserialization.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Source identifies the sender (e.g. "daemon", "hub", a session ID).
	Source string `json:"source,omitempty"`
	// Timestamp records when the envelope was created.
	Timestamp time.Time `json:"timestamp"`
}

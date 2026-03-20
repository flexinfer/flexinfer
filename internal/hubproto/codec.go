package hubproto

import (
	"encoding/json"
	"fmt"
	"time"
)

// WrapMCP wraps a raw MCP JSON-RPC message into an Envelope with DomainMCP.
func WrapMCP(msg json.RawMessage, source string) *Envelope {
	return &Envelope{
		Domain:    DomainMCP,
		Method:    "mcp",
		Payload:   msg,
		Source:    source,
		Timestamp: time.Now().UTC(),
	}
}

// UnwrapMCP extracts the raw MCP JSON-RPC payload from an Envelope.
// It returns an error if the envelope domain is not DomainMCP.
func UnwrapMCP(env *Envelope) (json.RawMessage, error) {
	if env == nil {
		return nil, fmt.Errorf("hubproto: cannot unwrap nil envelope")
	}
	if env.Domain != DomainMCP {
		return nil, fmt.Errorf("hubproto: expected domain %q, got %q", DomainMCP, env.Domain)
	}
	return env.Payload, nil
}

// Encode serializes an Envelope to JSON bytes.
func Encode(env *Envelope) ([]byte, error) {
	if env == nil {
		return nil, fmt.Errorf("hubproto: cannot encode nil envelope")
	}
	return json.Marshal(env)
}

// Decode deserializes JSON bytes into an Envelope.
func Decode(data []byte) (*Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("hubproto: decode: %w", err)
	}
	return &env, nil
}

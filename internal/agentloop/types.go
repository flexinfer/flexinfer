package agentloop

import (
	"context"
	"encoding/json"
)

// Roles used on the wire. The append-only loop only ever produces these.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one OpenAI-compatible chat message. The tool-calling fields
// follow the function-calling schema: an assistant turn may carry ToolCalls;
// a tool turn carries the matching ToolCallID plus the tool's result in
// Content.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a single function call the model requested.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall carries the requested tool name and its arguments. Per the
// OpenAI schema Arguments is a JSON-encoded string, not an object.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef is the schema advertised to the model in the request's `tools`
// array. It is part of the immutable prefix — adding or reordering tools
// between turns busts the prefix cache, so a session fixes its tool set up
// front.
type ToolDef struct {
	Type     string          `json:"type"`
	Function ToolFunctionDef `json:"function"`
}

// ToolFunctionDef is the function half of a ToolDef. Parameters is a raw
// JSON Schema object.
type ToolFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Tool is a real, invokable capability. Invoke receives the raw JSON
// arguments string the model produced and returns the textual result that
// is appended to the conversation as a tool message.
type Tool interface {
	Definition() ToolDef
	Invoke(ctx context.Context, arguments string) (string, error)
}

// FunctionTool is a ToolDef plus an invoke func — the easy way to build a
// Tool without a dedicated struct.
type FunctionTool struct {
	Def ToolDef
	Fn  func(ctx context.Context, arguments string) (string, error)
}

// Definition implements Tool.
func (f FunctionTool) Definition() ToolDef { return f.Def }

// Invoke implements Tool.
func (f FunctionTool) Invoke(ctx context.Context, arguments string) (string, error) {
	return f.Fn(ctx, arguments)
}

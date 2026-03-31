package openairesponses

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ContextMode defines how the next Responses turn receives prior context.
type ContextMode string

const (
	ContextModeChain        ContextMode = "chain"
	ContextModeConversation ContextMode = "conversation"
	ContextModeStateless    ContextMode = "stateless"
)

// ParseContextMode normalizes and validates the requested context mode.
func ParseContextMode(raw string) (ContextMode, error) {
	mode := ContextMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode == "" {
		mode = ContextModeChain
	}
	if err := mode.Validate(); err != nil {
		return "", err
	}
	return mode, nil
}

// Validate ensures the context mode is one of the supported values.
func (m ContextMode) Validate() error {
	switch m {
	case ContextModeChain, ContextModeConversation, ContextModeStateless:
		return nil
	default:
		return fmt.Errorf("invalid context mode %q (expected chain|conversation|stateless)", m)
	}
}

// ContextStrategy tracks mode and state identifiers for a turn.
type ContextStrategy struct {
	Mode               ContextMode
	PreviousResponseID string
	ConversationID     string
}

// Validate enforces compatibility rules for context state.
func (s ContextStrategy) Validate() error {
	if s.Mode == "" {
		s.Mode = ContextModeChain
	}
	if err := s.Mode.Validate(); err != nil {
		return err
	}

	prev := strings.TrimSpace(s.PreviousResponseID)
	conv := strings.TrimSpace(s.ConversationID)

	if prev != "" && conv != "" {
		return fmt.Errorf("previous_response_id and conversation are mutually exclusive")
	}

	switch s.Mode {
	case ContextModeConversation:
		if conv == "" {
			return fmt.Errorf("conversation mode requires conversation id")
		}
	case ContextModeStateless:
		if prev != "" || conv != "" {
			return fmt.Errorf("stateless mode cannot set previous_response_id or conversation")
		}
	}
	return nil
}

// ToolDefinition is a Responses-compatible description of a Loom tool.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
	Strict      bool
	Server      string
	Tool        string
}

// ToolCall represents a tool call requested by the model.
type ToolCall struct {
	CallID     string
	ToolName   string
	Arguments  json.RawMessage
	RawPayload json.RawMessage
}

// ToolResult is the normalized result returned to the loop.
type ToolResult struct {
	CallID     string
	Output     any
	IsError    bool
	ErrorText  string
	RawPayload json.RawMessage
}

// ExecutionIdentity carries actor metadata for policy and audit.
type ExecutionIdentity struct {
	AgentID   string
	SessionID string
	Namespace string
}

// TurnRequest defines one orchestration turn request.
type TurnRequest struct {
	Model   string
	Input   any
	Tools   []ToolDefinition
	Context ContextStrategy
	Meta    map[string]string
}

// Validate checks required turn request fields.
func (r TurnRequest) Validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return fmt.Errorf("model is required")
	}
	return r.Context.Validate()
}

// TurnResponse defines the normalized orchestration output for one loop step.
type TurnResponse struct {
	ResponseID       string
	ConversationID   string
	OutputText       string
	ToolCalls        []ToolCall
	Terminal         bool
	RawPayload       json.RawMessage
	PromptTokens     int
	CompletionTokens int
}

// ToolExecutor executes resolved Loom tool calls.
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, call ToolCall, identity ExecutionIdentity) (ToolResult, error)
}

// ResponsesClient sends turns to the Responses API.
type ResponsesClient interface {
	Create(ctx context.Context, req TurnRequest) (TurnResponse, error)
}

// ToolAdapter maps Loom inventory and calls into Responses tool forms.
type ToolAdapter interface {
	BuildTools(ctx context.Context, identity ExecutionIdentity) ([]ToolDefinition, error)
	ResolveCall(ctx context.Context, call ToolCall) (ToolCall, error)
}

// TelemetrySink receives orchestration loop telemetry events.
type TelemetrySink interface {
	RecordTurnStart(ctx context.Context, req TurnRequest, identity ExecutionIdentity)
	RecordTurnEnd(ctx context.Context, resp TurnResponse, err error, identity ExecutionIdentity)
	RecordToolCall(ctx context.Context, call ToolCall, result ToolResult, err error, identity ExecutionIdentity)
}

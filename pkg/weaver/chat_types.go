package weaver

// chatCompletionRequestWithTools extends the base ChatCompletionRequest
// with function-calling fields for tool use.
type chatCompletionRequestWithTools struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Tools       []chatTool    `json:"tools,omitempty"`
	ToolChoice  any           `json:"tool_choice,omitempty"`
}

// chatMessage is the wire format for chat messages, extended with tool_calls
// and tool_call_id for function calling.
type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// chatTool describes one tool available to the model.
type chatTool struct {
	Type     string       `json:"type"` // "function"
	Function chatFunction `json:"function"`
}

// chatFunction defines a callable function with JSON Schema parameters.
type chatFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// chatToolCall represents a tool call in the assistant's response.
type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function chatFunctionCall `json:"function"`
}

// chatFunctionCall carries the function name and serialized arguments.
type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// chatCompletionResponseWithTools extends the base response to include
// tool_calls in choices.
type chatCompletionResponseWithTools struct {
	ID      string                          `json:"id"`
	Object  string                          `json:"object"`
	Created int64                           `json:"created"`
	Model   string                          `json:"model"`
	Choices []chatCompletionChoiceWithTools `json:"choices"`
	Usage   chatCompletionUsage             `json:"usage"`
}

// chatCompletionChoiceWithTools is a choice that may contain tool calls.
type chatCompletionChoiceWithTools struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// chatCompletionUsage mirrors the standard usage block.
type chatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

package openairesponses

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeResponsesClient struct {
	reqs      []TurnRequest
	responses []TurnResponse
	err       error
}

func (f *fakeResponsesClient) Create(_ context.Context, req TurnRequest) (TurnResponse, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return TurnResponse{}, f.err
	}
	if len(f.responses) == 0 {
		return TurnResponse{Terminal: true}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

type fakeExecutor struct {
	results map[string]ToolResult
	err     error
	calls   []ToolCall
}

func (f *fakeExecutor) ExecuteTool(_ context.Context, call ToolCall, _ ExecutionIdentity) (ToolResult, error) {
	f.calls = append(f.calls, call)
	if f.err != nil {
		return ToolResult{}, f.err
	}
	if res, ok := f.results[call.ToolName]; ok {
		return res, nil
	}
	return ToolResult{CallID: call.CallID, Output: map[string]any{"ok": true}}, nil
}

type fakeAdapter struct {
	tools       []ToolDefinition
	buildErr    error
	resolveErr  error
	resolveCall []ToolCall
}

func (f *fakeAdapter) BuildTools(_ context.Context, _ ExecutionIdentity) ([]ToolDefinition, error) {
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	return f.tools, nil
}

func (f *fakeAdapter) ResolveCall(_ context.Context, call ToolCall) (ToolCall, error) {
	f.resolveCall = append(f.resolveCall, call)
	if f.resolveErr != nil {
		return ToolCall{}, f.resolveErr
	}
	return call, nil
}

type fakeTelemetry struct {
	turnStarts int
	turnEnds   int
	toolCalls  int
}

func (f *fakeTelemetry) RecordTurnStart(_ context.Context, _ TurnRequest, _ ExecutionIdentity) {
	f.turnStarts++
}

func (f *fakeTelemetry) RecordTurnEnd(_ context.Context, _ TurnResponse, _ error, _ ExecutionIdentity) {
	f.turnEnds++
}

func (f *fakeTelemetry) RecordToolCall(_ context.Context, _ ToolCall, _ ToolResult, _ error, _ ExecutionIdentity) {
	f.toolCalls++
}

func enabledConfig() Config {
	return Config{
		Enabled:           true,
		RequestTimeout:    10 * time.Second,
		MaxLoopIterations: 4,
	}
}

func TestOrchestratorRun_RequiresFeatureGate(t *testing.T) {
	orch := Orchestrator{
		Config: Config{
			Enabled:           false,
			RequestTimeout:    time.Second,
			MaxLoopIterations: 1,
		},
		Client: &fakeResponsesClient{},
	}
	_, err := orch.Run(context.Background(), TurnRequest{Model: "gpt-5"}, ExecutionIdentity{})
	if err == nil {
		t.Fatal("expected feature gate error")
	}
	if !strings.Contains(err.Error(), FeatureGateEnvVar) {
		t.Fatalf("expected error to mention %s, got: %v", FeatureGateEnvVar, err)
	}
}

func TestOrchestratorRun_TerminalNoToolCalls(t *testing.T) {
	client := &fakeResponsesClient{
		responses: []TurnResponse{
			{ResponseID: "resp_1", Terminal: true, OutputText: "done"},
		},
	}
	telemetry := &fakeTelemetry{}

	orch := Orchestrator{
		Config:    enabledConfig(),
		Client:    client,
		Telemetry: telemetry,
	}

	out, err := orch.Run(context.Background(), TurnRequest{Model: "gpt-5"}, ExecutionIdentity{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if out.Iterations != 1 {
		t.Fatalf("iterations = %d, want 1", out.Iterations)
	}
	if out.Final.ResponseID != "resp_1" {
		t.Fatalf("final response id = %q, want resp_1", out.Final.ResponseID)
	}
	if telemetry.turnStarts != 1 || telemetry.turnEnds != 1 {
		t.Fatalf("unexpected telemetry counts: starts=%d ends=%d", telemetry.turnStarts, telemetry.turnEnds)
	}
}

func TestOrchestratorRun_ExecutesToolLoopAndChainsContext(t *testing.T) {
	client := &fakeResponsesClient{
		responses: []TurnResponse{
			{
				ResponseID: "resp_1",
				ToolCalls: []ToolCall{
					{CallID: "call_1", ToolName: "math__add"},
				},
			},
			{
				ResponseID: "resp_2",
				Terminal:   true,
				OutputText: "42",
			},
		},
	}
	executor := &fakeExecutor{
		results: map[string]ToolResult{
			"math__add": {CallID: "call_1", Output: map[string]any{"sum": 42}},
		},
	}
	adapter := &fakeAdapter{
		tools: []ToolDefinition{
			{Name: "math__add", Server: "math", Tool: "add"},
		},
	}
	telemetry := &fakeTelemetry{}

	orch := Orchestrator{
		Config:    enabledConfig(),
		Client:    client,
		Executor:  executor,
		Adapter:   adapter,
		Telemetry: telemetry,
	}

	req := TurnRequest{
		Model: "gpt-5",
		Context: ContextStrategy{
			Mode: ContextModeChain,
		},
	}
	out, err := orch.Run(context.Background(), req, ExecutionIdentity{AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if out.Iterations != 2 {
		t.Fatalf("iterations = %d, want 2", out.Iterations)
	}
	if len(out.ToolResults) != 1 {
		t.Fatalf("tool results len = %d, want 1", len(out.ToolResults))
	}
	if len(client.reqs) != 2 {
		t.Fatalf("client req count = %d, want 2", len(client.reqs))
	}
	if client.reqs[1].Context.PreviousResponseID != "resp_1" {
		t.Fatalf("second request previous_response_id = %q, want resp_1", client.reqs[1].Context.PreviousResponseID)
	}
	if len(adapter.resolveCall) != 1 {
		t.Fatalf("resolved call count = %d, want 1", len(adapter.resolveCall))
	}
	if telemetry.toolCalls != 1 {
		t.Fatalf("telemetry tool calls = %d, want 1", telemetry.toolCalls)
	}
}

func TestOrchestratorRun_RequiresExecutorWhenToolCallsPresent(t *testing.T) {
	client := &fakeResponsesClient{
		responses: []TurnResponse{
			{
				ResponseID: "resp_1",
				ToolCalls:  []ToolCall{{CallID: "call_1", ToolName: "calc__sum"}},
			},
		},
	}
	orch := Orchestrator{
		Config: enabledConfig(),
		Client: client,
	}
	_, err := orch.Run(context.Background(), TurnRequest{Model: "gpt-5"}, ExecutionIdentity{})
	if !errors.Is(err, ErrMissingExecutor) {
		t.Fatalf("expected ErrMissingExecutor, got %v", err)
	}
}

func TestOrchestratorRun_StopsAtMaxIterations(t *testing.T) {
	client := &fakeResponsesClient{
		responses: []TurnResponse{
			{ResponseID: "resp_1", ToolCalls: []ToolCall{{CallID: "1", ToolName: "tool__a"}}},
			{ResponseID: "resp_2", ToolCalls: []ToolCall{{CallID: "2", ToolName: "tool__a"}}},
		},
	}
	orch := Orchestrator{
		Config: Config{
			Enabled:           true,
			RequestTimeout:    10 * time.Second,
			MaxLoopIterations: 1,
		},
		Client:   client,
		Executor: &fakeExecutor{},
	}
	_, err := orch.Run(context.Background(), TurnRequest{Model: "gpt-5"}, ExecutionIdentity{})
	if err == nil {
		t.Fatal("expected loop limit error")
	}
	if !errors.Is(err, ErrLoopLimitExceeded) {
		t.Fatalf("expected ErrLoopLimitExceeded, got %v", err)
	}
}

func TestOrchestratorRun_ConversationModePropagatesConversationID(t *testing.T) {
	client := &fakeResponsesClient{
		responses: []TurnResponse{
			{
				ResponseID:     "resp_1",
				ConversationID: "conv_1",
				ToolCalls:      []ToolCall{{CallID: "call_1", ToolName: "tool__a"}},
			},
			{ResponseID: "resp_2", Terminal: true},
		},
	}
	orch := Orchestrator{
		Config:   enabledConfig(),
		Client:   client,
		Executor: &fakeExecutor{},
	}
	req := TurnRequest{
		Model: "gpt-5",
		Context: ContextStrategy{
			Mode:           ContextModeConversation,
			ConversationID: "conv_0",
		},
	}
	_, err := orch.Run(context.Background(), req, ExecutionIdentity{})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(client.reqs) != 2 {
		t.Fatalf("client req count = %d, want 2", len(client.reqs))
	}
	if client.reqs[1].Context.ConversationID != "conv_0" {
		t.Fatalf("conversation id changed unexpectedly: %q", client.reqs[1].Context.ConversationID)
	}
}

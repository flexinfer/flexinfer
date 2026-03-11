package openairesponses

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrLoopLimitExceeded is returned when the orchestration loop does not reach terminal state in time.
	ErrLoopLimitExceeded = errors.New("responses orchestration exceeded max loop iterations")
	// ErrMissingClient is returned when no Responses client is configured.
	ErrMissingClient = errors.New("responses client is required")
	// ErrMissingExecutor is returned when the loop needs tool execution but no executor is configured.
	ErrMissingExecutor = errors.New("tool executor is required")
)

// LoopResult captures the final output of a non-stream orchestration run.
type LoopResult struct {
	Final       TurnResponse
	Iterations  int
	ToolResults []ToolResult
}

// Orchestrator executes non-stream Responses turns with optional tool loops.
type Orchestrator struct {
	Config    Config
	Client    ResponsesClient
	Adapter   ToolAdapter
	Executor  ToolExecutor
	Telemetry TelemetrySink
}

// Run executes a Responses turn and iterates through tool calls until terminal output.
func (o *Orchestrator) Run(ctx context.Context, req TurnRequest, identity ExecutionIdentity) (LoopResult, error) {
	if err := o.Config.RequireEnabled(); err != nil {
		return LoopResult{}, err
	}
	if err := o.Config.Validate(); err != nil {
		return LoopResult{}, err
	}
	if o.Client == nil {
		return LoopResult{}, ErrMissingClient
	}
	if err := req.Validate(); err != nil {
		return LoopResult{}, err
	}

	if len(req.Tools) == 0 && o.Adapter != nil {
		tools, err := o.Adapter.BuildTools(ctx, identity)
		if err != nil {
			return LoopResult{}, fmt.Errorf("build tools: %w", err)
		}
		req.Tools = tools
	}

	current := req
	allToolResults := make([]ToolResult, 0, 8)

	for iteration := 1; iteration <= o.Config.MaxLoopIterations; iteration++ {
		// Token preflight: check budget and compact if needed.
		if o.Config.TokenBudget > 0 {
			compacted, _, compactErr := CompactRequest(current, o.Config.TokenBudget, o.Config.Compaction)
			if compactErr != nil {
				return LoopResult{}, fmt.Errorf("token preflight iteration %d: %w", iteration, compactErr)
			}
			current = compacted
		}

		o.recordTurnStart(ctx, current, identity)
		resp, err := o.Client.Create(ctx, current)
		o.recordTurnEnd(ctx, resp, err, identity)
		if err != nil {
			return LoopResult{}, fmt.Errorf("responses create iteration %d: %w", iteration, err)
		}

		toolCalls := resp.ToolCalls
		if o.Adapter != nil && len(toolCalls) > 0 {
			resolved := make([]ToolCall, 0, len(toolCalls))
			for _, call := range toolCalls {
				c, resolveErr := o.Adapter.ResolveCall(ctx, call)
				if resolveErr != nil {
					return LoopResult{}, fmt.Errorf("resolve tool call %q: %w", call.ToolName, resolveErr)
				}
				resolved = append(resolved, c)
			}
			toolCalls = resolved
		}

		if resp.Terminal || len(toolCalls) == 0 {
			return LoopResult{
				Final:       resp,
				Iterations:  iteration,
				ToolResults: allToolResults,
			}, nil
		}

		if o.Executor == nil {
			return LoopResult{}, ErrMissingExecutor
		}

		turnToolResults := make([]ToolResult, 0, len(toolCalls))
		for _, call := range toolCalls {
			result, execErr := o.Executor.ExecuteTool(ctx, call, identity)
			if execErr != nil {
				o.recordToolCall(ctx, call, ToolResult{}, execErr, identity)
				return LoopResult{}, fmt.Errorf("execute tool %q: %w", call.ToolName, execErr)
			}
			if result.CallID == "" {
				result.CallID = call.CallID
			}
			o.recordToolCall(ctx, call, result, nil, identity)
			turnToolResults = append(turnToolResults, result)
			allToolResults = append(allToolResults, result)
		}

		current = o.buildNextRequest(current, resp, turnToolResults)
	}

	return LoopResult{}, fmt.Errorf("%w (%d)", ErrLoopLimitExceeded, o.Config.MaxLoopIterations)
}

func (o *Orchestrator) buildNextRequest(prev TurnRequest, resp TurnResponse, toolResults []ToolResult) TurnRequest {
	next := prev
	next.Input = toolResults
	switch prev.Context.Mode {
	case ContextModeConversation:
		if strings.TrimSpace(next.Context.ConversationID) == "" {
			next.Context.ConversationID = strings.TrimSpace(resp.ConversationID)
		}
	case ContextModeChain:
		next.Context.PreviousResponseID = strings.TrimSpace(resp.ResponseID)
	}
	return next
}

func (o *Orchestrator) recordTurnStart(ctx context.Context, req TurnRequest, identity ExecutionIdentity) {
	if o.Telemetry == nil {
		return
	}
	o.Telemetry.RecordTurnStart(ctx, req, identity)
}

func (o *Orchestrator) recordTurnEnd(ctx context.Context, resp TurnResponse, err error, identity ExecutionIdentity) {
	if o.Telemetry == nil {
		return
	}
	o.Telemetry.RecordTurnEnd(ctx, resp, err, identity)
}

func (o *Orchestrator) recordToolCall(ctx context.Context, call ToolCall, result ToolResult, err error, identity ExecutionIdentity) {
	if o.Telemetry == nil {
		return
	}
	o.Telemetry.RecordToolCall(ctx, call, result, err, identity)
}

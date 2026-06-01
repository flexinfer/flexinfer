package agentloop

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Completer is the one engine dependency on the transport. ChatClient
// satisfies it; tests substitute a canned implementation.
type Completer interface {
	Complete(ctx context.Context, msgs []Message, tools []ToolDef, maxTokens int) (Message, TurnMetrics, error)
}

// Stop reasons recorded on a Result.
const (
	StopFinal     = "final"      // model produced a tool-call-free answer
	StopMaxRounds = "max_rounds" // hit the round cap
	StopBudget    = "budget"     // next turn would overflow maxModelLen
)

// Engine drives the append-only ReAct loop.
type Engine struct {
	Client       Completer
	Registry     *Registry
	Budget       Budget
	MaxRounds    int
	OutputTokens int           // max_tokens per turn
	ToolTimeout  time.Duration // per-tool-call timeout; 0 = inherit ctx
	// OnRound, if set, is called after each round with that round's record —
	// the CLI uses it for live per-turn output.
	OnRound func(RoundRecord)
}

// RoundRecord captures one model turn plus any tool calls it triggered.
type RoundRecord struct {
	Round     int              `json:"round"`
	Metrics   TurnMetrics      `json:"metrics"`
	ToolCalls []ToolInvocation `json:"tool_calls,omitempty"`
	Final     bool             `json:"final"`
}

// ToolInvocation records one executed tool call and its outcome.
type ToolInvocation struct {
	Name    string `json:"name"`
	Args    string `json:"args"`
	Result  string `json:"result,omitempty"`
	Err     string `json:"error,omitempty"`
	Latency int64  `json:"latency_ms"`
}

// Result is the outcome of Run.
type Result struct {
	Answer       string        `json:"answer"`
	Stopped      string        `json:"stopped"`
	Rounds       []RoundRecord `json:"rounds"`
	FinishReason string        `json:"finish_reason,omitempty"`
}

// Run executes the loop: it appends userInput to the conversation, then on
// each round sends the full append-only context plus the fixed tool set. If
// the model returns tool calls, every call is executed and its result
// appended as a tool message; the loop continues. The first tool-call-free
// reply is the final answer. The loop also stops at MaxRounds or when a turn
// would overflow the context budget.
//
// A *BudgetError from the client is treated as a clean budget stop, not a
// failure — the partial Result is returned with err == nil and
// Stopped == StopBudget.
func (e *Engine) Run(ctx context.Context, conv *Conversation, userInput string) (*Result, error) {
	if e.Client == nil {
		return nil, fmt.Errorf("engine: nil client")
	}
	if e.MaxRounds <= 0 {
		return nil, fmt.Errorf("engine: MaxRounds must be > 0")
	}
	conv.Append(Message{Role: RoleUser, Content: userInput})

	res := &Result{Stopped: StopMaxRounds}
	var tools []ToolDef
	if e.Registry != nil {
		tools = e.Registry.Definitions()
	}

	for round := 0; round < e.MaxRounds; round++ {
		reply, metrics, err := e.Client.Complete(ctx, conv.Messages(), tools, e.OutputTokens)
		if err != nil {
			var be *BudgetError
			if errors.As(err, &be) {
				res.Stopped = StopBudget
				return res, nil
			}
			return res, fmt.Errorf("round %d: %w", round, err)
		}

		rec := RoundRecord{Round: round, Metrics: metrics}
		res.FinishReason = metrics.FinishReason
		conv.Append(reply)

		if len(reply.ToolCalls) == 0 {
			rec.Final = true
			res.Answer = reply.Content
			res.Stopped = StopFinal
			res.Rounds = append(res.Rounds, rec)
			e.emit(rec)
			return res, nil
		}

		rec.ToolCalls = e.runToolCalls(ctx, conv, reply.ToolCalls)
		res.Rounds = append(res.Rounds, rec)
		e.emit(rec)

		// Proactively stop before a turn that would overflow the budget.
		if be := e.Budget.Check(metrics.PromptTokens); be != nil {
			res.Stopped = StopBudget
			return res, nil
		}
	}
	return res, nil
}

// runToolCalls executes each requested tool call in order and appends a tool
// message per call. An unknown tool or an invoke error becomes a tool
// message whose content is the error text — the model sees the failure and
// can recover, rather than the loop crashing.
func (e *Engine) runToolCalls(ctx context.Context, conv *Conversation, calls []ToolCall) []ToolInvocation {
	out := make([]ToolInvocation, 0, len(calls))
	for _, call := range calls {
		inv := ToolInvocation{Name: call.Function.Name, Args: call.Function.Arguments}
		start := time.Now()
		result, err := e.invokeOne(ctx, call)
		inv.Latency = time.Since(start).Milliseconds()
		if err != nil {
			inv.Err = err.Error()
			result = "tool error: " + err.Error()
		} else {
			inv.Result = result
		}
		conv.Append(Message{
			Role:       RoleTool,
			Content:    result,
			Name:       call.Function.Name,
			ToolCallID: call.ID,
		})
		out = append(out, inv)
	}
	return out
}

// invokeOne dispatches a single tool call, timing it and applying the
// per-call timeout when configured.
func (e *Engine) invokeOne(ctx context.Context, call ToolCall) (string, error) {
	if e.Registry == nil {
		return "", fmt.Errorf("no tools registered")
	}
	tool, ok := e.Registry.Get(call.Function.Name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", call.Function.Name)
	}
	callCtx := ctx
	if e.ToolTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, e.ToolTimeout)
		defer cancel()
	}
	return tool.Invoke(callCtx, call.Function.Arguments)
}

func (e *Engine) emit(rec RoundRecord) {
	if e.OnRound != nil {
		e.OnRound(rec)
	}
}

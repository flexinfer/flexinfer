package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/openairesponses"
)

type responsesDaemonRPC func(ctx context.Context, method string, params any) (json.RawMessage, error)

type responsesToolAdapter struct {
	rpc       responsesDaemonRPC
	knownTool map[string]struct{}
}

type responsesToolExecutor struct {
	rpc responsesDaemonRPC
}

func defaultResponsesRuntimeFactory(_ context.Context) (responsesRuntimeDependencies, error) {
	cfg := openairesponses.LoadConfigFromEnv()
	client, err := openairesponses.NewAPIClient(openairesponses.APIClientConfig{
		APIKey:     openairesponses.APIKeyFromEnv(),
		BaseURL:    openairesponses.BaseURLFromEnv(),
		Timeout:    cfg.RequestTimeout,
		MaxRetries: cfg.MaxRetries,
		HTTPClient: httpclient.New(httpclient.Config{
			Timeout:        cfg.RequestTimeout,
			MaxRetries:     cfg.MaxRetries,
			RetryBaseDelay: 200 * time.Millisecond,
			RetryMaxDelay:  2 * time.Second,
		}),
		UserAgent: "loom responses run",
	})
	if err != nil {
		return responsesRuntimeDependencies{}, err
	}

	socket := responsesRuntimeSocketPath
	if strings.TrimSpace(socket) == "" {
		socket = resolveSocketPath(nil)
	}
	rpc := makeResponsesDaemonRPC(socket)
	return responsesRuntimeDependencies{
		Client:   client,
		Adapter:  &responsesToolAdapter{rpc: rpc},
		Executor: &responsesToolExecutor{rpc: rpc},
	}, nil
}

func makeResponsesDaemonRPC(socket string) responsesDaemonRPC {
	return func(ctx context.Context, method string, params any) (json.RawMessage, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err != nil {
			return nil, fmt.Errorf("dial daemon socket %s: %w", socket, err)
		}
		defer conn.Close()

		transport := mcp.NewStdioTransport(conn, conn)
		req, err := mcp.NewRequest(1, method, params)
		if err != nil {
			return nil, fmt.Errorf("create daemon request: %w", err)
		}
		if err := transport.Send(ctx, req); err != nil {
			return nil, fmt.Errorf("send daemon request %s: %w", method, err)
		}
		resp, err := transport.Recv(ctx)
		if err != nil {
			return nil, fmt.Errorf("receive daemon response %s: %w", method, err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("daemon error for %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (a *responsesToolAdapter) BuildTools(ctx context.Context, _ openairesponses.ExecutionIdentity) ([]openairesponses.ToolDefinition, error) {
	raw, err := a.rpc(ctx, "loom/tools", nil)
	if err != nil {
		return nil, err
	}

	var result bridge.ToolsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode daemon tools: %w", err)
	}

	tools := make([]openairesponses.ToolDefinition, 0, len(result.Tools))
	a.knownTool = make(map[string]struct{}, len(result.Tools))
	for _, tool := range result.Tools {
		schema, err := schemaToMap(tool.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("convert schema for %s: %w", tool.Name, err)
		}
		tools = append(tools, openairesponses.ToolDefinition{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
		a.knownTool[tool.Name] = struct{}{}
	}
	return tools, nil
}

func (a *responsesToolAdapter) ResolveCall(_ context.Context, call openairesponses.ToolCall) (openairesponses.ToolCall, error) {
	if len(a.knownTool) == 0 {
		return call, nil
	}
	if _, ok := a.knownTool[call.ToolName]; !ok {
		return openairesponses.ToolCall{}, fmt.Errorf("tool %q is not exposed by loom/tools", call.ToolName)
	}
	return call, nil
}

func (e *responsesToolExecutor) ExecuteTool(ctx context.Context, call openairesponses.ToolCall, identity openairesponses.ExecutionIdentity) (openairesponses.ToolResult, error) {
	args := map[string]any{}
	if len(call.Arguments) > 0 {
		if err := json.Unmarshal(call.Arguments, &args); err != nil {
			return openairesponses.ToolResult{}, fmt.Errorf("decode tool arguments for %s: %w", call.ToolName, err)
		}
	}

	params := map[string]any{
		"name":       call.ToolName,
		"arguments":  args,
		"agent_id":   identity.AgentID,
		"session_id": identity.SessionID,
	}
	if timeout := timeoutHint(ctx); timeout > 0 {
		params["_timeout"] = timeout.String()
	}

	raw, err := e.rpc(ctx, "tools/call", params)
	if err != nil {
		return openairesponses.ToolResult{}, err
	}

	var decoded any
	if err := bridge.UnmarshalToolResult(raw, &decoded); err != nil {
		return openairesponses.ToolResult{}, err
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return openairesponses.ToolResult{
		CallID:     call.CallID,
		Output:     decoded,
		RawPayload: raw,
	}, nil
}

func schemaToMap(schema mcp.InputSchema) (map[string]any, error) {
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func timeoutHint(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return 0
	}
	return timeout
}

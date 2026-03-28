package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// fetchServerTools gets tools from a single server using its own dedicated process.
func (d *Daemon) fetchServerTools(ctx context.Context, serverName string) ([]mcp.Tool, error) {
	// Get server spec
	spec, err := d.registry.GetServerSpec(serverName, d.cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("get server spec: %w", err)
	}

	if spec.Command == "" {
		return nil, fmt.Errorf("no command defined")
	}

	// Create timeout context - use shorter timeout to fail fast
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Expand variables in command
	command := d.expandVars(spec.Command)

	// Build command
	args := make([]string, len(spec.Args))
	for i, arg := range spec.Args {
		args[i] = d.expandVars(fmt.Sprint(arg))
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, d.expandVars(v)))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start: %w", err)
	}
	defer func() {
		stdin.Close()
		stdout.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

	transport := mcp.NewStdioTransport(stdout, stdin)

	if err := initializeMCPTransport(ctx, transport); err != nil {
		return nil, err
	}

	// Get tools
	toolsReq, _ := mcp.NewRequest(2, "tools/list", nil)
	if err := transport.Send(ctx, toolsReq); err != nil {
		return nil, fmt.Errorf("send tools/list: %w", err)
	}
	toolsResp, err := transport.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("recv tools/list: %w", err)
	}
	if toolsResp.Error != nil {
		return nil, fmt.Errorf("server error: %s", toolsResp.Error.Message)
	}

	var toolsList struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &toolsList); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return toolsList.Tools, nil
}

// fetchServerToolsViaPool performs a tools/list health probe using the connection
// pool, reusing an existing idle connection when available. This avoids spawning a
// fresh process for every health check interval.
func (d *Daemon) fetchServerToolsViaPool(ctx context.Context, serverName string) ([]mcp.Tool, error) {
	if d.pool == nil {
		return nil, fmt.Errorf("local pool not configured")
	}

	// Acquire callLock BEFORE pool.Get to match callPipeline.routeAndConnect
	// ordering. Reversed ordering (pool->lock) can deadlock against the
	// callPipeline path (lock->pool) when the pool is at capacity.
	mu, _, err := d.acquireCallLock(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("acquire call lock: %w", err)
	}
	defer mu.Unlock()

	conn, err := d.pool.Get(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("pool connect: %w", err)
	}
	defer d.pool.Put(conn)

	req, _ := mcp.NewRequest(1, "tools/list", nil)
	if err := conn.Transport.Send(ctx, req); err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("send tools/list: %w", err)
	}

	resp, err := conn.Transport.Recv(ctx)
	if err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("recv tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("server error: %s", resp.Error.Message)
	}

	var toolsList struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &toolsList); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return toolsList.Tools, nil
}

// initializeMCPTransport performs the MCP initialize handshake on a fresh transport.
func initializeMCPTransport(ctx context.Context, transport mcp.Transport) error {
	versions := []string{
		mcp.ProtocolVersion20250618,
		mcp.ProtocolVersion,
	}
	var lastErr error
	for _, protocolVersion := range versions {
		initReq, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
			ProtocolVersion: protocolVersion,
			Capabilities:    mcp.Capabilities{},
			ClientInfo:      mcp.ClientInfo{Name: "loom-daemon", Version: "0.1.0"},
		})
		if err := transport.Send(ctx, initReq); err != nil {
			lastErr = fmt.Errorf("send init (%s): %w", protocolVersion, err)
			continue
		}
		initResp, err := transport.Recv(ctx)
		if err != nil {
			lastErr = fmt.Errorf("recv init (%s): %w", protocolVersion, err)
			continue
		}
		if initResp != nil && initResp.Error != nil {
			lastErr = fmt.Errorf("init error (%s): %s", protocolVersion, initResp.Error.Message)
			continue
		}

		initNotif := &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"}
		if err := transport.Send(ctx, initNotif); err != nil {
			return fmt.Errorf("send initialized (%s): %w", protocolVersion, err)
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("initialize failed: no protocol versions attempted")
}

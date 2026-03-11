package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startMockWorkDaemon(t *testing.T, handler func(method string, params json.RawMessage) (any, error)) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "loom-work-cmd-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath := filepath.Join(dir, "daemon.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64*1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					var req struct {
						ID     any             `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					if err := json.Unmarshal(buf[:n], &req); err != nil {
						continue
					}
					result, callErr := handler(req.Method, req.Params)
					var resp any
					if callErr != nil {
						resp = map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"error": map[string]any{
								"code":    -32603,
								"message": callErr.Error(),
							},
						}
					} else {
						resp = map[string]any{
							"jsonrpc": "2.0",
							"id":      req.ID,
							"result":  result,
						}
					}
					out, _ := json.Marshal(resp)
					out = append(out, '\n')
					_, _ = c.Write(out)
				}
			}(conn)
		}
	}()

	return sockPath
}

func TestAgentWorkStartCmd_Success(t *testing.T) {
	sockPath := startMockWorkDaemon(t, func(method string, params json.RawMessage) (any, error) {
		if method != "tools/call" {
			return map[string]any{}, nil
		}
		var req struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(params, &req)
		switch req.Name {
		case "agent_context__agent_session_list":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[]}`}}, "structuredContent": nil}, nil
		case "agent_context__agent_session_start":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"session_id":"sess-1"}`}}}, nil
		case "agent_context__agent_worktree_allocate":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"assignment_id":"wt-1","worktree_path":"/tmp/wt-1","branch":"codex/agent-context-consistency"}`}}}, nil
		case "agent_context__agent_task_add":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"task_ids":["task-1"]}`}}}, nil
		case "agent_context__agent_task_update":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}}, nil
		case "agent_context__agent_presence_heartbeat", "agent_context__agent_presence_register":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}}, nil
		default:
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{}`}}}, nil
		}
	})
	t.Setenv("LOOM_SOCKET", sockPath)

	cmd := newAgentWorkStartCmd()
	cmd.SetArgs([]string{
		"--agent-id", "codex-1",
		"--namespace", "loom-core/agent-context-consistency",
		"--worktree-branch", "codex/agent-context-consistency",
		"--task-title", "Implement consistency hardening",
		"--quiet",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("work-start command failed: %v", err)
	}
}

func TestAgentWorkStartCmd_FailsFastOnMutationError(t *testing.T) {
	sockPath := startMockWorkDaemon(t, func(method string, params json.RawMessage) (any, error) {
		if method != "tools/call" {
			return map[string]any{}, nil
		}
		var req struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(params, &req)
		switch req.Name {
		case "agent_context__agent_session_list":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[]}`}}}, nil
		case "agent_context__agent_session_start":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"session_id":"sess-1"}`}}}, nil
		case "agent_context__agent_worktree_allocate":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"assignment_id":"wt-1","worktree_path":"/tmp/wt-1","branch":"codex/agent-context-consistency"}`}}}, nil
		case "agent_context__agent_task_add":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"task_ids":["task-1"]}`}}}, nil
		case "agent_context__agent_task_update":
			return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "mutation rejected"}}}, nil
		default:
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{}`}}}, nil
		}
	})
	t.Setenv("LOOM_SOCKET", sockPath)

	cmd := newAgentWorkStartCmd()
	cmd.SetArgs([]string{
		"--agent-id", "codex-1",
		"--namespace", "loom-core/agent-context-consistency",
		"--worktree-branch", "codex/agent-context-consistency",
		"--task-title", "Implement consistency hardening",
		"--quiet",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected work-start command to fail on task-update mutation error")
	}
	if !strings.Contains(err.Error(), "step=task-update") {
		t.Fatalf("expected fail-fast step context, got: %v", err)
	}
}

func TestAgentWorkHandoffCmd_Success(t *testing.T) {
	sockPath := startMockWorkDaemon(t, func(method string, params json.RawMessage) (any, error) {
		if method != "tools/call" {
			return map[string]any{}, nil
		}
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(params, &req)
		switch req.Name {
		case "agent_context__agent_session_list":
			if got, _ := req.Arguments["agent_id"].(string); got == "source-agent" {
				return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[{"id":"sess-source","agent_id":"source-agent","status":"active"}]}`}}}, nil
			}
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"sessions":[]}`}}}, nil
		case "agent_context__agent_context_add":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"ok":true}`}}}, nil
		case "agent_context__agent_handoff_create":
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{"handoff_id":"handoff-1"}`}}}, nil
		default:
			return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": `{}`}}}, nil
		}
	})
	t.Setenv("LOOM_SOCKET", sockPath)

	cmd := newAgentWorkHandoffCmd()
	cmd.SetArgs([]string{
		"--source-agent-id", "source-agent",
		"--target-agent-id", "target-agent",
		"--instructions", "Continue rollout verification",
		"--quiet",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("work-handoff command failed: %v", err)
	}
}

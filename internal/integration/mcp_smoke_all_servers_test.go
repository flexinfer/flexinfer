package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestMCP_AllServers_InitializeAndToolsList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if os.Getenv("LOOM_RUN_MCP_SMOKE") != "1" {
		t.Skip("set LOOM_RUN_MCP_SMOKE=1 to run MCP server smoke tests (requires built binaries)")
	}

	repoRoot := os.Getenv("LOOM_REPO_ROOT")
	if repoRoot == "" {
		cwd, _ := os.Getwd()
		repoRoot = filepath.Join(cwd, "..", "..")
	}

	if _, err := os.Stat(filepath.Join(repoRoot, "bin")); err != nil {
		t.Skipf("skipping: %s", err)
	}

	cmdDir := filepath.Join(repoRoot, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("read cmd dir: %v", err)
	}

	var servers []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "mcp-") {
			servers = append(servers, name)
		}
	}
	sort.Strings(servers)
	if len(servers) == 0 {
		t.Fatalf("no MCP servers found in %s", cmdDir)
	}

	// Shared env tweaks to avoid external dependencies at startup.
	sharedEnv := map[string]string{
		"GODOT_AUTO_CONNECT": "false",
		"MEMORY_AUTO_SAVE":   "false",
	}

	// Map of servers that require external services and their env vars
	requiredEnvVars := map[string]string{
		"mcp-postgres": "POSTGRES_URL",
		"mcp-neo4j":    "NEO4J_URI",
		"mcp-redis":    "REDIS_URL",
	}

	ran := false
	for _, serverName := range servers {
		t.Run(serverName, func(t *testing.T) {
			if envVar, ok := requiredEnvVars[serverName]; ok && os.Getenv(envVar) == "" {
				t.Skipf("%s not set; skipping %s smoke test", envVar, serverName)
			}

			binary, err := findBinary(serverName)
			if err != nil {
				t.Skipf("%s not found: %v", serverName, err)
			}
			ran = true

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			client, err := NewMCPClientWithEnv(ctx, sharedEnv, binary)
			if err != nil {
				t.Fatalf("start %s: %v", serverName, err)
			}
			defer client.Close()

			// initialize
			initResp, err := client.Send("initialize", map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
			})
			if err != nil {
				t.Fatalf("initialize failed: %v", err)
			}
			if initResp.Error != nil {
				t.Fatalf("initialize error: %d %s", initResp.Error.Code, initResp.Error.Message)
			}

			// tools/list
			toolsResp, err := client.Send("tools/list", nil)
			if err != nil {
				t.Fatalf("tools/list failed: %v", err)
			}
			if toolsResp.Error != nil {
				t.Fatalf("tools/list error: %d %s", toolsResp.Error.Code, toolsResp.Error.Message)
			}

			var result struct {
				Tools []struct {
					Name        string `json:"name"`
					InputSchema struct {
						Type string `json:"type"`
					} `json:"inputSchema"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(toolsResp.Result, &result); err != nil {
				t.Fatalf("parse tools/list result: %v", err)
			}
			if len(result.Tools) == 0 {
				t.Fatalf("expected at least one tool")
			}
			for _, tool := range result.Tools {
				if strings.TrimSpace(tool.Name) == "" {
					t.Fatalf("expected tool name to be non-empty")
				}
				if tool.InputSchema.Type == "" {
					t.Fatalf("tool %s missing inputSchema.type", tool.Name)
				}
			}
		})
	}

	if !ran {
		t.Skip("no MCP server binaries found; run `make build` and re-run with LOOM_REPO_ROOT set if needed")
	}
}

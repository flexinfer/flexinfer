package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockMCPHandler returns an http.Handler that serves as a minimal mock hub for Streamable HTTP.
// It responds to initialize, tools/list, and tools/call with simple canned responses.
func mockMCPHandler(t *testing.T, tools []string) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var msg MCPMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		switch msg.Method {
		case "initialize":
			resp := MCPMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: mustMarshal(map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "mock-hub", "version": "0.0.1"},
				}),
			}
			json.NewEncoder(w).Encode(resp)

		case "tools/list":
			toolList := make([]map[string]any, 0, len(tools))
			for _, name := range tools {
				toolList = append(toolList, map[string]any{
					"name":        name,
					"description": fmt.Sprintf("Mock tool %s", name),
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
				})
			}
			resp := MCPMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  mustMarshal(map[string]any{"tools": toolList}),
			}
			json.NewEncoder(w).Encode(resp)

		case "tools/call":
			resp := MCPMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: mustMarshal(map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "mock hub response"},
					},
				}),
			}
			json.NewEncoder(w).Encode(resp)

		case "notifications/initialized":
			// No response for notifications
			return

		default:
			resp := MCPMessage{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error:   &MCPError{Code: -32601, Message: fmt.Sprintf("method not found: %s", msg.Method)},
			}
			json.NewEncoder(w).Encode(resp)
		}
	})

	return mux
}

// startMockHub starts an httptest server that acts as a minimal MCP hub.
func startMockHub(t *testing.T, tools []string) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(mockMCPHandler(t, tools))
	return srv, srv.Close
}

// mustMarshal marshals v to json.RawMessage and panics on error.
func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(b)
}

// echoServerScript returns a minimal shell script that acts as an echo MCP server.
// It reads JSON-RPC messages from stdin and responds with canned responses.
// Used by stdio-based integration tests.
var _ = echoServerScript // ensure linter sees usage

func echoServerScript() string {
	return strings.TrimSpace(`
#!/usr/bin/env python3
import json
import sys

def handle(msg):
    method = msg.get("method", "")
    msg_id = msg.get("id")

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "echo-server", "version": "0.0.1"}
            }
        }
    elif method == "notifications/initialized":
        return None
    elif method == "tools/list":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "tools": [{
                    "name": "echo",
                    "description": "Echo tool for testing",
                    "inputSchema": {"type": "object", "properties": {"message": {"type": "string"}}}
                }]
            }
        }
    elif method == "tools/call":
        params = msg.get("params", {})
        args = params.get("arguments", {})
        text = args.get("message", "no message")
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "content": [{"type": "text", "text": "echo: " + text}]
            }
        }
    else:
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "error": {"code": -32601, "message": "method not found: " + method}
        }

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
        resp = handle(msg)
        if resp:
            print(json.dumps(resp), flush=True)
    except Exception as e:
        print(json.dumps({"jsonrpc": "2.0", "error": {"code": -32603, "message": str(e)}}), flush=True)
`)
}

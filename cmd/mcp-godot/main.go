// mcp-godot provides a Godot debugging MCP server for scene tree inspection,
// node manipulation, and log reading.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "dev"

// GodotClient handles TCP communication with the Godot debug plugin
type GodotClient struct {
	host         string
	port         int
	conn         net.Conn
	mu           sync.Mutex
	autoConnect  bool
	reconnectMs  int
	responseChan chan json.RawMessage
	errorChan    chan error
	doneCh       chan struct{} // signals readResponses to exit
}

func NewGodotClient(host string, port int, autoConnect bool, reconnectMs int) *GodotClient {
	return &GodotClient{
		host:         host,
		port:         port,
		autoConnect:  autoConnect,
		reconnectMs:  reconnectMs,
		responseChan: make(chan json.RawMessage, 1),
		errorChan:    make(chan error, 1),
		doneCh:       make(chan struct{}),
	}
}

func (c *GodotClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	addr := net.JoinHostPort(c.host, strconv.Itoa(c.port))
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second) //nolint:noctx // Connect() doesn't take context; refactor needed
	if err != nil {
		return fmt.Errorf("failed to connect to Godot at %s: %w", addr, err)
	}

	c.conn = conn

	// Start reading responses
	go c.readResponses()

	return nil
}

func (c *GodotClient) readResponses() {
	reader := bufio.NewReader(c.conn)
	for {
		// Check if we should exit before blocking on read
		select {
		case <-c.doneCh:
			return
		default:
		}

		// Set a read deadline so we can periodically check doneCh
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		}
		c.mu.Unlock()

		line, err := reader.ReadString('\n')
		if err != nil {
			// Check if this was a timeout - if so, loop and check doneCh
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			c.mu.Lock()
			c.conn = nil
			c.mu.Unlock()
			select {
			case c.errorChan <- err:
			case <-c.doneCh:
			}
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		select {
		case c.responseChan <- json.RawMessage(line):
		case <-c.doneCh:
			return
		}
	}
}

// Close closes the client connection and stops the read goroutine.
func (c *GodotClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.doneCh:
		// Already closed
	default:
		close(c.doneCh)
	}

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *GodotClient) CallCommand(cmd map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.conn == nil {
		c.mu.Unlock()
		if c.autoConnect {
			if err := c.Connect(); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("godot client not connected")
		}
	} else {
		c.mu.Unlock()
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal command: %w", err)
	}

	c.mu.Lock()
	_, err = c.conn.Write(append(data, '\n'))
	c.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to send command: %w", err)
	}

	// Wait for response with timeout - use NewTimer to avoid leaking timers
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case resp := <-c.responseChan:
		return resp, nil
	case err := <-c.errorChan:
		return nil, err
	case <-timer.C:
		return nil, fmt.Errorf("timeout waiting for response")
	case <-c.doneCh:
		return nil, fmt.Errorf("client closed")
	}
}

// LogReader reads Godot log files
type LogReader struct {
	basePath string
}

func NewLogReader(basePath string) *LogReader {
	// Expand ~ to home directory
	if strings.HasPrefix(basePath, "~/") {
		home, _ := os.UserHomeDir()
		basePath = filepath.Join(home, basePath[2:])
	}
	return &LogReader{basePath: basePath}
}

func (r *LogReader) ReadRecent(lines int, filter string) []string {
	logFile := filepath.Join(r.basePath, "kk_logs.jsonl")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return []string{}
	}

	allLines := strings.Split(strings.TrimSpace(string(data)), "\n")

	// Take last N lines
	if lines > 0 && len(allLines) > lines {
		allLines = allLines[len(allLines)-lines:]
	}

	// Filter if specified
	if filter != "" {
		var filtered []string
		for _, line := range allLines {
			if strings.Contains(line, filter) {
				filtered = append(filtered, line)
			}
		}
		return filtered
	}

	return allLines
}

func (r *LogReader) TailStream(ctx context.Context, durationMs int, filter string) []string {
	logFile := filepath.Join(r.basePath, "kk_logs.jsonl")
	var collected []string
	lastLineCount := 0

	endTime := time.Now().Add(time.Duration(durationMs) * time.Millisecond)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for time.Now().Before(endTime) {
		select {
		case <-ctx.Done():
			return collected
		case <-ticker.C:
			data, err := os.ReadFile(logFile)
			if err != nil {
				continue
			}

			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) < lastLineCount {
				lastLineCount = 0 // File rotated/truncated
			}

			newLines := lines[lastLineCount:]
			for _, line := range newLines {
				if filter == "" || strings.Contains(line, filter) {
					collected = append(collected, line)
				}
			}
			lastLineCount = len(lines)
		}
	}

	return collected
}

var (
	godotClient *GodotClient
	logReader   *LogReader
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	// Load config from environment
	host := os.Getenv("GODOT_HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	port := 6550
	if p := os.Getenv("GODOT_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}

	logPath := os.Getenv("GODOT_LOG_PATH")
	if logPath == "" {
		home, _ := os.UserHomeDir()
		logPath = filepath.Join(home, "Library/Application Support/Godot")
	}

	autoConnect := os.Getenv("GODOT_AUTO_CONNECT") != "false"

	reconnectMs := 5000
	if r := os.Getenv("GODOT_RECONNECT_MS"); r != "" {
		fmt.Sscanf(r, "%d", &reconnectMs)
	}

	// Initialize clients
	godotClient = NewGodotClient(host, port, autoConnect, reconnectMs)
	defer godotClient.Close()
	logReader = NewLogReader(logPath)

	logger.Info("starting server", "name", "mcp-godot", "version", version, "host", host, "port", port)

	server := mcp.NewServer("mcp-godot", version)
	server.SetInstructions("Godot debugging server. Requires Godot plugin running on localhost:6550.")

	registerTools(server)

	return server.Run(ctx)
}

func registerTools(server *mcp.Server) {
	// godot_scene_tree
	server.AddTool(mcp.Tool{
		Name:        "godot_scene_tree",
		Description: "Fetch the current scene tree from Godot (plugin must be running)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Node path to start from (default: /root)",
				},
			},
		},
	}, handleSceneTree)

	// godot_inspect
	server.AddTool(mcp.Tool{
		Name:        "godot_inspect",
		Description: "Inspect a node by path (plugin must be running)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"node_path": map[string]any{
					"type":        "string",
					"description": "Path to the node to inspect",
				},
			},
			Required: []string{"node_path"},
		},
	}, handleInspect)

	// godot_call
	server.AddTool(mcp.Tool{
		Name:        "godot_call",
		Description: "Call a method on a node (plugin must be running)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"node_path": map[string]any{
					"type":        "string",
					"description": "Path to the node",
				},
				"method": map[string]any{
					"type":        "string",
					"description": "Method name to call",
				},
				"args": map[string]any{
					"type":        "array",
					"description": "Arguments to pass to the method",
				},
			},
			Required: []string{"node_path", "method"},
		},
	}, handleCall)

	// godot_signal
	server.AddTool(mcp.Tool{
		Name:        "godot_signal",
		Description: "Emit a signal on a node (plugin must be running)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"node_path": map[string]any{
					"type":        "string",
					"description": "Path to the node",
				},
				"signal": map[string]any{
					"type":        "string",
					"description": "Signal name to emit",
				},
				"args": map[string]any{
					"type":        "array",
					"description": "Arguments to pass with the signal",
				},
			},
			Required: []string{"node_path", "signal"},
		},
	}, handleSignal)

	// godot_eval
	server.AddTool(mcp.Tool{
		Name:        "godot_eval",
		Description: "Evaluate a GDScript expression (plugin must be running)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "GDScript expression to evaluate",
				},
			},
			Required: []string{"expression"},
		},
	}, handleEval)

	// godot_set
	server.AddTool(mcp.Tool{
		Name:        "godot_set",
		Description: "Set a property on a node (plugin must be running)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"node_path": map[string]any{
					"type":        "string",
					"description": "Path to the node",
				},
				"property": map[string]any{
					"type":        "string",
					"description": "Property name to set",
				},
				"value": map[string]any{
					"description": "Value to set",
				},
			},
			Required: []string{"node_path", "property", "value"},
		},
	}, handleSet)

	// godot_screenshot
	server.AddTool(mcp.Tool{
		Name:        "godot_screenshot",
		Description: "Capture a screenshot (plugin must be running)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"save_path": map[string]any{
					"type":        "string",
					"description": "Path to save the screenshot",
				},
			},
		},
	}, handleScreenshot)

	// godot_logs
	server.AddTool(mcp.Tool{
		Name:        "godot_logs",
		Description: "Tail recent Godot game/editor logs from the local log file",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"lines": map[string]any{
					"type":        "integer",
					"description": "Number of lines to return (1-500, default 50)",
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter string to match",
				},
			},
		},
	}, handleLogs)

	// godot_logs_stream
	server.AddTool(mcp.Tool{
		Name:        "godot_logs_stream",
		Description: "Stream Godot logs in real time (polls log file)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"duration": map[string]any{
					"type":        "integer",
					"description": "Duration in seconds to stream (1-300, default 60)",
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter string to match",
				},
			},
		},
	}, handleLogsStream)
}

func handleSceneTree(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	path := v.String("path", "/root")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := godotClient.CallCommand(map[string]any{
		"cmd":  "scene_tree",
		"path": path,
	})
	if err != nil {
		return mcp.TextResult(fmt.Sprintf("scene_tree failed: %v", err)), nil
	}

	return mcp.JSONResult(resp)
}

func handleInspect(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	nodePath := v.Required("node_path")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := godotClient.CallCommand(map[string]any{
		"cmd":  "inspect",
		"path": nodePath,
	})
	if err != nil {
		return mcp.TextResult(fmt.Sprintf("inspect failed: %v", err)), nil
	}

	return mcp.JSONResult(resp)
}

func handleCall(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	nodePath := v.Required("node_path")
	method := v.Required("method")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	callArgs, _ := args["args"].([]any)
	if callArgs == nil {
		callArgs = []any{}
	}

	resp, err := godotClient.CallCommand(map[string]any{
		"cmd":    "call",
		"path":   nodePath,
		"method": method,
		"args":   callArgs,
	})
	if err != nil {
		return mcp.TextResult(fmt.Sprintf("call failed: %v", err)), nil
	}

	return mcp.JSONResult(resp)
}

func handleSignal(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	nodePath := v.Required("node_path")
	signalName := v.Required("signal")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	signalArgs, _ := args["args"].([]any)
	if signalArgs == nil {
		signalArgs = []any{}
	}

	resp, err := godotClient.CallCommand(map[string]any{
		"cmd":    "signal",
		"path":   nodePath,
		"signal": signalName,
		"args":   signalArgs,
	})
	if err != nil {
		return mcp.TextResult(fmt.Sprintf("signal failed: %v", err)), nil
	}

	return mcp.JSONResult(resp)
}

func handleEval(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	expr := v.Required("expression")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := godotClient.CallCommand(map[string]any{
		"cmd":  "eval",
		"expr": expr,
	})
	if err != nil {
		return mcp.TextResult(fmt.Sprintf("eval failed: %v", err)), nil
	}

	return mcp.JSONResult(resp)
}

func handleSet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	nodePath := v.Required("node_path")
	prop := v.Required("property")
	value := v.RequiredAny("value")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := godotClient.CallCommand(map[string]any{
		"cmd":   "set",
		"path":  nodePath,
		"prop":  prop,
		"value": value,
	})
	if err != nil {
		return mcp.TextResult(fmt.Sprintf("set failed: %v", err)), nil
	}

	return mcp.JSONResult(resp)
}

func handleScreenshot(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	savePath := v.String("save_path", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cmd := map[string]any{"cmd": "screenshot"}
	if savePath != "" {
		cmd["save_path"] = savePath
	}

	resp, err := godotClient.CallCommand(cmd)
	if err != nil {
		return mcp.TextResult(fmt.Sprintf("screenshot failed: %v", err)), nil
	}

	return mcp.JSONResult(resp)
}

func handleLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	lines := v.IntRange("lines", 50, 1, 500)
	filter := v.String("filter", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	logLines := logReader.ReadRecent(lines, filter)
	return mcp.TextResult(strings.Join(logLines, "\n")), nil
}

func handleLogsStream(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	duration := v.IntRange("duration", 60, 1, 300)
	filter := v.String("filter", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	logLines := logReader.TailStream(ctx, duration*1000, filter)
	return mcp.TextResult(strings.Join(logLines, "\n")), nil
}

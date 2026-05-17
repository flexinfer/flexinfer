// Command mcp-loom-widget is a minimal MCP server that exposes the
// `loom_fleet_show` tool and the corresponding ui:// widget resource
// for the MCP Apps extension (Anthropic Jan 2026 spec; ChatGPT Apps
// SDK compatibility follows the same wire format).
//
// Slice 1b-α of the cross-agent GUI integration plan
// (.loom/24-product-spec-loom-fleet-widget-2026-05-16.md). This first
// cut ships a hard-coded HTML placeholder so the wire format can be
// validated in Claude Code Desktop end-to-end before any Skybridge
// + Vite work begins.
//
// Why hand-rolled JSON-RPC instead of gitlab.flexinfer.ai/libs/mcp-go:
// the library does not yet expose the `_meta` fields that MCP Apps
// requires on tools/list responses and tools/call results. Once that
// support lands upstream, this binary can switch to the library and
// drop the dispatch loop here.
package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

const (
	serverName    = "mcp-loom-widget"
	serverVersion = "0.2.0"
	// widgetURI is the ui://-scheme resource that hosts read. The MCP
	// Apps spec scopes the host's sandboxed iframe to a single resource
	// per tool via _meta.ui.resourceUri; we expose just one widget.
	widgetURI      = "ui://widget/loom-fleet.html"
	widgetMimeType = "text/html"

	toolShow      = "loom_fleet_show"
	toolDashboard = "loom_fleet_get_dashboard"
	toolPresence  = "loom_fleet_get_presence"
	toolSessions  = "loom_fleet_get_sessions"
	toolStream    = "loom_fleet_get_stream"

	pathDashboard = "/api/mobile/v1/dashboard"
	pathPresence  = "/api/mobile/v1/presence"
	pathSessions  = "/api/mobile/v1/sessions"
	pathStream    = "/api/mobile/v1/stream"
)

// relayPaths is the HUD-path allowlist passed to hudClient.get. Each
// relay tool maps to exactly one path; the indirection keeps the
// allowlist enforced even if a future code change accidentally passes
// user input through.
var relayPaths = []string{pathDashboard, pathPresence, pathSessions, pathStream}

//go:embed widget.html
var widgetHTML []byte

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	srv := newServer(logger)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil && err != io.EOF {
		logger.Error("mcp-loom-widget: serve failed", "error", err)
		os.Exit(1)
	}
}

// rpcRequest is the inbound JSON-RPC 2.0 envelope. id is interface{}
// because the spec allows numbers, strings, or null (notifications).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse mirrors JSON-RPC 2.0. Exactly one of Result/Error is
// populated. Notifications produce no response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// server owns the stdio dispatch loop and the (currently single)
// widget resource. Safe for sequential dispatch; writes to stdout are
// serialized through writeMu so future async notifications don't
// interleave with responses.
type server struct {
	logger      *slog.Logger
	hud         *hudClient
	writeMu     sync.Mutex
	initialized bool
}

func newServer(logger *slog.Logger) *server {
	return &server{logger: logger, hud: newHUDClient()}
}

// newServerWithHUD lets tests inject a hudClient pointed at an
// httptest.NewServer; production code uses newServer.
func newServerWithHUD(logger *slog.Logger, hud *hudClient) *server {
	return &server{logger: logger, hud: hud}
}

// Serve runs the JSON-RPC 2.0 stdio loop until ctx is done or in
// reaches EOF. Each line on in is one JSON object; each response is
// one JSON object on out. The MCP transport requires no framing beyond
// newline-delimited JSON for stdio.
func (s *server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Allow up to 4 MiB per line — embedded base64 payloads can be
	// large, and MCP doesn't bound message size.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Copy because Bytes() reuses its buffer.
		raw := append([]byte(nil), line...)
		s.dispatch(raw, out)
	}
	return scanner.Err()
}

// dispatch parses one inbound message and routes it. Errors in
// envelope parsing produce a -32700 (parse error) response when id is
// recoverable; otherwise we log and drop. Method handlers return either
// a result or an *rpcError; notifications return nil to suppress output.
func (s *server) dispatch(raw []byte, out io.Writer) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		s.writeResponse(out, rpcResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
		})
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeResponse(out, rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32600, Message: "invalid request: jsonrpc must be 2.0"},
		})
		return
	}

	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	result, rpcErr := s.handle(req)

	if isNotification {
		return
	}

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	s.writeResponse(out, resp)
}

// handle is the method router. It returns (result, *rpcError) where
// exactly one is non-nil. Unknown methods produce -32601.
func (s *server) handle(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized", "notifications/initialized":
		// Spec calls this a notification; no response expected. We
		// flip our state flag here so subsequent methods know the
		// client finished the handshake. Unknown variants are tolerated.
		s.initialized = true
		return nil, nil
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(req)
	case "resources/list":
		return s.handleResourcesList()
	case "resources/read":
		return s.handleResourcesRead(req)
	case "ping":
		// MCP optional ping/pong; respond with an empty object.
		return map[string]any{}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

func (s *server) handleInitialize(_ rpcRequest) (any, *rpcError) {
	return map[string]any{
		"protocolVersion": "2025-06-18",
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
		},
	}, nil
}

// handleToolsList declares:
//   - loom_fleet_show — the user-facing widget-bearing tool (the host
//     renders the widget when this is invoked); _meta.ui.resourceUri
//     points to the embedded HTML bundle.
//   - loom_fleet_get_dashboard / _get_presence / _get_sessions — the
//     widget-facing relay tools. The widget calls these via the MCP
//     Apps ToolsCall bridge; this server fetches HUD on its behalf so
//     the LOOM_HUD_TOKEN never enters the LLM's context window.
func (s *server) handleToolsList() (any, *rpcError) {
	show := map[string]any{
		"name":        toolShow,
		"description": "Show the current loom fleet (active agents, sessions, tasks) as an inline widget.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		"_meta": map[string]any{
			"ui": map[string]any{
				"resourceUri": widgetURI,
			},
		},
	}
	dashboard := map[string]any{
		"name":        toolDashboard,
		"description": "Fetch the loom HUD mobile dashboard (relay; widget-facing). Returns the raw HUD JSON.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	presence := map[string]any{
		"name":        toolPresence,
		"description": "Fetch the loom HUD agent presence list (relay; widget-facing). Returns the raw HUD JSON.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	sessions := map[string]any{
		"name":        toolSessions,
		"description": "Fetch the loom HUD agent sessions list (relay; widget-facing). Returns the raw HUD JSON.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	stream := map[string]any{
		"name":        toolStream,
		"description": "Fetch the most recent loom HUD context stream entries (decisions, findings, tasks). Relay; widget-facing.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	return map[string]any{"tools": []any{show, dashboard, presence, sessions, stream}}, nil
}

// handleToolsCall routes the four tools:
//   - loom_fleet_show: returns a short text summary for the LLM and
//     the widget pointer in _meta so the host renders inline.
//   - loom_fleet_get_{dashboard,presence,sessions}: relay HUD JSON
//     back as a text content block. The widget uses this via the MCP
//     Apps bridge to refresh its data without ever seeing the bearer
//     token.
func (s *server) handleToolsCall(req rpcRequest) (any, *rpcError) {
	var params struct {
		Name string `json:"name"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
	}
	switch params.Name {
	case toolShow:
		return map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": "Loom fleet widget rendered inline.",
				},
			},
			"_meta": map[string]any{
				"ui": map[string]any{
					"resourceUri": widgetURI,
				},
			},
		}, nil
	case toolDashboard:
		return s.relay(pathDashboard)
	case toolPresence:
		return s.relay(pathPresence)
	case toolSessions:
		return s.relay(pathSessions)
	case toolStream:
		return s.relay(pathStream)
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + params.Name}
	}
}

// relay fetches one HUD path and returns the body as a text content
// block. Errors come back as an isError tool result so the widget can
// surface them without the JSON-RPC error code path (which the host
// might present as a hard failure rather than a recoverable state).
func (s *server) relay(path string) (any, *rpcError) {
	body, err := s.hud.get(context.Background(), path, relayPaths)
	if err != nil {
		s.logger.Warn("hud relay failed", "path", path, "error", err)
		return map[string]any{
			"content": []any{
				map[string]any{
					"type": "text",
					"text": "loom HUD relay failed: " + err.Error(),
				},
			},
			"isError": true,
		}, nil
	}
	return map[string]any{
		"content": []any{
			map[string]any{
				"type":     "text",
				"text":     string(body),
				"mimeType": "application/json",
			},
		},
	}, nil
}

func (s *server) handleResourcesList() (any, *rpcError) {
	return map[string]any{
		"resources": []any{
			map[string]any{
				"uri":         widgetURI,
				"name":        "Loom Fleet widget",
				"description": "Interactive loom fleet dashboard for MCP Apps hosts.",
				"mimeType":    widgetMimeType,
			},
		},
	}, nil
}

func (s *server) handleResourcesRead(req rpcRequest) (any, *rpcError) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	if params.URI != widgetURI {
		return nil, &rpcError{Code: -32602, Message: "unknown resource: " + params.URI}
	}
	return map[string]any{
		"contents": []any{
			map[string]any{
				"uri":      widgetURI,
				"mimeType": widgetMimeType,
				"text":     string(widgetHTML),
			},
		},
	}, nil
}

func (s *server) writeResponse(out io.Writer, resp rpcResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("marshal response failed", "error", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := fmt.Fprintf(out, "%s\n", data); err != nil {
		s.logger.Error("write response failed", "error", err)
	}
}

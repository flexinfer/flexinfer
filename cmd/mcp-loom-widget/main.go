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
	"strings"
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

	toolShow          = "loom_fleet_show"
	toolDashboard     = "loom_fleet_get_dashboard"
	toolPresence      = "loom_fleet_get_presence"
	toolSessions      = "loom_fleet_get_sessions"
	toolStream        = "loom_fleet_get_stream"
	toolHandoffs      = "loom_fleet_get_handoffs"
	toolHandoffAccept = "loom_fleet_handoff_accept"
	toolHandoffReject = "loom_fleet_handoff_reject"

	pathDashboard     = "/api/mobile/v1/dashboard"
	pathPresence      = "/api/mobile/v1/presence"
	pathSessions      = "/api/mobile/v1/sessions"
	pathStream        = "/api/mobile/v1/stream"
	pathHandoffs      = "/api/mobile/v1/handoffs"
	pathHandoffAccept = "/api/mobile/v1/handoffs/{handoff_id}/accept"
	pathHandoffReject = "/api/mobile/v1/handoffs/{handoff_id}/reject"
)

// relayPaths is the HUD-path allowlist passed to hudClient.get. Each
// GET relay tool maps to exactly one path; the indirection keeps the
// allowlist enforced even if a future code change accidentally passes
// user input through.
var relayPaths = []string{pathDashboard, pathPresence, pathSessions, pathStream, pathHandoffs}

// relayPostPaths is the matching allowlist for hudClient.post. These
// are TEMPLATES — placeholders like {handoff_id} are substituted from
// validated input before the request is built. See hudClient.post.
var relayPostPaths = []string{pathHandoffAccept, pathHandoffReject}

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
	handoffs := map[string]any{
		"name":        toolHandoffs,
		"description": "Fetch the loom HUD pending agent handoff inbox (cross-agent coordination). Relay; widget-facing.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	}
	handoffAccept := map[string]any{
		"name":        toolHandoffAccept,
		"description": "Accept a pending agent handoff. Either session_id or target_agent_id is required (target_agent_id auto-resolves the destination agent's active session). Relay; widget-facing.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"handoff_id":      map[string]any{"type": "string"},
				"session_id":      map[string]any{"type": "string"},
				"target_agent_id": map[string]any{"type": "string"},
				"import_entries":  map[string]any{"type": "boolean"},
			},
			"required": []string{"handoff_id"},
		},
	}
	handoffReject := map[string]any{
		"name":        toolHandoffReject,
		"description": "Reject a pending agent handoff. Optional reason is surfaced to the source agent. Relay; widget-facing.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"handoff_id": map[string]any{"type": "string"},
				"reason":     map[string]any{"type": "string"},
			},
			"required": []string{"handoff_id"},
		},
	}
	return map[string]any{"tools": []any{show, dashboard, presence, sessions, stream, handoffs, handoffAccept, handoffReject}}, nil
}

// handleToolsCall routes the tools the widget can invoke:
//   - loom_fleet_show: returns a short text summary for the LLM and
//     the widget pointer in _meta so the host renders inline.
//   - loom_fleet_get_{dashboard,presence,sessions,stream,handoffs}:
//     relay HUD JSON back as a text content block. The widget uses
//     these via the MCP Apps bridge to refresh data without ever
//     seeing the bearer token.
//   - loom_fleet_handoff_{accept,reject}: mutating relays that POST
//     to /api/mobile/v1/handoffs/{id}/{accept,reject}. The widget's
//     Accept/Reject buttons call these.
func (s *server) handleToolsCall(req rpcRequest) (any, *rpcError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
	}
	switch params.Name {
	case toolShow:
		return s.handleFleetShow()
	case toolDashboard:
		return s.relay(pathDashboard)
	case toolPresence:
		return s.relay(pathPresence)
	case toolSessions:
		return s.relay(pathSessions)
	case toolStream:
		return s.relay(pathStream)
	case toolHandoffs:
		return s.relay(pathHandoffs)
	case toolHandoffAccept:
		return s.relayHandoffMutation(pathHandoffAccept, params.Arguments,
			[]string{"session_id", "target_agent_id"},
			[]string{"handoff_id"},
			[]string{"session_id", "target_agent_id", "import_entries"})
	case toolHandoffReject:
		return s.relayHandoffMutation(pathHandoffReject, params.Arguments,
			nil,
			[]string{"handoff_id"},
			[]string{"reason"})
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + params.Name}
	}
}

// relayHandoffMutation is the shared body for Accept/Reject handoff
// tool calls. It extracts handoff_id, validates required arguments
// and any "at-least-one" semantic constraints, builds the POST body
// from the named keys, and dispatches via hudClient.post.
//
// requireAnyOf is satisfied if at least one of the named fields is
// present and non-empty (Accept needs session_id OR target_agent_id;
// Reject has no such constraint and passes nil).
//
// requireAll fields must all be present and non-empty.
//
// bodyKeys controls which arguments are forwarded as POST body
// fields; this acts as a tiny allowlist so unexpected keys don't
// leak through to the HUD.
func (s *server) relayHandoffMutation(template string, args map[string]any, requireAnyOf, requireAll, bodyKeys []string) (any, *rpcError) {
	handoffID := stringArg(args, "handoff_id")
	for _, req := range requireAll {
		if v := stringArg(args, req); v == "" {
			return nil, &rpcError{Code: -32602, Message: req + " is required"}
		}
	}
	if len(requireAnyOf) > 0 {
		ok := false
		for _, k := range requireAnyOf {
			if stringArg(args, k) != "" {
				ok = true
				break
			}
		}
		if !ok {
			return nil, &rpcError{Code: -32602, Message: "one of " + strings.Join(requireAnyOf, ", ") + " is required"}
		}
	}

	body := map[string]any{}
	for _, k := range bodyKeys {
		if v, ok := args[k]; ok && v != nil && v != "" {
			body[k] = v
		}
	}

	respBody, err := s.hud.post(context.Background(), template,
		map[string]string{"handoff_id": handoffID}, body, relayPostPaths)
	if err != nil {
		s.logger.Warn("hud handoff mutation failed", "template", template, "error", err)
		return map[string]any{
			"content": []any{map[string]any{
				"type": "text",
				"text": "loom HUD mutation failed: " + err.Error(),
			}},
			"isError": true,
		}, nil
	}
	return map[string]any{
		"content": []any{map[string]any{
			"type":     "text",
			"text":     string(respBody),
			"mimeType": "application/json",
		}},
	}, nil
}

// stringArg pulls a string from MCP tool arguments without crashing
// on missing keys or wrong types. Returns "" when absent or non-string.
func stringArg(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// handleFleetShow returns BOTH a markdown text summary of the live
// HUD dashboard AND the MCP Apps widget pointer in _meta. Hosts that
// render MCP Apps widgets (Claude.ai, Claude Desktop chat-only,
// ChatGPT, VS Code+Copilot, MCP Inspector) show the widget; hosts
// that don't render widgets (Claude Code in any flavor, as of
// 2026-05-17) show the markdown summary inline. Both surfaces stay
// useful from one tool call.
//
// Slice 2-γ remediation: was previously returning only the assertive
// text "Loom fleet widget rendered inline." which mis-claimed
// successful render in hosts that ignored the widget pointer. See
// .loom/brainstorm-widget-rendering-breakdown-2026-05-17.md.
func (s *server) handleFleetShow() (any, *rpcError) {
	body, err := s.hud.get(context.Background(), pathDashboard, relayPaths)
	text := renderFleetMarkdown(body, err)
	return map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": text,
			},
		},
		"_meta": map[string]any{
			"ui": map[string]any{
				"resourceUri": widgetURI,
			},
		},
	}, nil
}

// renderFleetMarkdown formats the mobile-api dashboard envelope as a
// compact markdown summary. When the HUD fetch fails (network, auth,
// CF Access, etc.) returns a clear diagnostic block instead — the
// host still shows it inline, the user knows the relay failed, and
// they get the URL to investigate. Never returns an empty string.
func renderFleetMarkdown(body []byte, fetchErr error) string {
	header := "**Loom Fleet**"
	if fetchErr != nil {
		return header + "\n\n" +
			"⚠️ Could not reach the loom HUD: `" + fetchErr.Error() + "`\n\n" +
			"_If this host renders MCP Apps widgets, the inline widget above may still work " +
			"with mock data. Otherwise, check `LOOM_HUD_URL` / `LOOM_HUD_TOKEN` / " +
			"`LOOM_HUD_CF_ACCESS_*` env on the mcp-loom-widget process._"
	}

	// envelope is the {ok, data, meta} wrapper from
	// internal/hud/domain/mobile/auth.go writeMobileJSON. Tolerates
	// unwrapped bodies for forward compatibility.
	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return header + "\n\n⚠️ HUD response not parseable: `" + err.Error() + "`"
	}
	payload := env.Data
	if payload == nil {
		// Maybe the HUD returned an unwrapped object; try parsing
		// the raw body as the dashboard payload directly.
		payload = body
	}
	if !env.OK && env.Error.Code != "" {
		return header + "\n\n⚠️ HUD returned an error: `" + env.Error.Code + ": " + env.Error.Message + "`"
	}

	var dash struct {
		DaemonRunning  bool   `json:"daemon_running"`
		ServerCount    int    `json:"server_count"`
		ActiveSessions int    `json:"active_sessions"`
		ActiveAgents   int    `json:"active_agents"`
		IdleAgents     int    `json:"idle_agents"`
		OfflineAgents  int    `json:"offline_agents"`
		UpdatedAt      string `json:"updated_at"`
		Health         struct {
			TotalServers    int `json:"total_servers"`
			HealthyServers  int `json:"healthy_servers"`
			DegradedServers int `json:"degraded_servers"`
			DownServers     int `json:"down_servers"`
		} `json:"health"`
		Spawns struct {
			Active int `json:"active"`
			Total  int `json:"total"`
		} `json:"spawns"`
		LastHeartbeat struct {
			AgentID   string `json:"agent_id"`
			Timestamp string `json:"timestamp"`
			Count1h   int    `json:"count_1h"`
		} `json:"last_heartbeat"`
	}
	if err := json.Unmarshal(payload, &dash); err != nil {
		return header + "\n\n⚠️ Dashboard payload not parseable: `" + err.Error() + "`"
	}

	var sb strings.Builder
	sb.WriteString(header)
	totalAgents := dash.ActiveAgents + dash.IdleAgents + dash.OfflineAgents
	sb.WriteString(fmt.Sprintf(" · %d agents tracked, %d live sessions\n\n", totalAgents, dash.ActiveSessions))

	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|---|---|\n")
	daemonState := "running"
	if !dash.DaemonRunning {
		daemonState = "down"
	}
	sb.WriteString(fmt.Sprintf("| Daemon | %s |\n", daemonState))
	sb.WriteString(fmt.Sprintf("| Agents | %d active · %d idle · %d offline |\n",
		dash.ActiveAgents, dash.IdleAgents, dash.OfflineAgents))
	sb.WriteString(fmt.Sprintf("| Sessions | %d active |\n", dash.ActiveSessions))
	sb.WriteString(fmt.Sprintf("| MCP servers | %d total |\n", dash.ServerCount))
	if dash.Health.TotalServers > 0 {
		sb.WriteString(fmt.Sprintf("| Server health | %d healthy · %d degraded · %d down |\n",
			dash.Health.HealthyServers, dash.Health.DegradedServers, dash.Health.DownServers))
	}
	if dash.Spawns.Total > 0 || dash.Spawns.Active > 0 {
		sb.WriteString(fmt.Sprintf("| Spawns | %d active · %d total |\n", dash.Spawns.Active, dash.Spawns.Total))
	}
	if dash.LastHeartbeat.AgentID != "" {
		sb.WriteString(fmt.Sprintf("| Last heartbeat | `%s` · %d/h |\n",
			dash.LastHeartbeat.AgentID, dash.LastHeartbeat.Count1h))
	}

	sb.WriteString("\n_Hosts that render MCP Apps widgets show an interactive ")
	sb.WriteString("version of this above (Claude.ai web, Claude Desktop, ChatGPT, ")
	sb.WriteString("MCP Inspector). Claude Code shows this markdown summary; the ")
	sb.WriteString("widget renderer is a vendor gap, not a wire-format bug._")
	return sb.String()
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

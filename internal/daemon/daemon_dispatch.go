package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (d *Daemon) handleMessage(ctx context.Context, msg *mcp.Message) (resp *mcp.Message, err error) {
	if msg == nil {
		err = fmt.Errorf("nil message")
		return nil, err
	}

	attrs := []attribute.KeyValue{
		attribute.String("mcp.method", msg.Method),
	}
	if msg.ID != nil {
		attrs = append(attrs, attribute.String("mcp.request_id", fmt.Sprint(msg.ID)))
	}

	ctx, span := d.daemonTracer().Start(ctx, "daemon.rpc."+msg.Method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
	defer func() {
		span.SetAttributes(attribute.Bool("loom.has_response", resp != nil))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	switch msg.Method {
	case "initialize":
		resp, err = d.handleInitialize(ctx, msg)
	case "notifications/initialized":
		resp, err = nil, nil
	case "loom/status":
		resp, err = d.handleStatus(ctx, msg)
	case "loom/servers":
		resp, err = d.handleServers(ctx, msg)
	case "loom/health":
		resp, err = d.handleHealth(ctx, msg)
	case "loom/tools":
		resp, err = d.handleTools(ctx, msg)
	case "loom/tools/search":
		resp, err = d.handleToolsSearch(ctx, msg)
	case "loom/tools/get":
		resp, err = d.handleToolGet(ctx, msg)
	case "loom/resources":
		resp, err = d.handleResources(ctx, msg)
	case "loom/call", "tools/call":
		resp, err = d.handleCall(ctx, msg)
	case "loom/reload":
		resp, err = d.handleReload(ctx, msg)
	case "loom/config-hash":
		resp, err = d.handleConfigHash(ctx, msg)
	case "loom/profile":
		resp, err = d.handleProfile(ctx, msg)
	case "loom/tunnels":
		resp, err = d.handleTunnels(ctx, msg)
	case "loom/cache/stats":
		resp, err = d.handleCacheStats(ctx, msg)
	case "loom/cache/clear":
		resp, err = d.handleCacheClear(ctx, msg)
	case "loom/cost-stats":
		resp, err = d.handleCostStats(ctx, msg)
	case "loom/rbac-config":
		resp, err = d.handleRBACConfig(ctx, msg)
	case "loom/rbac-simulate":
		resp, err = d.handleRBACSimulate(ctx, msg)
	case "loom/otel-status":
		resp, err = d.handleOTelStatus(ctx, msg)
	case "loom/session/open":
		resp, err = d.handleSessionOpen(ctx, msg)
	case "loom/session/heartbeat":
		resp, err = d.handleSessionHeartbeat(ctx, msg)
	case "loom/session/status":
		resp, err = d.handleSessionStatus(ctx, msg)
	case "loom/session/close":
		resp, err = d.handleSessionClose(ctx, msg)
	default:
		resp = mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, fmt.Sprintf("unknown method: %s", msg.Method))
	}
	return resp, err
}

func (d *Daemon) handleInitialize(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	result := mcp.InitializeResult{
		ProtocolVersion: negotiateProtocolVersion(msg.Params),
		Capabilities:    mcp.Capabilities{},
		ServerInfo: mcp.ServerInfo{
			Name:    "loom",
			Version: "0.1.0",
		},
		Instructions: "Loom daemon - unified MCP hub management",
	}
	return mcp.NewResponse(msg.ID, result)
}

func negotiateProtocolVersion(raw json.RawMessage) string {
	defaultVersion := mcp.ProtocolVersion20250618

	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return defaultVersion
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return defaultVersion
	}

	requested := strings.TrimSpace(params.ProtocolVersion)
	switch requested {
	case mcp.ProtocolVersion20250618, mcp.ProtocolVersion:
		return requested
	default:
		return defaultVersion
	}
}

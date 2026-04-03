package daemon

import (
	"context"
	"encoding/json"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/hubproto"
)

// hubKeepaliveLoop periodically probes the hub WebSocket connection to detect
// dead connections before agents encounter them during tool calls.
// It uses the configured PingIntervalSeconds (default 30s) as the probe interval.
func (d *Daemon) hubKeepaliveLoop() {
	defer d.wg.Done()

	interval := time.Duration(d.fileCfg.Hub.PingIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.hubKeepalivePing()
		}
	}
}

// hubKeepalivePing borrows a connection from the hub pool, sends a lightweight
// tools/list probe wrapped in a DomainControl envelope, and observes the result.
// On failure, the connection is marked unhealthy and the pool/client are cleaned up.
// For backward compatibility, it gracefully handles raw (non-envelope) pong responses.
func (d *Daemon) hubKeepalivePing() {
	if d.hubPool == nil || d.hubClient == nil {
		return
	}

	// Use a well-known server name from the hub pool stats to probe.
	// If the pool has no idle connections, skip — nothing to keep alive.
	stats := d.hubPool.Stats()
	if stats.IdleConns == 0 {
		return
	}

	// Pick any hub-capable server from the registry to probe.
	serverName := d.pickHubServer()
	if serverName == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, span := d.daemonTracer().Start(ctx, "daemon.hub.keepalive",
		trace.WithAttributes(attribute.String("mcp.server", serverName)),
	)
	defer span.End()

	conn, err := d.hubPool.Get(ctx, serverName)
	if err != nil {
		span.AddEvent("daemon.hub.keepalive.skip", trace.WithAttributes(
			attribute.String("reason", "no connection available"),
		))
		d.logger.Debug("hub keepalive: no connection available", "server", serverName, "error", err)
		return
	}

	// Build the inner MCP probe request.
	req, _ := mcp.NewRequest(1, "tools/list", json.RawMessage(`{}`))

	// Wrap the probe in a DomainControl envelope with method "ping".
	pingEnv := d.buildControlPing(req)

	sendCtx, sendCancel := context.WithTimeout(ctx, 5*time.Second)
	sendErr := conn.Transport.Send(sendCtx, pingEnv)
	sendCancel()
	if sendErr != nil {
		span.AddEvent("daemon.hub.keepalive.send_fail", trace.WithAttributes(
			attribute.String("error", sendErr.Error()),
		))
		d.logger.Warn("hub keepalive: send failed, clearing connection",
			"server", serverName, "error", sendErr)
		conn.Healthy = false
		d.hubPool.Put(conn)
		d.hubPool.ClearServer(serverName)
		d.hubClient.CloseConnection(serverName)
		return
	}

	recvCtx, recvCancel := context.WithTimeout(ctx, 5*time.Second)
	resp, recvErr := conn.Transport.Recv(recvCtx)
	recvCancel()
	if recvErr != nil {
		span.AddEvent("daemon.hub.keepalive.recv_fail", trace.WithAttributes(
			attribute.String("error", recvErr.Error()),
		))
		d.logger.Warn("hub keepalive: recv failed, clearing connection",
			"server", serverName, "error", recvErr)
		conn.Healthy = false
		d.hubPool.Put(conn)
		d.hubPool.ClearServer(serverName)
		d.hubClient.CloseConnection(serverName)
		return
	}

	// Backward compat: accept both envelope-wrapped and raw MCP responses.
	d.handlePongResponse(resp)

	// Success: return healthy connection to pool (keeps it warm).
	span.AddEvent("daemon.hub.keepalive.success")
	d.hubPool.Put(conn)
	d.logger.Debug("hub keepalive: probe succeeded", "server", serverName)
}

// buildControlPing wraps an MCP probe request in a DomainControl "ping"
// envelope and returns it as an MCP message for transport.
func (d *Daemon) buildControlPing(inner *mcp.Message) *mcp.Message {
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		// Fallback: send the raw request if marshaling fails.
		d.logger.Debug("hub keepalive: failed to marshal inner request, sending raw", "error", err)
		return inner
	}

	env := &hubproto.Envelope{
		Domain:    hubproto.DomainControl,
		Method:    "ping",
		RequestID: "keepalive",
		Payload:   json.RawMessage(innerBytes),
		Source:    "daemon",
		Timestamp: time.Now().UTC(),
	}

	envBytes, err := hubproto.Encode(env)
	if err != nil {
		d.logger.Debug("hub keepalive: failed to encode envelope, sending raw", "error", err)
		return inner
	}

	// Wrap the envelope JSON as the params of a synthetic MCP message so it
	// can be sent over the existing MCP transport layer.
	msg, _ := mcp.NewRequest(0, "hub/envelope", json.RawMessage(envBytes))
	return msg
}

// handlePongResponse processes a keepalive response, accepting both
// envelope-wrapped pongs and raw MCP responses for backward compatibility.
func (d *Daemon) handlePongResponse(resp *mcp.Message) {
	if resp == nil {
		return
	}

	// Try to decode as an envelope response. If the hub supports envelopes,
	// the response will be a hub/envelope method with an envelope payload.
	if resp.Method == "hub/envelope" && resp.Result != nil {
		env, err := hubproto.Decode(resp.Result)
		if err == nil && env.Domain == hubproto.DomainControl {
			d.logger.Debug("hub keepalive: received envelope pong",
				"method", env.Method, "request_id", env.RequestID)
			return
		}
	}

	// Backward compat: raw MCP response (non-envelope hub).
	d.logger.Debug("hub keepalive: received raw pong (non-envelope)")
}

// pickHubServer returns the name of a hub-capable server from the registry.
// Returns "" if no hub servers exist.
func (d *Daemon) pickHubServer() string {
	if d.registry == nil {
		return ""
	}
	for _, srv := range d.registry.Servers {
		if srv == nil || srv.IsLocalOnly() {
			continue
		}
		return srv.Name
	}
	return ""
}

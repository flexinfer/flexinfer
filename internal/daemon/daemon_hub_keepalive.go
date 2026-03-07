package daemon

import (
	"context"
	"encoding/json"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
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
// tools/list probe, and observes the result. On failure, the connection is
// marked unhealthy and the pool/client are cleaned up.
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

	conn, err := d.hubPool.Get(ctx, serverName)
	if err != nil {
		d.logger.Debug("hub keepalive: no connection available", "server", serverName, "error", err)
		return
	}

	// Send a lightweight tools/list probe.
	req, _ := mcp.NewRequest(1, "tools/list", json.RawMessage(`{}`))

	sendCtx, sendCancel := context.WithTimeout(ctx, 5*time.Second)
	sendErr := conn.Transport.Send(sendCtx, req)
	sendCancel()
	if sendErr != nil {
		d.logger.Warn("hub keepalive: send failed, clearing connection",
			"server", serverName, "error", sendErr)
		conn.Healthy = false
		d.hubPool.Put(conn)
		d.hubPool.ClearServer(serverName)
		d.hubClient.CloseConnection(serverName)
		return
	}

	recvCtx, recvCancel := context.WithTimeout(ctx, 5*time.Second)
	_, recvErr := conn.Transport.Recv(recvCtx)
	recvCancel()
	if recvErr != nil {
		d.logger.Warn("hub keepalive: recv failed, clearing connection",
			"server", serverName, "error", recvErr)
		conn.Healthy = false
		d.hubPool.Put(conn)
		d.hubPool.ClearServer(serverName)
		d.hubClient.CloseConnection(serverName)
		return
	}

	// Success: return healthy connection to pool (keeps it warm).
	d.hubPool.Put(conn)
	d.logger.Debug("hub keepalive: probe succeeded", "server", serverName)
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

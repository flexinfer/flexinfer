// proxy_session.go — daemon session lease, epoch tracking, and keepalive logic.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// proxyOpenSession opens a session with the daemon after a successful initialize.
// Non-blocking and non-fatal: older daemons that don't support sessions will return
// method_not_found, which is silently ignored.
func proxyOpenSession(ctx context.Context, transport mcp.Transport) {
	if proxySessionDisabled {
		return
	}

	openParams := map[string]any{
		"version":  version,
		"host_pid": strconv.Itoa(os.Getpid()),
	}
	if agentHintGlobal != "" {
		openParams["agent_hint"] = agentHintGlobal
	}
	if proxySessionID != "" {
		openParams["prior_session_id"] = proxySessionID
	}

	req, _ := mcp.NewRequest(99, "loom/session/open", openParams)
	resp, err := proxyDaemonRoundTrip(ctx, transport, req, "loom/session/open")
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom proxy: session open failed: %v\n", err)
		return
	}

	// Older daemons return method_not_found -- silently ignore.
	if resp.Error != nil {
		return
	}

	var result struct {
		SessionID   string `json:"session_id"`
		DaemonEpoch int64  `json:"daemon_epoch"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return
	}

	proxySessionID = result.SessionID
	proxyDaemonEpoch = result.DaemonEpoch
}

// proxySessionHeartbeat sends a keepalive heartbeat to the daemon to extend the
// session lease. On rejection (expired session or epoch mismatch), the session ID
// is cleared so the next ensureDaemon() re-opens a fresh session.
func proxySessionHeartbeat(ctx context.Context, transport mcp.Transport) {
	if proxySessionID == "" || proxySessionDisabled {
		return
	}

	req, _ := mcp.NewRequest(97, "loom/session/heartbeat", map[string]any{
		"session_id":   proxySessionID,
		"daemon_epoch": proxyDaemonEpoch,
	})

	resp, err := proxyDaemonRoundTrip(ctx, transport, req, "loom/session/heartbeat")
	if err != nil {
		fmt.Fprintf(os.Stderr, "loom proxy: session heartbeat failed: %v\n", err)
		return
	}

	// On error response (expired session, epoch mismatch, method_not_found),
	// clear session so the next request re-opens.
	if resp.Error != nil {
		fmt.Fprintf(os.Stderr, "loom proxy: session heartbeat rejected: %s\n", resp.Error.Message)
		proxySessionID = ""
		return
	}
}

// nextSessionKeepaliveInterval returns the appropriate keepalive interval
// based on whether the proxy has recently forwarded a tool call.
func nextSessionKeepaliveInterval() time.Duration {
	last := lastProxyCallTime.Load()
	if last == 0 {
		return sessionKeepaliveIdle
	}
	elapsed := time.Since(time.Unix(0, last))
	if elapsed < sessionIdleThreshold {
		return sessionKeepaliveActive
	}
	return sessionKeepaliveIdle
}

// proxyCloseSession sends a graceful session close to the daemon with a short timeout.
// The prior session ID is preserved so the next open can pass it for resume tracking.
func proxyCloseSession(ctx context.Context, transport mcp.Transport) {
	if proxySessionID == "" {
		return
	}

	req, _ := mcp.NewRequest(98, "loom/session/close", map[string]any{
		"session_id": proxySessionID,
	})

	closeCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	// Best-effort close; ignore errors because shutdown may already be tearing
	// down either side of the transport.
	_, _ = proxyDaemonRoundTrip(closeCtx, transport, req, "loom/session/close")

	// proxySessionID is preserved as prior_session_id for the next open call.
}

package daemon

import (
	"context"
	"encoding/json"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// --- loom/session/open ---

type sessionOpenParams struct {
	AgentHint      string `json:"agent_hint,omitempty"`
	HostPID        string `json:"host_pid,omitempty"`
	Version        string `json:"version,omitempty"`
	PriorSessionID string `json:"prior_session_id,omitempty"`
}

type sessionOpenResult struct {
	SessionID   string `json:"session_id"`
	DaemonEpoch int64  `json:"daemon_epoch"`
	LeaseSecs   int    `json:"lease_seconds"`
}

func (d *Daemon) handleSessionOpen(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.sessions == nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "session manager not initialized"), nil
	}

	var params sessionOpenParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}

	sess := d.sessions.Open(SessionClientInfo{
		AgentHint: params.AgentHint,
		HostPID:   params.HostPID,
		Version:   params.Version,
	}, params.PriorSessionID)

	d.logger.Info("proxy session opened",
		"session_id", sess.ID,
		"prior_id", sess.PriorID,
		"epoch", sess.DaemonEpoch,
		"agent_hint", params.AgentHint,
	)

	return mcp.NewResponse(msg.ID, sessionOpenResult{
		SessionID:   sess.ID,
		DaemonEpoch: sess.DaemonEpoch,
		LeaseSecs:   int(d.sessions.leaseTime.Seconds()),
	})
}

// --- loom/session/heartbeat ---

type sessionHeartbeatParams struct {
	SessionID   string `json:"session_id"`
	DaemonEpoch int64  `json:"daemon_epoch"`
}

type sessionHeartbeatResult struct {
	SessionID   string `json:"session_id"`
	DaemonEpoch int64  `json:"daemon_epoch"`
	LeaseSecs   int    `json:"lease_seconds"`
	State       string `json:"state"`
}

func (d *Daemon) handleSessionHeartbeat(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.sessions == nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "session manager not initialized"), nil
	}

	var params sessionHeartbeatParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}

	if params.SessionID == "" {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "missing session_id"), nil
	}

	sess, err := d.sessions.Heartbeat(params.SessionID, params.DaemonEpoch)
	if err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidRequest, err.Error()), nil
	}

	return mcp.NewResponse(msg.ID, sessionHeartbeatResult{
		SessionID:   sess.ID,
		DaemonEpoch: sess.DaemonEpoch,
		LeaseSecs:   int(d.sessions.leaseTime.Seconds()),
		State:       string(sess.State),
	})
}

// --- loom/session/status ---

type sessionStatusResult struct {
	DaemonEpoch    int64  `json:"daemon_epoch"`
	ActiveSessions int    `json:"active_sessions"`
	TotalSessions  int    `json:"total_sessions"`
	DrainState     string `json:"drain_state"`
}

func (d *Daemon) handleSessionStatus(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.sessions == nil {
		return mcp.NewResponse(msg.ID, sessionStatusResult{
			DaemonEpoch: d.daemonEpoch,
			DrainState:  "none",
		})
	}

	return mcp.NewResponse(msg.ID, sessionStatusResult{
		DaemonEpoch:    d.daemonEpoch,
		ActiveSessions: d.sessions.ActiveCount(),
		TotalSessions:  d.sessions.Count(),
		DrainState:     "none",
	})
}

// --- loom/session/close ---

type sessionCloseParams struct {
	SessionID string `json:"session_id"`
}

type sessionCloseResult struct {
	Closed bool `json:"closed"`
}

func (d *Daemon) handleSessionClose(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.sessions == nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "session manager not initialized"), nil
	}

	var params sessionCloseParams
	if msg.Params != nil {
		_ = json.Unmarshal(msg.Params, &params)
	}

	if params.SessionID == "" {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "missing session_id"), nil
	}

	closed := d.sessions.Close(params.SessionID)
	if closed {
		d.logger.Info("proxy session closed", "session_id", params.SessionID)
	}

	return mcp.NewResponse(msg.ID, sessionCloseResult{Closed: closed})
}

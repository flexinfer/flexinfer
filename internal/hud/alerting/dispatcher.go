package alerting

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// SSEBroadcaster sends events to connected browser clients.
type SSEBroadcaster interface {
	Broadcast(event bridge.SSEEvent)
}

// PushBridgeOps handles mobile push notifications.
type PushBridgeOps interface {
	HandleEvent(event bridge.SSEEvent)
}

// NudgeQueueOps enqueues advice nudges to agents.
type NudgeQueueOps interface {
	QueueNudge(agentID, nudgeType, lane, content, fromAgent string) string
}

// AgentLister returns active agent IDs for nudge dispatch.
type AgentLister interface {
	ActiveAgentIDs() []string
}

// Dispatcher routes alerts to SSE hub, mobile push, and agent nudge queue.
type Dispatcher struct {
	sse        SSEBroadcaster
	push       PushBridgeOps
	nudgeQueue NudgeQueueOps
	agents     AgentLister
	logger     *slog.Logger
}

// NewDispatcher creates a Dispatcher. Any dependency may be nil (skipped).
func NewDispatcher(
	sse SSEBroadcaster,
	push PushBridgeOps,
	nudgeQueue NudgeQueueOps,
	agents AgentLister,
	logger *slog.Logger,
) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		sse:        sse,
		push:       push,
		nudgeQueue: nudgeQueue,
		agents:     agents,
		logger:     logger.With("component", "alert-dispatcher"),
	}
}

// Dispatch sends an alert to all configured channels.
func (d *Dispatcher) Dispatch(alert Alert) {
	// Build SSE event payload.
	data, err := json.Marshal(alert)
	if err != nil {
		d.logger.Warn("alerting: failed to marshal alert for dispatch", "error", err)
		return
	}

	event := bridge.SSEEvent{
		ID:        fmt.Sprintf("pipeline.alert-%s-%d", alert.ID, time.Now().UnixMilli()),
		Type:      "pipeline.alert",
		Timestamp: alert.FiredAt,
		Data:      data,
	}

	// Broadcast to browser clients via SSE.
	if d.sse != nil {
		d.sse.Broadcast(event)
	}

	// Trigger mobile push for critical alerts.
	if d.push != nil && alert.Severity == "critical" {
		d.push.HandleEvent(event)
	}

	// Enqueue nudge to all active agents.
	if d.nudgeQueue != nil && d.agents != nil {
		content := fmt.Sprintf("[Pipeline Alert] %s: %s (severity=%s, pipeline=%d, project=%s)",
			alert.Title, alert.Message, alert.Severity, alert.Pipeline.ID, alert.Pipeline.Project)

		for _, agentID := range d.agents.ActiveAgentIDs() {
			d.nudgeQueue.QueueNudge(agentID, "message", "advice", content, "hud")
		}
	}

	d.logger.Info("alert dispatched",
		"alert_id", alert.ID,
		"severity", alert.Severity,
		"pipeline", alert.Pipeline.ID,
	)
}

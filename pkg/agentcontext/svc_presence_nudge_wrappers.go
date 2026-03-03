package agentcontext

import "go.opentelemetry.io/otel/trace"

// Tracer returns the service's OTel tracer. Returns a noop tracer if none was configured.
func (s *Service) Tracer() trace.Tracer {
	return s.tracer
}

// OnPresenceEvent registers a callback invoked when an agent's presence
// state transitions (e.g., active -> idle, idle -> offline).
func (s *Service) OnPresenceEvent(fn func(eventType string, agentID string, oldStatus, newStatus PresenceStatus)) {
	s.presence.SetOnEvent(fn)
}

// AddNudge enqueues a nudge for the given agent, delivered on next heartbeat.
func (s *Service) AddNudge(agentID string, nudge *Nudge) { s.nudges.Add(agentID, nudge) }

// DrainNudges returns and clears all pending nudges for the given agent.
func (s *Service) DrainNudges(agentID string) []*Nudge { return s.nudges.Drain(agentID) }

// PendingNudgeCount returns the number of pending nudges for the given agent.
func (s *Service) PendingNudgeCount(agentID string) int { return s.nudges.PendingCount(agentID) }

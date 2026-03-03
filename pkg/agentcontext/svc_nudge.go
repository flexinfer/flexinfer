package agentcontext

import "sync"

// NudgeSvc manages a per-agent nudge queue, delivered on next heartbeat.
type NudgeSvc struct {
	mu     sync.Mutex
	nudges map[string][]*Nudge // agentID -> pending nudges
}

// NewNudgeSvc creates a new NudgeSvc.
func NewNudgeSvc() *NudgeSvc {
	return &NudgeSvc{
		nudges: make(map[string][]*Nudge),
	}
}

// Add enqueues a nudge for the given agent.
func (ns *NudgeSvc) Add(agentID string, nudge *Nudge) {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	ns.nudges[agentID] = append(ns.nudges[agentID], nudge)
}

// Drain returns and clears all pending nudges for the given agent.
func (ns *NudgeSvc) Drain(agentID string) []*Nudge {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	nudges := ns.nudges[agentID]
	delete(ns.nudges, agentID)
	return nudges
}

// PendingCount returns the number of pending nudges for the given agent.
func (ns *NudgeSvc) PendingCount(agentID string) int {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return len(ns.nudges[agentID])
}

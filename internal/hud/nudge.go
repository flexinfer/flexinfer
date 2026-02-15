// nudge.go implements the agent nudge queue for the HUD.
//
// Nudges are messages or directives queued for agents, delivered via
// heartbeat response. This enables the HUD to asynchronously communicate
// with agents without requiring real-time push channels.
package hud

import (
	"fmt"
	"sync"
	"time"
)

// NudgeEntry is a pending nudge for an agent, delivered via heartbeat.
type NudgeEntry struct {
	ID        string `json:"id"`
	Type      string `json:"type"`       // context_inject, task_redirect, pause_request, message
	Content   string `json:"content"`    // nudge payload
	FromAgent string `json:"from_agent"` // source: "hud" or another agent ID
	CreatedAt string `json:"created_at"` // RFC3339
}

// NudgeQueue manages pending nudges per agent.
type NudgeQueue struct {
	mu     sync.Mutex
	nudges map[string][]NudgeEntry // agentID -> pending nudges
}

// NewNudgeQueue creates an empty nudge queue.
func NewNudgeQueue() *NudgeQueue {
	return &NudgeQueue{nudges: make(map[string][]NudgeEntry)}
}

// Add enqueues a nudge for an agent, delivered on next heartbeat.
func (q *NudgeQueue) Add(agentID string, entry NudgeEntry) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nudges[agentID] = append(q.nudges[agentID], entry)
}

// Drain returns and clears all pending nudges for an agent.
func (q *NudgeQueue) Drain(agentID string) []NudgeEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	nudges := q.nudges[agentID]
	delete(q.nudges, agentID)
	return nudges
}

// Count returns the number of pending nudges for an agent.
func (q *NudgeQueue) Count(agentID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.nudges[agentID])
}

// NewNudgeID generates a unique nudge ID.
func NewNudgeID(targetAgent string) string {
	return fmt.Sprintf("nudge-%s-%d", targetAgent, time.Now().UnixMilli())
}

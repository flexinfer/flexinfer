package hud

import (
	"encoding/json"
	"sync"
	"time"
)

// TimelineEntry represents a single event in the activity timeline.
type TimelineEntry struct {
	Timestamp time.Time       `json:"timestamp"`
	EventType string          `json:"event_type"`
	AgentID   string          `json:"agent_id,omitempty"`
	AgentType string          `json:"agent_type,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// EventLog is a thread-safe ring buffer that stores recent agent lifecycle events
// for the unified activity timeline.
type EventLog struct {
	mu      sync.RWMutex
	entries []TimelineEntry
	head    int  // next write position
	full    bool // true once the buffer has wrapped
	cap     int
}

// NewEventLog creates an EventLog with the given capacity.
func NewEventLog(capacity int) *EventLog {
	if capacity <= 0 {
		capacity = 1000
	}
	return &EventLog{
		entries: make([]TimelineEntry, capacity),
		cap:     capacity,
	}
}

// Append adds an entry to the ring buffer.
func (l *EventLog) Append(entry TimelineEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.entries[l.head] = entry
	l.head = (l.head + 1) % l.cap
	if l.head == 0 {
		l.full = true
	}
}

// count returns the number of stored entries (caller must hold lock).
func (l *EventLog) count() int {
	if l.full {
		return l.cap
	}
	return l.head
}

// snapshot returns all stored entries newest-first (caller must hold lock).
func (l *EventLog) snapshot() []TimelineEntry {
	n := l.count()
	if n == 0 {
		return nil
	}
	result := make([]TimelineEntry, n)
	for i := 0; i < n; i++ {
		// Walk backwards from the most recent entry.
		idx := (l.head - 1 - i + l.cap) % l.cap
		result[i] = l.entries[idx]
	}
	return result
}

// Since returns entries newer than t, sorted newest-first, up to limit.
func (l *EventLog) Since(t time.Time, limit int) []TimelineEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	all := l.snapshot()
	var filtered []TimelineEntry
	for _, e := range all {
		if !e.Timestamp.After(t) {
			break // entries are newest-first, so once we pass t we're done
		}
		filtered = append(filtered, e)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	return filtered
}

// All returns the most recent entries up to limit, newest-first.
func (l *EventLog) All(limit int) []TimelineEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	all := l.snapshot()
	if limit > 0 && len(all) > limit {
		return all[:limit]
	}
	return all
}

// Len returns the number of stored entries.
func (l *EventLog) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.count()
}

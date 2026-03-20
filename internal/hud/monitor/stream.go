package monitor

import (
	"log/slog"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// StreamEntry is the flat shape broadcast to frontends via SSE.
type StreamEntry struct {
	ID        string  `json:"id"`
	EntryType string  `json:"entry_type"`
	AgentID   string  `json:"agent_id"`
	Agent     string  `json:"agent"`
	Namespace string  `json:"namespace"`
	Title     string  `json:"title"`
	Content   string  `json:"content,omitempty"`
	Timestamp string  `json:"timestamp"`
	Score     float64 `json:"score,omitempty"`
}

const maxEntries = 500

// StreamMonitor polls the agent context stream and maintains a cached,
// deduplicated list of recent entries. It broadcasts only new (delta)
// entries via the OnRefresh callback.
type StreamMonitor struct {
	BaseMonitor[[]StreamEntry]
	agent    *bridge.AgentBridge
	seen     map[string]struct{} // dedup by entry ID
	lastPoll time.Time           // "since" watermark for incremental fetching
}

// NewStreamMonitor creates a StreamMonitor backed by the given agent bridge.
func NewStreamMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *StreamMonitor {
	m := &StreamMonitor{
		agent: agent,
		seen:  make(map[string]struct{}),
	}
	m.InitBase(logger, nil, "stream-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *StreamMonitor) Start(interval time.Duration) {
	// Use StartManual because StreamMonitor has custom refresh semantics
	// (delta-based OnRefresh, watermark tracking) that do not fit the
	// standard RefreshFunc pattern where Update replaces the snapshot.
	m.StartManual()
	go func() {
		if err := m.Refresh(); err != nil {
			m.Logger.Warn("initial stream refresh failed", "error", err)
		}
	}()
	go m.pollLoop(interval)
}

// Entries returns a thread-safe copy of the cached entries (most-recent-first).
func (m *StreamMonitor) Entries() []StreamEntry {
	m.RLock()
	defer m.RUnlock()

	snap := m.GetSnapshot()
	cp := make([]StreamEntry, len(snap))
	copy(cp, snap)
	return cp
}

// Refresh polls the agent bridge for new context entries since the last
// watermark, deduplicates, and broadcasts the delta.
func (m *StreamMonitor) Refresh() error {
	raw, err := m.agent.ContextStream(m.lastPoll, 100)
	if err != nil {
		m.Logger.Warn("stream: failed to fetch context stream", "error", err)
		return err
	}
	pollTime := time.Now()

	// Flatten and deduplicate.
	var delta []StreamEntry
	batchSeen := make(map[string]struct{}, len(raw))
	for _, r := range raw {
		id := r.Entry.ID
		if id == "" {
			continue
		}
		if _, dup := batchSeen[id]; dup {
			continue
		}
		batchSeen[id] = struct{}{}
		m.RLock()
		_, dup := m.seen[id]
		m.RUnlock()
		if dup {
			continue
		}

		delta = append(delta, StreamEntry{
			ID:        id,
			EntryType: r.Entry.EntryType,
			AgentID:   r.Entry.AgentID,
			Agent:     r.Entry.AgentID,
			Namespace: r.Entry.Namespace,
			Title:     r.Entry.Title,
			Content:   r.Entry.Content,
			Timestamp: r.Entry.Timestamp,
			Score:     r.Score,
		})
	}

	// Update watermark to now after any successful fetch, even if no entries
	// were added, so retries do not keep querying the same time window.
	m.lastPoll = pollTime

	if len(delta) == 0 {
		return nil
	}

	// Prepend delta, cap at maxEntries.
	m.Lock()
	for _, e := range delta {
		m.seen[e.ID] = struct{}{}
	}
	entries := m.GetSnapshot()
	entries = append(delta, entries...)
	if len(entries) > maxEntries {
		// Trim oldest entries and remove from seen map.
		trimmed := entries[maxEntries:]
		entries = entries[:maxEntries]
		for _, e := range trimmed {
			delete(m.seen, e.ID)
		}
	}
	m.SetSnapshot(entries)
	m.Unlock()

	// Notify listeners with the delta only (outside lock).
	m.FireOnRefresh(delta)

	m.Logger.Debug("stream refresh", "new_entries", len(delta), "total", len(entries))
	return nil
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
// On consecutive errors, it backs off by skipping ticker ticks.
func (m *StreamMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-m.StopCh():
			m.Logger.Debug("stream monitor stopped")
			return
		case <-ticker.C:
			if err := m.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					m.Logger.Warn("stream refresh error", "error", err)
				}
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-m.StopCh():
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					m.Logger.Info("stream refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}

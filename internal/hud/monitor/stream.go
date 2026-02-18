package monitor

import (
	"log/slog"
	"sync"
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

// StreamMonitor polls the agent context stream and maintains a cached,
// deduplicated list of recent entries. It broadcasts only new (delta)
// entries via the OnRefresh callback.
type StreamMonitor struct {
	agent  *bridge.AgentBridge
	logger *slog.Logger

	mu       sync.RWMutex
	entries  []StreamEntry       // most-recent-first, capped at maxEntries
	seen     map[string]struct{} // dedup by entry ID
	lastPoll time.Time           // "since" watermark for incremental fetching

	onRefresh func([]StreamEntry) // called with NEW entries only (delta)

	stopCh   chan struct{}
	stopOnce sync.Once
}

const maxEntries = 500

// NewStreamMonitor creates a StreamMonitor backed by the given agent bridge.
func NewStreamMonitor(agent *bridge.AgentBridge, logger *slog.Logger) *StreamMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &StreamMonitor{
		agent:  agent,
		logger: logger.With("component", "stream-monitor"),
		seen:   make(map[string]struct{}),
		stopCh: make(chan struct{}),
	}
}

// OnRefresh registers a callback that fires after each successful refresh
// with only the NEW entries (delta). Used to broadcast data via SSE.
func (s *StreamMonitor) OnRefresh(fn func([]StreamEntry)) {
	s.onRefresh = fn
}

// Start begins the background polling goroutine at the given interval.
func (s *StreamMonitor) Start(interval time.Duration) {
	// Run initial refresh asynchronously so HUD/TUI startup is non-blocking
	// when downstream services are slow or unavailable.
	go func() {
		if err := s.Refresh(); err != nil {
			s.logger.Warn("initial stream refresh failed", "error", err)
		}
	}()
	go s.pollLoop(interval)
}

// Stop signals the background goroutine to exit. Safe to call multiple times.
func (s *StreamMonitor) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// Entries returns a thread-safe copy of the cached entries (most-recent-first).
func (s *StreamMonitor) Entries() []StreamEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cp := make([]StreamEntry, len(s.entries))
	copy(cp, s.entries)
	return cp
}

// Refresh polls the agent bridge for new context entries since the last
// watermark, deduplicates, and broadcasts the delta.
func (s *StreamMonitor) Refresh() error {
	raw, err := s.agent.ContextStream(s.lastPoll, 100)
	if err != nil {
		s.logger.Warn("stream: failed to fetch context stream", "error", err)
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
		s.mu.RLock()
		_, dup := s.seen[id]
		s.mu.RUnlock()
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
	s.lastPoll = pollTime

	if len(delta) == 0 {
		return nil
	}

	// Prepend delta, cap at maxEntries.
	s.mu.Lock()
	for _, e := range delta {
		s.seen[e.ID] = struct{}{}
	}
	s.entries = append(delta, s.entries...)
	if len(s.entries) > maxEntries {
		// Trim oldest entries and remove from seen map.
		trimmed := s.entries[maxEntries:]
		s.entries = s.entries[:maxEntries]
		for _, e := range trimmed {
			delete(s.seen, e.ID)
		}
	}
	s.mu.Unlock()

	// Notify listeners with the delta only (outside lock).
	if s.onRefresh != nil {
		s.onRefresh(delta)
	}

	s.logger.Debug("stream refresh", "new_entries", len(delta), "total", len(s.entries))
	return nil
}

// pollLoop runs Refresh on a ticker until stopCh is closed.
// On consecutive errors, it backs off by skipping ticker ticks.
func (s *StreamMonitor) pollLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	consecutiveErrors := 0
	for {
		select {
		case <-s.stopCh:
			s.logger.Debug("stream monitor stopped")
			return
		case <-ticker.C:
			if err := s.Refresh(); err != nil {
				consecutiveErrors++
				if consecutiveErrors <= 3 {
					s.logger.Warn("stream refresh error", "error", err)
				}
				skipTicks := min(consecutiveErrors-1, 4)
				for range skipTicks {
					select {
					case <-s.stopCh:
						return
					case <-ticker.C:
					}
				}
			} else {
				if consecutiveErrors > 0 {
					s.logger.Info("stream refresh recovered", "after_errors", consecutiveErrors)
				}
				consecutiveErrors = 0
			}
		}
	}
}

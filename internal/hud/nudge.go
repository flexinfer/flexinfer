// nudge.go implements the agent nudge queue for the HUD.
//
// Nudges are messages or directives queued for agents, delivered via
// heartbeat response. This enables the HUD to asynchronously communicate
// with agents without requiring real-time push channels.
package hud

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// NudgeDropPolicy controls how queue overflow is handled.
type NudgeDropPolicy string

const (
	DropPolicyDropOld   NudgeDropPolicy = "drop_old"
	DropPolicyDropNew   NudgeDropPolicy = "drop_new"
	DropPolicySummarize NudgeDropPolicy = "summarize"
)

const (
	defaultNudgeQueueCap = 64
)

var nudgeIDSeq atomic.Uint64

// NudgeEntry is a pending nudge for an agent, delivered via heartbeat.
type NudgeEntry struct {
	ID        string `json:"id"`
	Type      string `json:"type"`       // context_inject, task_redirect, pause_request, message
	Lane      string `json:"lane"`       // control, handoff, advice, default
	Content   string `json:"content"`    // nudge payload
	FromAgent string `json:"from_agent"` // source: "hud" or another agent ID
	CreatedAt string `json:"created_at"` // RFC3339
}

// NudgeQueueConfig defines queue behavior.
type NudgeQueueConfig struct {
	Debounce     time.Duration
	Cap          int
	DropPolicy   NudgeDropPolicy
	LanePriority []string
}

// NudgeQueuePolicyUpdate applies partial runtime updates to queue policy.
type NudgeQueuePolicyUpdate struct {
	DebounceMs   *int     `json:"debounce_ms,omitempty"`
	Cap          *int     `json:"cap,omitempty"`
	DropPolicy   *string  `json:"drop_policy,omitempty"`
	LanePriority []string `json:"lane_priority,omitempty"`
}

// NudgeQueueStatus provides queue state for introspection.
type NudgeQueueStatus struct {
	AgentID      string          `json:"agent_id"`
	Pending      int             `json:"pending"`
	ByLane       map[string]int  `json:"by_lane"`
	Dropped      int             `json:"dropped"`
	DebounceMs   int             `json:"debounce_ms"`
	Cap          int             `json:"cap"`
	DropPolicy   NudgeDropPolicy `json:"drop_policy"`
	LanePriority []string        `json:"lane_priority"`
	LastAddedAt  string          `json:"last_added_at,omitempty"`
}

type nudgeQueueState struct {
	byLane       map[string][]NudgeEntry
	lastAddedAt  time.Time
	droppedCount int
	summaryLines []string
}

// NudgeQueue manages pending nudges per agent.
type NudgeQueue struct {
	mu     sync.Mutex
	cfg    NudgeQueueConfig
	nudges map[string]*nudgeQueueState // agentID -> pending state
}

// NudgeQueueConfigFromEnv loads queue policy from environment variables.
//
// Supported variables:
//   - LOOM_HUD_NUDGE_QUEUE_DEBOUNCE_MS (int, default 0)
//   - LOOM_HUD_NUDGE_QUEUE_CAP (int, default 64)
//   - LOOM_HUD_NUDGE_QUEUE_DROP_POLICY (drop_old, drop_new, summarize)
func NudgeQueueConfigFromEnv() NudgeQueueConfig {
	cfg := defaultNudgeQueueConfig()

	if raw := strings.TrimSpace(os.Getenv("LOOM_HUD_NUDGE_QUEUE_DEBOUNCE_MS")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			cfg.Debounce = time.Duration(v) * time.Millisecond
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_HUD_NUDGE_QUEUE_CAP")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			cfg.Cap = v
		}
	}
	if raw := strings.TrimSpace(os.Getenv("LOOM_HUD_NUDGE_QUEUE_DROP_POLICY")); raw != "" {
		cfg.DropPolicy = normalizeDropPolicy(raw)
	}

	return sanitizeNudgeQueueConfig(cfg)
}

// NewNudgeQueue creates an empty nudge queue.
func NewNudgeQueue() *NudgeQueue {
	return NewNudgeQueueWithConfig(NudgeQueueConfigFromEnv())
}

// NewNudgeQueueWithConfig creates an empty nudge queue with explicit policy.
func NewNudgeQueueWithConfig(cfg NudgeQueueConfig) *NudgeQueue {
	cfg = sanitizeNudgeQueueConfig(cfg)
	return &NudgeQueue{
		cfg:    cfg,
		nudges: make(map[string]*nudgeQueueState),
	}
}

// Config returns the current queue policy.
func (q *NudgeQueue) Config() NudgeQueueConfig {
	q.mu.Lock()
	defer q.mu.Unlock()
	return cloneNudgeQueueConfig(q.cfg)
}

// UpdateConfig applies a validated runtime policy update.
func (q *NudgeQueue) UpdateConfig(update NudgeQueuePolicyUpdate) (NudgeQueueConfig, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	cfg := cloneNudgeQueueConfig(q.cfg)
	if err := applyNudgeQueuePolicyUpdate(&cfg, update); err != nil {
		return NudgeQueueConfig{}, err
	}
	q.cfg = sanitizeNudgeQueueConfig(cfg)
	return cloneNudgeQueueConfig(q.cfg), nil
}

// Add enqueues a nudge for an agent, delivered on next heartbeat.
func (q *NudgeQueue) Add(agentID string, entry NudgeEntry) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if strings.TrimSpace(entry.Lane) == "" {
		entry.Lane = "default"
	}
	if strings.TrimSpace(entry.CreatedAt) == "" {
		entry.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	state := q.ensureState(agentID)
	state.lastAddedAt = time.Now().UTC()

	if q.totalPendingLocked(state) >= q.cfg.Cap {
		switch q.cfg.DropPolicy {
		case DropPolicyDropNew:
			state.droppedCount++
			q.addSummaryLineLocked(state, entry)
			return
		case DropPolicyDropOld, DropPolicySummarize:
			if dropped, ok := q.dropOldestLocked(state); ok {
				state.droppedCount++
				q.addSummaryLineLocked(state, dropped)
			}
		}
	}

	state.byLane[entry.Lane] = append(state.byLane[entry.Lane], entry)
}

// Drain returns and clears all pending nudges for an agent.
func (q *NudgeQueue) Drain(agentID string) []NudgeEntry {
	q.mu.Lock()
	defer q.mu.Unlock()

	state := q.nudges[agentID]
	if state == nil {
		return nil
	}
	if q.cfg.Debounce > 0 && !state.lastAddedAt.IsZero() && time.Since(state.lastAddedAt) < q.cfg.Debounce {
		return nil
	}

	out := make([]NudgeEntry, 0, q.totalPendingLocked(state)+1)
	seen := make(map[string]bool, len(state.byLane))
	for _, lane := range q.cfg.LanePriority {
		out = append(out, state.byLane[lane]...)
		seen[lane] = true
	}
	// Append unknown lanes in stable order to preserve debuggability.
	extraLanes := make([]string, 0)
	for lane := range state.byLane {
		if !seen[lane] {
			extraLanes = append(extraLanes, lane)
		}
	}
	sort.Strings(extraLanes)
	for _, lane := range extraLanes {
		out = append(out, state.byLane[lane]...)
	}

	if q.cfg.DropPolicy == DropPolicySummarize && state.droppedCount > 0 {
		summary := NudgeEntry{
			ID:        NewNudgeID(agentID) + "-summary",
			Type:      "queue_summary",
			Lane:      "control",
			FromAgent: "hud",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if len(state.summaryLines) > 0 {
			summary.Content = fmt.Sprintf(
				"Nudge queue dropped %d item(s) (cap=%d, policy=%s). Recent dropped: %s",
				state.droppedCount,
				q.cfg.Cap,
				q.cfg.DropPolicy,
				strings.Join(state.summaryLines, " | "),
			)
		} else {
			summary.Content = fmt.Sprintf(
				"Nudge queue dropped %d item(s) (cap=%d, policy=%s).",
				state.droppedCount,
				q.cfg.Cap,
				q.cfg.DropPolicy,
			)
		}
		out = append([]NudgeEntry{summary}, out...)
	}

	delete(q.nudges, agentID)
	return out
}

// Count returns the number of pending nudges for an agent.
func (q *NudgeQueue) Count(agentID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	state := q.nudges[agentID]
	if state == nil {
		return 0
	}
	return q.totalPendingLocked(state)
}

// Status returns queue metrics for a specific agent.
func (q *NudgeQueue) Status(agentID string) NudgeQueueStatus {
	q.mu.Lock()
	defer q.mu.Unlock()

	status := NudgeQueueStatus{
		AgentID:      agentID,
		ByLane:       make(map[string]int),
		DebounceMs:   int(q.cfg.Debounce / time.Millisecond),
		Cap:          q.cfg.Cap,
		DropPolicy:   q.cfg.DropPolicy,
		LanePriority: append([]string(nil), q.cfg.LanePriority...),
	}

	state := q.nudges[agentID]
	if state == nil {
		return status
	}

	for lane, items := range state.byLane {
		status.ByLane[lane] = len(items)
		status.Pending += len(items)
	}
	status.Dropped = state.droppedCount
	if !state.lastAddedAt.IsZero() {
		status.LastAddedAt = state.lastAddedAt.UTC().Format(time.RFC3339Nano)
	}
	return status
}

func defaultNudgeQueueConfig() NudgeQueueConfig {
	return NudgeQueueConfig{
		Debounce:   0,
		Cap:        defaultNudgeQueueCap,
		DropPolicy: DropPolicySummarize,
		LanePriority: []string{
			"control",
			"handoff",
			"advice",
			"default",
		},
	}
}

func sanitizeNudgeQueueConfig(cfg NudgeQueueConfig) NudgeQueueConfig {
	if cfg.Cap <= 0 {
		cfg.Cap = defaultNudgeQueueCap
	}
	if cfg.Debounce < 0 {
		cfg.Debounce = 0
	}
	cfg.DropPolicy = normalizeDropPolicy(string(cfg.DropPolicy))
	cfg.LanePriority = normalizeLanePriority(cfg.LanePriority)
	if len(cfg.LanePriority) == 0 {
		cfg.LanePriority = append([]string(nil), defaultNudgeQueueConfig().LanePriority...)
	}
	return cfg
}

func normalizeDropPolicy(raw string) NudgeDropPolicy {
	if policy, ok := parseDropPolicyStrict(raw); ok {
		return policy
	}
	return DropPolicySummarize
}

func parseDropPolicyStrict(raw string) (NudgeDropPolicy, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "drop_old", "old":
		return DropPolicyDropOld, true
	case "drop_new", "new":
		return DropPolicyDropNew, true
	case "summarize", "summary":
		return DropPolicySummarize, true
	default:
		return "", false
	}
}

func normalizeLanePriority(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, lane := range in {
		lane = strings.ToLower(strings.TrimSpace(lane))
		if lane == "" || seen[lane] {
			continue
		}
		seen[lane] = true
		out = append(out, lane)
	}
	return out
}

func cloneNudgeQueueConfig(cfg NudgeQueueConfig) NudgeQueueConfig {
	out := cfg
	out.LanePriority = append([]string(nil), cfg.LanePriority...)
	return out
}

func applyNudgeQueuePolicyUpdate(cfg *NudgeQueueConfig, update NudgeQueuePolicyUpdate) error {
	if update.DebounceMs != nil {
		if *update.DebounceMs < 0 {
			return fmt.Errorf("debounce_ms must be >= 0")
		}
		cfg.Debounce = time.Duration(*update.DebounceMs) * time.Millisecond
	}
	if update.Cap != nil {
		if *update.Cap <= 0 {
			return fmt.Errorf("cap must be > 0")
		}
		cfg.Cap = *update.Cap
	}
	if update.DropPolicy != nil {
		policy, ok := parseDropPolicyStrict(*update.DropPolicy)
		if !ok {
			return fmt.Errorf("drop_policy must be one of: drop_old, drop_new, summarize")
		}
		cfg.DropPolicy = policy
	}
	if update.LanePriority != nil {
		lanes := normalizeLanePriority(update.LanePriority)
		if len(lanes) == 0 {
			return fmt.Errorf("lane_priority must include at least one non-empty lane")
		}
		cfg.LanePriority = lanes
	}
	return nil
}

func (q *NudgeQueue) ensureState(agentID string) *nudgeQueueState {
	state := q.nudges[agentID]
	if state == nil {
		state = &nudgeQueueState{
			byLane: make(map[string][]NudgeEntry),
		}
		q.nudges[agentID] = state
	}
	return state
}

func (q *NudgeQueue) totalPendingLocked(state *nudgeQueueState) int {
	total := 0
	for _, items := range state.byLane {
		total += len(items)
	}
	return total
}

func (q *NudgeQueue) dropOldestLocked(state *nudgeQueueState) (NudgeEntry, bool) {
	var (
		oldest     NudgeEntry
		oldestLane string
		oldestIdx  int
		hasOldest  bool
		oldestTs   time.Time
	)

	for lane, items := range state.byLane {
		for idx, item := range items {
			ts, ok := parseCreatedAt(item.CreatedAt)
			if !ok {
				ts = time.Time{}
			}
			if !hasOldest || ts.Before(oldestTs) {
				hasOldest = true
				oldest = item
				oldestLane = lane
				oldestIdx = idx
				oldestTs = ts
			}
		}
	}
	if !hasOldest {
		return NudgeEntry{}, false
	}

	items := state.byLane[oldestLane]
	state.byLane[oldestLane] = append(items[:oldestIdx], items[oldestIdx+1:]...)
	if len(state.byLane[oldestLane]) == 0 {
		delete(state.byLane, oldestLane)
	}
	return oldest, true
}

func parseCreatedAt(raw string) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return ts, true
	}
	ts, err = time.Parse(time.RFC3339Nano, raw)
	if err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func (q *NudgeQueue) addSummaryLineLocked(state *nudgeQueueState, entry NudgeEntry) {
	if q.cfg.DropPolicy != DropPolicySummarize {
		return
	}
	content := strings.TrimSpace(entry.Content)
	if content == "" {
		content = entry.Type
	}
	if len(content) > 80 {
		content = content[:77] + "..."
	}
	state.summaryLines = append(state.summaryLines, content)
	if len(state.summaryLines) > 5 {
		state.summaryLines = state.summaryLines[len(state.summaryLines)-5:]
	}
}

// NewNudgeID generates a unique nudge ID.
func NewNudgeID(targetAgent string) string {
	return fmt.Sprintf("nudge-%s-%d-%d", targetAgent, time.Now().UnixMilli(), nudgeIDSeq.Add(1))
}

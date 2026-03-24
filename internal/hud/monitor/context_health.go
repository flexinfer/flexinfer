package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Default configuration for context health monitoring.
const (
	DefaultTokenBudget       = 100000
	DefaultCompactionThresh  = 0.8
	DefaultStaleThreshold    = 60 * time.Minute
	DefaultFreshnessMaxAge   = 60 * time.Minute
	DefaultFreshnessFullMark = 5 * time.Minute
)

// Health score weight distribution.
const (
	weightFreshness  = 0.30
	weightHeadroom   = 0.30
	weightEfficiency = 0.20
	weightCoverage   = 0.20
)

// ContextHealthSnapshot is the aggregate context health state across all agents.
type ContextHealthSnapshot struct {
	Agents          []AgentContextHealth `json:"agents"`
	SystemHealth    int                  `json:"system_health"`
	TotalBudget     int                  `json:"total_budget"`
	TotalUsed       int                  `json:"total_used"`
	CompactionQueue int                  `json:"compaction_queue"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

// AgentContextHealth tracks per-agent context budget and health metrics.
type AgentContextHealth struct {
	AgentID           string  `json:"agent_id"`
	SessionID         string  `json:"session_id"`
	Namespace         string  `json:"namespace"`
	TokenBudget       int     `json:"token_budget"`
	TokensUsed        int     `json:"tokens_used"`
	BudgetUtilization float64 `json:"budget_utilization"`
	HealthScore       int     `json:"health_score"`
	CompactionNeeded  bool    `json:"compaction_needed"`
	StaleEntries      int     `json:"stale_entries"`
	LastEntryAge      string  `json:"last_entry_age"`
	RecallHitRate     float64 `json:"recall_hit_rate"`
	Recommendation    string  `json:"recommendation,omitempty"`
}

// ContextHealthConfig holds tunable parameters for the health monitor.
type ContextHealthConfig struct {
	TokenBudget       int
	CompactionThresh  float64
	StaleThreshold    time.Duration
	FreshnessMaxAge   time.Duration
	FreshnessFullMark time.Duration
}

// DefaultContextHealthConfig returns sensible defaults.
func DefaultContextHealthConfig() ContextHealthConfig {
	return ContextHealthConfig{
		TokenBudget:       DefaultTokenBudget,
		CompactionThresh:  DefaultCompactionThresh,
		StaleThreshold:    DefaultStaleThreshold,
		FreshnessMaxAge:   DefaultFreshnessMaxAge,
		FreshnessFullMark: DefaultFreshnessFullMark,
	}
}

// Compactor is the interface for triggering compaction. The coordinator
// satisfies this when available.
type Compactor interface {
	RunCompression(ctx context.Context) error
}

// BudgetOverride stores per-agent budget overrides.
type BudgetOverride struct {
	AgentID     string `json:"agent_id"`
	TokenBudget int    `json:"token_budget"`
}

// ContextHealthMonitor polls session data to compute per-agent context health
// and triggers auto-compaction when utilization exceeds the threshold.
type ContextHealthMonitor struct {
	BaseMonitor[ContextHealthSnapshot]
	agent     *bridge.AgentBridge
	config    ContextHealthConfig
	compactor Compactor
	overrides map[string]int // agent_id -> custom budget
}

// NewContextHealthMonitor creates a ContextHealthMonitor backed by the given
// agent bridge and optional compactor.
func NewContextHealthMonitor(agent *bridge.AgentBridge, compactor Compactor, logger *slog.Logger) *ContextHealthMonitor {
	m := &ContextHealthMonitor{
		agent:     agent,
		config:    DefaultContextHealthConfig(),
		compactor: compactor,
		overrides: make(map[string]int),
	}
	m.InitBase(logger, nil, "context-health-monitor")
	return m
}

// Start begins the background polling goroutine at the given interval.
func (m *ContextHealthMonitor) Start(interval time.Duration) {
	m.BaseMonitor.Start(interval, m.refresh)
}

// SetCompactor sets or replaces the compactor used for auto-compaction.
// This is called after coordinator initialization to wire the LLM compressor.
func (m *ContextHealthMonitor) SetCompactor(c Compactor) {
	m.Lock()
	defer m.Unlock()
	m.compactor = c
}

// SetBudgetOverride sets a custom token budget for an agent.
func (m *ContextHealthMonitor) SetBudgetOverride(agentID string, budget int) {
	m.Lock()
	defer m.Unlock()
	m.overrides[agentID] = budget
}

// GetBudgetOverride returns the budget override for an agent, or 0 if unset.
func (m *ContextHealthMonitor) GetBudgetOverride(agentID string) (int, bool) {
	m.RLock()
	defer m.RUnlock()
	v, ok := m.overrides[agentID]
	return v, ok
}

// AgentHealth returns the health entry for a specific agent from the latest
// snapshot, or nil if not found.
func (m *ContextHealthMonitor) AgentHealth(agentID string) *AgentContextHealth {
	snap := m.Snapshot()
	for i := range snap.Agents {
		if snap.Agents[i].AgentID == agentID {
			cp := snap.Agents[i]
			return &cp
		}
	}
	return nil
}

// TriggerCompaction manually triggers compaction for a session via the
// coordinator compressor. Returns an error if no compactor is configured.
func (m *ContextHealthMonitor) TriggerCompaction(ctx context.Context, _ string) error {
	if m.compactor == nil {
		return fmt.Errorf("context: compactor not available")
	}
	return m.compactor.RunCompression(ctx)
}

// Refresh forces an immediate refresh. Exposed for external callers.
func (m *ContextHealthMonitor) Refresh() error {
	snap, err := m.refresh(context.Background())
	if err != nil {
		return err
	}
	m.Update(snap)
	return nil
}

// refresh fetches all active sessions and computes health for each.
func (m *ContextHealthMonitor) refresh(_ context.Context) (ContextHealthSnapshot, error) {
	sessions, err := m.agent.Sessions()
	if err != nil {
		return ContextHealthSnapshot{}, fmt.Errorf("context: fetch sessions: %w", err)
	}

	now := time.Now()
	agents := make([]AgentContextHealth, 0, len(sessions))
	compactionQueue := 0

	for _, sess := range sessions {
		if sess.Status != "active" {
			continue
		}

		budget := m.budgetFor(sess.AgentID)
		tokensUsed := sess.TotalTokens

		utilization := 0.0
		if budget > 0 {
			utilization = float64(tokensUsed) / float64(budget)
			if utilization > 1.0 {
				utilization = 1.0
			}
		}

		staleCount := m.countStaleEntries(sess.ID)
		lastAge := m.computeLastEntryAge(sess, now)
		hitRate := m.estimateRecallHitRate(sess)

		health := ComputeHealthScore(
			lastAge,
			utilization,
			tokensUsed,
			sess.EntryCount,
			m.config,
		)

		needsCompaction := utilization > m.config.CompactionThresh
		if needsCompaction {
			compactionQueue++
		}

		entry := AgentContextHealth{
			AgentID:           sess.AgentID,
			SessionID:         sess.ID,
			Namespace:         sess.Namespace,
			TokenBudget:       budget,
			TokensUsed:        tokensUsed,
			BudgetUtilization: math.Round(utilization*1000) / 1000,
			HealthScore:       health,
			CompactionNeeded:  needsCompaction,
			StaleEntries:      staleCount,
			LastEntryAge:      formatDuration(lastAge),
			RecallHitRate:     hitRate,
			Recommendation:    recommend(utilization, health, staleCount),
		}
		agents = append(agents, entry)
	}

	systemHealth := computeSystemHealth(agents)

	totalBudget := 0
	totalUsed := 0
	for _, a := range agents {
		totalBudget += a.TokenBudget
		totalUsed += a.TokensUsed
	}

	snap := ContextHealthSnapshot{
		Agents:          agents,
		SystemHealth:    systemHealth,
		TotalBudget:     totalBudget,
		TotalUsed:       totalUsed,
		CompactionQueue: compactionQueue,
		UpdatedAt:       now,
	}

	// Auto-compaction trigger: if any session exceeds the threshold and a
	// compactor is available, fire-and-forget a compaction cycle.
	if compactionQueue > 0 && m.compactor != nil {
		go func() {
			if err := m.compactor.RunCompression(context.Background()); err != nil {
				m.Logger.Warn("context: auto-compaction failed", "error", err)
			} else {
				m.Logger.Info("context: auto-compaction triggered", "queue_depth", compactionQueue)
			}
		}()
	}

	return snap, nil
}

// budgetFor returns the token budget for an agent, using overrides when set.
func (m *ContextHealthMonitor) budgetFor(agentID string) int {
	m.RLock()
	defer m.RUnlock()
	if v, ok := m.overrides[agentID]; ok && v > 0 {
		return v
	}
	return m.config.TokenBudget
}

// countStaleEntries returns the count of entries older than the stale threshold.
// Uses a best-effort approach; returns 0 on error.
func (m *ContextHealthMonitor) countStaleEntries(sessionID string) int {
	entries, err := m.agent.SessionEntries(sessionID, 200)
	if err != nil {
		return 0
	}
	now := time.Now()
	stale := 0
	for _, e := range entries {
		ts, err := time.Parse(time.RFC3339, e.Entry.Timestamp)
		if err != nil {
			continue
		}
		if now.Sub(ts) > m.config.StaleThreshold {
			stale++
		}
	}
	return stale
}

// computeLastEntryAge returns how long ago the most recent entry was created.
func (m *ContextHealthMonitor) computeLastEntryAge(sess bridge.SessionInfo, now time.Time) time.Duration {
	// Use the session start time as fallback.
	sessionStart, err := time.Parse(time.RFC3339, sess.StartedAt)
	if err != nil {
		return m.config.FreshnessMaxAge
	}
	age := now.Sub(sessionStart)

	// Try to get actual last entry timestamp from entries.
	entries, err := m.agent.SessionEntries(sess.ID, 1)
	if err == nil && len(entries) > 0 {
		if ts, err := time.Parse(time.RFC3339, entries[0].Entry.Timestamp); err == nil {
			age = now.Sub(ts)
		}
	}
	return age
}

// estimateRecallHitRate estimates the recall hit rate for a session.
// This is a simplified heuristic based on entry count relative to tokens.
func (m *ContextHealthMonitor) estimateRecallHitRate(sess bridge.SessionInfo) float64 {
	if sess.EntryCount == 0 || sess.TotalTokens == 0 {
		return 0.0
	}
	// Heuristic: sessions with good token density have better recall.
	// Ideal is ~50 tokens per entry (concise, well-structured context).
	avgTokens := float64(sess.TotalTokens) / float64(sess.EntryCount)
	if avgTokens <= 0 {
		return 0.0
	}
	// Score peaks at ~50 tokens/entry, decreasing for bloated entries.
	ratio := 50.0 / avgTokens
	if ratio > 1.0 {
		ratio = 1.0
	}
	return math.Round(ratio*100) / 100
}

// ComputeHealthScore calculates a composite health score (0-100) based on
// freshness, budget headroom, efficiency, and coverage.
func ComputeHealthScore(
	lastEntryAge time.Duration,
	utilization float64,
	tokensUsed int,
	entryCount int,
	cfg ContextHealthConfig,
) int {
	freshness := computeFreshness(lastEntryAge, cfg.FreshnessFullMark, cfg.FreshnessMaxAge)
	headroom := computeHeadroom(utilization)
	efficiency := computeEfficiency(tokensUsed, entryCount)
	coverage := computeCoverage(entryCount)

	score := freshness*weightFreshness +
		headroom*weightHeadroom +
		efficiency*weightEfficiency +
		coverage*weightCoverage

	result := int(math.Round(score))
	if result > 100 {
		return 100
	}
	if result < 0 {
		return 0
	}
	return result
}

// computeFreshness scores 100 if age < fullMark, linear decrease to 0 at maxAge.
func computeFreshness(age, fullMark, maxAge time.Duration) float64 {
	if age <= fullMark {
		return 100.0
	}
	if age >= maxAge {
		return 0.0
	}
	return 100.0 * (1.0 - float64(age-fullMark)/float64(maxAge-fullMark))
}

// computeHeadroom scores 100 if utilization < 0.5, linear decrease to 0 at 1.0.
func computeHeadroom(utilization float64) float64 {
	if utilization <= 0.5 {
		return 100.0
	}
	if utilization >= 1.0 {
		return 0.0
	}
	return 100.0 * (1.0 - (utilization-0.5)/0.5)
}

// computeEfficiency scores based on token density (tokens per entry).
// Ideal range: 30-100 tokens per entry.
func computeEfficiency(tokensUsed, entryCount int) float64 {
	if entryCount == 0 || tokensUsed == 0 {
		return 50.0 // neutral when no data
	}
	avg := float64(tokensUsed) / float64(entryCount)
	// Optimal range: 30-100 tokens per entry.
	if avg >= 30 && avg <= 100 {
		return 100.0
	}
	if avg < 30 {
		return 100.0 * (avg / 30.0)
	}
	// >100: linearly decrease to 0 at 500.
	if avg >= 500 {
		return 0.0
	}
	return 100.0 * (1.0 - (avg-100.0)/400.0)
}

// computeCoverage scores based on entry count. More entries = better coverage
// up to a saturation point.
func computeCoverage(entryCount int) float64 {
	if entryCount >= 50 {
		return 100.0
	}
	if entryCount <= 0 {
		return 0.0
	}
	return 100.0 * (float64(entryCount) / 50.0)
}

// computeSystemHealth returns the average health score across all agents.
func computeSystemHealth(agents []AgentContextHealth) int {
	if len(agents) == 0 {
		return 100 // no agents = healthy by default
	}
	total := 0
	for _, a := range agents {
		total += a.HealthScore
	}
	return total / len(agents)
}

// recommend generates a human-readable recommendation based on metrics.
func recommend(utilization float64, health int, staleEntries int) string {
	if utilization > 0.9 {
		return "Critical: context budget nearly exhausted, compaction strongly recommended"
	}
	if utilization > 0.8 {
		return "Warning: context budget is high, consider compaction"
	}
	if staleEntries > 20 {
		return "Many stale entries detected, consider pruning old context"
	}
	if health < 50 {
		return "Low health score, review context quality and freshness"
	}
	return ""
}

// formatDuration returns a human-readable duration string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

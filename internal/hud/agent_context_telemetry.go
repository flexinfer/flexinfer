package hud

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

const (
	contextTelemetryEventType          = "agent.context.telemetry"
	contextTelemetryReasonHeartbeat    = "heartbeat"
	contextTelemetryReasonSessionStart = "session_start"
	contextTelemetryReasonContextAdd   = "context_add"
	contextTelemetryInspectLimit       = 200
	contextTelemetryHeartbeatTTL       = 30 * time.Second
	contextTelemetryContextAddTTL      = 5 * time.Second
)

// AgentContextTelemetrySnapshot captures a sampled context-budget view for an agent/session.
type AgentContextTelemetrySnapshot struct {
	AgentID                string `json:"agent_id,omitempty"`
	AgentType              string `json:"agent_type"`
	SessionID              string `json:"session_id,omitempty"`
	Namespace              string `json:"namespace,omitempty"`
	SessionStatus          string `json:"session_status,omitempty"`
	Reason                 string `json:"reason"`
	EstimatedTokens        int    `json:"estimated_tokens"`
	ContextEstimatedTokens int    `json:"context_estimated_tokens"`
	ToolSchemaTokens       int    `json:"tool_schema_tokens"`
	FileInjectionTokens    int    `json:"file_injection_tokens"`
	SystemPromptTokens     int    `json:"system_prompt_tokens"`
	ResponseBudgetTokens   int    `json:"response_budget_tokens"`
	EntryCount             int    `json:"entry_count"`
	MemoryTotalTokens      int    `json:"memory_total_tokens"`
	MemoryShortTermTokens  int    `json:"memory_short_term_tokens"`
	MemoryLongTermTokens   int    `json:"memory_long_term_tokens"`
	Truncated              bool   `json:"truncated"`
	RetrievedAt            string `json:"retrieved_at,omitempty"`
}

// AgentContextMetrics exposes Prometheus metrics for sampled agent context pressure.
type AgentContextMetrics struct {
	PromptEstimatedTokens *prometheus.GaugeVec
	ContextTokens         *prometheus.GaugeVec
	ToolSchemaTokens      *prometheus.GaugeVec
	FileInjectionTokens   *prometheus.GaugeVec
	SystemPromptTokens    *prometheus.GaugeVec
	ResponseBudgetTokens  *prometheus.GaugeVec
	EntryCount            *prometheus.GaugeVec
	MemoryTotalTokens     *prometheus.GaugeVec
	InspectSamplesTotal   *prometheus.CounterVec
	InspectFailuresTotal  *prometheus.CounterVec
	InspectDuration       *prometheus.HistogramVec

	registry *prometheus.Registry
}

// AgentContextLatestStore keeps the latest sampled context telemetry snapshots in memory.
type AgentContextLatestStore struct {
	mu     sync.RWMutex
	latest map[string]AgentContextTelemetrySnapshot
}

// NewAgentContextLatestStore creates a new in-memory latest-snapshot store.
func NewAgentContextLatestStore() *AgentContextLatestStore {
	return &AgentContextLatestStore{
		latest: make(map[string]AgentContextTelemetrySnapshot),
	}
}

// NewAgentContextMetrics creates a dedicated registry for agent context telemetry.
func NewAgentContextMetrics() *AgentContextMetrics {
	m := &AgentContextMetrics{
		registry: prometheus.NewRegistry(),
	}
	labels := []string{"agent_type", "session_status", "reason"}

	m.PromptEstimatedTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_prompt_estimated_tokens",
			Help:      "Latest sampled total estimated prompt tokens for an agent context snapshot",
		},
		labels,
	)
	m.ContextTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_entries_tokens",
			Help:      "Latest sampled context entry tokens for an agent context snapshot",
		},
		labels,
	)
	m.ToolSchemaTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_tool_schema_tokens",
			Help:      "Latest sampled tool schema tokens for an agent context snapshot",
		},
		labels,
	)
	m.FileInjectionTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_file_injection_tokens",
			Help:      "Latest sampled file injection tokens for an agent context snapshot",
		},
		labels,
	)
	m.SystemPromptTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_system_prompt_tokens",
			Help:      "Latest sampled system prompt tokens for an agent context snapshot",
		},
		labels,
	)
	m.ResponseBudgetTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_response_budget_tokens",
			Help:      "Latest sampled response budget tokens for an agent context snapshot",
		},
		labels,
	)
	m.EntryCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_entry_count",
			Help:      "Latest sampled session entry count for an agent context snapshot",
		},
		labels,
	)
	m.MemoryTotalTokens = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_memory_total_tokens",
			Help:      "Latest sampled total memory tokens available during an agent context snapshot",
		},
		labels,
	)
	m.InspectSamplesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_inspect_samples_total",
			Help:      "Total successful agent context telemetry samples",
		},
		[]string{"agent_type", "reason"},
	)
	m.InspectFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_inspect_failures_total",
			Help:      "Total failed agent context telemetry samples",
		},
		[]string{"agent_type", "reason"},
	)
	m.InspectDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "loom",
			Subsystem: "hud",
			Name:      "agent_context_inspect_duration_seconds",
			Help:      "Latency of agent context telemetry sampling",
			Buckets:   prometheus.ExponentialBuckets(0.01, 2, 10),
		},
		[]string{"agent_type", "reason"},
	)

	m.registry.MustRegister(
		m.PromptEstimatedTokens,
		m.ContextTokens,
		m.ToolSchemaTokens,
		m.FileInjectionTokens,
		m.SystemPromptTokens,
		m.ResponseBudgetTokens,
		m.EntryCount,
		m.MemoryTotalTokens,
		m.InspectSamplesTotal,
		m.InspectFailuresTotal,
		m.InspectDuration,
	)

	return m
}

func (m *AgentContextMetrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

func (m *AgentContextMetrics) RecordSample(sample AgentContextTelemetrySnapshot, duration time.Duration) {
	if m == nil {
		return
	}
	agentType := normalizedAgentContextAgentType(sample.AgentType, sample.AgentID)
	sessionStatus := normalizedAgentContextSessionStatus(sample.SessionStatus)
	reason := normalizedAgentContextReason(sample.Reason)

	m.PromptEstimatedTokens.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.EstimatedTokens))
	m.ContextTokens.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.ContextEstimatedTokens))
	m.ToolSchemaTokens.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.ToolSchemaTokens))
	m.FileInjectionTokens.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.FileInjectionTokens))
	m.SystemPromptTokens.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.SystemPromptTokens))
	m.ResponseBudgetTokens.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.ResponseBudgetTokens))
	m.EntryCount.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.EntryCount))
	m.MemoryTotalTokens.WithLabelValues(agentType, sessionStatus, reason).Set(float64(sample.MemoryTotalTokens))
	m.InspectSamplesTotal.WithLabelValues(agentType, reason).Inc()
	m.InspectDuration.WithLabelValues(agentType, reason).Observe(duration.Seconds())
}

func (m *AgentContextMetrics) RecordInspectFailure(agentType, reason string) {
	if m == nil {
		return
	}
	m.InspectFailuresTotal.WithLabelValues(
		normalizedAgentContextAgentType(agentType, ""),
		normalizedAgentContextReason(reason),
	).Inc()
}

func (a *App) handleAgentMetrics(w http.ResponseWriter, r *http.Request) {
	if a.agentContextMetrics == nil {
		a.writeError(w, http.StatusServiceUnavailable, "agent context metrics are not initialized", nil)
		return
	}
	a.agentContextMetrics.Handler().ServeHTTP(w, r)
}

func (a *App) maybeSampleAgentContextTelemetry(agentID, sessionID, agentType, reason string) {
	if a == nil || a.agent == nil || a.agentContextMetrics == nil {
		return
	}
	if strings.TrimSpace(agentID) == "" && strings.TrimSpace(sessionID) == "" {
		return
	}

	if ttl := contextTelemetryThrottle(reason); ttl > 0 && a.cache != nil {
		cacheKey := contextTelemetryCacheKey(reason, agentID, sessionID)
		if _, ok := a.cache.Get(cacheKey); ok {
			return
		}
		a.cache.Set(cacheKey, true, ttl)
	}

	go a.recordAgentContextTelemetry(agentID, sessionID, agentType, reason)
}

func (a *App) recordAgentContextTelemetry(agentID, sessionID, agentType, reason string) {
	start := time.Now()
	sample, err := a.collectAgentContextTelemetry(agentID, sessionID, agentType, reason)
	if err != nil {
		if a.agentContextMetrics != nil {
			a.agentContextMetrics.RecordInspectFailure(agentType, reason)
		}
		if a.logger != nil {
			a.logger.Debug("agent context telemetry inspect failed",
				"agent_id", agentID,
				"session_id", sessionID,
				"reason", reason,
				"error", err,
			)
		}
		return
	}
	if a.agentContextMetrics != nil {
		a.agentContextMetrics.RecordSample(*sample, time.Since(start))
	}
	if a.agentContextLatest != nil {
		a.agentContextLatest.Record(*sample)
	}
	a.broadcastAgentEvent(contextTelemetryEventType, sample)
}

func (s *AgentContextLatestStore) Record(sample AgentContextTelemetrySnapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest[agentContextSnapshotKey(sample)] = sample
}

func (s *AgentContextLatestStore) List(filter AgentContextTelemetryFilter) []AgentContextTelemetrySnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]AgentContextTelemetrySnapshot, 0, len(s.latest))
	for _, sample := range s.latest {
		if filter.matches(sample) {
			snapshots = append(snapshots, sample)
		}
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		return telemetrySnapshotTime(snapshots[i]).After(telemetrySnapshotTime(snapshots[j]))
	})
	if filter.Limit > 0 && len(snapshots) > filter.Limit {
		snapshots = snapshots[:filter.Limit]
	}
	return snapshots
}

type AgentContextTelemetryFilter struct {
	AgentID   string
	SessionID string
	AgentType string
	Reason    string
	Limit     int
}

func (f AgentContextTelemetryFilter) matches(sample AgentContextTelemetrySnapshot) bool {
	if v := strings.TrimSpace(f.AgentID); v != "" && sample.AgentID != v {
		return false
	}
	if v := strings.TrimSpace(f.SessionID); v != "" && sample.SessionID != v {
		return false
	}
	if v := normalizedAgentContextAgentType(f.AgentType, ""); v != "" && strings.TrimSpace(f.AgentType) != "" && sample.AgentType != v {
		return false
	}
	if v := normalizedAgentContextReason(f.Reason); v != "" && strings.TrimSpace(f.Reason) != "" && sample.Reason != v {
		return false
	}
	return true
}

func (a *App) collectAgentContextTelemetry(agentID, sessionID, agentType, reason string) (*AgentContextTelemetrySnapshot, error) {
	result, err := a.agent.ContextInspect(agentID, sessionID, false, contextTelemetryInspectLimit)
	if err != nil {
		return nil, err
	}

	sample := &AgentContextTelemetrySnapshot{
		AgentID:                firstNonEmpty(result.AgentID, agentID),
		AgentType:              normalizedAgentContextAgentType(agentType, firstNonEmpty(result.AgentID, agentID)),
		SessionID:              result.SessionID,
		Namespace:              result.Namespace,
		SessionStatus:          normalizedAgentContextSessionStatus(result.SessionStatus),
		Reason:                 normalizedAgentContextReason(reason),
		EstimatedTokens:        result.EstimatedTokens,
		ContextEstimatedTokens: result.ContextEstimatedTokens,
		ToolSchemaTokens:       contextInspectSectionTokens(result, "tools_schema"),
		FileInjectionTokens:    contextInspectSectionTokens(result, "file_injections"),
		SystemPromptTokens:     contextInspectSectionTokens(result, "system_prompt"),
		ResponseBudgetTokens:   contextInspectSectionTokens(result, "response_budget"),
		EntryCount:             result.EntryCount,
		Truncated:              result.Truncated,
		RetrievedAt:            result.RetrievedAt,
	}
	if result.Memory != nil {
		sample.MemoryTotalTokens = result.Memory.TotalTokens
		sample.MemoryShortTermTokens = result.Memory.ShortTermMemory.Tokens
		sample.MemoryLongTermTokens = result.Memory.LongTermMemory.Tokens
	}
	return sample, nil
}

func contextInspectSectionTokens(result *bridge.ContextInspectResult, section string) int {
	if result == nil {
		return 0
	}
	for _, s := range result.Sections {
		if strings.EqualFold(strings.TrimSpace(s.Section), section) {
			return s.EstimatedTokens
		}
	}
	return 0
}

func contextTelemetryThrottle(reason string) time.Duration {
	switch normalizedAgentContextReason(reason) {
	case contextTelemetryReasonHeartbeat:
		return contextTelemetryHeartbeatTTL
	case contextTelemetryReasonContextAdd:
		return contextTelemetryContextAddTTL
	default:
		return 0
	}
}

func contextTelemetryCacheKey(reason, agentID, sessionID string) string {
	return fmt.Sprintf("agent_context_telemetry:%s:%s:%s", normalizedAgentContextReason(reason), strings.TrimSpace(agentID), strings.TrimSpace(sessionID))
}

func agentContextSnapshotKey(sample AgentContextTelemetrySnapshot) string {
	identity := strings.TrimSpace(sample.SessionID)
	if identity == "" {
		identity = strings.TrimSpace(sample.AgentID)
	}
	return fmt.Sprintf("%s:%s", normalizedAgentContextAgentType(sample.AgentType, sample.AgentID), identity)
}

func telemetrySnapshotTime(sample AgentContextTelemetrySnapshot) time.Time {
	if sample.RetrievedAt == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, sample.RetrievedAt)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func normalizedAgentContextAgentType(agentType, agentID string) string {
	if trimmed := strings.ToLower(strings.TrimSpace(agentType)); trimmed != "" {
		return trimmed
	}
	lowerID := strings.ToLower(strings.TrimSpace(agentID))
	switch {
	case strings.Contains(lowerID, "claude"):
		return "claude"
	case strings.Contains(lowerID, "codex"), strings.Contains(lowerID, "openai"):
		return "codex"
	case strings.Contains(lowerID, "gemini"):
		return "gemini"
	case strings.Contains(lowerID, "kilo"):
		return "kilocode"
	case strings.Contains(lowerID, "proxy"):
		return "proxy"
	default:
		return "unknown"
	}
}

func normalizedAgentContextSessionStatus(status string) string {
	if trimmed := strings.ToLower(strings.TrimSpace(status)); trimmed != "" {
		return trimmed
	}
	return "unknown"
}

func normalizedAgentContextReason(reason string) string {
	if trimmed := strings.ToLower(strings.TrimSpace(reason)); trimmed != "" {
		return trimmed
	}
	return "unspecified"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// handleAgentContextTelemetry returns the latest sampled telemetry snapshots.
// GET /api/agent/context-telemetry?agent_id=...&session_id=...&agent_type=...&reason=...&limit=20
func (a *App) handleAgentContextTelemetry(w http.ResponseWriter, r *http.Request) {
	if a.agentContextLatest == nil {
		a.writeError(w, http.StatusServiceUnavailable, "agent context telemetry is not initialized", nil)
		return
	}

	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			a.writeError(w, http.StatusBadRequest, "limit must be a positive integer", err)
			return
		}
		limit = parsed
	}

	filter := AgentContextTelemetryFilter{
		AgentID:   strings.TrimSpace(r.URL.Query().Get("agent_id")),
		SessionID: strings.TrimSpace(r.URL.Query().Get("session_id")),
		AgentType: strings.TrimSpace(r.URL.Query().Get("agent_type")),
		Reason:    strings.TrimSpace(r.URL.Query().Get("reason")),
		Limit:     limit,
	}
	snapshots := a.agentContextLatest.List(filter)
	a.writeJSON(w, http.StatusOK, map[string]any{
		"snapshots": snapshots,
		"count":     len(snapshots),
	})
}

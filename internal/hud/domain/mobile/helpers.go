package mobile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordination"
)

func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(buf[:])
}

func extractBearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// ExtractDeviceID returns the X-Device-ID header, truncated to MaxDeviceIDLen.
func ExtractDeviceID(r *http.Request) string {
	id := strings.TrimSpace(r.Header.Get("X-Device-ID"))
	if len(id) > MaxDeviceIDLen {
		id = id[:MaxDeviceIDLen]
	}
	return id
}

func actorFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func parseMobileLimit(r *http.Request, fallback, max int) int {
	limit := fallback
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > max {
		return max
	}
	if limit <= 0 {
		return fallback
	}
	return limit
}

func parseMobileTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	return time.Time{}
}

func chooseFirstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeMobileTaskStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return "pending"
	case "active", "in_progress":
		return "in_progress"
	case "blocked":
		return "blocked"
	case "completed", "done":
		return "completed"
	default:
		return "unknown"
	}
}

func normalizeMobilePriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "critical":
		return "critical"
	default:
		return "medium"
	}
}

// MapMobileTask converts a bridge.TaskInfo to the mobile task DTO.
func MapMobileTask(task bridge.TaskInfo) taskDTO {
	tags := task.Tags
	if tags == nil {
		tags = []string{}
	}
	blockedBy := task.BlockedBy
	if blockedBy == nil {
		blockedBy = []string{}
	}
	return taskDTO{
		ID:             task.ID,
		SessionID:      task.SessionID,
		AgentID:        task.AgentID,
		Namespace:      task.Namespace,
		Project:        bridge.CanonicalProject(task.Project, task.Namespace, task.PipelineRef),
		Title:          task.Title,
		Context:        task.Context,
		Priority:       normalizeMobilePriority(task.Priority),
		Status:         normalizeMobileTaskStatus(task.Status),
		TaskKind:       "explicit",
		SourcePlatform: "agent_context",
		SourceKind:     "explicit",
		SourceID:       task.ID,
		NativeKey:      task.ID,
		PipelineRef:    task.PipelineRef,
		WorkflowID:     task.WorkflowID,
		IsProjected:    false,
		Tags:           tags,
		BlockedBy:      blockedBy,
		CreatedAt:      task.CreatedAt,
		UpdatedAt:      task.UpdatedAt,
	}
}

func normalizeMobileWorkflowStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "active":
		return "running"
	case "waiting_approval", "pending_approval":
		return "waiting_approval"
	case "completed", "succeeded", "success":
		return "completed"
	case "failed", "error":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "unknown"
	}
}

func mapMobileWorkflowSteps(steps []bridge.WorkflowStep) []map[string]any {
	result := make([]map[string]any, 0, len(steps))
	for _, step := range steps {
		result = append(result, map[string]any{
			"id":     step.ID,
			"name":   step.Name,
			"status": normalizeMobileWorkflowStatus(step.Status),
			"type":   step.Type,
			"error":  step.Error,
		})
	}
	return result
}

func normalizeMobilePresenceStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active":
		return "active"
	case "idle":
		return "idle"
	case "offline":
		return "offline"
	default:
		return "unknown"
	}
}

func agentSortTime(ua unifiedAgent) time.Time {
	if ua.LastHeartbeat != "" {
		if t := parseMobileTime(ua.LastHeartbeat); !t.IsZero() {
			return t
		}
	}
	if ua.SessionStarted != "" {
		if t := parseMobileTime(ua.SessionStarted); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func inferAgentType(agentID string) string {
	prefixes := []string{"claude-code", "gemini-cli", "codex", "zed", "proxy"}
	lower := strings.ToLower(agentID)
	for _, p := range prefixes {
		if lower == p || strings.HasPrefix(lower, p+"-") {
			if p == "zed" || p == "proxy" {
				return "codex"
			}
			return p
		}
	}
	return "unknown"
}

func filterMobileCoordinationAgents(agents []coordination.AgentSummary, agentFilter, statusFilter string) []coordination.AgentSummary {
	filtered := make([]coordination.AgentSummary, 0, len(agents))
	for _, agent := range agents {
		if agentFilter != "" && !strings.EqualFold(agent.AgentID, agentFilter) {
			continue
		}
		if statusFilter != "" && !strings.EqualFold(agent.Status, statusFilter) {
			continue
		}
		filtered = append(filtered, agent)
	}
	return filtered
}

func filterMobileRelations(relations []coordination.RelationEdge, agentFilter string) []coordination.RelationEdge {
	if agentFilter == "" {
		return relations
	}
	filtered := make([]coordination.RelationEdge, 0, len(relations))
	for _, relation := range relations {
		if strings.EqualFold(relation.Source, agentFilter) || strings.EqualFold(relation.Target, agentFilter) ||
			strings.EqualFold(relation.SourceLabel, agentFilter) || strings.EqualFold(relation.TargetLabel, agentFilter) {
			filtered = append(filtered, relation)
		}
	}
	return filtered
}

func filterMobileBlockers(blockers []coordination.BlockerRelation, activeOnly bool) []coordination.BlockerRelation {
	filtered := make([]coordination.BlockerRelation, 0, len(blockers))
	for _, blocker := range blockers {
		if activeOnly && blocker.Resolved {
			continue
		}
		filtered = append(filtered, blocker)
	}
	return filtered
}

func filterMobileTaskBlockers(blockers []coordination.BlockerRelation, tasks []taskDTO, agentFilter, sessionFilter string) []coordination.BlockerRelation {
	taskIDs := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		taskIDs[task.ID] = struct{}{}
	}
	filtered := make([]coordination.BlockerRelation, 0, len(blockers))
	for _, blocker := range blockers {
		if len(taskIDs) > 0 {
			if _, ok := taskIDs[blocker.TaskID]; !ok {
				continue
			}
		}
		if agentFilter != "" && !strings.EqualFold(blocker.TaskAgentID, agentFilter) && !strings.EqualFold(blocker.BlockedByAgentID, agentFilter) {
			continue
		}
		if sessionFilter != "" {
			matched := false
			for _, task := range tasks {
				if task.ID == blocker.TaskID && strings.EqualFold(task.SessionID, sessionFilter) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		filtered = append(filtered, blocker)
	}
	return filtered
}

func filterMobileNamespaces(namespaces []coordination.NamespaceSummary, tasks []taskDTO) []coordination.NamespaceSummary {
	taskNamespaces := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if strings.TrimSpace(task.Namespace) != "" {
			taskNamespaces[task.Namespace] = struct{}{}
		}
	}
	if len(taskNamespaces) == 0 {
		return namespaces
	}
	filtered := make([]coordination.NamespaceSummary, 0, len(namespaces))
	for _, namespace := range namespaces {
		if _, ok := taskNamespaces[namespace.Namespace]; ok {
			filtered = append(filtered, namespace)
		}
	}
	return filtered
}

func buildMobileAttentionLanes(snapshot coordination.Snapshot) []map[string]any {
	lanes := make([]map[string]any, 0, 6)
	for _, agent := range limitMobileSlice(snapshot.Agents, 3) {
		if !agent.NeedsAttention {
			continue
		}
		summary := strings.Join(limitMobileSlice(agent.AttentionReasons, 2), " · ")
		lanes = append(lanes, map[string]any{
			"type":     "agent",
			"id":       agent.AgentID,
			"label":    "Agent lane",
			"route":    "people",
			"scope":    preferMobileValue(agent.Namespace, "unscoped"),
			"summary":  summary,
			"severity": mobileAttentionSeverity(summary),
		})
	}
	for _, namespace := range limitMobileSlice(snapshot.Namespaces, 3) {
		if !namespace.NeedsAttention {
			continue
		}
		summary := strings.Join(limitMobileSlice(namespace.AttentionReasons, 2), " · ")
		lanes = append(lanes, map[string]any{
			"type":     "namespace",
			"id":       namespace.Namespace,
			"label":    "Work lane",
			"route":    "work",
			"scope":    fmt.Sprintf("%d tasks", namespace.TaskCount),
			"summary":  summary,
			"severity": mobileAttentionSeverity(summary),
		})
	}

	// Merge-ready lane: surface branches ready to merge for quick dispatch.
	if snapshot.Summary.MergeReadyBranches > 0 {
		lanes = append(lanes, map[string]any{
			"type":     "merge",
			"id":       "merge-ready",
			"label":    "Merge ready",
			"route":    "dispatch",
			"scope":    fmt.Sprintf("%d branch%s", snapshot.Summary.MergeReadyBranches, pluralSE(snapshot.Summary.MergeReadyBranches)),
			"summary":  fmt.Sprintf("%d branch%s ready to merge", snapshot.Summary.MergeReadyBranches, pluralSE(snapshot.Summary.MergeReadyBranches)),
			"severity": "info",
		})
	}

	// File conflict lane: surface active file conflicts needing resolution.
	if snapshot.Summary.ConflictFiles > 0 {
		lanes = append(lanes, map[string]any{
			"type":     "conflict",
			"id":       "file-conflicts",
			"label":    "File conflicts",
			"route":    "dispatch",
			"scope":    fmt.Sprintf("%d file%s", snapshot.Summary.ConflictFiles, pluralS(snapshot.Summary.ConflictFiles)),
			"summary":  fmt.Sprintf("%d file%s claimed by multiple agents", snapshot.Summary.ConflictFiles, pluralS(snapshot.Summary.ConflictFiles)),
			"severity": "critical",
		})
	}

	return limitMobileSlice(lanes, 8)
}

func mobileAttentionSeverity(summary string) string {
	summary = strings.ToLower(strings.TrimSpace(summary))
	switch {
	case strings.Contains(summary, "conflict"), strings.Contains(summary, "blocked"), strings.Contains(summary, "orphan"):
		return "critical"
	case summary == "":
		return "info"
	default:
		return "warning"
	}
}

func preferMobileValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func pluralSE(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}

func limitMobileSlice[T any](items []T, limit int) []T {
	if limit <= 0 || len(items) <= limit {
		return items
	}
	return items[:limit]
}

func normalizeMobileMemoryTier(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "working", true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "working", "working_memory":
		return "working", true
	case "short", "short_term", "short_term_memory":
		return "short_term", true
	case "long", "long_term", "long_term_memory":
		return "long_term", true
	default:
		return "", false
	}
}

func normalizeMobileMemoryTierOutput(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "working", "working_memory":
		return "working"
	case "short", "short_term", "short_term_memory":
		return "short_term"
	case "long", "long_term", "long_term_memory":
		return "long_term"
	default:
		return "working"
	}
}

func normalizeMobileImportance(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "critical":
		return "critical"
	default:
		return "medium"
	}
}

func parseMobileTypeFilter(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	result := map[string]struct{}{}
	for _, token := range strings.Split(raw, ",") {
		trimmed := strings.ToLower(strings.TrimSpace(token))
		if trimmed == "" {
			continue
		}
		result[trimmed] = struct{}{}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeMobileReasoningStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "in_progress", "running":
		return "active"
	case "completed", "done":
		return "completed"
	case "abandoned", "failed", "cancelled", "canceled":
		return "abandoned"
	default:
		return "unknown"
	}
}

func toMobileText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func eventHasField(raw json.RawMessage, field, value string) bool {
	if len(raw) == 0 || field == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	got, _ := payload[field].(string)
	return strings.TrimSpace(got) == value
}

func eventHasSessionID(raw json.RawMessage, sessionID string) bool {
	if len(raw) == 0 || sessionID == "" {
		return false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	got, _ := payload["session_id"].(string)
	return strings.TrimSpace(got) == sessionID
}

// AlertPolicyMatrix returns the canonical event-to-severity-interruption-action matrix.
func AlertPolicyMatrix() []AlertPolicyEntry {
	return []AlertPolicyEntry{
		{EventType: "hud.health", Severity: "critical", InterruptionLevel: "time_sensitive", Title: "Server Down", AllowedActions: []string{"view_dashboard", "acknowledge"}, Conditional: true},
		{EventType: "hud.health", Severity: "warning", InterruptionLevel: "active", Title: "Server Degraded", AllowedActions: []string{"view_dashboard", "acknowledge"}, Conditional: true},
		{EventType: "agent.session.reaped", Severity: "warning", InterruptionLevel: "active", Title: "Session Reaped", AllowedActions: []string{"view_session", "acknowledge"}},
		{EventType: "hud.workflow.reject", Severity: "warning", InterruptionLevel: "active", Title: "Workflow Rejected", AllowedActions: []string{"acknowledge"}},
		{EventType: "agent.session.start", Severity: "info", InterruptionLevel: "passive", Title: "Session Started", AllowedActions: []string{"view_session", "acknowledge"}},
		{EventType: "agent.session.end", Severity: "info", InterruptionLevel: "passive", Title: "Session Ended", AllowedActions: []string{"view_session", "acknowledge"}},
		{EventType: "agent.nudge.created", Severity: "info", InterruptionLevel: "passive", Title: "Agent Nudge Queued", AllowedActions: []string{"acknowledge"}},
		{EventType: "hud.workflow.approve", Severity: "info", InterruptionLevel: "passive", Title: "Workflow Approved", AllowedActions: []string{"acknowledge"}},
		{EventType: "hud.handoff.created", Severity: "info", InterruptionLevel: "passive", Title: "Handoff Created", AllowedActions: []string{"acknowledge"}},
		{EventType: "coordinator.plan.complete", Severity: "info", InterruptionLevel: "passive", Title: "Plan Complete", AllowedActions: []string{"acknowledge"}},
	}
}

// sortSliceStable is a convenience wrapper for sort.SliceStable.
func sortSliceStable[T any](s []T, less func(i, j int) bool) {
	sort.SliceStable(s, less)
}

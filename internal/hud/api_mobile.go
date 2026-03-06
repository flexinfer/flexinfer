package hud

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

const maxDeviceIDLen = 128
const (
	mobileDefaultLimit = 50
	mobileMaxLimit     = 200
)

const (
	mobileScopeRead          = "mobile:read"
	mobileScopeSessionCreate = "mobile:session:create"
	mobileScopeSessionEnd    = "mobile:session:end"
	mobileScopePush          = "mobile:push"
)

// mobileEnvelope is the standard response shape for /api/mobile/v1 endpoints.
type mobileEnvelope struct {
	OK    bool    `json:"ok"`
	Data  any     `json:"data,omitempty"`
	Error any     `json:"error,omitempty"`
	Meta  mobMeta `json:"meta"`
}

type mobMeta struct {
	RequestID string `json:"request_id"`
	Timestamp string `json:"timestamp"`
}

type mobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(buf[:])
}

func (a *App) writeMobileJSON(w http.ResponseWriter, status int, data any) {
	env := mobileEnvelope{
		OK:   true,
		Data: data,
		Meta: mobMeta{
			RequestID: newRequestID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
	a.writeJSON(w, status, env)
}

func (a *App) writeMobileError(w http.ResponseWriter, status int, code, message string) {
	env := mobileEnvelope{
		OK: false,
		Error: mobError{
			Code:    code,
			Message: message,
		},
		Meta: mobMeta{
			RequestID: newRequestID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
	a.writeJSON(w, status, env)
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

func (a *App) isMobileOperatorToken(r *http.Request) bool {
	expected := strings.TrimSpace(a.config.MobileOperatorToken)
	if expected == "" {
		return false
	}
	actual := extractBearerToken(r)
	if actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func (a *App) mobileTokenOutsideMobileAPI(r *http.Request) bool {
	if !a.isMobileOperatorToken(r) {
		return false
	}
	return !strings.HasPrefix(r.URL.Path, "/api/mobile/v1/")
}

func (a *App) requireMobileScope(w http.ResponseWriter, r *http.Request, requiredScope string) bool {
	expected := strings.TrimSpace(a.config.MobileOperatorToken)
	if expected == "" {
		a.writeMobileError(w, http.StatusForbidden, "not_configured", "mobile operator token is not configured; set HUD_MOBILE_OPERATOR_TOKEN")
		return false
	}

	actual := extractBearerToken(r)
	if actual == "" {
		a.writeMobileError(w, http.StatusUnauthorized, "unauthorized", "mobile bearer token is required")
		return false
	}

	if subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		a.writeMobileError(w, http.StatusUnauthorized, "unauthorized", "invalid mobile bearer token")
		return false
	}

	// Check revocation list.
	if a.mobileRevocationList != nil && a.mobileRevocationList.IsRevoked(actual) {
		a.writeMobileError(w, http.StatusUnauthorized, "token_revoked", "mobile bearer token has been revoked")
		return false
	}

	if !a.mobileScopeAllowed(requiredScope) {
		a.writeMobileError(w, http.StatusForbidden, "forbidden", "mobile token missing required scope")
		return false
	}

	// Check rate limit.
	if a.mobileRateLimiter != nil {
		isMutation := requiredScope != mobileScopeRead
		if !a.mobileRateLimiter.Allow(actorFromRequest(r), isMutation) {
			a.writeMobileError(w, http.StatusTooManyRequests, "rate_limited", "mobile API rate limit exceeded")
			return false
		}
	}

	return true
}

func (a *App) mobileScopeAllowed(required string) bool {
	if required == "" {
		return true
	}
	raw := strings.TrimSpace(a.config.MobileOperatorScopes)
	if raw == "" {
		return false
	}
	for _, scope := range strings.Split(raw, ",") {
		if strings.TrimSpace(scope) == required {
			return true
		}
	}
	return false
}

// extractDeviceID reads the optional X-Device-ID header from the request.
func extractDeviceID(r *http.Request) string {
	id := strings.TrimSpace(r.Header.Get("X-Device-ID"))
	if len(id) > maxDeviceIDLen {
		id = id[:maxDeviceIDLen]
	}
	return id
}

// logMobileAudit records a structured audit entry for mobile mutation operations.
func (a *App) logMobileAudit(r *http.Request, action string, targets map[string]string, outcome string, auditErr error) {
	attrs := []any{
		"source", "mobile",
		"action", action,
		"endpoint", r.Method + " " + r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"outcome", outcome,
	}
	if deviceID := extractDeviceID(r); deviceID != "" {
		attrs = append(attrs, "device_id", deviceID)
	}
	for k, v := range targets {
		attrs = append(attrs, k, v)
	}
	if auditErr != nil {
		attrs = append(attrs, "error", auditErr.Error())
	}
	a.logger.Info("mobile_audit", attrs...)
}

// --- Mobile companion v1 handlers ---

func (a *App) handleMobilePing(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}
	a.writeMobileJSON(w, http.StatusOK, map[string]any{"pong": true})
}

func (a *App) handleMobileDashboard(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	fleetSnap := a.fleetMonitor.Snapshot()
	healthSum := a.healthMonitor.Summary()

	// Collect recent critical timeline entries (last 10).
	var recentTimeline []TimelineEntry
	if a.eventLog != nil {
		recentTimeline = a.eventLog.All(10)
	}
	if recentTimeline == nil {
		recentTimeline = []TimelineEntry{}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"daemon_running":  fleetSnap.DaemonRunning,
		"server_count":    fleetSnap.ServerCount,
		"active_sessions": fleetSnap.ActiveSessions,
		"active_agents":   fleetSnap.ActiveAgents,
		"idle_agents":     fleetSnap.IdleAgents,
		"offline_agents":  fleetSnap.OfflineAgents,
		"updated_at":      fleetSnap.UpdatedAt,
		"health": map[string]any{
			"total_servers":    healthSum.TotalServers,
			"healthy_servers":  healthSum.HealthyServers,
			"degraded_servers": healthSum.DegradedServers,
			"down_servers":     healthSum.DownServers,
			"idle_servers":     healthSum.IdleServers,
		},
		"recent_timeline": recentTimeline,
	})
}

type mobileControlPlaneCostTopAgent struct {
	AgentID   string `json:"agent_id"`
	CallCount int64  `json:"call_count"`
	Errors    int64  `json:"errors"`
	Denied    int64  `json:"denied"`
	Cached    int64  `json:"cached"`
}

type mobileControlPlaneCostTopServer struct {
	Server    string `json:"server"`
	CallCount int64  `json:"call_count"`
	Errors    int64  `json:"errors"`
}

type mobileControlPlaneCost struct {
	Enabled         bool                             `json:"enabled"`
	Timestamp       string                           `json:"timestamp,omitempty"`
	TotalCalls      int64                            `json:"total_calls"`
	TotalErrors     int64                            `json:"total_errors"`
	TotalDenied     int64                            `json:"total_denied"`
	TotalCached     int64                            `json:"total_cached"`
	TotalDurationMs int64                            `json:"total_duration_ms"`
	TopAgent        *mobileControlPlaneCostTopAgent  `json:"top_agent,omitempty"`
	TopServer       *mobileControlPlaneCostTopServer `json:"top_server,omitempty"`
}

type mobileControlPlaneRBAC struct {
	Enabled         bool   `json:"enabled"`
	DefaultPolicy   string `json:"default_policy,omitempty"`
	RoleCount       int    `json:"role_count"`
	BindingCount    int    `json:"binding_count"`
	GlobalDenyCount int    `json:"global_deny_count"`
	RateLimitCount  int    `json:"rate_limit_count"`
	DeniedCount     int    `json:"denied_count"`
}

type mobileControlPlaneOTel struct {
	OTLPConfigured  bool   `json:"otlp_configured"`
	OTLPEndpoint    string `json:"otlp_endpoint,omitempty"`
	JSONLogsEnabled bool   `json:"json_logs_enabled"`
	TracedServers   int    `json:"traced_servers"`
	TotalServers    int    `json:"total_servers"`
	TraceCoverage   string `json:"trace_coverage,omitempty"`
}

type mobileControlPlaneHealth struct {
	TotalServers    int `json:"total_servers"`
	HealthyServers  int `json:"healthy_servers"`
	DegradedServers int `json:"degraded_servers"`
	DownServers     int `json:"down_servers"`
	IdleServers     int `json:"idle_servers"`
	HubTargets      int `json:"hub_targets"`
	LocalTargets    int `json:"local_targets"`
	Unavailable     int `json:"unavailable_targets"`
}

func (a *App) handleMobileControlPlane(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	costSnap := mobileControlPlaneCost{}
	if a.costMonitor != nil {
		snapshot := a.costMonitor.Snapshot()
		costSnap = mobileControlPlaneCost{
			Enabled:         snapshot.Enabled,
			Timestamp:       snapshot.Timestamp,
			TotalCalls:      snapshot.TotalCalls,
			TotalErrors:     snapshot.TotalErrors,
			TotalDenied:     snapshot.TotalDenied,
			TotalCached:     snapshot.TotalCached,
			TotalDurationMs: snapshot.TotalDuration,
		}
		for _, agent := range snapshot.ByAgent {
			if costSnap.TopAgent == nil || agent.CallCount > costSnap.TopAgent.CallCount {
				costSnap.TopAgent = &mobileControlPlaneCostTopAgent{
					AgentID:   agent.AgentID,
					CallCount: agent.CallCount,
					Errors:    agent.Errors,
					Denied:    agent.Denied,
					Cached:    agent.Cached,
				}
			}
		}
		for _, server := range snapshot.ByServer {
			if costSnap.TopServer == nil || server.CallCount > costSnap.TopServer.CallCount {
				costSnap.TopServer = &mobileControlPlaneCostTopServer{
					Server:    server.Server,
					CallCount: server.CallCount,
					Errors:    server.Errors,
				}
			}
		}
	}

	rbac := mobileControlPlaneRBAC{}
	if result, err := a.client.RBACConfig(); err != nil {
		a.logger.Debug("mobile control-plane: rbac-config call failed", "error", err)
	} else if result != nil {
		rbac = mobileControlPlaneRBAC{
			Enabled:         result.Enabled,
			DefaultPolicy:   strings.TrimSpace(result.DefaultPolicy),
			RoleCount:       len(result.Roles),
			BindingCount:    len(result.Bindings),
			GlobalDenyCount: len(result.GlobalDeny),
			RateLimitCount:  len(result.RateLimits),
			DeniedCount:     len(result.RecentDenied),
		}
	}

	otel := mobileControlPlaneOTel{}
	if result, err := a.client.OTelStatus(); err != nil {
		a.logger.Debug("mobile control-plane: otel-status call failed", "error", err)
	} else if result != nil {
		otel = mobileControlPlaneOTel{
			OTLPConfigured:  result.OTLPConfigured,
			OTLPEndpoint:    strings.TrimSpace(result.OTLPEndpoint),
			JSONLogsEnabled: result.JSONLogsEnabled,
			TracedServers:   result.TracedServers,
			TotalServers:    result.TotalServers,
			TraceCoverage:   strings.TrimSpace(result.TraceCoverage),
		}
	}

	health := mobileControlPlaneHealth{}
	if a.healthMonitor != nil {
		healthSum := a.healthMonitor.Summary()
		health = mobileControlPlaneHealth{
			TotalServers:    healthSum.TotalServers,
			HealthyServers:  healthSum.HealthyServers,
			DegradedServers: healthSum.DegradedServers,
			DownServers:     healthSum.DownServers,
			IdleServers:     healthSum.IdleServers,
		}
		for _, server := range a.healthMonitor.Servers() {
			switch strings.ToLower(strings.TrimSpace(server.Target)) {
			case "hub":
				health.HubTargets++
			case "local":
				health.LocalTargets++
			default:
				health.Unavailable++
			}
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"cost":   costSnap,
		"rbac":   rbac,
		"otel":   otel,
		"health": health,
	})
}

func (a *App) handleMobileSessions(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	sessions, err := a.agent.Sessions()
	if err != nil {
		// Graceful degradation: fall back to cached fleet snapshot sessions
		// instead of returning a hard 502 to the mobile client.
		a.logger.Warn("mobile: sessions upstream error, falling back to cache", "error", err)
		snap := a.fleetMonitor.Snapshot()
		sessions = snap.Sessions
	}
	if sessions == nil {
		sessions = []bridge.SessionInfo{}
	}
	a.writeMobileJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (a *App) handleMobileSessionDetail(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "session_id is required")
		return
	}

	sessions, err := a.agent.Sessions()
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list sessions")
		return
	}

	for _, s := range sessions {
		if strings.TrimSpace(s.ID) == sessionID {
			a.writeMobileJSON(w, http.StatusOK, map[string]any{"session": s})
			return
		}
	}

	a.writeMobileError(w, http.StatusNotFound, "not_found", "session not found")
}

func (a *App) handleMobileSessionEvents(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "session_id is required")
		return
	}

	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	events := make([]TimelineEntry, 0, limit)
	if a.eventLog != nil {
		for _, evt := range a.eventLog.All(1000) {
			if eventHasSessionID(evt.Data, sessionID) {
				events = append(events, evt)
				if len(events) >= limit {
					break
				}
			}
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"events":     events,
	})
}

type mobileTaskCounts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Completed  int `json:"completed"`
}

type mobileTaskDTO struct {
	ID        string   `json:"id"`
	SessionID string   `json:"session_id"`
	AgentID   string   `json:"agent_id"`
	Namespace string   `json:"namespace"`
	Title     string   `json:"title"`
	Context   string   `json:"context"`
	Priority  string   `json:"priority"`
	Status    string   `json:"status"`
	Tags      []string `json:"tags"`
	BlockedBy []string `json:"blocked_by"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

func (a *App) handleMobileTasks(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	limit := parseMobileLimit(r, mobileDefaultLimit, mobileMaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	sessionFilter := strings.TrimSpace(r.URL.Query().Get("session_id"))
	searchFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))

	var (
		tasks []bridge.TaskInfo
		err   error
	)
	if sessionFilter != "" {
		tasks, err = a.agent.Tasks(sessionFilter)
	} else {
		tasks, err = a.agent.AllTasks()
	}
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list tasks")
		return
	}
	if tasks == nil {
		tasks = []bridge.TaskInfo{}
	}

	filtered := make([]bridge.TaskInfo, 0, len(tasks))
	counts := mobileTaskCounts{}
	for _, t := range tasks {
		taskStatus := normalizeMobileTaskStatus(t.Status)
		if statusFilter != "" && taskStatus != statusFilter {
			continue
		}
		if agentFilter != "" && !strings.EqualFold(strings.TrimSpace(t.AgentID), agentFilter) {
			continue
		}
		if searchFilter != "" {
			searchHaystack := strings.ToLower(strings.Join([]string{
				t.ID,
				t.SessionID,
				t.AgentID,
				t.Namespace,
				t.Title,
				t.Context,
			}, " "))
			if !strings.Contains(searchHaystack, searchFilter) {
				continue
			}
		}
		filtered = append(filtered, t)
		switch taskStatus {
		case "pending":
			counts.Pending++
		case "in_progress":
			counts.InProgress++
		case "blocked":
			counts.Blocked++
		case "completed":
			counts.Completed++
		}
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		ti := parseMobileTime(filtered[i].UpdatedAt)
		tj := parseMobileTime(filtered[j].UpdatedAt)
		if ti.Equal(tj) {
			return filtered[i].ID < filtered[j].ID
		}
		return ti.After(tj)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	result := make([]mobileTaskDTO, len(filtered))
	for i, t := range filtered {
		result[i] = mapMobileTask(t)
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"tasks":  result,
		"counts": counts,
	})
}

type mobileWorkflowDTO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name,omitempty"`
	Status      string  `json:"status"`
	CurrentStep string  `json:"current_step,omitempty"`
	Progress    float64 `json:"progress"`
	StartedAt   string  `json:"started_at"`
	CompletedAt string  `json:"completed_at,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type mobileWorkflowEventDTO struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Timestamp string `json:"timestamp"`
	StepID    string `json:"step_id,omitempty"`
	StepName  string `json:"step_name,omitempty"`
	Details   string `json:"details,omitempty"`
}

func (a *App) handleMobileWorkflows(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	limit := parseMobileLimit(r, mobileDefaultLimit, mobileMaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	workflows := a.workflowMonitor.Workflows()
	if workflows == nil {
		workflows = []bridge.WorkflowInfo{}
	}

	filtered := make([]bridge.WorkflowInfo, 0, len(workflows))
	pendingApprovals := 0
	for _, wf := range workflows {
		status := normalizeMobileWorkflowStatus(wf.Status)
		if statusFilter != "" && status != statusFilter {
			continue
		}
		if agentFilter != "" && !a.mobileWorkflowMatchesAgent(wf.ID, agentFilter) {
			continue
		}
		if status == "waiting_approval" {
			pendingApprovals++
		}
		filtered = append(filtered, wf)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		ti := parseMobileTime(filtered[i].CreatedAt)
		tj := parseMobileTime(filtered[j].CreatedAt)
		if ti.Equal(tj) {
			return filtered[i].ID < filtered[j].ID
		}
		return ti.After(tj)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	result := make([]mobileWorkflowDTO, len(filtered))
	for i, wf := range filtered {
		result[i] = mapMobileWorkflow(wf)
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"workflows":         result,
		"pending_approvals": pendingApprovals,
	})
}

func (a *App) handleMobileWorkflowDetail(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	workflowID := strings.TrimSpace(r.PathValue("workflow_id"))
	if workflowID == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "workflow_id is required")
		return
	}

	detail, err := a.workflowMonitor.Detail(workflowID)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load workflow detail")
		return
	}

	stepNames := make(map[string]string, len(detail.Steps))
	for _, step := range detail.Steps {
		stepNames[step.ID] = step.Name
	}
	events := make([]mobileWorkflowEventDTO, 0, len(detail.Events))
	for _, evt := range detail.Events {
		entry := mobileWorkflowEventDTO{
			ID:        evt.ID,
			EventType: evt.EventType,
			Timestamp: evt.Timestamp,
			StepID:    evt.StepID,
		}
		if entry.StepID != "" {
			entry.StepName = stepNames[entry.StepID]
		}
		if len(evt.Details) > 0 {
			if msg, ok := evt.Details["message"].(string); ok {
				entry.Details = strings.TrimSpace(msg)
			}
			if entry.Details == "" {
				if raw, marshalErr := json.Marshal(evt.Details); marshalErr == nil {
					entry.Details = string(raw)
				}
			}
		}
		events = append(events, entry)
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"workflow": map[string]any{
			"id":           detail.ID,
			"name":         detail.Name,
			"status":       normalizeMobileWorkflowStatus(detail.Status),
			"current_step": detail.CurrentStep,
			"progress":     detail.Progress,
			"started_at":   chooseFirstNonEmpty(detail.StartedAt, detail.CreatedAt),
			"completed_at": detail.CompletedAt,
			"error":        detail.Error,
			"steps":        mapMobileWorkflowSteps(detail.Steps),
		},
		"events": events,
	})
}

type mobilePresenceSummary struct {
	ActiveAgents  int `json:"active_agents"`
	IdleAgents    int `json:"idle_agents"`
	OfflineAgents int `json:"offline_agents"`
	TotalAgents   int `json:"total_agents"`
	ClaimCount    int `json:"claim_count"`
	WorktreeCount int `json:"worktree_count"`
}

func (a *App) handleMobilePresence(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	snap := a.fleetMonitor.Snapshot()
	limit := parseMobileLimit(r, mobileDefaultLimit, mobileMaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	agents := make([]bridge.PresenceInfo, 0, len(snap.Agents))
	for _, agent := range snap.Agents {
		status := normalizeMobilePresenceStatus(agent.Status)
		if statusFilter != "" && status != statusFilter {
			continue
		}
		if agentFilter != "" && !strings.EqualFold(agent.AgentID, agentFilter) {
			continue
		}
		agent.Status = status
		agents = append(agents, agent)
	}
	sort.SliceStable(agents, func(i, j int) bool {
		ti := parseMobileTime(agents[i].LastHeartbeat)
		tj := parseMobileTime(agents[j].LastHeartbeat)
		if ti.Equal(tj) {
			return agents[i].AgentID < agents[j].AgentID
		}
		return ti.After(tj)
	})
	if len(agents) > limit {
		agents = agents[:limit]
	}

	claims := make([]bridge.FileClaimInfo, 0, len(snap.FileClaims))
	for _, claim := range snap.FileClaims {
		if agentFilter != "" && !strings.EqualFold(claim.AgentID, agentFilter) {
			continue
		}
		claims = append(claims, claim)
	}
	sort.SliceStable(claims, func(i, j int) bool {
		ti := parseMobileTime(claims[i].CreatedAt)
		tj := parseMobileTime(claims[j].CreatedAt)
		if ti.Equal(tj) {
			return claims[i].ID < claims[j].ID
		}
		return ti.After(tj)
	})
	if len(claims) > limit {
		claims = claims[:limit]
	}

	worktrees := make([]bridge.WorktreeInfo, 0, len(snap.Worktrees))
	for _, wt := range snap.Worktrees {
		if agentFilter != "" && !strings.EqualFold(wt.AgentID, agentFilter) {
			continue
		}
		worktrees = append(worktrees, wt)
	}
	sort.SliceStable(worktrees, func(i, j int) bool {
		ti := parseMobileTime(worktrees[i].CreatedAt)
		tj := parseMobileTime(worktrees[j].CreatedAt)
		if ti.Equal(tj) {
			return worktrees[i].AssignmentID < worktrees[j].AssignmentID
		}
		return ti.After(tj)
	})
	if len(worktrees) > limit {
		worktrees = worktrees[:limit]
	}

	summary := mobilePresenceSummary{
		TotalAgents:   len(agents),
		ClaimCount:    len(claims),
		WorktreeCount: len(worktrees),
	}
	for _, agent := range agents {
		switch agent.Status {
		case "active":
			summary.ActiveAgents++
		case "idle":
			summary.IdleAgents++
		case "offline":
			summary.OfflineAgents++
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"agents":    agents,
		"claims":    claims,
		"worktrees": worktrees,
		"summary":   summary,
	})
}

func (a *App) handleMobileMemoryStats(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	stats := a.memoryMonitor.Stats()
	if stats == nil {
		directStats, err := a.agent.MemoryStats()
		if err != nil {
			a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load memory stats")
			return
		}
		stats = directStats
	}

	resp := map[string]any{
		"working_memory": map[string]any{
			"items":  stats.WorkingMemory.Items,
			"tokens": stats.WorkingMemory.Tokens,
		},
		"short_term_memory": map[string]any{
			"items":  stats.ShortTermMemory.Items,
			"tokens": stats.ShortTermMemory.Tokens,
		},
		"long_term_memory": map[string]any{
			"items":  stats.LongTermMemory.Items,
			"tokens": stats.LongTermMemory.Tokens,
		},
		"total_items":  stats.TotalItems,
		"total_tokens": stats.TotalTokens,
		"compression": map[string]any{
			"ratio":            stats.CompressionRatio,
			"added_24h":        stats.ItemsAddedLast24h,
			"compressed_24h":   stats.ItemsCompressedLast24h,
			"estimated_saved":  int(float64(stats.TotalTokens) * (1 - stats.CompressionRatio)),
			"compressed_items": stats.ItemsCompressedLast24h,
		},
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{"stats": resp})
}

type mobileMemoryItemDTO struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Content      string  `json:"content,omitempty"`
	Tier         string  `json:"tier"`
	Importance   string  `json:"importance"`
	ImportanceSc float64 `json:"importance_score"`
	Tokens       int     `json:"tokens"`
	Status       string  `json:"status,omitempty"`
	Category     string  `json:"category,omitempty"`
	AccessedAt   string  `json:"accessed_at,omitempty"`
	LastAccessed string  `json:"last_accessed,omitempty"`
}

func (a *App) handleMobileMemoryItems(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	tier, ok := normalizeMobileMemoryTier(strings.TrimSpace(r.URL.Query().Get("tier")))
	if !ok {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "tier must be one of working, short_term, long_term")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	limit := parseMobileLimit(r, mobileDefaultLimit, mobileMaxLimit)

	items, err := a.agent.MemoryRecall(tier, query, limit)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list memory items")
		return
	}
	if items == nil {
		items = []bridge.MemoryItem{}
	}

	sort.SliceStable(items, func(i, j int) bool {
		ti := parseMobileTime(chooseFirstNonEmpty(items[i].LastAccessed, items[i].AccessedAt))
		tj := parseMobileTime(chooseFirstNonEmpty(items[j].LastAccessed, items[j].AccessedAt))
		if ti.Equal(tj) {
			return items[i].ID < items[j].ID
		}
		return ti.After(tj)
	})

	result := make([]mobileMemoryItemDTO, len(items))
	for i, item := range items {
		result[i] = mobileMemoryItemDTO{
			ID:           item.ID,
			Title:        item.Title,
			Content:      item.Content,
			Tier:         normalizeMobileMemoryTierOutput(item.Tier),
			Importance:   normalizeMobileImportance(item.Importance),
			ImportanceSc: item.ImportanceScore,
			Tokens:       item.Tokens,
			Status:       strings.TrimSpace(item.Status),
			Category:     strings.TrimSpace(item.Category),
			AccessedAt:   item.AccessedAt,
			LastAccessed: item.LastAccessed,
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"items": result,
		"tier":  tier,
	})
}

type mobileStreamEntryDTO struct {
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

func (a *App) handleMobileStream(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	limit := parseMobileLimit(r, mobileDefaultLimit, mobileMaxLimit)
	typeFilter := parseMobileTypeFilter(r.URL.Query().Get("types"))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	sessionFilter := strings.TrimSpace(r.URL.Query().Get("session_id"))

	var (
		entries []bridge.ContextEntryInfo
		err     error
	)
	if sessionFilter != "" {
		entries, err = a.agent.SessionEntries(sessionFilter, mobileMaxLimit)
	} else {
		entries, err = a.agent.ContextStream(time.Time{}, mobileMaxLimit)
	}
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load context stream")
		return
	}

	result := make([]mobileStreamEntryDTO, 0, limit)
	for _, entry := range entries {
		kind := strings.TrimSpace(entry.Entry.EntryType)
		if len(typeFilter) > 0 {
			if _, ok := typeFilter[strings.ToLower(kind)]; !ok {
				continue
			}
		}
		if agentFilter != "" && !strings.EqualFold(entry.Entry.AgentID, agentFilter) {
			continue
		}
		result = append(result, mobileStreamEntryDTO{
			ID:        entry.Entry.ID,
			EntryType: kind,
			AgentID:   entry.Entry.AgentID,
			Agent:     entry.Entry.AgentID,
			Namespace: entry.Entry.Namespace,
			Title:     entry.Entry.Title,
			Content:   entry.Entry.Content,
			Timestamp: entry.Entry.Timestamp,
			Score:     entry.Score,
		})
		if len(result) >= limit {
			break
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{"entries": result})
}

type mobileTopologyNode struct {
	AgentID     string `json:"agent_id"`
	Status      string `json:"status"`
	AgentType   string `json:"agent_type"`
	CurrentTask string `json:"current_task,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRURL       string `json:"pr_url,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

type mobileTopologyEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	EdgeType string `json:"edge_type"`
	Weight   int    `json:"weight"`
	Label    string `json:"label,omitempty"`
	Status   string `json:"status,omitempty"`
}

type mobileTopologyCluster struct {
	Project  string   `json:"project"`
	AgentIDs []string `json:"agent_ids"`
}

func (a *App) handleMobileTopology(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	graph := computeTopology(a.fleetMonitor.Snapshot(), a)

	nodes := make([]mobileTopologyNode, len(graph.Nodes))
	for i, node := range graph.Nodes {
		nodes[i] = mobileTopologyNode{
			AgentID:     node.AgentID,
			Status:      normalizeMobilePresenceStatus(node.Status),
			AgentType:   node.AgentType,
			CurrentTask: node.CurrentTask,
			Branch:      node.Branch,
			PRURL:       node.PRUrl,
			Namespace:   node.Namespace,
		}
	}
	edges := make([]mobileTopologyEdge, len(graph.Edges))
	for i, edge := range graph.Edges {
		edges[i] = mobileTopologyEdge(edge)
	}
	clusters := make([]mobileTopologyCluster, len(graph.Clusters))
	for i, cluster := range graph.Clusters {
		agentIDs := cluster.AgentIDs
		if agentIDs == nil {
			agentIDs = []string{}
		}
		clusters[i] = mobileTopologyCluster{
			Project:  cluster.Project,
			AgentIDs: agentIDs,
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"nodes":      nodes,
		"edges":      edges,
		"clusters":   clusters,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (a *App) handleMobileGraphStats(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}
	stats, err := a.agent.GraphStats()
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load graph stats")
		return
	}
	if stats.EntityTypes == nil {
		stats.EntityTypes = map[string]int{}
	}
	if stats.RelationTypes == nil {
		stats.RelationTypes = map[string]int{}
	}
	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"stats": map[string]any{
			"total_entities":  stats.EntityCount,
			"total_relations": stats.RelationCount,
			"entity_types":    stats.EntityTypes,
			"relation_types":  stats.RelationTypes,
		},
	})
}

type mobileGraphEntityDTO struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	EntityType  string         `json:"entity_type"`
	Description string         `json:"description,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Properties  map[string]any `json:"properties"`
}

func (a *App) handleMobileGraphEntities(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	limit := parseMobileLimit(r, mobileDefaultLimit, mobileMaxLimit)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	entityType := strings.TrimSpace(r.URL.Query().Get("type"))

	entities, err := a.agent.EntityFind(query, entityType, limit)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list entities")
		return
	}
	if entities == nil {
		entities = []bridge.EntityInfo{}
	}

	sort.SliceStable(entities, func(i, j int) bool {
		ni := strings.ToLower(strings.TrimSpace(entities[i].Name))
		nj := strings.ToLower(strings.TrimSpace(entities[j].Name))
		if ni == nj {
			return entities[i].ID < entities[j].ID
		}
		return ni < nj
	})

	result := make([]mobileGraphEntityDTO, len(entities))
	for i, entity := range entities {
		entityKind := strings.TrimSpace(chooseFirstNonEmpty(entity.EntityType, entity.Type))
		if entityKind == "" {
			entityKind = "unknown"
		}
		props := entity.Properties
		if props == nil {
			props = map[string]any{}
		}
		result[i] = mobileGraphEntityDTO{
			ID:          entity.ID,
			Name:        entity.Name,
			EntityType:  entityKind,
			Description: entity.Description,
			Namespace:   entity.Namespace,
			Properties:  props,
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{"entities": result})
}

func (a *App) handleMobileGraphPath(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}
	sourceID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	if sourceID == "" || targetID == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "source_id and target_id are required")
		return
	}

	maxDepth := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("max_depth")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			a.writeMobileError(w, http.StatusBadRequest, "bad_request", "max_depth must be a positive integer")
			return
		}
		if parsed > 20 {
			parsed = 20
		}
		maxDepth = parsed
	}

	path, err := a.agent.GraphFindPath(sourceID, targetID, maxDepth)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to compute graph path")
		return
	}
	if path == nil {
		path = []bridge.EntityInfo{}
	}

	nodes := make([]mobileGraphEntityDTO, len(path))
	for i, entity := range path {
		entityKind := strings.TrimSpace(chooseFirstNonEmpty(entity.EntityType, entity.Type))
		if entityKind == "" {
			entityKind = "unknown"
		}
		props := entity.Properties
		if props == nil {
			props = map[string]any{}
		}
		nodes[i] = mobileGraphEntityDTO{
			ID:          entity.ID,
			Name:        entity.Name,
			EntityType:  entityKind,
			Description: entity.Description,
			Namespace:   entity.Namespace,
			Properties:  props,
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"path": map[string]any{
			"nodes":  nodes,
			"length": len(nodes),
		},
	})
}

type mobileReasoningStepDTO struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
	Evidence    string  `json:"evidence,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

type mobileReasoningChainDTO struct {
	ID          string                   `json:"id"`
	Title       string                   `json:"title"`
	Status      string                   `json:"status"`
	StepCount   int                      `json:"step_count"`
	Confidence  float64                  `json:"confidence,omitempty"`
	CreatedAt   string                   `json:"created_at"`
	CompletedAt string                   `json:"completed_at,omitempty"`
	Steps       []mobileReasoningStepDTO `json:"steps,omitempty"`
}

func (a *App) handleMobileReasoningChains(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	limit := parseMobileLimit(r, mobileDefaultLimit, mobileMaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))

	chains, err := a.agent.ReasoningChainList()
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list reasoning chains")
		return
	}
	if chains == nil {
		chains = []bridge.ReasoningChainInfo{}
	}

	filtered := make([]bridge.ReasoningChainInfo, 0, len(chains))
	for _, chain := range chains {
		status := normalizeMobileReasoningStatus(chain.Status)
		if statusFilter != "" && status != statusFilter {
			continue
		}
		chain.Status = status
		filtered = append(filtered, chain)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		ti := parseMobileTime(filtered[i].CreatedAt)
		tj := parseMobileTime(filtered[j].CreatedAt)
		if ti.Equal(tj) {
			return filtered[i].ID < filtered[j].ID
		}
		return ti.After(tj)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	result := make([]mobileReasoningChainDTO, len(filtered))
	for i, chain := range filtered {
		result[i] = mobileReasoningChainDTO{
			ID:          chain.ID,
			Title:       chain.Title,
			Status:      chain.Status,
			StepCount:   chain.StepCount,
			Confidence:  chain.Confidence,
			CreatedAt:   chain.CreatedAt,
			CompletedAt: chain.CompletedAt,
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{"chains": result})
}

func (a *App) handleMobileReasoningChainDetail(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	chainID := strings.TrimSpace(r.PathValue("chain_id"))
	if chainID == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "chain_id is required")
		return
	}

	detail, err := a.agent.ReasoningChainGet(chainID)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load reasoning chain")
		return
	}

	steps := make([]mobileReasoningStepDTO, len(detail.Steps))
	for i, step := range detail.Steps {
		steps[i] = mobileReasoningStepDTO{
			ID:          step.ID,
			Description: step.Description,
			Confidence:  step.Confidence,
			Evidence:    step.Evidence,
			CreatedAt:   step.CreatedAt,
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"chain": mobileReasoningChainDTO{
			ID:          detail.ID,
			Title:       detail.Title,
			Status:      normalizeMobileReasoningStatus(detail.Status),
			StepCount:   detail.StepCount,
			Confidence:  detail.Confidence,
			CreatedAt:   detail.CreatedAt,
			CompletedAt: detail.CompletedAt,
			Steps:       steps,
		},
	})
}

func (a *App) handleMobileEventsStream(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}
	a.handleSSE(w, r)
}

func (a *App) handleMobileSessionCreate(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeSessionCreate) {
		return
	}

	var body bridge.SessionStartParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.AgentID == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "agent_id is required")
		return
	}

	a.logMobileAudit(r, "session_create", map[string]string{
		"agent_id":  body.AgentID,
		"namespace": body.Namespace,
	}, "initiated", nil)

	result, err := a.agent.StartSession(body)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to start session")
		return
	}

	a.broadcastAgentEvent("agent.session.start", map[string]any{
		"session_id": result.SessionID,
		"agent_id":   body.AgentID,
		"agent_type": body.AgentType,
		"namespace":  body.Namespace,
	})
	if !result.AlreadyExisted {
		a.fleetMonitor.IncrementKPI("sessions", 1)
	}
	go a.fleetMonitor.Refresh()
	go a.maybeAutoProvisionSandbox(body.Namespace)

	a.writeMobileJSON(w, http.StatusOK, result)
}

func (a *App) handleMobileSessionEnd(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeSessionEnd) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "session_id is required")
		return
	}

	var body struct {
		Summarize *bool `json:"summarize,omitempty"`
	}
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			a.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				a.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
				return
			}
		}
	}

	summarize := true
	if body.Summarize != nil {
		summarize = *body.Summarize
	}

	a.logMobileAudit(r, "session_end", map[string]string{
		"session_id": sessionID,
		"summarize":  strconv.FormatBool(summarize),
	}, "initiated", nil)

	endParams := bridge.SessionEndParams{
		SessionID: sessionID,
		Summarize: body.Summarize,
	}
	ended, err := a.agent.EndSession(endParams)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to end session")
		return
	}

	if ended {
		a.broadcastAgentEvent("agent.session.end", map[string]any{
			"session_id": sessionID,
		})
		go a.fleetMonitor.Refresh()
		if a.coordinator != nil {
			go a.coordinator.OnSessionEnd(sessionID, "")
		}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"ended":      ended,
		"session_id": sessionID,
	})
}

func (a *App) handleMobileAudit(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))

	// Audit entries are currently only in structured log output, not in a
	// queryable store. Return matching event-log entries as a best-effort
	// proxy until persistent audit storage is added (M5).
	var entries []TimelineEntry
	if a.eventLog != nil {
		for _, evt := range a.eventLog.All(500) {
			if sourceFilter != "" && !eventHasField(evt.Data, "source", sourceFilter) {
				continue
			}
			entries = append(entries, evt)
			if len(entries) >= limit {
				break
			}
		}
	}
	if entries == nil {
		entries = []TimelineEntry{}
	}

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"source":  sourceFilter,
		"count":   len(entries),
	})
}

func (a *App) handleMobileAdminRevoke(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdminToken(w, r) {
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			a.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				a.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
				return
			}
		}
	}

	if strings.TrimSpace(body.Token) == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "token field is required")
		return
	}

	if a.mobileRevocationList != nil {
		a.mobileRevocationList.Revoke(body.Token)
	}

	a.logMobileAudit(r, "token_revoke", nil, "success", nil)
	a.writeMobileJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

// --- Notification policy (MBL-6) ---

// mobileAlertPolicyEntry describes a single event type's notification policy.
type mobileAlertPolicyEntry struct {
	EventType         string   `json:"event_type"`
	Severity          string   `json:"severity"`
	InterruptionLevel string   `json:"interruption_level"`
	Title             string   `json:"title"`
	AllowedActions    []string `json:"allowed_actions"`
	Conditional       bool     `json:"conditional"`
}

// mobileAlertPolicyMatrix returns the canonical event-to-severity-interruption-action
// matrix. Health events are listed twice (conditional on payload).
func mobileAlertPolicyMatrix() []mobileAlertPolicyEntry {
	return []mobileAlertPolicyEntry{
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

func (a *App) handleMobileAlertsPolicy(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}
	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"policy":  mobileAlertPolicyMatrix(),
		"version": "v1",
	})
}

// --- Push token registration (MBL-7) ---

func (a *App) handleMobilePushRegister(w http.ResponseWriter, r *http.Request) {
	if !a.config.MobilePushEnabled {
		a.writeMobileError(w, http.StatusNotFound, "not_found", "push notifications are not enabled")
		return
	}
	if !a.requireMobileScope(w, r, mobileScopePush) {
		return
	}

	var body struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	token := strings.TrimSpace(body.Token)
	platform := strings.TrimSpace(body.Platform)

	if token == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	if platform != "apns" && platform != "fcm" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "platform must be 'apns' or 'fcm'")
		return
	}

	deviceID := extractDeviceID(r)
	regID := a.deviceTokenStore.Register(token, deviceID, platform)

	a.logMobileAudit(r, "push_register", map[string]string{
		"platform": platform,
	}, "success", nil)

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"registered":      true,
		"registration_id": regID,
	})
}

func (a *App) handleMobilePushUnregister(w http.ResponseWriter, r *http.Request) {
	if !a.config.MobilePushEnabled {
		a.writeMobileError(w, http.StatusNotFound, "not_found", "push notifications are not enabled")
		return
	}
	if !a.requireMobileScope(w, r, mobileScopePush) {
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		a.writeMobileError(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}

	removed := a.deviceTokenStore.Invalidate(token)

	a.logMobileAudit(r, "push_unregister", nil, "success", nil)

	a.writeMobileJSON(w, http.StatusOK, map[string]any{
		"removed": removed,
	})
}

// handleMobileSandbox returns sandbox/devbox summary via mobile envelope.
// GET /api/mobile/v1/sandbox
func (a *App) handleMobileSandbox(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	if cached, ok := a.cache.Get("sandbox_summary"); ok {
		a.writeMobileJSON(w, http.StatusOK, cached)
		return
	}

	result, err := a.client.CallTool("devbox_summary", nil)
	if err != nil {
		a.logger.Debug("devbox_summary call failed, returning unavailable", "error", err)
		fallback := map[string]any{"available": false}
		a.cache.Set("sandbox_summary", fallback, 5*time.Second)
		a.writeMobileJSON(w, http.StatusOK, fallback)
		return
	}

	summary, err := bridge.ParseToolResultMap(result)
	if err != nil {
		a.logger.Debug("devbox_summary unmarshal failed", "error", err)
		a.writeMobileJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	summary["available"] = true
	a.cache.Set("sandbox_summary", summary, 5*time.Second)
	a.writeMobileJSON(w, http.StatusOK, summary)
}

// handleMobileSandboxStart triggers devbox_build for a project.
// POST /api/mobile/v1/sandbox/start
func (a *App) handleMobileSandboxStart(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeSessionCreate) {
		return
	}

	var body struct {
		Project string `json:"project"`
		AgentID string `json:"agent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeMobileError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if body.Project == "" {
		a.writeMobileError(w, http.StatusBadRequest, "missing_project", "project is required")
		return
	}

	args := map[string]any{"project": body.Project}
	if body.AgentID != "" {
		args["agent_id"] = body.AgentID
	}
	result, err := a.client.CallTool("devbox_build", args)
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "devbox_build_failed", "failed to start sandbox: "+err.Error())
		return
	}

	a.cache.Invalidate("sandbox_summary")

	parsed, err := bridge.ParseToolResultMap(result)
	if err != nil {
		a.writeMobileJSON(w, http.StatusOK, map[string]any{"started": true, "project": body.Project})
		return
	}
	parsed["started"] = true
	a.writeMobileJSON(w, http.StatusOK, parsed)
}

// handleMobileSandboxStop stops a running sandbox for a project.
// POST /api/mobile/v1/sandbox/stop
func (a *App) handleMobileSandboxStop(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeSessionCreate) {
		return
	}

	var body struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeMobileError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if body.Project == "" {
		a.writeMobileError(w, http.StatusBadRequest, "missing_project", "project is required")
		return
	}

	_, err := a.client.CallTool("devbox_stop", map[string]any{"project": body.Project})
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "devbox_stop_failed", "failed to stop sandbox: "+err.Error())
		return
	}

	a.cache.Invalidate("sandbox_summary")
	a.writeMobileJSON(w, http.StatusOK, map[string]any{"stopped": true, "project": body.Project})
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

func mapMobileTask(task bridge.TaskInfo) mobileTaskDTO {
	tags := task.Tags
	if tags == nil {
		tags = []string{}
	}
	blockedBy := task.BlockedBy
	if blockedBy == nil {
		blockedBy = []string{}
	}
	return mobileTaskDTO{
		ID:        task.ID,
		SessionID: task.SessionID,
		AgentID:   task.AgentID,
		Namespace: task.Namespace,
		Title:     task.Title,
		Context:   task.Context,
		Priority:  normalizeMobilePriority(task.Priority),
		Status:    normalizeMobileTaskStatus(task.Status),
		Tags:      tags,
		BlockedBy: blockedBy,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
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

func mapMobileWorkflow(workflow bridge.WorkflowInfo) mobileWorkflowDTO {
	return mobileWorkflowDTO{
		ID:          workflow.ID,
		Name:        workflow.Name,
		Status:      normalizeMobileWorkflowStatus(workflow.Status),
		CurrentStep: workflow.CurrentStep,
		Progress:    workflow.Progress,
		StartedAt:   workflow.CreatedAt,
		Error:       workflow.Error,
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

func (a *App) mobileWorkflowMatchesAgent(workflowID, agentID string) bool {
	workflowID = strings.TrimSpace(workflowID)
	agentID = strings.TrimSpace(agentID)
	if workflowID == "" || agentID == "" {
		return true
	}
	detail, err := a.workflowMonitor.Detail(workflowID)
	if err != nil {
		return false
	}
	lowerAgent := strings.ToLower(agentID)
	if strings.Contains(strings.ToLower(detail.Name), lowerAgent) {
		return true
	}
	for _, event := range detail.Events {
		if strings.Contains(strings.ToLower(event.StepID), lowerAgent) {
			return true
		}
		for _, value := range event.Details {
			if strings.Contains(strings.ToLower(strings.TrimSpace(toMobileText(value))), lowerAgent) {
				return true
			}
		}
	}
	return false
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

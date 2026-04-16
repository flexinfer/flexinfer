// app_routes_operations.go contains session, task, and CRUD HTTP handlers
// plus background reapers for session and push-token lifecycle management.
//
// Handlers in this file make direct bridge calls (not cached snapshots)
// because they require parameterized queries or mutation semantics.
package hud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// --- API handlers: Direct bridge calls (parameterized queries) ---

const (
	hudSessionListTimeout = 3 * time.Second
	hudSessionListLimit   = 1000
	hudSessionTraceLimit  = 100
	hudSessionTraceMax    = 500
)

func (a *App) handleSessions(w http.ResponseWriter, r *http.Request) {
	req := bridge.SessionListRequest{
		AgentID:   strings.TrimSpace(r.URL.Query().Get("agent_id")),
		Namespace: strings.TrimSpace(r.URL.Query().Get("namespace")),
		Status:    normalizeSessionStatusQuery(r.URL.Query().Get("status")),
		Limit:     hudSessionListLimit,
	}
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 {
			req.Limit = parsed
		}
	}

	params, err := req.Params()
	if err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid session query", err)
		return
	}

	sessions, err := a.agent.SessionsWithParams(params, hudSessionListTimeout)
	if err != nil {
		a.logger.Warn("sessions upstream error, falling back to fleet snapshot", "error", err)
		sessions = a.fleetMonitor.Snapshot().Sessions
	}
	if sessions == nil {
		sessions = []bridge.SessionInfo{}
	}

	var since *time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if parsedSince, parseErr := time.Parse(time.RFC3339, sinceStr); parseErr == nil {
			since = &parsedSince
		}
	}

	sessions = filterSessionsForResponse(sessions, req, since)
	a.writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func normalizeSessionStatusQuery(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	switch trimmed {
	case "", "all", "*":
		return ""
	default:
		return trimmed
	}
}

func filterSessionsForResponse(sessions []bridge.SessionInfo, req bridge.SessionListRequest, since *time.Time) []bridge.SessionInfo {
	filtered := make([]bridge.SessionInfo, 0, len(sessions))
	wantAgentID := strings.TrimSpace(req.AgentID)
	wantNamespace := strings.TrimSpace(req.Namespace)
	wantStatus := strings.ToLower(strings.TrimSpace(req.Status))

	for _, s := range sessions {
		if wantAgentID != "" && strings.TrimSpace(s.AgentID) != wantAgentID {
			continue
		}
		if wantNamespace != "" && strings.TrimSpace(s.Namespace) != wantNamespace {
			continue
		}
		if wantStatus != "" && strings.ToLower(strings.TrimSpace(s.Status)) != wantStatus {
			continue
		}
		if since != nil {
			if s.EndedAt == "" {
				filtered = append(filtered, s)
				continue
			}
			started, err := time.Parse(time.RFC3339, s.StartedAt)
			if err != nil || started.Before(*since) {
				continue
			}
		}
		filtered = append(filtered, s)
		if req.Limit > 0 && len(filtered) >= req.Limit {
			break
		}
	}

	return filtered
}

func (a *App) handleSessionEntries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing session id", nil)
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	entries, err := a.agent.SessionEntries(id, limit)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to get session entries", err)
		return
	}
	flat := make([]map[string]any, len(entries))
	for i, e := range entries {
		flat[i] = map[string]any{
			"id":          e.Entry.ID,
			"entry_type":  e.Entry.EntryType,
			"agent_id":    e.Entry.AgentID,
			"namespace":   e.Entry.Namespace,
			"title":       e.Entry.Title,
			"content":     e.Entry.Content,
			"timestamp":   e.Entry.Timestamp,
			"score":       e.Score,
			"file_path":   e.Entry.FilePath,
			"line_start":  e.Entry.LineStart,
			"line_end":    e.Entry.LineEnd,
			"token_count": e.Entry.TokenCount,
		}
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"entries": flat})
}

type sessionTraceError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

type sessionTraceResponse struct {
	Session      *bridge.SessionInfo `json:"session,omitempty"`
	AgentID      string              `json:"agent_id,omitempty"`
	SessionID    string              `json:"session_id"`
	Entries      []map[string]any    `json:"entries"`
	Events       []TimelineEntry     `json:"events"`
	Traces       []traceAPIEntry     `json:"traces"`
	TraceEnabled bool                `json:"trace_enabled"`
	TracePath    string              `json:"trace_path,omitempty"`
	Errors       []sessionTraceError `json:"errors,omitempty"`
	RetrievedAt  string              `json:"retrieved_at"`
}

func (a *App) handleSessionTrace(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("id"))
	if sessionID == "" {
		a.writeError(w, http.StatusBadRequest, "missing session id", nil)
		return
	}
	limit := parseSessionTraceLimit(r.URL.Query().Get("limit"))
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	resp := sessionTraceResponse{
		SessionID:   sessionID,
		AgentID:     agentID,
		Entries:     []map[string]any{},
		Events:      []TimelineEntry{},
		Traces:      []traceAPIEntry{},
		RetrievedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if session := a.findSessionForTrace(sessionID); session != nil {
		resp.Session = session
		resp.AgentID = session.AgentID
	}

	if entries, err := a.agent.SessionEntries(sessionID, limit); err != nil {
		resp.Errors = append(resp.Errors, sessionTraceError{Source: "context_entries", Message: err.Error()})
	} else {
		resp.Entries = flattenSessionEntries(entries)
	}

	resp.Events = a.sessionTraceEvents(sessionID, resp.Session, resp.AgentID, limit)

	if traces, err := a.fetchAuditTraces(limit); err != nil {
		resp.Errors = append(resp.Errors, sessionTraceError{Source: "audit_traces", Message: err.Error()})
	} else {
		resp.TraceEnabled = traces.Enabled
		resp.TracePath = traces.Path
		resp.Traces = filterSessionTraceAuditEntries(traces.Traces, resp.Session, resp.AgentID, limit)
	}

	a.writeJSON(w, http.StatusOK, resp)
}

func parseSessionTraceLimit(raw string) int {
	limit := hudSessionTraceLimit
	if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && parsed > 0 {
		limit = parsed
	}
	if limit > hudSessionTraceMax {
		return hudSessionTraceMax
	}
	return limit
}

func (a *App) findSessionForTrace(sessionID string) *bridge.SessionInfo {
	snap := a.fleetMonitor.Snapshot()
	for i := range snap.Sessions {
		if strings.TrimSpace(snap.Sessions[i].ID) == sessionID {
			session := snap.Sessions[i]
			return &session
		}
	}
	return nil
}

func flattenSessionEntries(entries []bridge.ContextEntryInfo) []map[string]any {
	flat := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		flat = append(flat, map[string]any{
			"id":          e.Entry.ID,
			"entry_type":  e.Entry.EntryType,
			"agent_id":    e.Entry.AgentID,
			"namespace":   e.Entry.Namespace,
			"title":       e.Entry.Title,
			"content":     e.Entry.Content,
			"timestamp":   e.Entry.Timestamp,
			"score":       e.Score,
			"file_path":   e.Entry.FilePath,
			"line_start":  e.Entry.LineStart,
			"line_end":    e.Entry.LineEnd,
			"token_count": e.Entry.TokenCount,
		})
	}
	return flat
}

func (a *App) sessionTraceEvents(sessionID string, session *bridge.SessionInfo, agentID string, limit int) []TimelineEntry {
	if a.eventLog == nil {
		return []TimelineEntry{}
	}
	events := make([]TimelineEntry, 0, limit)
	for _, evt := range a.eventLog.All(1000) {
		if eventMatchesSessionTrace(evt, sessionID, session, agentID) {
			events = append(events, evt)
			if limit > 0 && len(events) >= limit {
				break
			}
		}
	}
	if events == nil {
		return []TimelineEntry{}
	}
	return events
}

func eventMatchesSessionTrace(evt TimelineEntry, sessionID string, session *bridge.SessionInfo, agentID string) bool {
	if sessionID != "" && eventDataString(evt.Data, "session_id") == sessionID {
		return true
	}
	if agentID == "" || evt.AgentID != agentID {
		return false
	}
	if session == nil {
		return true
	}
	return timeWithinSessionTraceBounds(evt.Timestamp, session)
}

func filterSessionTraceAuditEntries(traces []traceAPIEntry, session *bridge.SessionInfo, agentID string, limit int) []traceAPIEntry {
	capacity := len(traces)
	if limit > 0 && limit < capacity {
		capacity = limit
	}
	filtered := make([]traceAPIEntry, 0, capacity)
	for _, trace := range traces {
		if agentID != "" && trace.AgentID != agentID {
			continue
		}
		if session != nil {
			ts := parseSessionTraceTime(trace.Timestamp)
			if !ts.IsZero() && !timeWithinSessionTraceBounds(ts, session) {
				continue
			}
		}
		filtered = append(filtered, trace)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}
	if filtered == nil {
		return []traceAPIEntry{}
	}
	return filtered
}

func timeWithinSessionTraceBounds(ts time.Time, session *bridge.SessionInfo) bool {
	if session == nil || ts.IsZero() {
		return true
	}
	if start := parseSessionTraceTime(session.StartedAt); !start.IsZero() && ts.Before(start.Add(-2*time.Second)) {
		return false
	}
	if end := parseSessionTraceTime(session.EndedAt); !end.IsZero() && ts.After(end.Add(2*time.Second)) {
		return false
	}
	return true
}

func eventDataString(data json.RawMessage, key string) string {
	if len(data) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}
	if raw, ok := payload[key].(string); ok {
		return strings.TrimSpace(raw)
	}
	return ""
}

func parseSessionTraceTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	return time.Time{}
}

func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	var (
		tasks []bridge.TaskInfo
		err   error
	)
	if sessionID != "" {
		tasks, err = a.agent.Tasks(sessionID)
	} else {
		tasks, err = a.agent.AllTasks()
	}
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list tasks", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

// --- API handlers: CRUD operations (v2) ---

func (a *App) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID  string   `json:"session_id"`
		Title      string   `json:"title"`
		Priority   string   `json:"priority"`
		Tags       []string `json:"tags"`
		Context    string   `json:"context"`
		FilePath   string   `json:"file_path"`
		LineNumber int      `json:"line_number"`
		BlockedBy  []string `json:"blocked_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" {
		a.writeError(w, http.StatusBadRequest, "title is required", nil)
		return
	}
	body.Priority = normalizeTaskPriority(body.Priority)
	body.Tags = normalizeStringList(body.Tags)
	body.BlockedBy = normalizeStringList(body.BlockedBy)
	body.Context = strings.TrimSpace(body.Context)
	body.FilePath = strings.TrimSpace(body.FilePath)
	if body.LineNumber < 0 {
		body.LineNumber = 0
	}
	taskResult, err := a.agent.CreateTask(bridge.CreateTaskParams{
		SessionID:  body.SessionID,
		Title:      body.Title,
		Priority:   body.Priority,
		Tags:       body.Tags,
		Context:    body.Context,
		FilePath:   body.FilePath,
		LineNumber: body.LineNumber,
		BlockedBy:  body.BlockedBy,
	})
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create task", err)
		return
	}
	a.broadcastAgentEvent("hud.task.create", map[string]any{
		"title":    body.Title,
		"priority": body.Priority,
	})
	taskID := ""
	if taskResult != nil && len(taskResult.TaskIDs) > 0 {
		taskID = taskResult.TaskIDs[0]
	}
	a.writeJSON(w, http.StatusCreated, map[string]any{"status": "created", "task_id": taskID})
}

func (a *App) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		a.writeError(w, http.StatusBadRequest, "missing task id", nil)
		return
	}
	var body struct {
		Status     string `json:"status"`
		Priority   string `json:"priority"`
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if err := a.agent.UpdateTask(bridge.UpdateTaskParams{
		ID:         id,
		Status:     body.Status,
		Priority:   body.Priority,
		Resolution: body.Resolution,
	}); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to update task", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// handleSSE delegates to the SSE hub to stream real-time daemon events to
// browser clients. Falls back to heartbeat-only if the hub is not initialized.
func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	if a.sseHub != nil {
		a.sseHub.ServeHTTP(w, r)
		return
	}

	// Fallback: heartbeat-only SSE stream.
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, http.StatusInternalServerError, "streaming not supported", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if a.config.Dev {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}

	fmt.Fprintf(w, "event: connected\ndata: {\"time\":%q}\n\n", time.Now().Format(time.RFC3339))
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case t := <-ticker.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":%q}\n\n", t.Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

func (a *App) handleTemplateList(w http.ResponseWriter, _ *http.Request) {
	templates, err := a.agent.TemplateList()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list templates", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (a *App) handleAnnotationList(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("file")
	annotations, err := a.agent.AnnotationGet(filePath)
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list annotations", err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"annotations": annotations})
}

func (a *App) handleAnnotationCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
		Category string `json:"category"`
		Line     int    `json:"line"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.FilePath == "" || body.Content == "" {
		a.writeError(w, http.StatusBadRequest, "file_path and content are required", nil)
		return
	}
	if err := a.agent.AnnotationAdd(body.FilePath, body.Content, body.Category, body.Line); err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to create annotation", err)
		return
	}
	a.writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

// --- Sandbox helpers ---

// doSandboxStart calls devbox_build through the daemon and refreshes the
// sandbox monitor. Returns the parsed result map or an error.
func (a *App) doSandboxStart(project, agentID string) (map[string]any, error) {
	args := map[string]any{"project": project}
	if agentID != "" {
		args["agent_id"] = agentID
	}
	result, err := a.client.CallTool("devbox_build", args)
	if err != nil {
		return nil, err
	}
	go a.sandboxMonitor.Refresh()

	parsed, err := bridge.ParseToolResultMap(result)
	if err != nil {
		return nil, nil // non-fatal: build succeeded but response is opaque
	}
	return parsed, nil
}

// doSandboxStop stops a running sandbox and refreshes the sandbox monitor.
func (a *App) doSandboxStop(project string) error {
	_, err := a.client.CallTool("devbox_stop", map[string]any{"project": project})
	if err != nil {
		return err
	}
	go a.sandboxMonitor.Refresh()
	return nil
}

// doSandboxExecAsync starts a long-running sandbox command and returns its exec metadata.
func (a *App) doSandboxExecAsync(project, command, timeout, agentID string) (map[string]any, error) {
	args := map[string]any{
		"project": project,
		"command": command,
	}
	if timeout != "" {
		args["timeout"] = timeout
	}
	if agentID != "" {
		args["agent_id"] = agentID
	}
	result, err := a.client.CallTool("devbox_exec_async", args)
	if err != nil {
		return nil, err
	}
	go a.sandboxMonitor.Refresh()

	parsed, err := bridge.ParseToolResultMap(result)
	if err != nil {
		return nil, nil
	}
	return parsed, nil
}

// doSandboxExecPoll reads the latest status of an async sandbox exec.
func (a *App) doSandboxExecPoll(execID string) (map[string]any, error) {
	result, err := a.client.CallTool("devbox_exec_poll", map[string]any{"exec_id": execID})
	if err != nil {
		return nil, err
	}
	parsed, err := bridge.ParseToolResultMap(result)
	if err != nil {
		return nil, nil
	}
	return parsed, nil
}

// doSandboxStatus calls devbox_status, optionally filtered to a project.
// Returns the "sandboxes" array from the tool result.
func (a *App) doSandboxStatus(project string) ([]map[string]any, error) {
	args := map[string]any{}
	if project != "" {
		args["project"] = project
	}
	result, err := a.client.CallTool("devbox_status", args)
	if err != nil {
		return nil, err
	}
	parsed, err := bridge.ParseToolResultMap(result)
	if err != nil {
		return nil, nil
	}
	sandboxes, _ := parsed["sandboxes"].([]any)
	out := make([]map[string]any, 0, len(sandboxes))
	for _, s := range sandboxes {
		if sm, ok := s.(map[string]any); ok {
			out = append(out, sm)
		}
	}
	return out, nil
}

// --- Session reaper ---

// sessionReaper periodically checks for offline agents with active sessions
// and auto-ends them. This ensures heartbeat-only agents (like Codex) get
// reliable session cleanup without native session-end hooks.
func isMobileManagedPresence(agent bridge.PresenceInfo) bool {
	if strings.EqualFold(strings.TrimSpace(agent.AgentType), "mobile") {
		return true
	}
	desc := strings.ToLower(strings.TrimSpace(agent.Description))
	return strings.HasPrefix(desc, "mobile session")
}

func (a *App) sessionReaper(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	const offlineThreshold = 5 * time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap := a.fleetMonitor.Snapshot()
			now := time.Now()

			for _, agent := range snap.Agents {
				if agent.Status != "offline" {
					continue
				}
				if isMobileManagedPresence(agent) {
					continue
				}
				// Check if agent has been offline long enough.
				hb, err := time.Parse(time.RFC3339, agent.LastHeartbeat)
				if err != nil {
					continue
				}
				if now.Sub(hb) < offlineThreshold {
					continue
				}

				// Find active session for this agent.
				session, err := a.agent.GetActiveSession(agent.AgentID)
				if err != nil || session == nil {
					continue
				}

				a.logger.Info("session reaper: ending orphaned session",
					"agent_id", agent.AgentID,
					"session_id", session.ID,
					"offline_since", agent.LastHeartbeat)

				summarize := true
				_, endErr := a.agent.EndSession(bridge.SessionEndParams{
					SessionID: session.ID,
					AgentID:   agent.AgentID,
					Summarize: &summarize,
				})
				if endErr != nil {
					a.logger.Warn("session reaper: failed to end session",
						"agent_id", agent.AgentID, "error", endErr)
					continue
				}

				a.broadcastAgentEvent("agent.session.reaped", map[string]any{
					"agent_id":   agent.AgentID,
					"session_id": session.ID,
					"reason":     "offline_timeout",
				})

				go a.fleetMonitor.Refresh()
			}
		}
	}
}

// --- Push token reaper ---

// pushTokenReaper periodically removes stale push registration tokens.
func (a *App) pushTokenReaper(ctx context.Context) {
	if a.deviceTokenStore == nil {
		return
	}
	ticker := time.NewTicker(pushTokenCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if removed := a.cleanupStalePushTokensNow(time.Now(), pushTokenMaxIdle); removed > 0 {
				a.logger.Info("push token reaper removed stale device tokens", "removed", removed)
			}
		}
	}
}

// cleanupStalePushTokensNow removes tokens that have been idle longer than maxIdle.
// It is extracted for deterministic tests and one-shot invocations.
func (a *App) cleanupStalePushTokensNow(now time.Time, maxIdle time.Duration) int {
	if a.deviceTokenStore == nil || maxIdle <= 0 {
		return 0
	}
	cutoff := now.Add(-maxIdle)
	return a.deviceTokenStore.CleanupStale(cutoff)
}

// --- Validation helpers ---

func normalizeTaskPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low", "medium", "high", "critical":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return "medium"
	}
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		normalized = append(normalized, v)
	}
	return normalized
}

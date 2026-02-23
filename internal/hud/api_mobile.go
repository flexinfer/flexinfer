package hud

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxDeviceIDLen = 128

const (
	mobileScopeRead          = "mobile:read"
	mobileScopeSessionCreate = "mobile:session:create"
	mobileScopeSessionEnd    = "mobile:session:end"
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

func (a *App) handleMobileSessions(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	sessions, err := a.agent.Sessions()
	if err != nil {
		a.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list sessions")
		return
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

	// Read the body to extract audit fields before forwarding.
	var reqBody struct {
		AgentID   string `json:"agent_id"`
		Namespace string `json:"namespace"`
	}
	bodyBytes, _ := io.ReadAll(r.Body)
	if len(bytes.TrimSpace(bodyBytes)) > 0 {
		_ = json.Unmarshal(bodyBytes, &reqBody)
	}

	a.logMobileAudit(r, "session_create", map[string]string{
		"agent_id":  reqBody.AgentID,
		"namespace": reqBody.Namespace,
	}, "initiated", nil)

	// Reconstruct request body for the downstream handler.
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	r.ContentLength = int64(len(bodyBytes))
	a.handleAgentSessionStart(w, r)
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
		Summarize bool `json:"summarize"`
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

	a.logMobileAudit(r, "session_end", map[string]string{
		"session_id": sessionID,
		"summarize":  strconv.FormatBool(body.Summarize),
	}, "initiated", nil)

	proxyBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"summarize":  body.Summarize,
	})
	proxyReq := r.Clone(r.Context())
	proxyReq.Body = io.NopCloser(bytes.NewReader(proxyBody))
	proxyReq.ContentLength = int64(len(proxyBody))
	proxyReq.Header.Set("Content-Type", "application/json")
	a.handleAgentSessionEnd(w, proxyReq)
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

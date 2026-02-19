package hud

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	mobileScopeRead          = "mobile:read"
	mobileScopeSessionCreate = "mobile:session:create"
	mobileScopeSessionEnd    = "mobile:session:end"
)

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
		a.writeError(w, http.StatusForbidden, "mobile operator token is not configured; set HUD_MOBILE_OPERATOR_TOKEN", nil)
		return false
	}

	actual := extractBearerToken(r)
	if actual == "" {
		a.writeError(w, http.StatusUnauthorized, "mobile bearer token is required", nil)
		return false
	}

	if subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		a.writeError(w, http.StatusUnauthorized, "invalid mobile bearer token", nil)
		return false
	}

	if !a.mobileScopeAllowed(requiredScope) {
		a.writeError(w, http.StatusForbidden, "mobile token missing required scope", nil)
		return false
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

// --- Mobile companion v1 handlers ---

func (a *App) handleMobilePing(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleMobileDashboard(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	snap := a.fleetMonitor.Snapshot()
	a.writeJSON(w, http.StatusOK, map[string]any{
		"daemon_running":  snap.DaemonRunning,
		"server_count":    snap.ServerCount,
		"active_sessions": snap.ActiveSessions,
		"active_agents":   snap.ActiveAgents,
		"idle_agents":     snap.IdleAgents,
		"offline_agents":  snap.OfflineAgents,
		"updated_at":      snap.UpdatedAt,
	})
}

func (a *App) handleMobileSessions(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}
	a.handleSessions(w, r)
}

func (a *App) handleMobileSessionDetail(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		a.writeError(w, http.StatusBadRequest, "session_id is required", nil)
		return
	}

	sessions, err := a.agent.Sessions()
	if err != nil {
		a.writeError(w, http.StatusBadGateway, "failed to list sessions", err)
		return
	}

	for _, s := range sessions {
		if strings.TrimSpace(s.ID) == sessionID {
			a.writeJSON(w, http.StatusOK, map[string]any{"session": s})
			return
		}
	}

	a.writeError(w, http.StatusNotFound, "session not found", nil)
}

func (a *App) handleMobileSessionEvents(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeRead) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		a.writeError(w, http.StatusBadRequest, "session_id is required", nil)
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

	a.writeJSON(w, http.StatusOK, map[string]any{
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
	a.handleAgentSessionStart(w, r)
}

func (a *App) handleMobileSessionEnd(w http.ResponseWriter, r *http.Request) {
	if !a.requireMobileScope(w, r, mobileScopeSessionEnd) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		a.writeError(w, http.StatusBadRequest, "session_id is required", nil)
		return
	}

	var body struct {
		Summarize bool `json:"summarize"`
	}
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			a.writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				a.writeError(w, http.StatusBadRequest, "invalid request body", err)
				return
			}
		}
	}

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

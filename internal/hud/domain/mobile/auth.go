package mobile

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"time"
)

// writeMobileJSON writes a successful mobile envelope response.
func (d *MobileDomain) writeMobileJSON(w http.ResponseWriter, status int, data any) {
	env := Envelope{
		OK:   true,
		Data: data,
		Meta: EnvMeta{
			RequestID: newRequestID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
	d.deps.WriteJSON(w, status, env)
}

// writeMobileError writes an error mobile envelope response.
func (d *MobileDomain) writeMobileError(w http.ResponseWriter, status int, code, message string) {
	env := Envelope{
		OK: false,
		Error: envError{
			Code:    code,
			Message: message,
		},
		Meta: EnvMeta{
			RequestID: newRequestID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		},
	}
	d.deps.WriteJSON(w, status, env)
}

// requireMobileScope validates the mobile bearer token and required scope.
func (d *MobileDomain) requireMobileScope(w http.ResponseWriter, r *http.Request, requiredScope string) bool {
	cfg := d.deps.MobileConfig()
	expected := strings.TrimSpace(cfg.OperatorToken)
	if expected == "" {
		d.writeMobileError(w, http.StatusForbidden, "not_configured", "mobile operator token is not configured; set HUD_MOBILE_OPERATOR_TOKEN")
		return false
	}

	actual := extractBearerToken(r)
	if actual == "" {
		d.writeMobileError(w, http.StatusUnauthorized, "unauthorized", "mobile bearer token is required")
		return false
	}

	if subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		d.writeMobileError(w, http.StatusUnauthorized, "unauthorized", "invalid mobile bearer token")
		return false
	}

	// Check revocation list.
	if rl := d.deps.RevocationList(); rl != nil && rl.IsRevoked(actual) {
		d.writeMobileError(w, http.StatusUnauthorized, "token_revoked", "mobile bearer token has been revoked")
		return false
	}

	if !d.mobileScopeAllowed(requiredScope) {
		d.writeMobileError(w, http.StatusForbidden, "forbidden", "mobile token missing required scope")
		return false
	}

	// Check rate limit.
	if limiter := d.deps.RateLimiter(); limiter != nil {
		isMutation := requiredScope != ScopeRead
		if !limiter.Allow(actorFromRequest(r), isMutation) {
			d.writeMobileError(w, http.StatusTooManyRequests, "rate_limited", "mobile API rate limit exceeded")
			return false
		}
	}

	return true
}

// mobileScopeAllowed checks if the given scope is allowed by the configured scopes.
func (d *MobileDomain) mobileScopeAllowed(required string) bool {
	if required == "" {
		return true
	}
	raw := strings.TrimSpace(d.deps.MobileConfig().OperatorScopes)
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

// logMobileAudit records a structured audit entry for mobile mutation operations.
func (d *MobileDomain) logMobileAudit(r *http.Request, action string, targets map[string]string, outcome string, auditErr error) {
	attrs := []any{
		"source", "mobile",
		"action", action,
		"endpoint", r.Method + " " + r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"outcome", outcome,
	}
	if deviceID := ExtractDeviceID(r); deviceID != "" {
		attrs = append(attrs, "device_id", deviceID)
	}
	for k, v := range targets {
		attrs = append(attrs, k, v)
	}
	if auditErr != nil {
		attrs = append(attrs, "error", auditErr.Error())
	}
	d.deps.Logger().Info("mobile_audit", attrs...)
}

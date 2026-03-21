package hud

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// extractBearerToken extracts a bearer token from the Authorization header.
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

// isMobileOperatorToken returns true if the request carries the configured
// mobile operator bearer token.
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

// mobileTokenOutsideMobileAPI returns true when a request uses the mobile
// operator token but targets a non-mobile API path. Used by middleware to
// allow mobile tokens to access agent endpoints.
func (a *App) mobileTokenOutsideMobileAPI(r *http.Request) bool {
	if !a.isMobileOperatorToken(r) {
		return false
	}
	return !strings.HasPrefix(r.URL.Path, "/api/mobile/v1/")
}

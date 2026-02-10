package proxy

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// requestIDKey is the context key for request IDs.
type requestIDKey struct{}

// checkRateLimit returns true if the request is allowed, false if rate limited.
func (p *Proxy) checkRateLimit(modelName string) bool {
	if !p.rateLimitEnabled {
		return true
	}

	// Check global limit first (protects K8s API server)
	if p.globalLimiter != nil && !p.globalLimiter.Allow() {
		rateLimitedTotal.WithLabelValues(modelName, "global").Inc()
		return false
	}

	// Check per-model limit
	if p.rateLimitPerModel > 0 {
		limiter := p.getModelLimiter(modelName)
		if !limiter.Allow() {
			rateLimitedTotal.WithLabelValues(modelName, "per_model").Inc()
			return false
		}
	}

	return true
}

// getModelLimiter returns a per-model rate limiter, creating one if needed.
func (p *Proxy) getModelLimiter(modelName string) *rate.Limiter {
	if val, ok := p.modelLimiters.Load(modelName); ok {
		return val.(*rate.Limiter)
	}
	limiter := rate.NewLimiter(rate.Limit(p.rateLimitPerModel), p.rateLimitBurst)
	actual, _ := p.modelLimiters.LoadOrStore(modelName, limiter)
	return actual.(*rate.Limiter)
}

// checkAuth validates the bearer token. Returns true if authenticated or auth is disabled.
func (p *Proxy) checkAuth(r *http.Request) bool {
	if !p.authEnabled {
		return true
	}
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	return auth[len(prefix):] == p.authToken
}

// generateRequestID creates a unique request ID for tracing.
// Uses the incoming X-Request-ID header if present, otherwise generates a new one.
func generateRequestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	// Simple time-based ID: compact, sortable, collision-resistant for single-process use.
	return fmt.Sprintf("%d-%04x", time.Now().UnixNano(), rand.Intn(0xFFFF))
}

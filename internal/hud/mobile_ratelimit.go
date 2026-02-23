package hud

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// MobileRateLimitConfig holds per-category limits for mobile API endpoints.
type MobileRateLimitConfig struct {
	MutationPerMinute int // Max mutation requests per actor per minute (0 = disabled).
	ReadPerMinute     int // Max read requests per actor per minute (0 = disabled).
}

// MobileRateLimiter enforces per-actor, per-category rate limits using
// UTC minute-window counters (same pattern as daemon RBAC rate limiting).
type MobileRateLimiter struct {
	mu       sync.Mutex
	counters map[string]mobileRateLimitCounter
	cfg      MobileRateLimitConfig
	now      func() time.Time // injectable clock for testing
}

type mobileRateLimitCounter struct {
	WindowStart time.Time
	Count       int
}

// NewMobileRateLimiter creates a rate limiter with the given config.
// Returns nil if both limits are zero (disabled).
func NewMobileRateLimiter(cfg MobileRateLimitConfig) *MobileRateLimiter {
	if cfg.MutationPerMinute <= 0 && cfg.ReadPerMinute <= 0 {
		return nil
	}
	return &MobileRateLimiter{
		counters: make(map[string]mobileRateLimitCounter),
		cfg:      cfg,
		now:      time.Now,
	}
}

// Allow checks whether the request is within the rate limit.
// Returns true if allowed, false if the limit is exceeded.
func (rl *MobileRateLimiter) Allow(actor string, isMutation bool) bool {
	limit := rl.cfg.ReadPerMinute
	category := "read"
	if isMutation {
		limit = rl.cfg.MutationPerMinute
		category = "mutation"
	}
	if limit <= 0 {
		return true
	}

	key := actor + ":" + category
	now := rl.now().UTC()
	windowStart := now.Truncate(time.Minute)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	counter := rl.counters[key]
	if counter.WindowStart != windowStart {
		counter = mobileRateLimitCounter{WindowStart: windowStart, Count: 0}
	}
	counter.Count++
	rl.counters[key] = counter

	return counter.Count <= limit
}

// actorFromRequest extracts a rate-limit actor key from the request.
// Uses the remote IP address (port stripped).
func actorFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

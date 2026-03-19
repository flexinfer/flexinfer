package hud

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
	"unicode/utf8"
)

// --- Push notification infrastructure (MBL-7, M4/M5) ---
//
// This file provides the retry policy, payload guardrails, and device token
// registry required for reliable APNs/FCM push delivery. Actual transport
// (HTTP/2 to APNs, FCM v1 API) is deferred to M5; this layer defines the
// contracts and validation that any transport must satisfy.

// MaxAPNsPayloadBytes is the APNs limit for regular push notifications.
const MaxAPNsPayloadBytes = 4096

// MaxFCMPayloadBytes is the FCM limit for data messages.
const MaxFCMPayloadBytes = 4096

// PushRetryAction describes what the caller should do after a push attempt.
type PushRetryAction int

const (
	// PushNoRetry means the push failed permanently (bad request, auth error).
	PushNoRetry PushRetryAction = iota
	// PushRetryWithBackoff means the push should be retried with exponential backoff.
	PushRetryWithBackoff
	// PushRetryAfter means the provider returned a Retry-After hint.
	PushRetryAfter
	// PushInvalidateToken means the device token is invalid and should be removed.
	PushInvalidateToken
)

// PushRetryDecision is the result of classifying a push response.
type PushRetryDecision struct {
	Action     PushRetryAction
	RetryAfter time.Duration // Populated when Action == PushRetryAfter.
	Reason     string        // Human-readable reason for logging.
}

// ClassifyPushResponse determines the retry action for a given HTTP status code
// from APNs or FCM. This implements the retry matrix from MBL-7:
//
//	| Status | Action           | Reason                                |
//	|--------|------------------|---------------------------------------|
//	| 200    | NoRetry          | Success                               |
//	| 400    | NoRetry          | Bad request (fix payload, don't retry)|
//	| 401    | NoRetry          | Auth error (fix credentials)          |
//	| 403    | NoRetry          | Forbidden (check provisioning)        |
//	| 404    | InvalidateToken  | APNs: device not registered           |
//	| 405    | NoRetry          | Method not allowed                    |
//	| 410    | InvalidateToken  | APNs: token expired/unregistered      |
//	| 429    | RetryAfter       | Throttled (honor Retry-After)         |
//	| 500    | RetryWithBackoff | Server error                          |
//	| 503    | RetryWithBackoff | Service unavailable                   |
func ClassifyPushResponse(statusCode int, retryAfterHint time.Duration) PushRetryDecision {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return PushRetryDecision{Action: PushNoRetry, Reason: "success"}
	case statusCode == 404:
		return PushRetryDecision{Action: PushInvalidateToken, Reason: "device not registered (404)"}
	case statusCode == 410:
		return PushRetryDecision{Action: PushInvalidateToken, Reason: "token expired or unregistered (410)"}
	case statusCode == 429:
		after := retryAfterHint
		if after <= 0 {
			after = 30 * time.Second // Default throttle backoff.
		}
		return PushRetryDecision{Action: PushRetryAfter, RetryAfter: after, Reason: "rate limited (429)"}
	case statusCode >= 500:
		return PushRetryDecision{Action: PushRetryWithBackoff, Reason: "server error"}
	default:
		// 400, 401, 403, 405, other 4xx: do not retry.
		return PushRetryDecision{Action: PushNoRetry, Reason: "client error, do not retry"}
	}
}

// PushBackoffConfig controls exponential backoff for retries.
type PushBackoffConfig struct {
	BaseDelay  time.Duration // Initial delay (default: 1s).
	MaxDelay   time.Duration // Maximum delay cap (default: 5m).
	MaxRetries int           // Maximum retry count before giving up (default: 5).
}

// DefaultPushBackoff returns sensible defaults for push retry backoff.
func DefaultPushBackoff() PushBackoffConfig {
	return PushBackoffConfig{
		BaseDelay:  1 * time.Second,
		MaxDelay:   5 * time.Minute,
		MaxRetries: 5,
	}
}

// BackoffDelay computes the delay for retry attempt n (0-indexed).
// Uses 2^n * BaseDelay with jitter-free capping at MaxDelay.
func (c PushBackoffConfig) BackoffDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := c.BaseDelay
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > c.MaxDelay {
			return c.MaxDelay
		}
	}
	return delay
}

// --- Payload validation and truncation ---

// PushPayload represents the notification content to be sent.
type PushPayload struct {
	Title    string            `json:"title"`
	Body     string            `json:"body"`
	Category string            `json:"category,omitempty"`  // Maps to APNs category / FCM click_action.
	ThreadID string            `json:"thread_id,omitempty"` // Groups notifications in iOS notification center.
	Data     map[string]string `json:"data,omitempty"`      // Custom key-value pairs.
}

// APNsEnvelope wraps a PushPayload into the APNs JSON structure.
func (p PushPayload) APNsEnvelope() map[string]any {
	alert := map[string]string{"title": p.Title, "body": p.Body}
	aps := map[string]any{"alert": alert, "sound": "default"}
	if p.Category != "" {
		aps["category"] = p.Category
	}
	if p.ThreadID != "" {
		aps["thread-id"] = p.ThreadID
	}
	env := map[string]any{"aps": aps}
	for k, v := range p.Data {
		env[k] = v
	}
	return env
}

// ValidateAndTruncate ensures the JSON-encoded payload fits within maxBytes.
// If oversized, it truncates the Body field to fit. Returns the encoded JSON
// and whether truncation occurred.
func (p PushPayload) ValidateAndTruncate(maxBytes int) ([]byte, bool, error) {
	envelope := p.APNsEnvelope()
	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, false, err
	}
	if len(data) <= maxBytes {
		return data, false, nil
	}

	// Truncate body to fit. Estimate overhead by encoding with empty body.
	truncated := p
	truncated.Body = ""
	overhead, err := json.Marshal(truncated.APNsEnvelope())
	if err != nil {
		return nil, false, err
	}

	// Available bytes for body, minus quotes and escape overhead (~10 byte margin).
	available := maxBytes - len(overhead) - 10
	if available < 3 {
		// Can't fit any body; return with empty body.
		truncated.Body = ""
		data, err = json.Marshal(truncated.APNsEnvelope())
		return data, true, err
	}

	truncated.Body = truncateUTF8(p.Body, available)
	data, err = json.Marshal(truncated.APNsEnvelope())
	if err != nil {
		return nil, false, err
	}

	// Final size check (JSON escaping may expand the body).
	for len(data) > maxBytes && len(truncated.Body) > 3 {
		truncated.Body = truncateUTF8(truncated.Body, len(truncated.Body)-10)
		data, err = json.Marshal(truncated.APNsEnvelope())
		if err != nil {
			return nil, false, err
		}
	}

	return data, true, nil
}

// truncateUTF8 truncates s to at most maxBytes, ending with "..." if truncated.
// Never breaks a multi-byte UTF-8 character.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes < 3 {
		return ""
	}
	target := maxBytes - 3 // Reserve space for "..."
	for target > 0 && !utf8.RuneStart(s[target]) {
		target--
	}
	return s[:target] + "..."
}

// --- Device token registry ---

// DeviceToken represents a registered push notification token.
type DeviceToken struct {
	Token     string    `json:"token"`
	DeviceID  string    `json:"device_id"`
	Platform  string    `json:"platform"` // "apns" or "fcm"
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

// DeviceTokenStore manages registered device tokens with thread-safe
// operations. Tokens are stored in-memory for v1; persistent storage
// is planned for M5.
type DeviceTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]DeviceToken // Keyed by token value.
	now    func() time.Time       // Injectable clock for testing.
}

// NewDeviceTokenStore creates a new in-memory device token store.
func NewDeviceTokenStore() *DeviceTokenStore {
	return &DeviceTokenStore{
		tokens: make(map[string]DeviceToken),
		now:    time.Now,
	}
}

// Register adds or updates a device token. Returns a registration ID.
func (s *DeviceTokenStore) Register(token, deviceID, platform string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	existing, exists := s.tokens[token]
	if exists {
		existing.LastUsed = now
		existing.DeviceID = deviceID
		s.tokens[token] = existing
		return tokenRegID(token)
	}

	s.tokens[token] = DeviceToken{
		Token:     token,
		DeviceID:  deviceID,
		Platform:  platform,
		CreatedAt: now,
		LastUsed:  now,
	}
	return tokenRegID(token)
}

// Invalidate removes a token from the store (e.g., after 410 from APNs).
func (s *DeviceTokenStore) Invalidate(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, existed := s.tokens[token]
	delete(s.tokens, token)
	return existed
}

// InvalidateByDeviceID removes all tokens for a given device.
func (s *DeviceTokenStore) InvalidateByDeviceID(deviceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for k, v := range s.tokens {
		if v.DeviceID == deviceID {
			delete(s.tokens, k)
			removed++
		}
	}
	return removed
}

// List returns all registered tokens. For diagnostics/admin use.
func (s *DeviceTokenStore) List() []DeviceToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]DeviceToken, 0, len(s.tokens))
	for _, v := range s.tokens {
		result = append(result, v)
	}
	return result
}

// Count returns the number of registered tokens.
func (s *DeviceTokenStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens)
}

// CleanupStale removes tokens not used since the given cutoff time.
// Returns the number of tokens removed.
func (s *DeviceTokenStore) CleanupStale(cutoff time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for k, v := range s.tokens {
		if v.LastUsed.Before(cutoff) {
			delete(s.tokens, k)
			removed++
		}
	}
	return removed
}

// tokenRegID produces a short deterministic registration ID from a token.
func tokenRegID(token string) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "reg_unknown"
	}
	return "reg_" + hex.EncodeToString(buf[:])
}

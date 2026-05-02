package hud

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http2"
)

// APNsSenderConfig holds configuration for the APNs HTTP/2 sender.
type APNsSenderConfig struct {
	KeyPath string // Path to .p8 signing key file.
	KeyID   string // APNs key ID.
	TeamID  string // Apple Developer Team ID.
	Topic   string // APNs topic (bundle ID).
	Sandbox bool   // Use sandbox endpoint (development).
}

// APNsSender delivers push notifications via APNs HTTP/2.
type APNsSender struct {
	config     APNsSenderConfig
	client     *http.Client
	tracer     trace.Tracer
	metrics    *HUDMetrics
	logger     *slog.Logger
	tokenStore *DeviceTokenStore // Optional: when set, 404/410 responses remove the token.

	// JWT token caching (tokens valid for 1 hour).
	mu       sync.RWMutex
	jwtToken string
	jwtExp   time.Time
}

// NewAPNsSender creates an APNs sender with HTTP/2 transport.
func NewAPNsSender(cfg APNsSenderConfig, tracer trace.Tracer, metrics *HUDMetrics, logger *slog.Logger) *APNsSender {
	transport := &http2.Transport{
		TLSClientConfig: &tls.Config{},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	return &APNsSender{
		config:  cfg,
		client:  client,
		tracer:  tracer,
		metrics: metrics,
		logger:  logger.With("component", "apns"),
	}
}

// WithTokenStore wires a DeviceTokenStore so 404/410 responses from APNs cause
// the token to be removed from the store. Without this, invalidation decisions
// are observed (logged + traced) but not actioned, leaving stale tokens that
// keep generating noisy 410 responses on every subsequent push.
func (s *APNsSender) WithTokenStore(store *DeviceTokenStore) *APNsSender {
	s.tokenStore = store
	return s
}

// Send delivers a push notification to the given device token.
func (s *APNsSender) Send(ctx context.Context, deviceToken string, payload PushPayload) error {
	ctx, span := s.tracer.Start(ctx, "push.apns_deliver",
		trace.WithAttributes(
			attribute.String("push.device_token_prefix", safeTokenPrefix(deviceToken)),
			attribute.String("push.category", payload.Category),
		),
	)
	defer span.End()

	start := time.Now()

	// Encode and validate payload.
	body, truncated, err := payload.ValidateAndTruncate(MaxAPNsPayloadBytes)
	if err != nil {
		span.SetAttributes(attribute.String("push.outcome", "encode_error"))
		return fmt.Errorf("encode APNs payload: %w", err)
	}
	if truncated {
		span.SetAttributes(attribute.Bool("push.truncated", true))
	}

	endpoint := apnsProductionEndpoint
	if s.config.Sandbox {
		endpoint = apnsSandboxEndpoint
	}
	url := fmt.Sprintf("%s/3/device/%s", endpoint, deviceToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("create APNs request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", s.config.Topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")

	// Set JWT authorization if key is configured.
	if s.config.KeyPath != "" {
		token, tokenErr := s.getJWTToken()
		if tokenErr != nil {
			span.SetAttributes(attribute.String("push.outcome", "auth_error"))
			return fmt.Errorf("generate APNs JWT: %w", tokenErr)
		}
		req.Header.Set("Authorization", "bearer "+token)
	}

	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return newBytesReadCloser(body), nil
	}
	req.Body = newBytesReadCloser(body)

	resp, err := s.client.Do(req)
	if err != nil {
		span.SetAttributes(attribute.String("push.outcome", "transport_error"))
		return fmt.Errorf("APNs HTTP request: %w", err)
	}
	defer resp.Body.Close()

	latency := time.Since(start).Seconds()
	if s.metrics != nil {
		s.metrics.PushDeliveryLatency.Record(ctx, latency)
	}

	decision := ClassifyPushResponse(resp.StatusCode, 0)
	span.SetAttributes(
		attribute.Int("push.status_code", resp.StatusCode),
		attribute.String("push.outcome", decision.Reason),
	)

	if decision.Action == PushInvalidateToken {
		removed := false
		if s.tokenStore != nil {
			removed = s.tokenStore.Invalidate(deviceToken)
		}
		span.SetAttributes(attribute.Bool("push.token_removed", removed))
		if s.metrics != nil && s.metrics.PushTokenInvalidated != nil {
			s.metrics.PushTokenInvalidated.Add(ctx, 1)
		}
		s.logger.Warn("APNs token invalidated",
			"token_prefix", safeTokenPrefix(deviceToken),
			"reason", decision.Reason,
			"removed_from_store", removed,
		)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("APNs returned %d: %s", resp.StatusCode, decision.Reason)
	}
	return nil
}

// getJWTToken returns a cached or freshly-generated JWT for APNs auth.
func (s *APNsSender) getJWTToken() (string, error) {
	s.mu.RLock()
	if s.jwtToken != "" && time.Now().Before(s.jwtExp) {
		token := s.jwtToken
		s.mu.RUnlock()
		return token, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock.
	if s.jwtToken != "" && time.Now().Before(s.jwtExp) {
		return s.jwtToken, nil
	}

	token, err := generateAPNsJWT(s.config.KeyPath, s.config.KeyID, s.config.TeamID)
	if err != nil {
		return "", err
	}
	s.jwtToken = token
	s.jwtExp = time.Now().Add(50 * time.Minute) // Refresh 10 min before expiry.
	return token, nil
}

const (
	apnsProductionEndpoint = "https://api.push.apple.com"
	apnsSandboxEndpoint    = "https://api.sandbox.push.apple.com"
)

// safeTokenPrefix returns the first 8 chars of a device token for logging.
func safeTokenPrefix(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8] + "..."
}

// generateAPNsJWT creates a signed JWT for APNs using the .p8 key.
func generateAPNsJWT(keyPath, keyID, teamID string) (string, error) {
	// ES256 JWT generation using crypto/ecdsa.
	// The .p8 file contains a PEM-encoded ECDSA P-256 private key.
	keyData, err := readKeyFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("read APNs key: %w", err)
	}

	key, err := parseP8Key(keyData)
	if err != nil {
		return "", fmt.Errorf("parse APNs key: %w", err)
	}

	now := time.Now()
	header := map[string]string{"alg": "ES256", "kid": keyID}
	claims := map[string]any{"iss": teamID, "iat": now.Unix()}

	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	return signES256(headerJSON, claimsJSON, key)
}

// bytesReadCloser wraps a byte slice as an io.ReadCloser.
type bytesReadCloser struct {
	data   []byte
	offset int
}

func newBytesReadCloser(data []byte) *bytesReadCloser {
	return &bytesReadCloser{data: data}
}

func (b *bytesReadCloser) Read(p []byte) (int, error) {
	if b.offset >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func (b *bytesReadCloser) Close() error { return nil }

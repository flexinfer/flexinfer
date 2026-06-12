package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestCheckAuth_Disabled(t *testing.T) {
	p := setupTestProxy(t)
	p.authEnabled = false

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	assert.True(t, p.checkAuth(req), "auth disabled should allow any request")
}

func TestCheckAuth_ValidToken(t *testing.T) {
	p := setupTestProxy(t)
	p.authEnabled = true
	p.authToken = "sk-test-secret-token"

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-test-secret-token")

	assert.True(t, p.checkAuth(req))
}

func TestCheckAuth_InvalidToken(t *testing.T) {
	p := setupTestProxy(t)
	p.authEnabled = true
	p.authToken = "sk-correct-token"

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer sk-wrong-token")

	assert.False(t, p.checkAuth(req))
}

func TestCheckAuth_MissingHeader(t *testing.T) {
	p := setupTestProxy(t)
	p.authEnabled = true
	p.authToken = "sk-test-token"

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	// No Authorization header set

	assert.False(t, p.checkAuth(req))
}

func TestCheckAuth_WrongScheme(t *testing.T) {
	p := setupTestProxy(t)
	p.authEnabled = true
	p.authToken = "sk-test-token"

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Basic c2stdGVzdC10b2tlbg==")

	assert.False(t, p.checkAuth(req), "Basic auth scheme should be rejected")
}

func TestCheckAuth_EmptyBearerToken(t *testing.T) {
	p := setupTestProxy(t)
	p.authEnabled = true
	p.authToken = "sk-test-token"

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("Authorization", "Bearer ")

	assert.False(t, p.checkAuth(req), "empty bearer value should not match a non-empty token")
}

func TestCheckRateLimit_Disabled(t *testing.T) {
	p := setupTestProxy(t)
	p.rateLimitEnabled = false

	// Should always allow when rate limiting is off, regardless of call count
	for i := 0; i < 100; i++ {
		assert.True(t, p.checkRateLimit("any-model"))
	}
}

func TestCheckRateLimit_GlobalLimit(t *testing.T) {
	p := setupTestProxy(t)
	p.rateLimitEnabled = true
	// Allow exactly 1 request (rate=1/s, burst=1), then block
	p.globalLimiter = rate.NewLimiter(1, 1)
	p.rateLimitPerModel = 0 // no per-model limit

	assert.True(t, p.checkRateLimit("test-model"), "first request should be allowed")
	assert.False(t, p.checkRateLimit("test-model"), "second request should be blocked by global limiter")
}

func TestCheckRateLimit_PerModelLimit(t *testing.T) {
	p := setupTestProxy(t)
	p.rateLimitEnabled = true
	p.globalLimiter = nil     // no global limit
	p.rateLimitPerModel = 1.0 // 1 req/s
	p.rateLimitBurst = 1

	assert.True(t, p.checkRateLimit("model-a"), "first request to model-a should pass")
	assert.False(t, p.checkRateLimit("model-a"), "second request to model-a should be blocked")

	// A different model should have its own independent limiter
	assert.True(t, p.checkRateLimit("model-b"), "first request to model-b should pass (independent limiter)")
}

func TestGetModelLimiter_CreateNew(t *testing.T) {
	p := setupTestProxy(t)
	p.rateLimitPerModel = 10.0
	p.rateLimitBurst = 5

	limiter := p.getModelLimiter("new-model")
	require.NotNil(t, limiter)
	assert.Equal(t, rate.Limit(10.0), limiter.Limit())
	assert.Equal(t, 5, limiter.Burst())
}

func TestGetModelLimiter_ReturnsCached(t *testing.T) {
	p := setupTestProxy(t)
	p.rateLimitPerModel = 10.0
	p.rateLimitBurst = 5

	first := p.getModelLimiter("cached-model")
	second := p.getModelLimiter("cached-model")

	// Must be the exact same pointer -- no new allocation on second call
	assert.Same(t, first, second, "second call should return the cached limiter")
}

func TestGenerateRequestID_UsesExistingHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("X-Request-ID", "incoming-req-42")

	id := generateRequestID(req)
	assert.Equal(t, "incoming-req-42", id)
}

func TestGenerateRequestID_GeneratesNew(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	// No X-Request-ID header

	id := generateRequestID(req)
	assert.NotEmpty(t, id)
	// Format: <unix_nano>-<4 hex digits>
	parts := strings.SplitN(id, "-", 2)
	require.Len(t, parts, 2, "generated ID should have format <timestamp>-<hex>")
	assert.NotEmpty(t, parts[0], "timestamp portion should not be empty")
	assert.Len(t, parts[1], 4, "hex portion should be 4 characters")

	// Two successive calls should produce different IDs (non-deterministic, but
	// extremely unlikely to collide given nanosecond timestamp + random hex)
	id2 := generateRequestID(req)
	assert.NotEqual(t, id, id2, "consecutive generated IDs should differ")
}

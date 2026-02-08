// Package httpclient provides a shared HTTP client for MCP servers
// with TLS skip verify, configurable timeouts, and retry logic.
package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds HTTP client configuration options.
type Config struct {
	// Timeout for individual requests (default: 30s)
	Timeout time.Duration

	// TLSSkipVerify disables TLS certificate verification
	TLSSkipVerify bool

	// MaxRetries is the maximum number of retry attempts (default: 0 = no retry)
	MaxRetries int

	// RetryBaseDelay is the initial delay between retries (default: 100ms)
	RetryBaseDelay time.Duration

	// RetryMaxDelay is the maximum delay between retries (default: 5s)
	RetryMaxDelay time.Duration

	// MaxResponseBytes limits the size of response bodies read by convenience
	// methods like GetJSON (default: 10MB). Set to 0 for no limit.
	MaxResponseBytes int
}

// DefaultConfig returns a Config with sensible defaults,
// reading from environment variables where applicable.
func DefaultConfig() Config {
	cfg := Config{
		Timeout:          30 * time.Second,
		TLSSkipVerify:    false,
		MaxRetries:       0,
		RetryBaseDelay:   100 * time.Millisecond,
		RetryMaxDelay:    5 * time.Second,
		MaxResponseBytes: 10 * 1024 * 1024, // 10MB
	}

	// Check TLS_SKIP_VERIFY env var
	if skipVerify := os.Getenv("TLS_SKIP_VERIFY"); skipVerify != "" {
		v := strings.ToLower(skipVerify)
		cfg.TLSSkipVerify = v == "true" || v == "1" || v == "yes"
	}

	// Check HTTP_TIMEOUT env var (in seconds)
	if timeout := os.Getenv("HTTP_TIMEOUT"); timeout != "" {
		if secs, err := strconv.Atoi(timeout); err == nil && secs > 0 {
			cfg.Timeout = time.Duration(secs) * time.Second
		}
	}

	// Check HTTP_RETRIES env var
	if retries := os.Getenv("HTTP_RETRIES"); retries != "" {
		if n, err := strconv.Atoi(retries); err == nil && n >= 0 {
			cfg.MaxRetries = n
		}
	}

	return cfg
}

// Client wraps http.Client with retry logic and convenience methods.
type Client struct {
	http   *http.Client
	config Config
}

// New creates a new Client with the given configuration.
func New(cfg Config) *Client {
	transport := &http.Transport{}

	if cfg.TLSSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	return &Client{
		http: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
		config: cfg,
	}
}

// NewDefault creates a new Client with default configuration from environment.
func NewDefault() *Client {
	return New(DefaultConfig())
}

// Do executes an HTTP request with optional retry logic.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff delay
			delay := c.backoffDelay(attempt)
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
			}
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			// Retry on network errors
			if isRetryableError(err) {
				continue
			}
			return nil, err
		}

		// Retry on server errors (5xx)
		if resp.StatusCode >= 500 && attempt < c.config.MaxRetries {
			resp.Body.Close()
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// Get performs an HTTP GET request.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// GetJSON performs an HTTP GET request and reads the response body.
func (c *Client) GetJSON(ctx context.Context, url string) ([]byte, error) {
	resp, err := c.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if c.config.MaxResponseBytes > 0 {
		reader = io.LimitReader(resp.Body, int64(c.config.MaxResponseBytes))
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// Post performs an HTTP POST request.
func (c *Client) Post(ctx context.Context, url string, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// SetHeader sets a default header on the underlying transport.
// Note: For per-request headers, set them on the request directly.
func (c *Client) SetHeader(key, value string) {
	// For consistent headers, wrap the transport
	c.http.Transport = &headerTransport{
		base:  c.http.Transport,
		key:   key,
		value: value,
	}
}

// HTTP returns the underlying http.Client for advanced usage.
func (c *Client) HTTP() *http.Client {
	return c.http
}

// backoffDelay calculates exponential backoff delay for the given attempt.
func (c *Client) backoffDelay(attempt int) time.Duration {
	delay := float64(c.config.RetryBaseDelay) * math.Pow(2, float64(attempt-1))
	if delay > float64(c.config.RetryMaxDelay) {
		delay = float64(c.config.RetryMaxDelay)
	}
	return time.Duration(delay)
}

// isRetryableError checks if an error is worth retrying.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// Retry on timeout and connection errors
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "EOF")
}

// headerTransport wraps a RoundTripper to add a default header.
type headerTransport struct {
	base  http.RoundTripper
	key   string
	value string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get(t.key) == "" {
		req.Header.Set(t.key, t.value)
	}
	if t.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	return t.base.RoundTrip(req)
}

// ReadBodyWithLimit reads up to maxBytes from r. If the response exceeds
// maxBytes, the returned slice is truncated and truncated is true.
// If maxBytes <= 0, the entire body is read with no limit.
func ReadBodyWithLimit(r io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		b, err := io.ReadAll(r)
		return b, false, err
	}

	b, err := io.ReadAll(io.LimitReader(r, int64(maxBytes+1)))
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxBytes {
		return b[:maxBytes], true, nil
	}
	return b, false, nil
}

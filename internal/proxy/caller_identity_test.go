package proxy

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestCallerIdentityFrom(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.RemoteAddr = "10.42.0.7:51234"
	r.Header.Set("X-Forwarded-For", "192.168.1.50, 10.42.0.1")
	r.Header.Set("User-Agent", "OpenAI/Python 1.30.0")
	r = r.WithContext(context.WithValue(r.Context(), requestIDKey{}, "req-abc123"))

	c := callerIdentityFrom(r)

	if c.requestID != "req-abc123" {
		t.Errorf("requestID = %q, want %q", c.requestID, "req-abc123")
	}
	if c.remoteAddr != "10.42.0.7:51234" {
		t.Errorf("remoteAddr = %q, want %q", c.remoteAddr, "10.42.0.7:51234")
	}
	if c.forwardedFor != "192.168.1.50, 10.42.0.1" {
		t.Errorf("forwardedFor = %q, want %q", c.forwardedFor, "192.168.1.50, 10.42.0.1")
	}
	if c.userAgent != "OpenAI/Python 1.30.0" {
		t.Errorf("userAgent = %q, want %q", c.userAgent, "OpenAI/Python 1.30.0")
	}
}

// A request that never passed through handleRequest (no request ID in context,
// no optional headers) must still produce a usable identity, not panic.
func TestCallerIdentityFrom_MissingFields(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/completions", nil)
	r.RemoteAddr = "10.42.0.9:40000"

	c := callerIdentityFrom(r)

	if c.requestID != "" {
		t.Errorf("requestID = %q, want empty", c.requestID)
	}
	if c.remoteAddr != "10.42.0.9:40000" {
		t.Errorf("remoteAddr = %q, want %q", c.remoteAddr, "10.42.0.9:40000")
	}
	if c.forwardedFor != "" || c.userAgent != "" {
		t.Errorf("forwardedFor/userAgent = %q/%q, want empty", c.forwardedFor, c.userAgent)
	}
}

func TestCallerIdentityLogAttrs(t *testing.T) {
	c := callerIdentity{
		requestID:    "rid",
		remoteAddr:   "1.2.3.4:5",
		forwardedFor: "6.7.8.9",
		userAgent:    "ua",
	}
	attrs := c.logAttrs()
	want := []any{
		"request_id", "rid",
		"remote_addr", "1.2.3.4:5",
		"x_forwarded_for", "6.7.8.9",
		"user_agent", "ua",
	}
	if len(attrs) != len(want) {
		t.Fatalf("logAttrs len = %d, want %d", len(attrs), len(want))
	}
	for i := range want {
		if attrs[i] != want[i] {
			t.Errorf("logAttrs[%d] = %v, want %v", i, attrs[i], want[i])
		}
	}
}

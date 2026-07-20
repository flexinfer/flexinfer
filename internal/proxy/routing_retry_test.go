package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

func TestClassifyUpstreamErr(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		reason    string
		retryable bool
	}{
		{"nil", nil, "", false},
		{"conn_refused", syscall.ECONNREFUSED, "conn_refused", true},
		{"host_unreachable", syscall.EHOSTUNREACH, "host_unreachable", true},
		{"net_unreachable", syscall.ENETUNREACH, "host_unreachable", true},
		{"conn_reset", syscall.ECONNRESET, "conn_reset", true},
		{"eof", io.EOF, "eof", true},
		{"unexpected_eof", io.ErrUnexpectedEOF, "eof", true},
		{"wrapped_refused", &wrapErr{syscall.ECONNREFUSED}, "conn_refused", true},
		{"timeout", timeoutErr{}, "timeout", true},
		{"generic_4xx_like", errors.New("some upstream weirdness"), "other", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, retryable := classifyUpstreamErr(tt.err)
			if reason != tt.reason || retryable != tt.retryable {
				t.Fatalf("classifyUpstreamErr(%v) = (%q,%v), want (%q,%v)",
					tt.err, reason, retryable, tt.reason, tt.retryable)
			}
		})
	}
}

type wrapErr struct{ inner error }

func (e *wrapErr) Error() string { return "wrapped: " + e.inner.Error() }
func (e *wrapErr) Unwrap() error { return e.inner }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestMaxForwardAttempts(t *testing.T) {
	cases := map[string]int{"": defaultMaxForwardAttempts, "0": defaultMaxForwardAttempts, "abc": defaultMaxForwardAttempts, "1": 1, "2": 2, "3": 3, "9": maxForwardAttemptsCap}
	for env, want := range cases {
		t.Setenv(envMaxForwardAttempts, env)
		if got := maxForwardAttempts(); got != want {
			t.Fatalf("maxForwardAttempts() with %q = %d, want %d", env, got, want)
		}
	}
}

// TestErrorHandlerRecordsTransportError verifies the loadOrCreateProxy
// ErrorHandler records a dial failure on the per-request forwardResult (and
// writes nothing) when a retry context is present, and falls back to a 502
// when it is not.
func TestErrorHandlerRecordsTransportError(t *testing.T) {
	// A target that refuses connections: bind then immediately close.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	p := &Proxy{}
	rp, ok := p.loadOrCreateProxy(deadURL)
	if !ok {
		t.Fatal("loadOrCreateProxy returned ok=false")
	}

	t.Run("records and does not write when retry context present", func(t *testing.T) {
		fr := &forwardResult{}
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"x"}`))
		req = req.WithContext(withForwardResult(context.Background(), fr))
		rec := httptest.NewRecorder()

		rp.ServeHTTP(rec, req)

		if fr.err == nil {
			t.Fatal("expected forwardResult.err to be set by ErrorHandler")
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("expected no body written, got %q", rec.Body.String())
		}
		if _, retryable := classifyUpstreamErr(fr.err); !retryable {
			t.Fatalf("expected a retryable dial error, got %v", fr.err)
		}
	})

	t.Run("writes 502 when no retry context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"input":"x"}`))
		rec := httptest.NewRecorder()

		rp.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadGateway {
			t.Fatalf("expected 502 from default error path, got %d", rec.Code)
		}
	})
}

// TestResolveTargetURL_SkipDirectSelfHeal verifies that attempt 0 uses the
// direct-load fast path (fromDirect=true) while a retry (skipDirect=true)
// bypasses it for the load-balanced Service DNS name.
func TestResolveTargetURL_SkipDirectSelfHeal(t *testing.T) {
	p := &Proxy{namespace: "flexinfer-system"}
	p.directLoadTargets.Store("bge", "http://10.0.0.5:8001")

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil)

	gotURL, _, fromDirect := p.resolveTargetURL(context.Background(), req, "bge", 8000, nil, false)
	if !fromDirect || gotURL != "http://10.0.0.5:8001" {
		t.Fatalf("attempt 0: got (%q, fromDirect=%v), want direct target", gotURL, fromDirect)
	}

	retryURL, _, fromDirectRetry := p.resolveTargetURL(context.Background(), req, "bge", 8000, nil, true)
	if fromDirectRetry {
		t.Fatal("retry: expected fromDirect=false (Service DNS), got true")
	}
	if !strings.Contains(retryURL, "bge.flexinfer-system.svc") {
		t.Fatalf("retry: expected Service DNS target, got %q", retryURL)
	}
}

// TestForwardWithRetry_InvalidatesStaleDirectTarget is the core self-heal test:
// a stale direct-load target that refuses connections must be deleted (so it
// can never pin a dead pod indefinitely) and the request must terminate with a
// clean 502 rather than hang. Runs with a single attempt so the test does not
// depend on out-of-cluster Service DNS resolution.
func TestForwardWithRetry_InvalidatesStaleDirectTarget(t *testing.T) {
	t.Setenv(envMaxForwardAttempts, "1") // single attempt: dead direct target only

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	p := &Proxy{namespace: "flexinfer-system"}
	p.directLoadTargets.Store("bge", deadURL)

	body := []byte(`{"input":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()

	p.forwardWithRetry(rec, req, "bge", "bge", 8000, body, body)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected terminal 502 after dial failure, got %d", rec.Code)
	}
	if _, ok := p.directLoadTargets.Load("bge"); ok {
		t.Fatal("stale direct-load target was not invalidated after dial failure")
	}
}

// The terminal "upstream forward failed" WARN must carry the caller identity
// fields so a failure pattern (e.g. a client cancelling every request on a
// timeout) is attributable to a workload without packet captures.
func TestForwardWithRetry_FailureLogIncludesCallerIdentity(t *testing.T) {
	t.Setenv(envMaxForwardAttempts, "1") // single attempt: dead direct target only

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := srv.URL
	srv.Close()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	p := &Proxy{namespace: "flexinfer-system"}
	p.directLoadTargets.Store("qwen", deadURL)

	body := []byte(`{"model":"qwen"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	req.RemoteAddr = "10.42.9.9:33333"
	req.Header.Set("User-Agent", "burner-client/1.0")
	req.Header.Set("X-Forwarded-For", "172.16.0.5")
	req = req.WithContext(context.WithValue(req.Context(), requestIDKey{}, "rid-42"))
	rec := httptest.NewRecorder()

	p.forwardWithRetry(rec, req, "qwen", "qwen", 8000, body, body)

	out := buf.String()
	if !strings.Contains(out, "upstream forward failed") {
		t.Fatalf("expected forward-failure WARN, got logs:\n%s", out)
	}
	for _, want := range []string{
		"remote_addr=10.42.9.9:33333",
		"user_agent=burner-client/1.0",
		"x_forwarded_for=172.16.0.5",
		"request_id=rid-42",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("forward-failure WARN missing %q; logs:\n%s", want, out)
		}
	}
}

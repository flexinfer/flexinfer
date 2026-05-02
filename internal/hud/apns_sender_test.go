package hud

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace/noop"
)

// roundTripperFunc adapts a function into an http.RoundTripper for testing.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newAPNsSenderForTest(rt http.RoundTripper) *APNsSender {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewAPNsSender(APNsSenderConfig{Topic: "ai.loom.companion"}, noop.NewTracerProvider().Tracer("test"), nil, logger)
	s.client = &http.Client{Transport: rt}
	return s
}

func fakeAPNsResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
	}
}

// Returning a 410 from APNs must remove the token from the registered store.
func TestAPNsSender_InvalidatesTokenOn410(t *testing.T) {
	store := NewDeviceTokenStore()
	store.Register("expired-token-12345678", "device-A", "apns")
	if store.Count() != 1 {
		t.Fatalf("setup: expected 1 token, got %d", store.Count())
	}

	s := newAPNsSenderForTest(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.String(), "expired-token-12345678") {
			t.Errorf("URL missing token: %s", req.URL.String())
		}
		return fakeAPNsResponse(http.StatusGone), nil
	})).WithTokenStore(store)

	err := s.Send(context.Background(), "expired-token-12345678", PushPayload{Title: "x", Body: "y"})
	if err == nil {
		t.Fatal("expected non-nil error for 410 response")
	}
	if !strings.Contains(err.Error(), "410") {
		t.Errorf("expected error to mention 410, got: %v", err)
	}
	if store.Count() != 0 {
		t.Errorf("expected token to be removed from store, still have %d", store.Count())
	}
}

// 404 (device not registered) must remove the token from the store.
func TestAPNsSender_InvalidatesTokenOn404(t *testing.T) {
	store := NewDeviceTokenStore()
	store.Register("missing-token-87654321", "device-B", "apns")

	s := newAPNsSenderForTest(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return fakeAPNsResponse(http.StatusNotFound), nil
	})).WithTokenStore(store)

	_ = s.Send(context.Background(), "missing-token-87654321", PushPayload{Title: "x", Body: "y"})
	if store.Count() != 0 {
		t.Errorf("expected token removed on 404, still have %d", store.Count())
	}
}

// Transient server errors must NOT touch the token store; the caller will retry.
func TestAPNsSender_RetryableErrorPreservesToken(t *testing.T) {
	store := NewDeviceTokenStore()
	store.Register("good-token-aaaabbbb", "device-C", "apns")

	s := newAPNsSenderForTest(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return fakeAPNsResponse(http.StatusInternalServerError), nil
	})).WithTokenStore(store)

	_ = s.Send(context.Background(), "good-token-aaaabbbb", PushPayload{Title: "x", Body: "y"})
	if store.Count() != 1 {
		t.Errorf("expected token preserved on 500, store has %d", store.Count())
	}
}

// 200 success must leave the token in place.
func TestAPNsSender_SuccessPreservesToken(t *testing.T) {
	store := NewDeviceTokenStore()
	store.Register("good-token-ccccdddd", "device-D", "apns")

	s := newAPNsSenderForTest(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return fakeAPNsResponse(http.StatusOK), nil
	})).WithTokenStore(store)

	if err := s.Send(context.Background(), "good-token-ccccdddd", PushPayload{Title: "x", Body: "y"}); err != nil {
		t.Errorf("expected nil error on 200, got %v", err)
	}
	if store.Count() != 1 {
		t.Errorf("expected token preserved on success, store has %d", store.Count())
	}
}

// When no token store is wired, the invalidation decision is observed but no
// store-mutation happens (and we don't panic on the nil store).
func TestAPNsSender_InvalidationWithoutStoreNoPanic(t *testing.T) {
	s := newAPNsSenderForTest(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return fakeAPNsResponse(http.StatusGone), nil
	}))

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Send must not panic without token store, got: %v", r)
		}
	}()
	_ = s.Send(context.Background(), "any-token-12345678", PushPayload{Title: "x", Body: "y"})
}

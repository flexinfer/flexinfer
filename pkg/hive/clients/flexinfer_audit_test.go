package clients

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewFlexInferAuditReviewer_NilClientReturnsNil(t *testing.T) {
	if got := NewFlexInferAuditReviewer(nil); got != nil {
		t.Errorf("nil flexinfer client should yield nil reviewer, got %+v", got)
	}
}

func TestFlexInferAuditReviewer_DefaultBackendName(t *testing.T) {
	r := &FlexInferAuditReviewer{} // no client; just check the default name
	if got := r.Backend(); got != "flexinfer" {
		t.Errorf("default backend name: got %q want %q", got, "flexinfer")
	}
}

func TestFlexInferAuditReviewer_OverrideBackendName(t *testing.T) {
	r := &FlexInferAuditReviewer{BackendName: "flexinfer-bulk"}
	if got := r.Backend(); got != "flexinfer-bulk" {
		t.Errorf("override backend name: got %q want flexinfer-bulk", got)
	}
}

func TestFlexInferAuditReviewer_NilReceiverReturnsDefaultBackend(t *testing.T) {
	var r *FlexInferAuditReviewer
	if got := r.Backend(); got != "flexinfer" {
		t.Errorf("nil receiver backend: got %q want flexinfer", got)
	}
}

func TestFlexInferAuditReviewer_NilClientReviewErrors(t *testing.T) {
	r := &FlexInferAuditReviewer{}
	_, _, err := r.Review(context.Background(), "x", "prompt", 1.0)
	if err == nil {
		t.Error("nil client must error on Review")
	}
}

func TestFlexInferAuditReviewer_RoundTripsChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Tiny canned response that satisfies the chat decoder + has a
		// model id we can assert on the Reviewer side.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"llama-4-70b","choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}}`))
	}))
	defer srv.Close()
	c, err := NewFlexInferClient(FlexInferConfig{ProxyURL: srv.URL})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	rev := NewFlexInferAuditReviewer(c)
	if rev == nil {
		t.Fatal("expected reviewer for non-nil client")
	}
	content, cost, err := rev.Review(context.Background(), "llama-4-70b", "prompt", 0)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if content != "hello" {
		t.Errorf("content: got %q want hello", content)
	}
	if cost < 0 {
		t.Errorf("cost should be non-negative, got %v", cost)
	}
}

func TestFlexInferAuditReviewer_PropagatesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, err := NewFlexInferClient(FlexInferConfig{ProxyURL: srv.URL})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	rev := NewFlexInferAuditReviewer(c)
	_, _, err = rev.Review(context.Background(), "x", "prompt", 0)
	if err == nil {
		t.Fatal("expected upstream 500 to propagate")
	}
	if !errors.Is(err, err) { // sanity: non-nil
		t.Fatalf("expected non-nil error chain")
	}
}

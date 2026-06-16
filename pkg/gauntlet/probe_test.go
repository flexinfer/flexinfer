package gauntlet

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sseServer returns a mock OpenAI-compatible completions endpoint that streams
// the given text tokens followed by a usage chunk and [DONE].
func sseServer(t *testing.T, tokens []string, usage int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, tok := range tokens {
			fmt.Fprintf(w, "data: {\"choices\":[{\"text\":%q}]}\n\n", tok)
			if fl != nil {
				fl.Flush()
			}
		}
		fmt.Fprintf(w, "data: {\"choices\":[{\"text\":\"\"}],\"usage\":{\"completion_tokens\":%d}}\n\n", usage)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestProbe_StreamCapturesTextTokensAndTTFT(t *testing.T) {
	srv := sseServer(t, []string{"The ", "answer ", "is ", "4."}, 4)
	defer srv.Close()

	// Deterministic clock: advance 100ms per now() call.
	var calls int
	clock := func() time.Time {
		calls++
		return time.Unix(0, 0).Add(time.Duration(calls) * 100 * time.Millisecond)
	}

	s, err := Probe(context.Background(), srv.Client(), srv.URL, ProbeRequest{Model: "m", Prompt: "2+2?"}, clock)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if !s.Served {
		t.Fatalf("expected served, got %+v", s)
	}
	if s.CompletionText != "The answer is 4." {
		t.Errorf("text = %q, want %q", s.CompletionText, "The answer is 4.")
	}
	if s.CompletionTokens != 4 {
		t.Errorf("tokens = %d, want 4 (from usage)", s.CompletionTokens)
	}
	if s.TTFT <= 0 {
		t.Errorf("TTFT = %s, want > 0", s.TTFT)
	}

	// Verdict integration: coherence + token floor should pass.
	v := Evaluate(s, Thresholds{CoherenceExpect: []string{"4"}, MinCompletionTokens: 1})
	if !v.Pass {
		t.Errorf("expected gauntlet pass, got %+v", v)
	}
}

func TestProbe_Non200IsNotServed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model loading", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s, err := Probe(context.Background(), srv.Client(), srv.URL, ProbeRequest{Model: "m", Prompt: "x"}, nil)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if s.Served {
		t.Fatal("expected not served on 503")
	}
	if !strings.Contains(s.Err, "503") {
		t.Errorf("err = %q, want it to mention 503", s.Err)
	}
}

func TestProbe_EmptyCompletionFails(t *testing.T) {
	srv := sseServer(t, []string{}, 0)
	defer srv.Close()

	s, err := Probe(context.Background(), srv.Client(), srv.URL, ProbeRequest{Model: "m", Prompt: "x"}, nil)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if s.Served {
		t.Error("empty completion should be marked not served")
	}
	if s.Err != "empty completion" {
		t.Errorf("err = %q, want \"empty completion\"", s.Err)
	}
}

func TestProbe_FallsBackToApproxTokens(t *testing.T) {
	// No usage chunk -> approxTokens (word count) kicks in.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"text\":\"one two three\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	s, err := Probe(context.Background(), srv.Client(), srv.URL, ProbeRequest{Model: "m", Prompt: "x"}, nil)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if s.CompletionTokens != 3 {
		t.Errorf("approx tokens = %d, want 3", s.CompletionTokens)
	}
}

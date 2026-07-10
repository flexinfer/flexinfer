package gauntlet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
)

// sseServer returns a mock OpenAI-compatible completions endpoint that streams
// the given text tokens followed by a usage chunk and [DONE].
func sseServer(t *testing.T, tokens []string, usage int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, tok := range tokens {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"text\":%q}]}\n\n", tok)
			if fl != nil {
				fl.Flush()
			}
		}
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"text\":\"\"}],\"usage\":{\"completion_tokens\":%d}}\n\n", usage)
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestProbe_ChatCompletionsCapturesDeltaContent(t *testing.T) {
	t.Setenv(benchmarkconfig.EnvWorkloadClass, benchmarkconfig.WorkloadClassBackground)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get(benchmarkconfig.HeaderInternalWorkloadClass); got != benchmarkconfig.WorkloadClassBackground {
			t.Fatalf("workload header = %q, want %q", got, benchmarkconfig.WorkloadClassBackground)
		}

		var body struct {
			Prompt   string `json:"prompt"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Prompt != "" {
			t.Fatalf("chat request unexpectedly contained prompt %q", body.Prompt)
		}
		if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "2+2?" {
			t.Fatalf("messages = %+v, want one user prompt", body.Messages)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"4\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"completion_tokens\":2}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	s, err := Probe(context.Background(), srv.Client(), srv.URL+"/v1/chat/completions", ProbeRequest{
		API:    ProbeAPIChat,
		Model:  "m",
		Prompt: "2+2?",
	}, nil)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if !s.Served || s.CompletionText != "4" || s.CompletionTokens != 2 {
		t.Fatalf("chat sample = %+v, want served text 4 with 2 tokens", s)
	}
}

func TestProbe_CompletionsUsesPromptPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt   string           `json:"prompt"`
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Prompt != "raw prompt" || len(body.Messages) != 0 {
			t.Fatalf("body = %+v, want raw prompt without messages", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"text\":\"raw answer\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	s, err := Probe(context.Background(), srv.Client(), srv.URL, ProbeRequest{
		API: ProbeAPICompletions, Model: "m", Prompt: "raw prompt",
	}, nil)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if !s.Served || s.CompletionText != "raw answer" {
		t.Fatalf("completion sample = %+v", s)
	}
}

func TestProbe_InvalidAPIFailsBeforeRequest(t *testing.T) {
	requested := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requested = true
	}))
	defer srv.Close()

	_, err := Probe(context.Background(), srv.Client(), srv.URL, ProbeRequest{API: ProbeAPI("responses")}, nil)
	if err == nil {
		t.Fatal("Probe accepted unsupported API")
	}
	if requested {
		t.Fatal("Probe sent HTTP request for unsupported API")
	}
}

func TestParseProbeAPI(t *testing.T) {
	tests := []struct {
		input string
		want  ProbeAPI
	}{
		{input: "", want: ProbeAPICompletions},
		{input: "completions", want: ProbeAPICompletions},
		{input: "chat", want: ProbeAPIChat},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseProbeAPI(tt.input)
			if err != nil {
				t.Fatalf("ParseProbeAPI(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseProbeAPI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}

	if _, err := ParseProbeAPI("responses"); err == nil {
		t.Fatal("ParseProbeAPI accepted unsupported mode")
	}
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
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"text\":\"one two three\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
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

func TestProbe_AppliesBackgroundWorkloadClass(t *testing.T) {
	t.Setenv(benchmarkconfig.EnvWorkloadClass, benchmarkconfig.WorkloadClassBackground)
	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get(benchmarkconfig.HeaderInternalWorkloadClass)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"text\":\"ok\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	s, err := Probe(context.Background(), srv.Client(), srv.URL, ProbeRequest{Model: "m", Prompt: "x"}, nil)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if !s.Served {
		t.Fatalf("expected served, got %+v", s)
	}
	if got := <-seen; got != benchmarkconfig.WorkloadClassBackground {
		t.Fatalf("workload header = %q, want %q", got, benchmarkconfig.WorkloadClassBackground)
	}
}

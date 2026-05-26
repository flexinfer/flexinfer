package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flexinfer/flexinfer/internal/proxy/spec_decode"
)

// fakeBackendServer responds to /v1/completions with a canned payload
// shape that mirrors vLLM's OpenAI server. Each test installs handlers
// for draft and verify; the server gives the test access to the last
// observed request so we can assert on the request shape.
type fakeBackendServer struct {
	t          *testing.T
	server     *httptest.Server
	draftResp  func(req completionsRequest) completionsResponse
	verifyResp func(req completionsRequest) completionsResponse
	lastDraft  completionsRequest
	lastVerify []completionsRequest
}

func newFakeBackendServer(t *testing.T) *fakeBackendServer {
	t.Helper()
	f := &fakeBackendServer{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/draft", f.handle("draft"))
	mux.HandleFunc("/verify", f.handle("verify"))
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeBackendServer) handle(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req completionsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var resp completionsResponse
		switch kind {
		case "draft":
			f.lastDraft = req
			if f.draftResp != nil {
				resp = f.draftResp(req)
			}
		case "verify":
			f.lastVerify = append(f.lastVerify, req)
			if f.verifyResp != nil {
				resp = f.verifyResp(req)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func newTestBackend(t *testing.T, server *fakeBackendServer) *httpBackend {
	t.Helper()
	be, err := newHTTPBackend(httpBackendConfig{
		httpClient:  server.server.Client(),
		draftURL:    server.server.URL + "/draft",
		verifyURL:   server.server.URL + "/verify",
		draftModel:  "draft-model",
		verifyModel: "verify-model",
		promptTopK:  5,
	})
	if err != nil {
		t.Fatalf("newHTTPBackend: %v", err)
	}
	return be
}

func TestHTTPBackend_Draft_HappyPath(t *testing.T) {
	f := newFakeBackendServer(t)
	f.draftResp = func(req completionsRequest) completionsResponse {
		var r completionsResponse
		r.Choices = make([]struct {
			Text           string                          `json:"text"`
			Logprobs       *completionLogprob              `json:"logprobs"`
			PromptLogprobs []map[string]promptLogprobEntry `json:"prompt_logprobs"`
		}, 1)
		r.Choices[0].Text = " alpha beta"
		r.Choices[0].Logprobs = &completionLogprob{
			Tokens:        []string{" alpha", " beta"},
			TokenLogprobs: []float64{-0.1, -0.4},
		}
		return r
	}

	be := newTestBackend(t, f)
	tokens, err := be.Draft(context.Background(), "hello", 2)
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0].Text != " alpha" || tokens[1].Text != " beta" {
		t.Errorf("unexpected token text: %+v", tokens)
	}
	if tokens[0].ID == 0 || tokens[1].ID == 0 {
		t.Errorf("token IDs must be non-zero (zero is reserved)")
	}
	if tokens[0].ID == tokens[1].ID {
		t.Errorf("distinct texts should hash to distinct IDs")
	}
	if f.lastDraft.Model != "draft-model" {
		t.Errorf("draft request model: got %q want draft-model", f.lastDraft.Model)
	}
	if f.lastDraft.MaxTokens != 2 {
		t.Errorf("draft request max_tokens: got %d want 2", f.lastDraft.MaxTokens)
	}
	if f.lastDraft.Logprobs == nil || *f.lastDraft.Logprobs != 1 {
		t.Errorf("draft request must set logprobs=1, got %v", f.lastDraft.Logprobs)
	}
}

func TestHTTPBackend_Draft_NonZeroReturn(t *testing.T) {
	f := newFakeBackendServer(t)
	f.draftResp = func(req completionsRequest) completionsResponse {
		return completionsResponse{}
	}
	be := newTestBackend(t, f)
	if _, err := be.Draft(context.Background(), "hi", 2); err == nil {
		t.Fatal("expected error on empty choices, got nil")
	}
}

func TestHTTPBackend_Verify_AlignsCandidatePositions(t *testing.T) {
	f := newFakeBackendServer(t)
	// First verify call is the prompt-only token count probe.
	// Second is the real verify with prompt+candidates.
	promptOnlyTokens := 5
	candidateTexts := []string{" alpha", " beta", " gamma"}
	f.verifyResp = func(req completionsRequest) completionsResponse {
		var r completionsResponse
		r.Choices = make([]struct {
			Text           string                          `json:"text"`
			Logprobs       *completionLogprob              `json:"logprobs"`
			PromptLogprobs []map[string]promptLogprobEntry `json:"prompt_logprobs"`
		}, 1)
		if req.PromptLogprobs == nil {
			// Prompt-only count probe.
			r.Usage.PromptTokens = promptOnlyTokens
			return r
		}
		// Full verify call: build prompt_logprobs of length
		// promptOnlyTokens + len(candidates). First N are prompt
		// positions (filled but irrelevant); last 3 align to the
		// candidate texts. Position 0 must be null per vLLM convention.
		total := promptOnlyTokens + len(candidateTexts)
		r.Choices[0].PromptLogprobs = make([]map[string]promptLogprobEntry, total)
		for i := 1; i < promptOnlyTokens; i++ {
			r.Choices[0].PromptLogprobs[i] = map[string]promptLogprobEntry{
				"999": {Logprob: -0.1, Rank: 1, DecodedToken: "prompt_tok"},
			}
		}
		// Candidate position 0: verifier argmax MATCHES draft (" alpha"),
		// so AcceptGreedy will accept.
		r.Choices[0].PromptLogprobs[promptOnlyTokens] = map[string]promptLogprobEntry{
			"1": {Logprob: -0.2, Rank: 1, DecodedToken: " alpha"},
		}
		// Candidate position 1: verifier argmax is " BETA" (different text),
		// draft is " beta". Both appear in top-K so we can read the
		// draft's logprob too.
		r.Choices[0].PromptLogprobs[promptOnlyTokens+1] = map[string]promptLogprobEntry{
			"2": {Logprob: -0.3, Rank: 1, DecodedToken: " BETA"},
			"3": {Logprob: -3.0, Rank: 5, DecodedToken: " beta"},
		}
		// Candidate position 2: verifier argmax is " gamma" (matches).
		r.Choices[0].PromptLogprobs[promptOnlyTokens+2] = map[string]promptLogprobEntry{
			"4": {Logprob: -0.1, Rank: 1, DecodedToken: " gamma"},
		}
		return r
	}

	be := newTestBackend(t, f)
	draft := []spec_decode.Token{
		{ID: tokenIDFromText(" alpha"), Text: " alpha", Logprob: -0.5},
		{ID: tokenIDFromText(" beta"), Text: " beta", Logprob: -0.5},
		{ID: tokenIDFromText(" gamma"), Text: " gamma", Logprob: -0.5},
	}
	got, err := be.Verify(context.Background(), "this is a prompt", draft)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 logprobs, got %d", len(got))
	}

	// Position 0: argmax matches draft → AcceptGreedy accepts.
	if got[0].Argmax.Text != " alpha" {
		t.Errorf("pos0 argmax: got %q want \" alpha\"", got[0].Argmax.Text)
	}
	if got[0].Argmax.ID != draft[0].ID {
		t.Errorf("pos0 argmax.ID should match draft.ID (same text → same hash)")
	}
	// Position 1: argmax does NOT match draft.
	if got[1].Argmax.Text != " BETA" {
		t.Errorf("pos1 argmax: got %q want \" BETA\"", got[1].Argmax.Text)
	}
	if got[1].Argmax.ID == draft[1].ID {
		t.Errorf("pos1 argmax.ID must NOT equal draft.ID")
	}
	// Position 1's DraftCandidateLogprob should be the -3.0 entry, not the fallback.
	if got[1].DraftCandidateLogprob != -3.0 {
		t.Errorf("pos1 draft logprob: got %v want -3.0", got[1].DraftCandidateLogprob)
	}
	// Position 2: argmax matches.
	if got[2].Argmax.Text != " gamma" {
		t.Errorf("pos2 argmax: got %q want \" gamma\"", got[2].Argmax.Text)
	}

	// Two calls: one prompt-only count, one verify.
	if len(f.lastVerify) != 2 {
		t.Errorf("expected 2 verify calls (count + verify), got %d", len(f.lastVerify))
	}
}

func TestHTTPBackend_Verify_PromptTokenCache(t *testing.T) {
	f := newFakeBackendServer(t)
	f.verifyResp = func(req completionsRequest) completionsResponse {
		var r completionsResponse
		r.Choices = make([]struct {
			Text           string                          `json:"text"`
			Logprobs       *completionLogprob              `json:"logprobs"`
			PromptLogprobs []map[string]promptLogprobEntry `json:"prompt_logprobs"`
		}, 1)
		if req.PromptLogprobs == nil {
			r.Usage.PromptTokens = 3
			return r
		}
		r.Choices[0].PromptLogprobs = []map[string]promptLogprobEntry{
			nil,
			{"a": {Rank: 1, DecodedToken: "x"}},
			{"b": {Rank: 1, DecodedToken: "y"}},
			{"c": {Rank: 1, DecodedToken: " hi"}},
		}
		return r
	}
	be := newTestBackend(t, f)

	draft := []spec_decode.Token{{ID: tokenIDFromText(" hi"), Text: " hi"}}
	for i := 0; i < 3; i++ {
		if _, err := be.Verify(context.Background(), "same prompt", draft); err != nil {
			t.Fatalf("Verify round %d: %v", i, err)
		}
	}
	// Three Verify rounds + one cached prompt-count call = 4 total.
	if got := len(f.lastVerify); got != 4 {
		t.Errorf("expected 4 verify HTTP calls (1 count + 3 verify), got %d", got)
	}
}

func TestHTTPBackend_Verify_MissingPromptLogprobs(t *testing.T) {
	f := newFakeBackendServer(t)
	f.verifyResp = func(req completionsRequest) completionsResponse {
		var r completionsResponse
		r.Choices = make([]struct {
			Text           string                          `json:"text"`
			Logprobs       *completionLogprob              `json:"logprobs"`
			PromptLogprobs []map[string]promptLogprobEntry `json:"prompt_logprobs"`
		}, 1)
		if req.PromptLogprobs == nil {
			r.Usage.PromptTokens = 2
		}
		// No prompt_logprobs in any response — simulates backend without
		// the vLLM extension.
		return r
	}
	be := newTestBackend(t, f)
	draft := []spec_decode.Token{{ID: tokenIDFromText("x"), Text: "x"}}
	_, err := be.Verify(context.Background(), "p", draft)
	if err == nil || !strings.Contains(err.Error(), "prompt_logprobs missing") {
		t.Errorf("expected prompt_logprobs missing error, got %v", err)
	}
}

func TestHTTPBackend_PostJSON_Non2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "vLLM error: model not found", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	be, err := newHTTPBackend(httpBackendConfig{
		httpClient:  server.Client(),
		draftURL:    server.URL,
		verifyURL:   server.URL,
		draftModel:  "d",
		verifyModel: "v",
		promptTopK:  5,
	})
	if err != nil {
		t.Fatalf("newHTTPBackend: %v", err)
	}
	_, err = be.Draft(context.Background(), "hi", 1)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 error, got %v", err)
	}
}

func TestNewHTTPBackend_RequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*httpBackendConfig)
		wantSub string
	}{
		{"missing draft-url", func(c *httpBackendConfig) { c.draftURL = "" }, "draft-url"},
		{"missing verify-url", func(c *httpBackendConfig) { c.verifyURL = "" }, "verify-url"},
		{"missing draft-model", func(c *httpBackendConfig) { c.draftModel = "" }, "draft-model"},
		{"missing verify-model", func(c *httpBackendConfig) { c.verifyModel = "" }, "verify-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := httpBackendConfig{
				draftURL:    "http://x/draft",
				verifyURL:   "http://x/verify",
				draftModel:  "d",
				verifyModel: "v",
				promptTopK:  5,
			}
			tc.mutate(&cfg)
			_, err := newHTTPBackend(cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("want error containing %q, got %v", tc.wantSub, err)
			}
		})
	}
}

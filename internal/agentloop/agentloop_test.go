package agentloop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeCompleter returns a scripted sequence of (Message, TurnMetrics) per
// call. It also records every message slice it was handed so tests can
// assert the append-only prefix-extension invariant.
type fakeCompleter struct {
	replies []Message
	metrics []TurnMetrics
	seen    [][]Message
	call    int
}

func (f *fakeCompleter) Complete(_ context.Context, msgs []Message, _ []ToolDef, _ int) (Message, TurnMetrics, error) {
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	f.seen = append(f.seen, cp)
	i := f.call
	f.call++
	var m TurnMetrics
	if i < len(f.metrics) {
		m = f.metrics[i]
	}
	return f.replies[i], m, nil
}

func echoTool(t *testing.T) Tool {
	t.Helper()
	return FunctionTool{
		Def: ToolDef{
			Type: "function",
			Function: ToolFunctionDef{
				Name:        "echo",
				Description: "echo back the input",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			},
		},
		Fn: func(_ context.Context, args string) (string, error) {
			var p struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal([]byte(args), &p)
			return "echoed: " + p.Text, nil
		},
	}
}

func TestConversationAppendOnlyPrefixExtension(t *testing.T) {
	c := NewConversation("SYS")
	c.Append(Message{Role: RoleUser, Content: "hi"})
	first := c.Messages()
	c.Append(Message{Role: RoleAssistant, Content: "hello"})
	second := c.Messages()

	require.Equal(t, RoleSystem, first[0].Role)
	require.Len(t, first, 2)
	require.Len(t, second, 3)
	// second must be a strict prefix-extension of first: every message in
	// first appears unchanged at the same index in second.
	for i := range first {
		require.Equal(t, first[i], second[i], "message %d changed — prefix busted", i)
	}
	// Mutating the returned slice must not corrupt internal state.
	second[0].Content = "TAMPERED"
	require.Equal(t, "SYS", c.Messages()[0].Content)
}

func TestRegistryDuplicateAndOrder(t *testing.T) {
	a := FunctionTool{Def: ToolDef{Function: ToolFunctionDef{Name: "a"}}}
	b := FunctionTool{Def: ToolDef{Function: ToolFunctionDef{Name: "b"}}}
	r, err := NewRegistry(a, b)
	require.NoError(t, err)
	require.Equal(t, 2, r.Len())
	defs := r.Definitions()
	require.Equal(t, "a", defs[0].Function.Name)
	require.Equal(t, "b", defs[1].Function.Name)

	_, err = NewRegistry(a, a)
	require.Error(t, err, "duplicate tool name must error")
}

func TestBudget(t *testing.T) {
	b := Budget{MaxModelLen: 20480, SystemTokens: 3000, OutputReserve: 48}
	require.Equal(t, 20480-3000-48, b.Usable())
	require.Equal(t, 20480-48, b.PromptCeiling())
	require.Nil(t, b.Check(100))
	require.Nil(t, b.Check(20432)) // exactly at ceiling
	be := b.Check(20500)
	require.NotNil(t, be)
	require.Equal(t, 20500-(20480-48), be.OverBy)

	// Degenerate budget clamps, never negative.
	require.Equal(t, 0, Budget{MaxModelLen: 10, SystemTokens: 8, OutputReserve: 8}.Usable())
}

func TestParseTurnMetrics(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderUpstreamMs, "1412")
	h.Set(HeaderFinishReason, "stop")
	h.Set(HeaderCachedTokens, "5000")
	m := parseTurnMetrics(h, 5154)
	require.Equal(t, int64(1412), m.UpstreamMs)
	require.Equal(t, 5154, m.PromptTokens)
	require.NotNil(t, m.CachedTokens)
	require.Equal(t, 5000, *m.CachedTokens)
	require.NotNil(t, m.PrefixHitRatio)
	require.InDelta(t, 5000.0/5154.0, *m.PrefixHitRatio, 1e-9)

	require.Nil(t, m.PrefixCacheHitRate, "engine-reported rate absent when header not set")

	// CachedTokens absent (the gemma4 fallback path) -> nil, not zero. But the
	// proxy-scraped engine rate IS present (the row-195 follow-up): the client
	// now reports the hit ratio directly even without cached_tokens.
	h2 := http.Header{}
	h2.Set(HeaderPromptTokens, "8000")
	h2.Set(HeaderPrefixHitRate, "0.9300")
	m2 := parseTurnMetrics(h2, 0)
	require.Nil(t, m2.CachedTokens)
	require.Nil(t, m2.PrefixHitRatio)
	require.Equal(t, 8000, m2.PromptTokens, "falls back to header when usage absent")
	require.NotNil(t, m2.PrefixCacheHitRate, "engine-reported rate parsed from proxy header")
	require.InDelta(t, 0.93, *m2.PrefixCacheHitRate, 1e-9)
}

func TestEngineRunToolThenFinal(t *testing.T) {
	reg, err := NewRegistry(echoTool(t))
	require.NoError(t, err)

	fc := &fakeCompleter{
		replies: []Message{
			{Role: RoleAssistant, ToolCalls: []ToolCall{{
				ID: "c1", Type: "function",
				Function: FunctionCall{Name: "echo", Arguments: `{"text":"hi"}`},
			}}},
			{Role: RoleAssistant, Content: "done"},
		},
		metrics: []TurnMetrics{
			{UpstreamMs: 1400, PromptTokens: 5000, FinishReason: "tool_calls"},
			{UpstreamMs: 1450, PromptTokens: 5400, FinishReason: "stop"},
		},
	}
	eng := &Engine{Client: fc, Registry: reg, MaxRounds: 8, OutputTokens: 48,
		Budget: Budget{MaxModelLen: 20480, OutputReserve: 48}}
	conv := NewConversation("SYS")

	res, err := eng.Run(context.Background(), conv, "please echo hi")
	require.NoError(t, err)
	require.Equal(t, StopFinal, res.Stopped)
	require.Equal(t, "done", res.Answer)
	require.Len(t, res.Rounds, 2)
	require.Len(t, res.Rounds[0].ToolCalls, 1)
	require.Equal(t, "echoed: hi", res.Rounds[0].ToolCalls[0].Result)
	require.True(t, res.Rounds[1].Final)

	// The second request the engine sent must be a prefix-extension of the
	// first — the cache-paying invariant, end to end through the loop.
	require.Greater(t, len(fc.seen), 1)
	first, second := fc.seen[0], fc.seen[1]
	for i := range first {
		require.Equal(t, first[i], second[i], "loop produced non-append-only context at %d", i)
	}
}

func TestEngineRunBudgetStop(t *testing.T) {
	reg, err := NewRegistry(echoTool(t))
	require.NoError(t, err)
	// Model keeps calling tools; PromptTokens exceeds the ceiling after
	// round 0, so the loop must stop with StopBudget rather than spin.
	toolReply := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{
		ID: "c1", Type: "function",
		Function: FunctionCall{Name: "echo", Arguments: `{"text":"x"}`},
	}}}
	fc := &fakeCompleter{
		replies: []Message{toolReply, toolReply, toolReply},
		metrics: []TurnMetrics{{PromptTokens: 21000}, {PromptTokens: 22000}, {PromptTokens: 23000}},
	}
	eng := &Engine{Client: fc, Registry: reg, MaxRounds: 8, OutputTokens: 48,
		Budget: Budget{MaxModelLen: 20480, OutputReserve: 48}}
	res, err := eng.Run(context.Background(), NewConversation("SYS"), "go")
	require.NoError(t, err)
	require.Equal(t, StopBudget, res.Stopped)
	require.Len(t, res.Rounds, 1, "should stop after the first over-budget round")
}

func TestBudgetErrorFromBody(t *testing.T) {
	be := budgetErrorFromBody([]byte(`{"error":{"message":"too big","tokens_submitted":21000,"tokens_budget":20432,"tokens_over":568}}`))
	require.NotNil(t, be)
	require.Equal(t, 21000, be.PromptTokens)
	require.Equal(t, 568, be.OverBy)

	be2 := budgetErrorFromBody([]byte(`{"error":{"message":"This model's maximum context length is 20480 tokens"}}`))
	require.NotNil(t, be2, "vLLM context-length message should be recognised")

	require.Nil(t, budgetErrorFromBody([]byte(`{"error":{"message":"some unrelated 400"}}`)))
}

func TestChatClientCompleteSetsCacheKeyAndParsesMetrics(t *testing.T) {
	var gotCacheKey, gotWantPrefixHit string
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCacheKey = r.Header.Get(HeaderCacheKey)
		gotWantPrefixHit = r.Header.Get(HeaderWantPrefixHit)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set(HeaderUpstreamMs, "1412")
		w.Header().Set(HeaderFinishReason, "stop")
		// gemma4 omits cached_tokens but the proxy supplies the scraped rate.
		w.Header().Set(HeaderPrefixHitRate, "0.9300")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5154}}`))
	}))
	defer srv.Close()

	c, err := NewChatClient(ChatClientConfig{Endpoint: srv.URL, Model: "m", CacheKey: "sess-42", WantPrefixHit: true})
	require.NoError(t, err)
	msg, metrics, err := c.Complete(context.Background(),
		[]Message{{Role: RoleSystem, Content: "SYS"}}, nil, 48)
	require.NoError(t, err)
	require.Equal(t, "hi", msg.Content)
	require.Equal(t, "sess-42", gotCacheKey, "cache key must pin prefix routing")
	require.Equal(t, "1", gotWantPrefixHit, "opt-in header sent when WantPrefixHit")
	require.Equal(t, "m", gotBody.Model)
	require.Equal(t, int64(1412), metrics.UpstreamMs)
	require.Equal(t, 5154, metrics.PromptTokens)
	require.Equal(t, "stop", metrics.FinishReason)
	require.NotNil(t, metrics.PrefixCacheHitRate, "engine-reported rate parsed even without cached_tokens")
	require.InDelta(t, 0.93, *metrics.PrefixCacheHitRate, 1e-9)
}

func TestChatClientOmitsPrefixHitOptInByDefault(t *testing.T) {
	var gotWantPrefixHit = "unset"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWantPrefixHit = r.Header.Get(HeaderWantPrefixHit)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10}}`))
	}))
	defer srv.Close()

	c, err := NewChatClient(ChatClientConfig{Endpoint: srv.URL, Model: "m"}) // WantPrefixHit false
	require.NoError(t, err)
	_, _, err = c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil, 48)
	require.NoError(t, err)
	require.Empty(t, gotWantPrefixHit, "no opt-in header on the zero-cost default path")
}

func TestChatClientContextOverflowIsBudgetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"This model's maximum context length is 20480 tokens"}}`))
	}))
	defer srv.Close()
	c, err := NewChatClient(ChatClientConfig{Endpoint: srv.URL, Model: "m"})
	require.NoError(t, err)
	_, _, err = c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "x"}}, nil, 48)
	var be *BudgetError
	require.ErrorAs(t, err, &be)
}

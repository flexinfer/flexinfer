package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// histSampleCount returns the number of observations recorded for a given
// label value on a token-shape histogram. Reads the live collector directly so
// it is independent of the registry.
func histSampleCount(t *testing.T, vec *prometheus.HistogramVec, model string) uint64 {
	t.Helper()
	obs, err := vec.GetMetricWithLabelValues(model)
	require.NoError(t, err)
	h, ok := obs.(prometheus.Histogram)
	require.True(t, ok, "observer should be a prometheus.Histogram")
	var m dto.Metric
	require.NoError(t, h.Write(&m))
	return m.GetHistogram().GetSampleCount()
}

// counterValue returns the current value of a {model,stream} completions counter.
func counterValue(t *testing.T, model, stream string) float64 {
	t.Helper()
	c, err := completionsTotal.GetMetricWithLabelValues(model, stream)
	require.NoError(t, err)
	var m dto.Metric
	require.NoError(t, c.Write(&m))
	return m.GetCounter().GetValue()
}

func TestParseRequestForUsageLog(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantMaxTokens int
		wantStream    bool
	}{
		{
			name:          "max_tokens int + stream true",
			body:          `{"model":"m","max_tokens":256,"stream":true}`,
			wantMaxTokens: 256,
			wantStream:    true,
		},
		{
			name:          "max_tokens float + no stream",
			body:          `{"model":"m","max_tokens":64.0}`,
			wantMaxTokens: 64,
			wantStream:    false,
		},
		{
			name:          "stream false explicit",
			body:          `{"model":"m","stream":false}`,
			wantMaxTokens: -1,
			wantStream:    false,
		},
		{
			name:          "neither field present",
			body:          `{"model":"m","temperature":0.7}`,
			wantMaxTokens: -1,
			wantStream:    false,
		},
		{
			name:          "negative max_tokens rejected",
			body:          `{"max_tokens":-1}`,
			wantMaxTokens: -1,
			wantStream:    false,
		},
		{
			name:          "stream wrong type ignored",
			body:          `{"stream":"yes"}`,
			wantMaxTokens: -1,
			wantStream:    false,
		},
		{
			name:          "malformed json",
			body:          `not json`,
			wantMaxTokens: -1,
			wantStream:    false,
		},
		{
			name:          "empty body",
			body:          ``,
			wantMaxTokens: -1,
			wantStream:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotMax, gotStream := parseRequestForUsageLog([]byte(tc.body))
			require.Equal(t, tc.wantMaxTokens, gotMax)
			require.Equal(t, tc.wantStream, gotStream)
		})
	}
}

func TestExtractUsageFields(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		wantPromptTokens  int
		wantCompletionTok int
		wantFinishReason  string
		wantOK            bool
	}{
		{
			name:              "chat completion full",
			body:              `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":23,"completion_tokens":11}}`,
			wantPromptTokens:  23,
			wantCompletionTok: 11,
			wantFinishReason:  "stop",
			wantOK:            true,
		},
		{
			name:              "finish_reason length",
			body:              `{"choices":[{"finish_reason":"length"}],"usage":{"prompt_tokens":1567,"completion_tokens":256}}`,
			wantPromptTokens:  1567,
			wantCompletionTok: 256,
			wantFinishReason:  "length",
			wantOK:            true,
		},
		{
			name:             "no choices, usage only",
			body:             `{"usage":{"prompt_tokens":5,"completion_tokens":0}}`,
			wantPromptTokens: 5,
			wantOK:           true,
		},
		{
			name:   "malformed json",
			body:   `garbage`,
			wantOK: false,
		},
		{
			name:   "empty json object",
			body:   `{}`,
			wantOK: true, // unmarshal succeeds, all zero values
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pt, ct, fr, ok := extractUsageFields([]byte(tc.body))
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantPromptTokens, pt)
			require.Equal(t, tc.wantCompletionTok, ct)
			require.Equal(t, tc.wantFinishReason, fr)
		})
	}
}

func TestIsCompletionPath(t *testing.T) {
	cases := map[string]bool{
		"/v1/chat/completions":                true,
		"/v1/completions":                     true,
		"/model/gemma4/v1/chat/completions":   true,
		"/model/foo/v1/completions":           true,
		"/v1/models":                          false,
		"/v1/chat/completions/something-else": false,
		"":                                    false,
	}
	for path, want := range cases {
		t.Run(path, func(t *testing.T) {
			require.Equal(t, want, isCompletionPath(path))
		})
	}
}

func TestExtractUsageFieldsFull_CachedTokens(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantCached     int
		wantCachedOK   bool
		wantOK         bool
		wantPromptTok  int
		wantFinishReas string
	}{
		{
			name: "vllm prefix-cache hit",
			body: `{"choices":[{"finish_reason":"stop"}],` +
				`"usage":{"prompt_tokens":1024,"completion_tokens":42,` +
				`"prompt_tokens_details":{"cached_tokens":896}}}`,
			wantCached:     896,
			wantCachedOK:   true,
			wantOK:         true,
			wantPromptTok:  1024,
			wantFinishReas: "stop",
		},
		{
			name: "vllm cache miss (explicit zero)",
			body: `{"choices":[{"finish_reason":"length"}],` +
				`"usage":{"prompt_tokens":512,"completion_tokens":256,` +
				`"prompt_tokens_details":{"cached_tokens":0}}}`,
			wantCached:     0,
			wantCachedOK:   true,
			wantOK:         true,
			wantPromptTok:  512,
			wantFinishReas: "length",
		},
		{
			name:           "engine omits prompt_tokens_details (llama.cpp)",
			body:           `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":23,"completion_tokens":11}}`,
			wantCached:     -1,
			wantCachedOK:   false,
			wantOK:         true,
			wantPromptTok:  23,
			wantFinishReas: "stop",
		},
		{
			name: "details present but cached_tokens missing",
			body: `{"usage":{"prompt_tokens":5,"completion_tokens":0,` +
				`"prompt_tokens_details":{}}}`,
			wantCached:    -1,
			wantCachedOK:  false,
			wantOK:        true,
			wantPromptTok: 5,
		},
		{
			name:         "malformed json",
			body:         `not json`,
			wantCached:   -1,
			wantCachedOK: false,
			wantOK:       false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pt, _, fr, cached, cachedOK, ok := extractUsageFieldsFull([]byte(tc.body))
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantCached, cached)
			require.Equal(t, tc.wantCachedOK, cachedOK)
			require.Equal(t, tc.wantPromptTok, pt)
			require.Equal(t, tc.wantFinishReas, fr)
		})
	}
}

func TestLogUpstreamUsage_EmitsHeaders(t *testing.T) {
	// Non-streaming completion with vLLM prefix-cache hit. Verifies the
	// F4-instant-followup headers land on the response so clients/operators
	// can read prefix-cache behavior without round-tripping to vLLM metrics.
	body := `{"choices":[{"finish_reason":"stop"}],` +
		`"usage":{"prompt_tokens":2048,"completion_tokens":42,` +
		`"prompt_tokens_details":{"cached_tokens":1792}}}`

	req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
	require.NoError(t, err)
	lc := &usageLogCtx{
		model:     "gemma4-26b-a4b-gptq",
		path:      "/v1/chat/completions",
		maxTokens: 256,
		stream:    false,
		startedAt: time.Now().Add(-150 * time.Millisecond),
	}
	req = req.WithContext(withUsageLogCtx(context.Background(), lc))

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(resp))

	// Upstream-ms is set on every completion response and should be
	// non-negative — we set startedAt 150ms in the past.
	gotMs, err := strconv.ParseInt(resp.Header.Get(headerUpstreamMs), 10, 64)
	require.NoError(t, err, "X-Flexinfer-Upstream-Ms should parse as int64")
	require.GreaterOrEqual(t, gotMs, int64(100), "upstream-ms should reflect elapsed time")

	require.Equal(t, "1792", resp.Header.Get(headerCachedTokens))
	require.Equal(t, "2048", resp.Header.Get(headerPromptTokens))
	require.Equal(t, "stop", resp.Header.Get(headerFinishReason))

	// Response body must round-trip unchanged for the downstream client.
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(out))
}

func TestLogUpstreamUsage_OmitsCachedHeaderWhenEngineSilent(t *testing.T) {
	// llama.cpp does not report prompt_tokens_details. We must not emit a
	// misleading "0" — the header is absent so clients can tell the
	// difference between "cache miss" and "engine doesn't report".
	body := `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":23,"completion_tokens":11}}`

	req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
	require.NoError(t, err)
	lc := &usageLogCtx{path: "/v1/chat/completions", startedAt: time.Now()}
	req = req.WithContext(withUsageLogCtx(context.Background(), lc))

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(resp))

	require.NotEmpty(t, resp.Header.Get(headerUpstreamMs))
	require.Empty(t, resp.Header.Get(headerCachedTokens),
		"X-Flexinfer-Cached-Tokens must be absent when the engine doesn't report it")
	require.Equal(t, "23", resp.Header.Get(headerPromptTokens))
}

func TestPrefixHitOptIn(t *testing.T) {
	cases := map[string]bool{
		"1":     true,
		"true":  true,
		"TRUE":  true,
		"yes":   true,
		"on":    true,
		" 1 ":   true,
		"0":     false,
		"false": false,
		"":      false,
		"no":    false,
		"maybe": false,
	}
	for v, want := range cases {
		t.Run(v, func(t *testing.T) {
			require.Equal(t, want, prefixHitOptIn(v))
		})
	}
}

func TestParsePrefixCacheHitRate(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantRate float64
		wantOK   bool
	}{
		{
			name: "gauge present",
			body: "# HELP vllm:gpu_prefix_cache_hit_rate GPU prefix cache hit rate\n" +
				"# TYPE vllm:gpu_prefix_cache_hit_rate gauge\n" +
				`vllm:gpu_prefix_cache_hit_rate{model_name="gemma4"} 0.93` + "\n",
			wantRate: 0.93,
			wantOK:   true,
		},
		{
			name: "counters only — lifetime ratio",
			body: `vllm:gpu_prefix_cache_queries_total{model_name="gemma4"} 363373` + "\n" +
				`vllm:gpu_prefix_cache_hits_total{model_name="gemma4"} 330800` + "\n",
			wantRate: 330800.0 / 363373.0,
			wantOK:   true,
		},
		{
			name: "gauge preferred over counters",
			body: `vllm:gpu_prefix_cache_hit_rate 0.5` + "\n" +
				`vllm:gpu_prefix_cache_hits_total 10` + "\n" +
				`vllm:gpu_prefix_cache_queries_total 100` + "\n",
			wantRate: 0.5,
			wantOK:   true,
		},
		{
			name: "non-gpu spelling counters",
			body: `vllm:prefix_cache_hits_total 25` + "\n" +
				`vllm:prefix_cache_queries_total 100` + "\n",
			wantRate: 0.25,
			wantOK:   true,
		},
		{
			name: "multiple series summed",
			body: `vllm:gpu_prefix_cache_hits_total{gpu="0"} 30` + "\n" +
				`vllm:gpu_prefix_cache_hits_total{gpu="1"} 20` + "\n" +
				`vllm:gpu_prefix_cache_queries_total{gpu="0"} 60` + "\n" +
				`vllm:gpu_prefix_cache_queries_total{gpu="1"} 40` + "\n",
			wantRate: 50.0 / 100.0,
			wantOK:   true,
		},
		{
			name:   "queries zero — no rate",
			body:   `vllm:gpu_prefix_cache_hits_total 0` + "\n" + `vllm:gpu_prefix_cache_queries_total 0` + "\n",
			wantOK: false,
		},
		{
			name:   "metric absent",
			body:   "# nothing relevant\nvllm:num_requests_running 3\n",
			wantOK: false,
		},
		{
			name:   "empty body",
			body:   "",
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rate, ok := parsePrefixCacheHitRate([]byte(tc.body))
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.InDelta(t, tc.wantRate, rate, 1e-9)
			}
		})
	}
}

func TestScrapePrefixCacheHitRate(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/metrics", r.URL.Path)
			_, _ = io.WriteString(w, `vllm:gpu_prefix_cache_hits_total 93`+"\n"+`vllm:gpu_prefix_cache_queries_total 100`+"\n")
		}))
		defer srv.Close()
		rate, ok := scrapePrefixCacheHitRate(context.Background(), srv.URL)
		require.True(t, ok)
		require.InDelta(t, 0.93, rate, 1e-9)
	})

	t.Run("non-200 returns not-ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		_, ok := scrapePrefixCacheHitRate(context.Background(), srv.URL)
		require.False(t, ok)
	})

	t.Run("empty target returns not-ok", func(t *testing.T) {
		_, ok := scrapePrefixCacheHitRate(context.Background(), "")
		require.False(t, ok)
	})

	t.Run("unreachable target returns not-ok", func(t *testing.T) {
		_, ok := scrapePrefixCacheHitRate(context.Background(), "http://127.0.0.1:0")
		require.False(t, ok)
	})
}

func TestLogUpstreamUsage_PrefixHitOptIn(t *testing.T) {
	// gemma4 omits prompt_tokens_details, so Cached-Tokens is absent. When the
	// client opts in, the proxy scrapes the engine /metrics and surfaces the
	// hit rate directly — closing the row-195 caveat.
	metricsBody := `vllm:gpu_prefix_cache_hits_total 930` + "\n" + `vllm:gpu_prefix_cache_queries_total 1000` + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, metricsBody)
	}))
	defer srv.Close()

	body := `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":2048,"completion_tokens":42}}`

	newResp := func(want bool) *http.Response {
		req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
		require.NoError(t, err)
		lc := &usageLogCtx{
			path:          "/v1/chat/completions",
			startedAt:     time.Now(),
			wantPrefixHit: want,
			targetURL:     srv.URL,
		}
		req = req.WithContext(withUsageLogCtx(context.Background(), lc))
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
			Request:    req,
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp
	}

	p := &Proxy{}

	// Opt-in: header present, engine-silent Cached-Tokens still absent.
	respIn := newResp(true)
	require.NoError(t, p.logUpstreamUsage(respIn))
	require.Equal(t, "0.9300", respIn.Header.Get(headerPrefixHitRate))
	require.Empty(t, respIn.Header.Get(headerCachedTokens))

	// Opt-out: no scrape, header absent (zero-cost default path).
	respOut := newResp(false)
	require.NoError(t, p.logUpstreamUsage(respOut))
	require.Empty(t, respOut.Header.Get(headerPrefixHitRate))
}

func TestLogUpstreamUsage_StreamingEmitsUpstreamMs(t *testing.T) {
	// Streaming requests don't have a buffered body to parse, but the
	// upstream-ms header must still land — ModifyResponse fires on the
	// upstream 200 OK before the first SSE chunk is forwarded, which is
	// the TTFT we want surfaced.
	req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
	require.NoError(t, err)
	lc := &usageLogCtx{
		path:      "/v1/chat/completions",
		stream:    true,
		startedAt: time.Now().Add(-180 * time.Millisecond),
	}
	req = req.WithContext(withUsageLogCtx(context.Background(), lc))

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "text/event-stream")

	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(resp))

	gotMs, err := strconv.ParseInt(resp.Header.Get(headerUpstreamMs), 10, 64)
	require.NoError(t, err)
	require.GreaterOrEqual(t, gotMs, int64(100))
	// No cached-tokens / prompt-tokens for streaming (we don't buffer SSE).
	require.Empty(t, resp.Header.Get(headerCachedTokens))
	require.Empty(t, resp.Header.Get(headerPromptTokens))
}

func TestLogUpstreamUsage_RecordsTokenShapeMetrics(t *testing.T) {
	// A non-streaming completion records one observation on each token-shape
	// histogram, labeled by the resolved model. This is the scrape-reliable
	// view of the per-lane traffic mix that grounds workload-conditional
	// decisions (e.g. blanket n-gram SD).
	const model = "shape-metric-nonstream-lane"
	body := `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":1500,"completion_tokens":900}}`

	req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
	require.NoError(t, err)
	lc := &usageLogCtx{
		model:         "alias",
		resolvedModel: model,
		path:          "/v1/chat/completions",
		startedAt:     time.Now(),
	}
	req = req.WithContext(withUsageLogCtx(context.Background(), lc))

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "application/json")

	beforeP := histSampleCount(t, requestPromptTokens, model)
	beforeC := histSampleCount(t, requestCompletionTokens, model)

	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(resp))

	require.Equal(t, beforeP+1, histSampleCount(t, requestPromptTokens, model),
		"prompt-token histogram should record one observation")
	require.Equal(t, beforeC+1, histSampleCount(t, requestCompletionTokens, model),
		"completion-token histogram should record one observation")
}

func TestLogUpstreamUsage_SkipsTokenShapeMetricsForStreamingWithoutUsageChunk(t *testing.T) {
	// A streaming response whose client did not request stream_options.include_usage
	// carries no terminal usage chunk, so the shape histograms must not record —
	// keeping the sampling bias explicit rather than silently logging a zero. The
	// body is fully drained and closed to exercise the sniffer's parse path.
	const model = "shape-metric-stream-nousage-lane"
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\n" +
		"data: [DONE]\n\n"
	req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
	require.NoError(t, err)
	lc := &usageLogCtx{
		resolvedModel: model,
		path:          "/v1/chat/completions",
		stream:        true,
		startedAt:     time.Now(),
	}
	req = req.WithContext(withUsageLogCtx(context.Background(), lc))

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(sse))),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "text/event-stream")

	before := histSampleCount(t, requestPromptTokens, model)
	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(resp))

	// Drain + close like the reverse proxy would; the stream must pass through
	// unchanged and the sniffer must find no usage chunk.
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, sse, string(out), "streaming body must pass through unchanged")
	require.Equal(t, before, histSampleCount(t, requestPromptTokens, model),
		"streaming requests without a usage chunk must not record token-shape observations")
}

func TestExtractStreamingUsage(t *testing.T) {
	tests := []struct {
		name         string
		tail         string
		wantPT       int
		wantCT       int
		wantCached   int
		wantCachedOK bool
		wantOK       bool
	}{
		{
			name: "vllm terminal usage chunk after null frames",
			tail: "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}],\"usage\":null}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1500,\"completion_tokens\":900,\"total_tokens\":2400}}\n\n" +
				"data: [DONE]\n\n",
			wantPT:     1500,
			wantCT:     900,
			wantCached: -1,
			wantOK:     true,
		},
		{
			name:   "truncated leading partial line then valid usage chunk",
			tail:   " delta\":{\"content\":\"xyz\"}}],\"usage\":null}\n\ndata: {\"usage\":{\"prompt_tokens\":42,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n",
			wantPT: 42,
			wantCT: 7,

			wantCached: -1,
			wantOK:     true,
		},
		{
			// The live RP-lane shape: a warm repeat streams cached_tokens on
			// the terminal chunk. This is the only place a streaming client
			// can see it — headers are long flushed by now.
			name: "terminal chunk carries prompt_tokens_details.cached_tokens",
			tail: "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7252,\"completion_tokens\":12," +
				"\"prompt_tokens_details\":{\"cached_tokens\":7072}}}\n\n" +
				"data: [DONE]\n\n",
			wantPT:       7252,
			wantCT:       12,
			wantCached:   7072,
			wantCachedOK: true,
			wantOK:       true,
		},
		{
			// A cold request on an APC lane: vLLM omits the detail block
			// entirely rather than sending cached_tokens: 0. Must stay
			// distinguishable from "engine never reports it".
			name: "cold request omits detail block",
			tail: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7252,\"completion_tokens\":12}}\n\n" +
				"data: [DONE]\n\n",
			wantPT:     7252,
			wantCT:     12,
			wantCached: -1,
			wantOK:     true,
		},
		{
			name: "explicit cached_tokens zero is reported, not treated as absent",
			tail: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":300,\"completion_tokens\":5," +
				"\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\n" +
				"data: [DONE]\n\n",
			wantPT:       300,
			wantCT:       5,
			wantCached:   0,
			wantCachedOK: true,
			wantOK:       true,
		},
		{
			// Guards the per-frame reset: a later usage frame without the
			// detail block must not inherit the earlier frame's cached count.
			name: "later usage frame without details does not inherit earlier cached count",
			tail: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":1," +
				"\"prompt_tokens_details\":{\"cached_tokens\":64}}}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":200,\"completion_tokens\":2}}\n\n" +
				"data: [DONE]\n\n",
			wantPT:     200,
			wantCT:     2,
			wantCached: -1,
			wantOK:     true,
		},
		{
			name:   "no usage chunk (include_usage not set)",
			tail:   "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}],\"usage\":null}\n\ndata: [DONE]\n\n",
			wantOK: false,
		},
		{
			name:   "zero-filled usage placeholder is not recorded",
			tail:   "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n",
			wantOK: false,
		},
		{
			name:   "done only",
			tail:   "data: [DONE]\n\n",
			wantOK: false,
		},
		{
			name:   "empty tail",
			tail:   "",
			wantOK: false,
		},
		{
			name:   "non-sse garbage",
			tail:   "not an sse stream at all\n{\"usage\":{\"completion_tokens\":5}}\n",
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pt, ct, cached, cachedOK, ok := extractStreamingUsage([]byte(tc.tail))
			require.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				require.Equal(t, tc.wantPT, pt)
				require.Equal(t, tc.wantCT, ct)
				require.Equal(t, tc.wantCachedOK, cachedOK)
				require.Equal(t, tc.wantCached, cached)
			}
		})
	}
}

func TestUsageSniffingBody_RecordsOnCloseAndPassesThrough(t *testing.T) {
	// The sniffer forwards the SSE stream byte-for-byte and records the
	// token-shape histograms once, on Close, from the terminal usage chunk.
	const model = "shape-metric-stream-usage-lane"
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}],\"usage\":null}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":256,\"completion_tokens\":2048}}\n\n" +
		"data: [DONE]\n\n"

	beforeP := histSampleCount(t, requestPromptTokens, model)
	beforeC := histSampleCount(t, requestCompletionTokens, model)

	body := newUsageSniffingBody(io.NopCloser(bytes.NewReader([]byte(sse))), model)

	// Read in small chunks to exercise the bounded-tail accumulation across
	// multiple Read calls.
	var got bytes.Buffer
	buf := make([]byte, 8)
	for {
		n, err := body.Read(buf)
		got.Write(buf[:n])
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}

	// Histograms are recorded on Close, not mid-stream.
	require.Equal(t, beforeP, histSampleCount(t, requestPromptTokens, model),
		"no observation should land before Close")
	require.NoError(t, body.Close())

	require.Equal(t, sse, got.String(), "stream must pass through unchanged")
	require.Equal(t, beforeP+1, histSampleCount(t, requestPromptTokens, model))
	require.Equal(t, beforeC+1, histSampleCount(t, requestCompletionTokens, model))

	// Close is idempotent — a second call must not double-record.
	require.NoError(t, body.Close())
	require.Equal(t, beforeP+1, histSampleCount(t, requestPromptTokens, model),
		"Close must record at most once")
}

func TestLogUpstreamUsage_StreamingRecordsTokenShapeFromUsageChunk(t *testing.T) {
	// End-to-end: logUpstreamUsage wraps a streaming completion body; once the
	// reverse proxy drains and closes it, the terminal usage chunk lands on the
	// token-shape histograms — closing the streaming blind spot.
	const model = "shape-metric-e2e-stream-lane"
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}],\"usage\":null}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":120,\"completion_tokens\":64}}\n\n" +
		"data: [DONE]\n\n"

	req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
	require.NoError(t, err)
	lc := &usageLogCtx{
		model:         "alias",
		resolvedModel: model,
		path:          "/v1/chat/completions",
		stream:        true,
		startedAt:     time.Now(),
	}
	req = req.WithContext(withUsageLogCtx(context.Background(), lc))

	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader([]byte(sse))),
		Request:    req,
	}
	resp.Header.Set("Content-Type", "text/event-stream")

	beforeP := histSampleCount(t, requestPromptTokens, model)
	beforeC := histSampleCount(t, requestCompletionTokens, model)

	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(resp))

	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, sse, string(out), "streaming body must pass through unchanged")
	require.Equal(t, beforeP+1, histSampleCount(t, requestPromptTokens, model))
	require.Equal(t, beforeC+1, histSampleCount(t, requestCompletionTokens, model))
}

func TestLogUpstreamUsage_CountsCompletionCoverage(t *testing.T) {
	// Every successful completion increments the coverage counter labeled by
	// stream flag — the denominator that exposes how much traffic the
	// non-streaming-only token-shape histograms miss.
	const model = "coverage-counter-lane"
	p := &Proxy{}

	mkResp := func(stream bool, ct string) *http.Response {
		req, err := http.NewRequest("POST", "/v1/chat/completions", nil)
		require.NoError(t, err)
		lc := &usageLogCtx{
			resolvedModel: model,
			path:          "/v1/chat/completions",
			stream:        stream,
			startedAt:     time.Now(),
		}
		req = req.WithContext(withUsageLogCtx(context.Background(), lc))
		resp := &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Request:    req,
		}
		resp.Header.Set("Content-Type", ct)
		return resp
	}

	beforeNon := counterValue(t, model, "false")
	beforeStream := counterValue(t, model, "true")

	require.NoError(t, p.logUpstreamUsage(mkResp(false, "application/json")))
	require.NoError(t, p.logUpstreamUsage(mkResp(true, "text/event-stream")))

	require.Equal(t, beforeNon+1, counterValue(t, model, "false"),
		"non-streaming completion should increment the false-stream counter")
	require.Equal(t, beforeStream+1, counterValue(t, model, "true"),
		"streaming completion should increment the true-stream counter")
}

// apcTokens reads the prefix-cache counters for a lane.
func apcTokens(t *testing.T, model string) (observed, cached float64) {
	t.Helper()
	return promtestutil.ToFloat64(observedPromptTokensTotal.WithLabelValues(model)),
		promtestutil.ToFloat64(cachedPromptTokensTotal.WithLabelValues(model))
}

// The regression this slice fixes: streamed completions carry cached_tokens on
// their terminal usage chunk, but the proxy used to drop it on the floor, so
// prefix-cache health was unmeasurable for the majority (streamed) traffic
// shape. The header cannot carry it — headers are flushed before the chunk
// exists — so the counters are the contract.
func TestUsageSniffingBody_RecordsPrefixCacheTokens(t *testing.T) {
	const model = "apc-stream-lane"
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}],\"usage\":null}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7252,\"completion_tokens\":12," +
		"\"prompt_tokens_details\":{\"cached_tokens\":7072}}}\n\n" +
		"data: [DONE]\n\n"

	obsBefore, cachedBefore := apcTokens(t, model)

	b := newUsageSniffingBody(io.NopCloser(bytes.NewReader([]byte(sse))), model)
	out, err := io.ReadAll(b)
	require.NoError(t, err)
	require.NoError(t, b.Close())
	require.Equal(t, sse, string(out), "stream must pass through byte-for-byte")

	obsAfter, cachedAfter := apcTokens(t, model)
	require.Equal(t, float64(7252), obsAfter-obsBefore)
	require.Equal(t, float64(7072), cachedAfter-cachedBefore)
}

// A cold request on an APC lane reports no detail block at all. Recording a
// zero-cached denominator would be wrong in the other direction, so neither
// counter may move — otherwise engines that never report the field would drag
// every lane's hit share toward zero.
func TestUsageSniffingBody_NoPrefixCacheDetailLeavesCountersUntouched(t *testing.T) {
	const model = "apc-stream-lane-silent"
	sse := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":7252,\"completion_tokens\":12}}\n\n" +
		"data: [DONE]\n\n"

	obsBefore, cachedBefore := apcTokens(t, model)

	b := newUsageSniffingBody(io.NopCloser(bytes.NewReader([]byte(sse))), model)
	_, err := io.ReadAll(b)
	require.NoError(t, err)
	require.NoError(t, b.Close())

	obsAfter, cachedAfter := apcTokens(t, model)
	require.Equal(t, obsBefore, obsAfter, "denominator must not move without a cached_tokens report")
	require.Equal(t, cachedBefore, cachedAfter)
}

// The non-streamed path feeds the same counters, so the APC hit share is one
// number across both traffic shapes.
func TestLogUpstreamUsage_NonStreamedFeedsPrefixCacheCounters(t *testing.T) {
	const model = "apc-nonstream-lane"
	body := `{"choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":9028,` +
		`"completion_tokens":40,"prompt_tokens_details":{"cached_tokens":8976}}}`

	obsBefore, cachedBefore := apcTokens(t, model)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(withUsageLogCtx(req.Context(), &usageLogCtx{
		model:         model,
		resolvedModel: model,
		path:          "/v1/chat/completions",
		startedAt:     time.Now(),
	}))
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Request:    req,
	}

	p := &Proxy{}
	require.NoError(t, p.logUpstreamUsage(resp))
	require.Equal(t, "8976", resp.Header.Get(headerCachedTokens))

	obsAfter, cachedAfter := apcTokens(t, model)
	require.Equal(t, float64(9028), obsAfter-obsBefore)
	require.Equal(t, float64(8976), cachedAfter-cachedBefore)
}

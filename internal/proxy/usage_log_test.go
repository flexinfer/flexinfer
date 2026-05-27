package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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

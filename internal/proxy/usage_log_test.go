package proxy

import (
	"testing"

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

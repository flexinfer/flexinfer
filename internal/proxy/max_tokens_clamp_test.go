package proxy

import (
	"encoding/json"
	"testing"

	"github.com/flexinfer/flexinfer/pkg/modelmeta"
)

func TestClampMaxTokensInBody(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		limits       modelmeta.TokenLimits
		reserve      int
		wantChanged  bool
		wantMaxToken int // asserted when wantChanged
	}{
		{
			name:         "overflow equals context window",
			body:         `{"model":"m","max_tokens":8192}`,
			limits:       modelmeta.TokenLimits{ContextWindow: 8192, MaxOutputTokens: 8192, MaxInputTokens: 8192},
			reserve:      512,
			wantChanged:  true,
			wantMaxToken: 8192 - 512,
		},
		{
			name:         "overflow just above headroom",
			body:         `{"model":"m","max_tokens":7900}`,
			limits:       modelmeta.TokenLimits{ContextWindow: 8192, MaxOutputTokens: 8192, MaxInputTokens: 8192},
			reserve:      512,
			wantChanged:  true,
			wantMaxToken: 8192 - 512,
		},
		{
			name:        "within headroom",
			body:        `{"model":"m","max_tokens":1024}`,
			limits:      modelmeta.TokenLimits{ContextWindow: 8192},
			reserve:     512,
			wantChanged: false,
		},
		{
			name:        "no max_tokens field",
			body:        `{"model":"m","temperature":0.7}`,
			limits:      modelmeta.TokenLimits{ContextWindow: 8192},
			reserve:     512,
			wantChanged: false,
		},
		{
			name:        "zero context window is a no-op",
			body:        `{"model":"m","max_tokens":8192}`,
			limits:      modelmeta.TokenLimits{ContextWindow: 0},
			reserve:     512,
			wantChanged: false,
		},
		{
			name:        "malformed body is a no-op",
			body:        `{not-json`,
			limits:      modelmeta.TokenLimits{ContextWindow: 8192},
			reserve:     512,
			wantChanged: false,
		},
		{
			name:        "non-object body is a no-op",
			body:        `[1,2,3]`,
			limits:      modelmeta.TokenLimits{ContextWindow: 8192},
			reserve:     512,
			wantChanged: false,
		},
		{
			name:        "negative max_tokens is ignored",
			body:        `{"model":"m","max_tokens":-1}`,
			limits:      modelmeta.TokenLimits{ContextWindow: 8192},
			reserve:     512,
			wantChanged: false,
		},
		{
			name:         "tiny context window clamps to at least 1",
			body:         `{"model":"m","max_tokens":100}`,
			limits:       modelmeta.TokenLimits{ContextWindow: 2},
			reserve:      512,
			wantChanged:  true,
			wantMaxToken: 1,
		},
		{
			name:         "default reserve (0 input) falls back to 512",
			body:         `{"model":"m","max_tokens":8192}`,
			limits:       modelmeta.TokenLimits{ContextWindow: 8192},
			reserve:      0,
			wantChanged:  true,
			wantMaxToken: 8192 - 512,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, orig, to := clampMaxTokensInBody([]byte(tc.body), tc.limits, tc.reserve)
			changed := orig >= 0
			if changed != tc.wantChanged {
				t.Fatalf("changed: want %v got %v (orig=%d to=%d)", tc.wantChanged, changed, orig, to)
			}
			if !tc.wantChanged {
				return
			}
			if to != tc.wantMaxToken {
				t.Fatalf("clamped max_tokens: want %d got %d", tc.wantMaxToken, to)
			}
			var parsed map[string]any
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("output body is not valid JSON: %v (body=%q)", err, string(out))
			}
			gotMax, ok := toPositiveInt(parsed["max_tokens"])
			if !ok {
				t.Fatalf("output max_tokens missing or not an int: %v", parsed["max_tokens"])
			}
			if gotMax != tc.wantMaxToken {
				t.Fatalf("serialized max_tokens: want %d got %d", tc.wantMaxToken, gotMax)
			}
		})
	}
}

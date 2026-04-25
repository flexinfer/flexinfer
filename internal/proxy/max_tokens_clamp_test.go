package proxy

import (
	"context"
	"encoding/json"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/modelmeta"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestMaybeClampMaxTokensForResponse_UsesProxyConfig(t *testing.T) {
	p := setupTestProxy(t)
	p.maxTokensClampEnabled = true
	p.maxTokensClampPromptReserve = 256

	ctx := context.Background()
	require.NoError(t, p.client.Create(ctx, &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{"maxModelLen":4096}`),
			},
		},
	}))

	out, orig, to := p.maybeClampMaxTokensForResponse(ctx, "test-model", []byte(`{"model":"test-model","max_tokens":4096}`))
	if orig != 4096 {
		t.Fatalf("original max_tokens=%d want 4096", orig)
	}
	if to != 4096-256 {
		t.Fatalf("clamped max_tokens=%d want %d", to, 4096-256)
	}

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(out, &parsed))
	got, ok := toPositiveInt(parsed["max_tokens"])
	if !ok {
		t.Fatalf("max_tokens missing or not integer: %v", parsed["max_tokens"])
	}
	if got != 4096-256 {
		t.Fatalf("serialized max_tokens=%d want %d", got, 4096-256)
	}
}

func TestMaybeClampMaxTokensForResponse_Disabled(t *testing.T) {
	p := setupTestProxy(t)
	p.maxTokensClampEnabled = false

	body := []byte(`{"model":"test-model","max_tokens":4096}`)
	out, orig, to := p.maybeClampMaxTokensForResponse(context.Background(), "test-model", body)
	if orig != -1 || to != -1 {
		t.Fatalf("disabled clamp returned orig=%d to=%d, want no-op", orig, to)
	}
	if string(out) != string(body) {
		t.Fatalf("disabled clamp modified body: %s", string(out))
	}
}

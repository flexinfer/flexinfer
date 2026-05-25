package proxy

import (
	"strings"
	"testing"
)

func TestEstimateTokensFromString_AsciiHeuristic(t *testing.T) {
	tests := []struct {
		name string
		in   string
		// Acceptable inclusive range. The estimator is a heuristic; tests
		// pin the order of magnitude rather than exact values so swapping
		// the constant (e.g., 3.5 → 4) does not cascade into churn.
		min int
		max int
	}{
		{"empty", "", 0, 0},
		{"single-word", "hello", 1, 3},
		{"short-sentence", "hello world how are you today", 7, 12},
		// 280 ASCII chars is roughly a tweet. Real tokenizers give ~70 tokens.
		{"tweet-length", strings.Repeat("hello world ", 24), 70, 90},
		// 4000 chars ≈ ~1k tokens.
		{"4000-chars", strings.Repeat("a", 4000), 1140, 1145},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := estimateTokensFromString(tc.in)
			if got < tc.min || got > tc.max {
				t.Errorf("estimateTokensFromString(%q): got %d, want in [%d,%d]",
					tc.name, got, tc.min, tc.max)
			}
		})
	}
}

func TestEstimateTokensFromString_HighRunesCharge(t *testing.T) {
	// 50 CJK characters should produce more tokens than 50 ASCII chars
	// because CJK is denser.
	cjk := strings.Repeat("你好", 25)   // 50 runes
	ascii := strings.Repeat("ab", 25) // 50 ASCII bytes
	gotCJK := estimateTokensFromString(cjk)
	gotASCII := estimateTokensFromString(ascii)
	if gotCJK <= gotASCII {
		t.Errorf("CJK estimate (%d) should exceed ASCII estimate (%d) for same rune count",
			gotCJK, gotASCII)
	}
}

func TestEstimatePromptTokensFromBody_ChatCompletions(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user",   "content": "Write a haiku about Go."}
		],
		"max_tokens": 64
	}`)
	tokens, ok := estimatePromptTokensFromBody(body)
	if !ok {
		t.Fatalf("expected estimator to succeed; got ok=false")
	}
	// Two short messages + overhead. Real tokenizer ~25 tokens.
	if tokens < 15 || tokens > 50 {
		t.Errorf("expected tokens in [15,50], got %d", tokens)
	}
}

func TestEstimatePromptTokensFromBody_CompletionsString(t *testing.T) {
	body := []byte(`{"model":"x","prompt":"Once upon a time"}`)
	tokens, ok := estimatePromptTokensFromBody(body)
	if !ok {
		t.Fatalf("ok=false on completions body")
	}
	if tokens < 3 || tokens > 12 {
		t.Errorf("expected tokens in [3,12], got %d", tokens)
	}
}

func TestEstimatePromptTokensFromBody_CompletionsBatchPrompt(t *testing.T) {
	body := []byte(`{"model":"x","prompt":["hello world","another short prompt"]}`)
	tokens, ok := estimatePromptTokensFromBody(body)
	if !ok {
		t.Fatalf("ok=false on batched completions")
	}
	if tokens < 5 || tokens > 20 {
		t.Errorf("expected tokens in [5,20], got %d", tokens)
	}
}

func TestEstimatePromptTokensFromBody_VisionContent(t *testing.T) {
	body := []byte(`{
		"model": "x",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "Describe this image"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,XXXX"}}
			]}
		]
	}`)
	tokens, ok := estimatePromptTokensFromBody(body)
	if !ok {
		t.Fatalf("ok=false on vision body")
	}
	// Text part ~5 tokens + image ~256 + per-message overhead.
	if tokens < 200 || tokens > 320 {
		t.Errorf("expected vision tokens in [200,320], got %d", tokens)
	}
}

func TestEstimatePromptTokensFromBody_Unparseable(t *testing.T) {
	for _, body := range [][]byte{
		nil,
		[]byte(""),
		[]byte("not json"),
		[]byte(`{"messages":}`), // malformed
		[]byte(`{"foo":"bar"}`), // no recognised field
	} {
		_, ok := estimatePromptTokensFromBody(body)
		if ok {
			t.Errorf("expected ok=false for body=%q", string(body))
		}
	}
}

func TestExtractMaxTokensFromBody(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		defaultTokens int
		wantTokens    int
		wantOK        bool
	}{
		{"present", `{"max_tokens":128}`, 256, 128, true},
		{"missing", `{"model":"x"}`, 256, 256, false},
		{"zero", `{"max_tokens":0}`, 256, 256, false},
		{"negative", `{"max_tokens":-1}`, 256, 256, false},
		{"float", `{"max_tokens":42.0}`, 256, 42, true},
		{"unparseable", `not json`, 256, 256, false},
		{"empty", ``, 256, 256, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractMaxTokensFromBody([]byte(tc.body), tc.defaultTokens)
			if got != tc.wantTokens || ok != tc.wantOK {
				t.Errorf("extractMaxTokensFromBody(%q): got (%d,%v), want (%d,%v)",
					tc.body, got, ok, tc.wantTokens, tc.wantOK)
			}
		})
	}
}

// Representative corpus for the hot-path benchmark.
var benchBodies = map[string][]byte{
	"short_chat":  []byte(`{"model":"x","messages":[{"role":"user","content":"hi"}],"max_tokens":64}`),
	"medium_chat": buildChatBody("user", strings.Repeat("How does this Go program compile under -gcflags=-l? ", 30)),
	"long_chat":   buildChatBody("user", strings.Repeat("Here is a snippet to consider. ", 400)),
	"very_long":   buildChatBody("user", strings.Repeat("hello world ", 4000)),
	"multi_turn":  buildMultiTurnChatBody(8, strings.Repeat("Let us discuss algorithms in depth. ", 20)),
	"cjk_chat":    buildChatBody("user", strings.Repeat("你好世界。这是一段中文测试文本。", 40)),
}

func BenchmarkEstimatePromptTokensFromBody(b *testing.B) {
	for name, body := range benchBodies {
		body := body
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				_, _ = estimatePromptTokensFromBody(body)
			}
		})
	}
}

func buildChatBody(role, content string) []byte {
	body := `{"model":"x","messages":[{"role":"` + role + `","content":` + jsonString(content) + `}],"max_tokens":64}`
	return []byte(body)
}

func buildMultiTurnChatBody(turns int, content string) []byte {
	var sb strings.Builder
	sb.WriteString(`{"model":"x","messages":[`)
	for i := 0; i < turns; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sb.WriteString(`{"role":"` + role + `","content":`)
		sb.WriteString(jsonString(content))
		sb.WriteString(`}`)
	}
	sb.WriteString(`],"max_tokens":64}`)
	return []byte(sb.String())
}

func jsonString(s string) string {
	// Avoid pulling in encoding/json just for the test-corpus builder; this
	// covers the small set of characters the fixtures produce.
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

package routing

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		body       string
		wantEmpty  bool
		wantPrefix string
	}{
		{
			name:      "X-Session-ID header",
			headers:   map[string]string{"X-Session-ID": "sess-123"},
			body:      `{}`,
			wantEmpty: false,
		},
		{
			name:      "X-Conversation-ID header",
			headers:   map[string]string{"X-Conversation-ID": "conv-456"},
			body:      `{}`,
			wantEmpty: false,
		},
		{
			name:      "session_id in body",
			headers:   map[string]string{},
			body:      `{"session_id": "body-sess-789"}`,
			wantEmpty: false,
		},
		{
			name:       "messages array creates hash",
			headers:    map[string]string{},
			body:       `{"messages": [{"role": "user", "content": "Hello"}]}`,
			wantEmpty:  false,
			wantPrefix: "msg:",
		},
		{
			name:      "empty body",
			headers:   map[string]string{},
			body:      ``,
			wantEmpty: true,
		},
		{
			name:      "invalid JSON",
			headers:   map[string]string{},
			body:      `not json`,
			wantEmpty: true,
		},
		{
			name:      "no session info",
			headers:   map[string]string{},
			body:      `{"model": "test"}`,
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			result := ExtractSessionID(req, []byte(tt.body))

			if tt.wantEmpty && result != "" {
				t.Errorf("expected empty session ID, got %q", result)
			}
			if !tt.wantEmpty && result == "" {
				t.Error("expected non-empty session ID")
			}
			if tt.wantPrefix != "" && len(result) > 0 {
				if len(result) < len(tt.wantPrefix) || result[:len(tt.wantPrefix)] != tt.wantPrefix {
					t.Errorf("expected prefix %q, got %q", tt.wantPrefix, result)
				}
			}
		})
	}
}

func TestExtractSessionID_Consistency(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	body := []byte(`{"messages": [{"role": "user", "content": "Hello, how are you?"}]}`)

	// Same body should produce same session ID
	id1 := ExtractSessionID(req, body)
	id2 := ExtractSessionID(req, body)

	if id1 != id2 {
		t.Errorf("same request produced different session IDs: %s vs %s", id1, id2)
	}

	// Different body should produce different session ID
	body2 := []byte(`{"messages": [{"role": "user", "content": "Different message"}]}`)
	id3 := ExtractSessionID(req, body2)

	if id1 == id3 {
		t.Error("different requests produced same session ID")
	}
}

func TestExtractPrefix(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantEmpty  bool
		wantPrefix string
	}{
		{
			name:       "system prompt prefix",
			body:       `{"messages": [{"role": "system", "content": "You are a helpful assistant."}]}`,
			wantEmpty:  false,
			wantPrefix: "sys:",
		},
		{
			name:       "explicit prefix field",
			body:       `{"prefix": "Custom prefix text"}`,
			wantEmpty:  false,
			wantPrefix: "pfx:",
		},
		{
			name:      "no system message",
			body:      `{"messages": [{"role": "user", "content": "Hello"}]}`,
			wantEmpty: true,
		},
		{
			name:      "empty body",
			body:      ``,
			wantEmpty: true,
		},
		{
			name:      "empty system content",
			body:      `{"messages": [{"role": "system", "content": ""}]}`,
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractPrefix([]byte(tt.body))

			if tt.wantEmpty && result != "" {
				t.Errorf("expected empty prefix, got %q", result)
			}
			if !tt.wantEmpty && result == "" {
				t.Error("expected non-empty prefix")
			}
			if tt.wantPrefix != "" && len(result) > 0 {
				if len(result) < len(tt.wantPrefix) || result[:len(tt.wantPrefix)] != tt.wantPrefix {
					t.Errorf("expected prefix %q, got %q", tt.wantPrefix, result)
				}
			}
		})
	}
}

func TestExtractPrefix_Consistency(t *testing.T) {
	body := []byte(`{"messages": [{"role": "system", "content": "You are an AI assistant specialized in coding."}]}`)

	// Same body should produce same prefix
	p1 := ExtractPrefix(body)
	p2 := ExtractPrefix(body)

	if p1 != p2 {
		t.Errorf("same body produced different prefixes: %s vs %s", p1, p2)
	}

	// Different system prompt should produce different prefix
	body2 := []byte(`{"messages": [{"role": "system", "content": "You are a creative writing assistant."}]}`)
	p3 := ExtractPrefix(body2)

	if p1 == p3 {
		t.Error("different system prompts produced same prefix")
	}
}

func TestExtractPrefixKey_PrecedenceAndSafety(t *testing.T) {
	t.Run("header overrides body key and prefix", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Flexinfer-Cache-Key", "tenant-a/doc-42")
		body := []byte(`{"cache_key":"other-key","prefix":"legacy-prefix","messages":[{"role":"system","content":"System Prompt"}]}`)

		key, source := ExtractPrefixKey(req, body)
		if key == "" {
			t.Fatal("expected non-empty key")
		}
		if source != KeySourceExplicitHeader {
			t.Fatalf("source=%s want %s", source, KeySourceExplicitHeader)
		}
	})

	t.Run("invalid explicit header falls back to body key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set("X-Flexinfer-Cache-Key", "bad key with spaces")
		body := []byte(`{"cacheKey":"tenant-b/doc-9","messages":[{"role":"system","content":"System Prompt"}]}`)

		key, source := ExtractPrefixKey(req, body)
		if key == "" {
			t.Fatal("expected non-empty key")
		}
		if source != KeySourceExplicitBody {
			t.Fatalf("source=%s want %s", source, KeySourceExplicitBody)
		}
	})

	t.Run("canonical key includes normalized system+document context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		bodyA := []byte(`{"messages":[{"role":"system","content":"  You   Are Helpful  "},{"role":"user","content":"hi"}],"document_context":" Important   Facts  "}`)
		bodyB := []byte(`{"messages":[{"role":"system","content":"you are helpful"},{"role":"user","content":"hi"}],"documentContext":"important facts"}`)

		keyA, sourceA := ExtractPrefixKey(req, bodyA)
		keyB, sourceB := ExtractPrefixKey(req, bodyB)

		if keyA == "" || keyB == "" {
			t.Fatal("expected non-empty canonical keys")
		}
		if keyA != keyB {
			t.Fatalf("expected same canonical key, got %q vs %q", keyA, keyB)
		}
		if sourceA != KeySourceCanonical || sourceB != KeySourceCanonical {
			t.Fatalf("expected canonical source, got %s and %s", sourceA, sourceB)
		}
	})
}

func TestRouter_Route(t *testing.T) {
	router := NewRouter()

	// Add endpoints for a model
	router.UpdateEndpoints("test-model", []string{
		"10.0.0.1:8000",
		"10.0.0.2:8000",
		"10.0.0.3:8000",
	})

	// Test session affinity routing
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Session-ID", "session-123")

	target1 := router.Route("test-model", StrategySessionAffinity, req, nil)
	target2 := router.Route("test-model", StrategySessionAffinity, req, nil)

	if target1 == "" {
		t.Error("expected target, got empty string")
	}
	if target1 != target2 {
		t.Errorf("session affinity broken: %s vs %s", target1, target2)
	}

	// Test prefix routing
	body := []byte(`{"messages": [{"role": "system", "content": "You are helpful."}]}`)
	target3 := router.Route("test-model", StrategyPrefix, req, body)
	target4 := router.Route("test-model", StrategyPrefix, req, body)

	if target3 == "" {
		t.Error("expected target for prefix routing, got empty string")
	}
	if target3 != target4 {
		t.Errorf("prefix routing inconsistent: %s vs %s", target3, target4)
	}

	// Test default strategy returns empty (should fall back to Service DNS)
	target5 := router.Route("test-model", StrategyDefault, req, body)
	if target5 != "" {
		t.Errorf("default strategy should return empty, got %s", target5)
	}
}

func TestRouter_EmptyEndpoints(t *testing.T) {
	router := NewRouter()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Session-ID", "session-123")

	// With no endpoints, should return empty string
	target := router.Route("unknown-model", StrategySessionAffinity, req, nil)
	if target != "" {
		t.Errorf("expected empty target for model with no endpoints, got %s", target)
	}
}

func TestRouter_UpdateEndpoints(t *testing.T) {
	router := NewRouter()

	// Initial endpoints
	router.UpdateEndpoints("test-model", []string{"10.0.0.1:8000", "10.0.0.2:8000"})

	ring := router.GetRing("test-model")
	if ring.Size() != 2 {
		t.Errorf("expected 2 endpoints, got %d", ring.Size())
	}

	// Update endpoints
	router.UpdateEndpoints("test-model", []string{"10.0.0.3:8000", "10.0.0.4:8000", "10.0.0.5:8000"})

	if ring.Size() != 3 {
		t.Errorf("expected 3 endpoints after update, got %d", ring.Size())
	}
}

func TestRouter_LeastLoaded(t *testing.T) {
	router := NewRouter()

	// Add endpoints
	router.UpdateEndpoints("test-model", []string{
		"10.0.0.1:8000",
		"10.0.0.2:8000",
		"10.0.0.3:8000",
	})

	// Create a load function that returns different loads for each pod
	loadFn := func(podAddr string) int64 {
		switch podAddr {
		case "10.0.0.1:8000":
			return 10 // High load
		case "10.0.0.2:8000":
			return 2 // Low load - should be selected
		case "10.0.0.3:8000":
			return 5 // Medium load
		default:
			return 0
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	// Route with least-loaded strategy
	target := router.RouteWithLoad("test-model", StrategyLeastLoaded, req, nil, loadFn)

	if target != "10.0.0.2:8000" {
		t.Errorf("expected least loaded pod 10.0.0.2:8000, got %s", target)
	}
}

func TestRouter_LeastLoaded_NoLoadFn(t *testing.T) {
	router := NewRouter()

	// Add endpoints
	router.UpdateEndpoints("test-model", []string{
		"10.0.0.1:8000",
		"10.0.0.2:8000",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	// Route with least-loaded but no load function - should return a valid node
	target := router.RouteWithLoad("test-model", StrategyLeastLoaded, req, nil, nil)

	if target == "" {
		t.Error("expected a target even without load function")
	}
}

func TestRouter_LeastLoaded_EqualLoad(t *testing.T) {
	router := NewRouter()

	// Add endpoints
	router.UpdateEndpoints("test-model", []string{
		"10.0.0.1:8000",
		"10.0.0.2:8000",
		"10.0.0.3:8000",
	})

	// All pods have equal load
	loadFn := func(podAddr string) int64 {
		return 5
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	// Should return one of the pods (first one found with min load)
	target := router.RouteWithLoad("test-model", StrategyLeastLoaded, req, nil, loadFn)

	if target == "" {
		t.Error("expected a target with equal load")
	}
}

func TestRouter_PrefixFallbackToSession(t *testing.T) {
	router := NewRouter()
	router.UpdateEndpoints("test-model", []string{
		"10.0.0.1:8000",
		"10.0.0.2:8000",
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("X-Session-ID", "session-abc")
	body := []byte(`{"messages":[{"role":"user","content":"no system prompt"}]}`)

	decision := router.RouteWithDecision("test-model", StrategyPrefix, req, body, nil)
	if decision.Target == "" {
		t.Fatal("expected non-empty target via session fallback")
	}
	if decision.KeySource != KeySourceSessionFallback {
		t.Fatalf("key source=%s want %s", decision.KeySource, KeySourceSessionFallback)
	}
}

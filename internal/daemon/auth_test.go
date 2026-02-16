package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTokenAuthMiddleware_Integration tests the auth middleware behavior
// using a standalone token-based handler (no Daemon dependency).
func TestTokenAuthMiddleware_Integration(t *testing.T) {
	t.Parallel()

	const testToken = "test-secret-token"

	// Build a simple token auth middleware directly
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("Authorization")
			if token != "Bearer "+testToken {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	wrapped := middleware(handler)

	// Without token: 401
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rr.Code)
	}

	// With wrong token: 401
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	rr2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", rr2.Code)
	}

	// With correct token: 200
	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req3.Header.Set("Authorization", "Bearer "+testToken)
	rr3 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr3, req3)

	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d", rr3.Code)
	}
}

// TestAuthContextFromRequest_Nil tests that nil context returns nil.
func TestAuthContextFromRequest_Nil(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := AuthContextFromRequest(req)
	if ctx != nil {
		t.Fatal("expected nil auth context for request without auth")
	}
}

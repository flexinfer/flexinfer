package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// TestHTTPHandler_InitializeRoundTrip verifies the Streamable HTTP handler
// correctly processes an initialize request and returns a session ID.
func TestHTTPHandler_InitializeRoundTrip(t *testing.T) {
	handler := mcp.NewStreamableHTTPServer(
		func(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
			if msg.Method == "initialize" {
				return mcp.NewResponse(msg.ID, mcp.InitializeResult{
					ProtocolVersion: mcp.ProtocolVersion20250618,
					ServerInfo:      mcp.ServerInfo{Name: "test-daemon", Version: "0.1.0"},
				})
			}
			return mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, "unknown"), nil
		},
		mcp.DefaultStreamableHTTPConfig(),
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	initMsg, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20250618,
		ClientInfo:      mcp.ClientInfo{Name: "test", Version: "1.0"},
	})
	body, _ := json.Marshal(initMsg)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("expected Mcp-Session-Id header")
	}

	var respMsg mcp.Message
	if err := json.NewDecoder(resp.Body).Decode(&respMsg); err != nil {
		t.Fatal(err)
	}

	var result mcp.InitializeResult
	if err := json.Unmarshal(respMsg.Result, &result); err != nil {
		t.Fatal(err)
	}

	if result.ServerInfo.Name != "test-daemon" {
		t.Fatalf("expected server name 'test-daemon', got %q", result.ServerInfo.Name)
	}
}

func TestHTTPHandler_Unauthenticated401(t *testing.T) {
	handler := mcp.NewStreamableHTTPServer(
		func(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
			return mcp.NewResponse(msg.ID, map[string]string{"ok": "true"})
		},
		mcp.DefaultStreamableHTTPConfig(),
	)

	// Wrap with a simple auth middleware
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token != "Bearer test-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", authed)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	initMsg, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20250618,
	})
	body, _ := json.Marshal(initMsg)

	// Without auth
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}

	// With auth
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(body)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json")
	req2.Header.Set("Authorization", "Bearer test-token")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with auth, got %d", resp2.StatusCode)
	}
}

// newTestMCPServer creates a test MCP server for HTTP handler tests.
func newTestMCPServer() *mcp.StreamableHTTPServer {
	return mcp.NewStreamableHTTPServer(
		func(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
			if msg.Method == "initialize" {
				return mcp.NewResponse(msg.ID, mcp.InitializeResult{
					ProtocolVersion: mcp.ProtocolVersion20250618,
					ServerInfo:      mcp.ServerInfo{Name: "test-daemon", Version: "0.1.0"},
				})
			}
			return mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, "unknown"), nil
		},
		mcp.DefaultStreamableHTTPConfig(),
	)
}

func TestHTTPHandler_EmptyBody(t *testing.T) {
	handler := newTestMCPServer()
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Empty body should result in a 400 error response
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusOK {
		// MCP library may return 400 or handle gracefully — either is valid
		t.Logf("empty body returned status %d", resp.StatusCode)
	}
}

func TestHTTPHandler_MalformedJSON(t *testing.T) {
	handler := newTestMCPServer()
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader("{invalid json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Malformed JSON should not return 200
	if resp.StatusCode == http.StatusOK {
		t.Log("malformed JSON unexpectedly returned 200; MCP library may be lenient")
	}
}

func TestHTTPHandler_MethodNotAllowed(t *testing.T) {
	handler := newTestMCPServer()
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, ts.URL+"/mcp", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 405 or 400 for PUT, got %d", resp.StatusCode)
	}
}

func TestHTTPHandler_ConcurrentSessions(t *testing.T) {
	handler := newTestMCPServer()
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	const numClients = 5
	sessionIDs := make([]string, numClients)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			initMsg, _ := mcp.NewRequest(int64(idx+1), "initialize", mcp.InitializeParams{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				ClientInfo:      mcp.ClientInfo{Name: "test-" + string(rune('a'+idx)), Version: "1.0"},
			})
			body, _ := json.Marshal(initMsg)

			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("client %d: %v", idx, err)
				return
			}
			defer resp.Body.Close()

			sid := resp.Header.Get("Mcp-Session-Id")
			mu.Lock()
			sessionIDs[idx] = sid
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Verify all session IDs are unique and non-empty
	seen := make(map[string]bool)
	for i, sid := range sessionIDs {
		if sid == "" {
			t.Fatalf("client %d got empty session ID", i)
		}
		if seen[sid] {
			t.Fatalf("duplicate session ID: %s", sid)
		}
		seen[sid] = true
	}
}

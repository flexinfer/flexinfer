package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

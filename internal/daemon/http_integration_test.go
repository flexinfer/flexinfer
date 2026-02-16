package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// TestHTTPIntegration_EndToEnd tests the full Streamable HTTP lifecycle:
// initialize, tool call, notification, session delete, and expired session.
func TestHTTPIntegration_EndToEnd(t *testing.T) {
	handler := mcp.NewStreamableHTTPServer(
		func(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
			switch msg.Method {
			case "initialize":
				return mcp.NewResponse(msg.ID, mcp.InitializeResult{
					ProtocolVersion: mcp.ProtocolVersion20250618,
					ServerInfo:      mcp.ServerInfo{Name: "integration-test", Version: "0.1.0"},
					Instructions:    "Integration test daemon",
				})
			case "tools/list":
				return mcp.NewResponse(msg.ID, map[string]any{
					"tools": []mcp.Tool{
						{Name: "echo", Description: "Echo back input"},
					},
				})
			case "tools/call":
				return mcp.NewResponse(msg.ID, mcp.CallToolResult{
					Content: []mcp.Content{{Type: "text", Text: "hello from integration test"}},
				})
			default:
				return mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, "unknown method: "+msg.Method), nil
			}
		},
		mcp.DefaultStreamableHTTPConfig(),
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	post := func(body *mcp.Message, sessionID string) *http.Response {
		t.Helper()
		data, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(data)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Step 1: Initialize
	initMsg, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20250618,
		Capabilities:    mcp.Capabilities{},
		ClientInfo:      mcp.ClientInfo{Name: "integration-test", Version: "1.0"},
	})

	resp := post(initMsg, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("step 1: expected 200, got %d", resp.StatusCode)
	}

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatal("step 1: expected Mcp-Session-Id header")
	}

	var initResp mcp.Message
	json.NewDecoder(resp.Body).Decode(&initResp)
	resp.Body.Close()

	var initResult mcp.InitializeResult
	json.Unmarshal(initResp.Result, &initResult)
	if initResult.ServerInfo.Name != "integration-test" {
		t.Fatalf("step 1: expected server name 'integration-test', got %q", initResult.ServerInfo.Name)
	}

	// Step 2: Send initialized notification
	notif := &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	resp2 := post(notif, sessionID)
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("step 2: expected 202, got %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	// Step 3: tools/call with session
	callMsg, _ := mcp.NewRequest(2, "tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "hello"},
	})

	resp3 := post(callMsg, sessionID)
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("step 3: expected 200, got %d", resp3.StatusCode)
	}

	var callResp mcp.Message
	json.NewDecoder(resp3.Body).Decode(&callResp)
	resp3.Body.Close()

	if callResp.Error != nil {
		t.Fatalf("step 3: unexpected error: %s", callResp.Error.Message)
	}

	var callResult mcp.CallToolResult
	json.Unmarshal(callResp.Result, &callResult)
	if len(callResult.Content) == 0 || callResult.Content[0].Text != "hello from integration test" {
		t.Fatalf("step 3: unexpected call result: %+v", callResult)
	}

	// Step 4: DELETE session
	delReq, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete, ts.URL+"/mcp", nil)
	delReq.Header.Set("Mcp-Session-Id", sessionID)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	delResp.Body.Close()

	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("step 4: expected 200 on DELETE, got %d", delResp.StatusCode)
	}

	// Step 5: Request with deleted session -> 404
	resp5 := post(callMsg, sessionID)
	if resp5.StatusCode != http.StatusNotFound {
		t.Fatalf("step 5: expected 404, got %d", resp5.StatusCode)
	}
	resp5.Body.Close()

	// Step 6: Request without auth (tested separately in TestHTTPHandler_Unauthenticated401)
}

// TestHTTPIntegration_ClientTransport tests the full round-trip using StreamableHTTPTransport.
func TestHTTPIntegration_ClientTransport(t *testing.T) {
	handler := mcp.NewStreamableHTTPServer(
		func(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
			switch msg.Method {
			case "initialize":
				return mcp.NewResponse(msg.ID, mcp.InitializeResult{
					ProtocolVersion: mcp.ProtocolVersion20250618,
					ServerInfo:      mcp.ServerInfo{Name: "transport-test", Version: "0.1.0"},
				})
			case "loom/tools":
				return mcp.NewResponse(msg.ID, map[string]any{
					"tools": []mcp.Tool{
						{Name: "test__echo", Description: "Echo tool"},
					},
				})
			default:
				return mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, msg.Method), nil
			}
		},
		mcp.DefaultStreamableHTTPConfig(),
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Create client transport (same path as remote proxy)
	transport := mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPClientConfig{
		Endpoint: ts.URL + "/mcp",
		Headers:  map[string]string{"X-Test": "integration"},
	})
	defer transport.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Initialize
	initMsg, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20250618,
		ClientInfo:      mcp.ClientInfo{Name: "transport-test", Version: "1.0"},
	})

	if err := transport.Send(ctx, initMsg); err != nil {
		t.Fatal(err)
	}
	initResp, err := transport.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initResp.Error != nil {
		t.Fatalf("init error: %s", initResp.Error.Message)
	}

	// Send initialized notification
	transport.Send(ctx, &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"})

	// Call loom/tools
	toolsMsg, _ := mcp.NewRequest(2, "loom/tools", nil)
	if err := transport.Send(ctx, toolsMsg); err != nil {
		t.Fatal(err)
	}
	toolsResp, err := transport.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if toolsResp.Error != nil {
		t.Fatalf("tools error: %s", toolsResp.Error.Message)
	}

	// Verify tools response
	var toolsResult struct {
		Tools []mcp.Tool `json:"tools"`
	}
	json.Unmarshal(toolsResp.Result, &toolsResult)

	if len(toolsResult.Tools) != 1 || toolsResult.Tools[0].Name != "test__echo" {
		t.Fatalf("unexpected tools: %+v", toolsResult)
	}
}

// TestHTTPIntegration_BearerTokenAuth tests that bearer token auth middleware works.
func TestHTTPIntegration_BearerTokenAuth(t *testing.T) {
	handler := mcp.NewStreamableHTTPServer(
		func(_ context.Context, msg *mcp.Message) (*mcp.Message, error) {
			return mcp.NewResponse(msg.ID, mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				ServerInfo:      mcp.ServerInfo{Name: "auth-test", Version: "0.1.0"},
			})
		},
		mcp.DefaultStreamableHTTPConfig(),
	)

	validToken := "loom_test_token_abc123"

	// Auth middleware
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+validToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	})

	mux := http.NewServeMux()
	mux.Handle("/mcp", authed)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Without token -> should fail
	transportNoAuth := mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPClientConfig{
		Endpoint: ts.URL + "/mcp",
	})

	initMsg, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion20250618,
	})

	err := transportNoAuth.Send(ctx, initMsg)
	if err == nil {
		t.Fatal("expected error without auth token")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got: %v", err)
	}
	transportNoAuth.Close()

	// With valid token -> should succeed
	transportAuth := mcp.NewStreamableHTTPTransport(mcp.StreamableHTTPClientConfig{
		Endpoint: ts.URL + "/mcp",
		Headers:  map[string]string{"Authorization": "Bearer " + validToken},
	})
	defer transportAuth.Close()

	if err := transportAuth.Send(ctx, initMsg); err != nil {
		t.Fatalf("expected success with auth: %v", err)
	}

	resp, err := transportAuth.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

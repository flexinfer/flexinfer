package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeHUD spins up an httptest.NewServer wired to a small mux that
// behaves like the real /api/mobile/v1 surface for the three relay
// paths. Each test customizes it before any tool call. The server is
// scoped to the test via t.Cleanup.
func fakeHUD(t *testing.T) (*httptest.Server, *fakeHUDState) {
	t.Helper()
	state := &fakeHUDState{
		dashboard:  `{"daemon_running":true,"active_sessions":3,"server_count":42}`,
		presence:   `{"agents":[{"agent_id":"claude-1","status":"active"}]}`,
		sessions:   `{"sessions":[]}`,
		expectAuth: "Bearer test-token",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mobile/v1/dashboard", func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastAuth = r.Header.Get("Authorization")
		if state.failNext {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, state.dashboard)
	})
	mux.HandleFunc("/api/mobile/v1/presence", func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, state.presence)
	})
	mux.HandleFunc("/api/mobile/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, state.sessions)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type fakeHUDState struct {
	dashboard  string
	presence   string
	sessions   string
	expectAuth string

	lastPath string
	lastAuth string
	failNext bool
}

// callTool returns the parsed `result` map from a tools/call response.
// Used by the relay tests below to assert on content shape without
// re-implementing the JSON-RPC scaffolding.
func callTool(t *testing.T, srv *server, toolName string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + toolName + `","arguments":{}}}`
	in := bytes.NewBufferString(body + "\n")
	out := &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, in, out); err != nil && err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal response: %v; raw=%q", err, out.String())
	}
	if errMap, _ := resp["error"].(map[string]any); errMap != nil {
		t.Fatalf("tool returned RPC error: %+v", errMap)
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("missing result: %+v", resp)
	}
	return result
}

// TestToolsList_AdvertisesRelayTools confirms the new relay tools
// appear in tools/list so the widget can discover them.
func TestToolsList_AdvertisesRelayTools(t *testing.T) {
	srv := newServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"
	out := &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, bytes.NewBufferString(body), out); err != nil && err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, tv := range tools {
		tm, _ := tv.(map[string]any)
		if n, _ := tm["name"].(string); n != "" {
			names[n] = true
		}
	}
	for _, want := range []string{toolShow, toolDashboard, toolPresence, toolSessions} {
		if !names[want] {
			t.Errorf("tools/list missing %q (got %v)", want, names)
		}
	}
}

// TestRelay_Dashboard_HappyPath asserts the dashboard relay tool
// returns the HUD body verbatim in content[0].text with the JSON mime
// type, and that the HUD GET was made with the Bearer token from the
// hudClient.
func TestRelay_Dashboard_HappyPath(t *testing.T) {
	hud, state := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callTool(t, srv, toolDashboard)

	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content count = %d, want 1", len(content))
	}
	c0, _ := content[0].(map[string]any)
	if c0["mimeType"] != "application/json" {
		t.Errorf("content[0].mimeType = %v, want application/json", c0["mimeType"])
	}
	text, _ := c0["text"].(string)
	if text != state.dashboard {
		t.Errorf("relay body mismatch:\n got  %q\n want %q", text, state.dashboard)
	}
	if state.lastPath != "/api/mobile/v1/dashboard" {
		t.Errorf("HUD got path %q, want /api/mobile/v1/dashboard", state.lastPath)
	}
	if state.lastAuth != state.expectAuth {
		t.Errorf("HUD got auth %q, want %q", state.lastAuth, state.expectAuth)
	}
}

// TestRelay_NoTokenOmitsAuthHeader confirms the Bearer header is not
// sent when LOOM_HUD_TOKEN is empty — useful when the HUD is local +
// trust-by-loopback. Tests the hudClient construction path.
func TestRelay_NoTokenOmitsAuthHeader(t *testing.T) {
	hud, state := fakeHUD(t)
	state.expectAuth = "" // we expect no Authorization header
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "", client: hud.Client()},
	)
	_ = callTool(t, srv, toolDashboard)
	if state.lastAuth != "" {
		t.Errorf("Authorization header sent unexpectedly: %q", state.lastAuth)
	}
}

// TestRelay_HUDFailureSurfacesAsIsError ensures HUD 4xx/5xx come back
// as a soft tool error (isError=true) rather than a JSON-RPC error
// envelope. The widget needs the error text in content[] so it can
// render a recoverable banner; an RPC error would surface as a hard
// failure in the host UI.
func TestRelay_HUDFailureSurfacesAsIsError(t *testing.T) {
	hud, state := fakeHUD(t)
	state.failNext = true
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callTool(t, srv, toolDashboard)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true on HUD failure, got result %+v", result)
	}
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	text, _ := c0["text"].(string)
	if !strings.Contains(text, "HUD returned 403") {
		t.Errorf("error text missing HUD status: %q", text)
	}
}

// TestRelay_Presence_HappyPath covers the second relay path so the
// path allowlist is exercised end-to-end (different path, same shape).
func TestRelay_Presence_HappyPath(t *testing.T) {
	hud, state := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callTool(t, srv, toolPresence)
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if c0["text"] != state.presence {
		t.Errorf("relay body mismatch: got %q want %q", c0["text"], state.presence)
	}
	if state.lastPath != "/api/mobile/v1/presence" {
		t.Errorf("wrong path: %q", state.lastPath)
	}
}

// TestHUDClient_AllowlistRejectsOtherPaths is an internal-defense test:
// even if a future tool handler passes an unauthorized path through to
// hudClient.get directly, the allowlist refuses. Tests the security
// boundary independent of the routing layer above.
func TestHUDClient_AllowlistRejectsOtherPaths(t *testing.T) {
	hud, _ := fakeHUD(t)
	hc := &hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()}
	_, err := hc.get(context.Background(), "/api/mobile/v1/secrets", relayPaths)
	if err == nil {
		t.Fatal("expected allowlist rejection, got nil")
	}
	if !strings.Contains(err.Error(), "not in allowlist") {
		t.Errorf("unexpected error: %v", err)
	}
}

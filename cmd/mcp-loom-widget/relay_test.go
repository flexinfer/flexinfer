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
		stream:     `{"entries":[{"id":"e1","entry_type":"decision","agent_id":"claude-1","title":"chose X","timestamp":"2026-05-16T20:00:00Z"}]}`,
		handoffs:   `{"handoffs":[{"id":"h1","from_agent":"claude-1","to_agent":"codex-2","status":"pending","summary":"finish slice 1b-δ","created_at":"2026-05-16T20:00:00Z"}],"total":1}`,
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
	mux.HandleFunc("/api/mobile/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, state.stream)
	})
	mux.HandleFunc("/api/mobile/v1/handoffs", func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastMethod = r.Method
		state.lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, state.handoffs)
	})
	// Mobile handoff accept/reject POST endpoints. Pattern matches
	// the actual mobile route registration ({handoff_id} segment).
	mux.HandleFunc("/api/mobile/v1/handoffs/", func(w http.ResponseWriter, r *http.Request) {
		state.lastPath = r.URL.Path
		state.lastMethod = r.Method
		state.lastAuth = r.Header.Get("Authorization")
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			state.lastBody = string(data)
		}
		if state.failNext {
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type fakeHUDState struct {
	dashboard  string
	presence   string
	sessions   string
	stream     string
	handoffs   string
	expectAuth string

	lastPath   string
	lastMethod string
	lastAuth   string
	lastBody   string
	failNext   bool
}

// callTool returns the parsed `result` map from a tools/call response.
// Used by the relay tests below to assert on content shape without
// re-implementing the JSON-RPC scaffolding.
func callTool(t *testing.T, srv *server, toolName string) map[string]any {
	return callToolWithArgs(t, srv, toolName, nil)
}

// callToolWithArgs is the variant the mutating relay tests use:
// arguments are forwarded to the tool handler so it can validate
// required fields + build the POST body.
func callToolWithArgs(t *testing.T, srv *server, toolName string, args map[string]any) map[string]any {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + toolName + `","arguments":` + string(argsJSON) + `}}`
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
	for _, want := range []string{toolShow, toolDashboard, toolPresence, toolSessions, toolStream, toolHandoffs, toolHandoffAccept, toolHandoffReject} {
		if !names[want] {
			t.Errorf("tools/list missing %q (got %v)", want, names)
		}
	}
}

// TestRelay_HandoffAccept_PostsBodyAndPath covers the happy-path
// mutating relay: handoff_id substitutes into the URL template,
// session_id flows through as the request body, and the resolved
// path + Bearer auth + POST verb all match expectations.
func TestRelay_HandoffAccept_PostsBodyAndPath(t *testing.T) {
	hud, state := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callToolWithArgs(t, srv, toolHandoffAccept, map[string]any{
		"handoff_id":     "h-abc-123",
		"session_id":     "sess-xyz",
		"import_entries": true,
	})

	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("unexpected isError: %+v", result)
	}
	if state.lastMethod != "POST" {
		t.Errorf("HUD got method %q, want POST", state.lastMethod)
	}
	if state.lastPath != "/api/mobile/v1/handoffs/h-abc-123/accept" {
		t.Errorf("HUD got path %q, want /api/mobile/v1/handoffs/h-abc-123/accept", state.lastPath)
	}
	if state.lastAuth != "Bearer test-token" {
		t.Errorf("HUD got auth %q, want Bearer test-token", state.lastAuth)
	}
	if !strings.Contains(state.lastBody, `"session_id":"sess-xyz"`) {
		t.Errorf("POST body missing session_id: %q", state.lastBody)
	}
	if !strings.Contains(state.lastBody, `"import_entries":true`) {
		t.Errorf("POST body missing import_entries: %q", state.lastBody)
	}
	// handoff_id was the URL placeholder, NOT a body field.
	if strings.Contains(state.lastBody, "handoff_id") {
		t.Errorf("POST body should not contain handoff_id (it's in URL): %q", state.lastBody)
	}
}

// TestRelay_HandoffReject_Minimal verifies the reject tool also
// works with just handoff_id (reason optional) and substitutes
// correctly into the reject URL template.
func TestRelay_HandoffReject_Minimal(t *testing.T) {
	hud, state := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callToolWithArgs(t, srv, toolHandoffReject, map[string]any{
		"handoff_id": "h-def-456",
		"reason":     "not relevant to current scope",
	})
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("unexpected isError: %+v", result)
	}
	if state.lastPath != "/api/mobile/v1/handoffs/h-def-456/reject" {
		t.Errorf("HUD got path %q, want .../h-def-456/reject", state.lastPath)
	}
	if !strings.Contains(state.lastBody, `"reason":"not relevant to current scope"`) {
		t.Errorf("POST body missing reason: %q", state.lastBody)
	}
}

// TestRelay_HandoffAccept_RejectsUnsafeID is the security regression:
// a handoff_id with traversal characters must be refused by the path
// allowlist + isSafeID validator before any HTTP request is made.
func TestRelay_HandoffAccept_RejectsUnsafeID(t *testing.T) {
	hud, _ := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callToolWithArgs(t, srv, toolHandoffAccept, map[string]any{
		"handoff_id": "../secrets/leak",
		"session_id": "sess-1",
	})
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for unsafe handoff_id, got %+v", result)
	}
}

// TestRelay_HandoffAccept_RequiresSessionOrTarget enforces the
// at-least-one constraint: passing handoff_id without session_id or
// target_agent_id must fail at the tool layer (before any HTTP).
func TestRelay_HandoffAccept_RequiresSessionOrTarget(t *testing.T) {
	hud, _ := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"loom_fleet_handoff_accept","arguments":{"handoff_id":"h1"}}}`
	in := bytes.NewBufferString(body + "\n")
	out := &bytes.Buffer{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Serve(ctx, in, out); err != nil && err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected RPC error for missing session_id+target_agent_id, got %+v", resp)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "session_id") {
		t.Errorf("error message should mention session_id: %q", msg)
	}
}

// TestHUDClient_PostRejectsUnknownTemplate exercises the internal
// boundary: even if a caller passes an arbitrary template to .post,
// the allowlist refuses. Defense-in-depth.
func TestHUDClient_PostRejectsUnknownTemplate(t *testing.T) {
	hud, _ := fakeHUD(t)
	hc := &hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()}
	_, err := hc.post(context.Background(),
		"/api/mobile/v1/secrets/{x}/exfiltrate",
		map[string]string{"x": "anything"},
		map[string]any{},
		relayPostPaths)
	if err == nil || !strings.Contains(err.Error(), "not in allowlist") {
		t.Errorf("expected allowlist rejection, got err=%v", err)
	}
}

// TestRelay_Handoffs_HappyPath covers the slice-2-α handoff inbox
// relay so the widget can render a pending-handoffs card. Uses the
// shared path-allowlist + Bearer-auth boundary.
func TestRelay_Handoffs_HappyPath(t *testing.T) {
	hud, state := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callTool(t, srv, toolHandoffs)
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content count = %d, want 1", len(content))
	}
	c0, _ := content[0].(map[string]any)
	if c0["text"] != state.handoffs {
		t.Errorf("relay body mismatch:\n got  %q\n want %q", c0["text"], state.handoffs)
	}
	if state.lastPath != "/api/mobile/v1/handoffs" {
		t.Errorf("HUD got path %q, want /api/mobile/v1/handoffs", state.lastPath)
	}
}

// TestRelay_Stream_HappyPath covers the new stream relay so the
// widget can build an event ticker. Uses the same path-allowlist +
// auth surface as the other relays.
func TestRelay_Stream_HappyPath(t *testing.T) {
	hud, state := fakeHUD(t)
	srv := newServerWithHUD(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&hudClient{baseURL: hud.URL, token: "test-token", client: hud.Client()},
	)
	result := callTool(t, srv, toolStream)
	content, _ := result["content"].([]any)
	c0, _ := content[0].(map[string]any)
	if c0["text"] != state.stream {
		t.Errorf("relay body mismatch: got %q want %q", c0["text"], state.stream)
	}
	if state.lastPath != "/api/mobile/v1/stream" {
		t.Errorf("wrong path: %q", state.lastPath)
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

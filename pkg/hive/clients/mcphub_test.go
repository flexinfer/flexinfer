package clients

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

// fakeTransport is a deterministic in-memory mcp.Transport stand-in.
// Tests pre-load `responses` keyed on inbound method name; the
// transport echoes them back with the matching ID. Sent messages are
// recorded for assertion.
type fakeTransport struct {
	mu        sync.Mutex
	sent      []mcp.Message
	responses map[string][]byte // method → result JSON to send back
	failOn    map[string]*mcp.Error
	closed    bool
	pending   []mcp.Message
}

func (f *fakeTransport) Send(_ context.Context, msg *mcp.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("transport closed")
	}
	f.sent = append(f.sent, *msg)
	if msg.Method == "notifications/initialized" {
		// Notification — no response.
		return nil
	}
	resp := mcp.Message{JSONRPC: "2.0", ID: msg.ID}
	if errPayload, bad := f.failOn[msg.Method]; bad {
		resp.Error = errPayload
	} else if body, ok := f.responses[msg.Method]; ok {
		resp.Result = body
	} else {
		// Default: empty success.
		resp.Result = []byte(`{}`)
	}
	f.pending = append(f.pending, resp)
	return nil
}

func (f *fakeTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return nil, errors.New("transport closed")
		}
		if len(f.pending) > 0 {
			msg := f.pending[0]
			f.pending = f.pending[1:]
			f.mu.Unlock()
			return &msg, nil
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (f *fakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeTransport) sentMessages() []mcp.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]mcp.Message, len(f.sent))
	copy(out, f.sent)
	return out
}

// makeCallToolResult marshals a CallToolResult with one text content
// block whose body is the JSON of body.
func makeCallToolResult(t *testing.T, body any) []byte {
	t.Helper()
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	res := mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: string(bodyJSON)}},
	}
	out, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return out
}

func newTestHubClient(t *testing.T, ft *fakeTransport) *MCPHubClient {
	t.Helper()
	c := newMCPHubClientWithDefaults(MCPHubConfig{
		HubURL:         "wss://stub",
		ConnectTimeout: 100 * time.Millisecond,
		CallTimeout:    500 * time.Millisecond,
	}, func(ctx context.Context, _ string) (mcp.Transport, error) {
		return ft, nil
	})
	return c
}

// ----- Config validation -----

func TestNewMCPHubClient_RequiresHubURL(t *testing.T) {
	if _, err := NewMCPHubClient(MCPHubConfig{}); err == nil {
		t.Error("expected error for empty HubURL")
	}
}

func TestNewMCPHubClient_AppliesDefaults(t *testing.T) {
	c, err := NewMCPHubClient(MCPHubConfig{HubURL: "wss://x"})
	if err != nil {
		t.Fatal(err)
	}
	if c.cfg.Profile != "loom-hive" {
		t.Errorf("Profile default = %q, want loom-hive", c.cfg.Profile)
	}
	if c.cfg.ConnectTimeout != 10*time.Second {
		t.Errorf("ConnectTimeout default = %v, want 10s", c.cfg.ConnectTimeout)
	}
	if c.cfg.CallTimeout != 60*time.Second {
		t.Errorf("CallTimeout default = %v, want 60s", c.cfg.CallTimeout)
	}
}

// ----- Initialize handshake -----

func TestCallTool_PerformsInitializeOnFirstCall(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
			"tools/call": makeCallToolResult(t, map[string]any{"ok": true}),
		},
	}
	c := newTestHubClient(t, ft)
	got, err := c.CallTool(context.Background(), "mcp-devbox", "devbox_quality_gate", map[string]any{"project": "loom-core"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(got, `"ok":true`) {
		t.Errorf("expected JSON body in result, got %q", got)
	}
	sent := ft.sentMessages()
	if len(sent) != 3 {
		t.Fatalf("expected 3 sends (initialize, initialized notif, tools/call), got %d", len(sent))
	}
	if sent[0].Method != "initialize" {
		t.Errorf("first message = %q, want initialize", sent[0].Method)
	}
	if sent[1].Method != "notifications/initialized" {
		t.Errorf("second message = %q, want notifications/initialized", sent[1].Method)
	}
	if sent[2].Method != "tools/call" {
		t.Errorf("third message = %q, want tools/call", sent[2].Method)
	}
}

func TestCallTool_ReusesTransportForSubsequentCalls(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
			"tools/call": makeCallToolResult(t, map[string]any{"ok": true}),
		},
	}
	c := newTestHubClient(t, ft)
	for i := 0; i < 3; i++ {
		if _, err := c.CallTool(context.Background(), "mcp-devbox", "tool", nil); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	sent := ft.sentMessages()
	initCount := 0
	for _, m := range sent {
		if m.Method == "initialize" {
			initCount++
		}
	}
	if initCount != 1 {
		t.Errorf("expected 1 initialize across 3 calls, got %d", initCount)
	}
}

// ----- Tool call args + body -----

func TestCallTool_PassesArgumentsThroughCallToolParams(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
			"tools/call": makeCallToolResult(t, map[string]any{}),
		},
	}
	c := newTestHubClient(t, ft)
	args := map[string]any{
		"project":  "loom-core",
		"agent_id": "claude-code",
		"timeout":  float64(120),
	}
	if _, err := c.CallTool(context.Background(), "mcp-devbox", "devbox_quality_gate", args); err != nil {
		t.Fatalf("call: %v", err)
	}
	sent := ft.sentMessages()
	var callMsg mcp.Message
	for _, m := range sent {
		if m.Method == "tools/call" {
			callMsg = m
			break
		}
	}
	var params mcp.CallToolParams
	if err := json.Unmarshal(callMsg.Params, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.Name != "devbox_quality_gate" {
		t.Errorf("tool name = %q", params.Name)
	}
	if params.Arguments["project"] != "loom-core" {
		t.Errorf("project arg = %v", params.Arguments["project"])
	}
	if params.Arguments["agent_id"] != "claude-code" {
		t.Errorf("agent_id arg = %v", params.Arguments["agent_id"])
	}
}

// ----- Error paths -----

func TestCallTool_JSONRPCErrorBubbles(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
		},
		failOn: map[string]*mcp.Error{
			"tools/call": {Code: -32602, Message: "invalid args"},
		},
	}
	c := newTestHubClient(t, ft)
	_, err := c.CallTool(context.Background(), "mcp-devbox", "tool", nil)
	if err == nil {
		t.Fatal("expected error from JSON-RPC error response")
	}
	if !strings.Contains(err.Error(), "invalid args") {
		t.Errorf("error message missing 'invalid args': %v", err)
	}
}

func TestCallTool_IsErrorOnResultBubblesText(t *testing.T) {
	bodyJSON, _ := json.Marshal(map[string]any{"reason": "no project"})
	res, _ := json.Marshal(mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{{Type: "text", Text: string(bodyJSON)}},
	})
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
			"tools/call": res,
		},
	}
	c := newTestHubClient(t, ft)
	body, err := c.CallTool(context.Background(), "mcp-devbox", "tool", nil)
	if err == nil {
		t.Fatal("expected error when IsError=true")
	}
	if !strings.Contains(body, "no project") {
		t.Errorf("body should still carry the reason: %q", body)
	}
}

func TestCallTool_InitializeFailureClosesTransport(t *testing.T) {
	ft := &fakeTransport{
		failOn: map[string]*mcp.Error{
			"initialize": {Code: -32603, Message: "auth failed"},
		},
	}
	c := newTestHubClient(t, ft)
	if _, err := c.CallTool(context.Background(), "mcp-devbox", "tool", nil); err == nil {
		t.Error("expected error on initialize failure")
	}
	if !ft.closed {
		t.Error("transport should be closed after init failure")
	}
}

func TestCallTool_RequiresServerAndToolName(t *testing.T) {
	c := newTestHubClient(t, &fakeTransport{})
	if _, err := c.CallTool(context.Background(), "", "tool", nil); err == nil {
		t.Error("expected error for empty server")
	}
	if _, err := c.CallTool(context.Background(), "srv", "", nil); err == nil {
		t.Error("expected error for empty tool")
	}
}

// ----- ID-multiplexing / out-of-band messages -----

func TestCallTool_SkipsOutOfBandMessages(t *testing.T) {
	// Inject a notification (no ID) that arrives BETWEEN our send and
	// our own response. The client must skip and keep reading until
	// it finds the matching id.
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{"protocolVersion":"2024-11-05","serverInfo":{"name":"x","version":"1"}}`),
		},
	}
	c := newTestHubClient(t, ft)
	// Drive the initialize so we can hand-craft the tools/call response.
	// Trigger initialize by calling once with a canned response.
	ft.responses["tools/call"] = makeCallToolResult(t, map[string]any{"first": true})
	if _, err := c.CallTool(context.Background(), "mcp-devbox", "tool", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Now stage: an unrelated notification, then the real response.
	ft.mu.Lock()
	notif := mcp.Message{JSONRPC: "2.0", Method: "log/message", Params: []byte(`{"text":"server status"}`)}
	ft.pending = append(ft.pending, notif)
	ft.responses["tools/call"] = makeCallToolResult(t, map[string]any{"second": true})
	ft.mu.Unlock()
	got, err := c.CallTool(context.Background(), "mcp-devbox", "tool", nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !strings.Contains(got, `"second":true`) {
		t.Errorf("expected to skip notification and find matching response: %q", got)
	}
}

// ----- Close lifecycle -----

func TestClose_ClosesAllTransports(t *testing.T) {
	ft := &fakeTransport{
		responses: map[string][]byte{
			"initialize": []byte(`{}`),
			"tools/call": makeCallToolResult(t, map[string]any{}),
		},
	}
	c := newTestHubClient(t, ft)
	if _, err := c.CallTool(context.Background(), "srv", "tool", nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if !ft.closed {
		t.Error("transport not closed after Close()")
	}
}

// ----- Env helper -----

func TestMCPHubConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"LOOM_MCP_HUB_URL":        "wss://hub.example/ws",
		"LOOM_MCP_HUB_PROFILE":    "loom-hive-prod",
		"LOOM_MCP_HUB_TOKEN":      "tok-abc",
		"CF_ACCESS_CLIENT_ID":     "cf-id",
		"CF_ACCESS_CLIENT_SECRET": "cf-secret",
	}
	cfg, ok := MCPHubConfigFromEnv(func(k string) string { return env[k] })
	if !ok {
		t.Fatal("expected ok=true with HubURL set")
	}
	if cfg.HubURL != "wss://hub.example/ws" {
		t.Errorf("HubURL = %q", cfg.HubURL)
	}
	if cfg.Profile != "loom-hive-prod" {
		t.Errorf("Profile = %q", cfg.Profile)
	}
	if cfg.Token != "tok-abc" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.CFAccessClientID != "cf-id" || cfg.CFAccessClientSecret != "cf-secret" {
		t.Errorf("CF Access creds wrong: %+v", cfg)
	}
}

func TestMCPHubConfigFromEnv_NoURLDisables(t *testing.T) {
	if _, ok := MCPHubConfigFromEnv(func(string) string { return "" }); ok {
		t.Error("ok should be false when HubURL unset")
	}
}

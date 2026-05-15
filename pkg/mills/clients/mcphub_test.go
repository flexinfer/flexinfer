package clients

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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
	if c.cfg.Profile != "loom-mills" {
		t.Errorf("Profile default = %q, want loom-mills", c.cfg.Profile)
	}
	if c.cfg.ConnectTimeout != 10*time.Second {
		t.Errorf("ConnectTimeout default = %v, want 10s", c.cfg.ConnectTimeout)
	}
	if c.cfg.CallTimeout != 10*time.Minute {
		t.Errorf("CallTimeout default = %v, want 10m", c.cfg.CallTimeout)
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

// ----- Transport invalidation + retry -----

// transportErrSequence implements mcp.Transport with a scripted error
// on the Nth Recv call. Used to simulate a half-broken cached
// connection: Send succeeds (or fails) and Recv emits a close-1006 /
// EOF style error to trigger the retry path.
type transportErrSequence struct {
	mu             sync.Mutex
	sendErrOnTry   int // 1-indexed call number on which Send returns sendErr; 0 disables
	sendTries      int
	sendErr        error
	recvErrOnTry   int // 1-indexed call number on which Recv returns recvErr; 0 disables
	recvTries      int
	recvErr        error
	defaultResults map[string][]byte // method → result bytes for successful calls
	failsInit      bool              // when true, return error on initialize
	closed         bool
	sent           []mcp.Message
	pending        []mcp.Message
}

func (f *transportErrSequence) Send(_ context.Context, msg *mcp.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return errors.New("transport closed")
	}
	if msg.Method == "tools/call" {
		f.sendTries++
		if f.sendErrOnTry != 0 && f.sendTries == f.sendErrOnTry {
			return f.sendErr
		}
	}
	f.sent = append(f.sent, *msg)
	if msg.Method == "notifications/initialized" {
		return nil
	}
	resp := mcp.Message{JSONRPC: "2.0", ID: msg.ID}
	if msg.Method == "initialize" && f.failsInit {
		resp.Error = &mcp.Error{Code: -32603, Message: "init refused"}
	} else if body, ok := f.defaultResults[msg.Method]; ok {
		resp.Result = body
	} else {
		resp.Result = []byte(`{}`)
	}
	f.pending = append(f.pending, resp)
	return nil
}

func (f *transportErrSequence) Recv(ctx context.Context) (*mcp.Message, error) {
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return nil, errors.New("transport closed")
		}
		if len(f.pending) > 0 {
			// Peek at the next pending message: only count Recv tries on
			// tools/call responses so init handshakes don't burn the
			// scripted error budget.
			next := f.pending[0]
			isToolsCall := next.Result != nil || next.Error != nil
			if isToolsCall {
				f.recvTries++
				if f.recvErrOnTry != 0 && f.recvTries == f.recvErrOnTry {
					f.mu.Unlock()
					return nil, f.recvErr
				}
			}
			f.pending = f.pending[1:]
			f.mu.Unlock()
			return &next, nil
		}
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (f *transportErrSequence) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func TestCallTool_RetriesOnceAfterTransportClose1006(t *testing.T) {
	// First Recv returns the gateway's close-1006 error (the exact
	// shape the canary failure log captured). The retry should redial
	// (a fresh transport) and succeed.
	dialCalls := 0
	var firstTransport *transportErrSequence
	successResult := makeCallToolResult(t, map[string]any{"passed": true})

	c := newMCPHubClientWithDefaults(MCPHubConfig{
		HubURL:         "wss://stub",
		ConnectTimeout: 100 * time.Millisecond,
		CallTimeout:    500 * time.Millisecond,
	}, func(_ context.Context, _ string) (mcp.Transport, error) {
		dialCalls++
		if dialCalls == 1 {
			firstTransport = &transportErrSequence{
				recvErrOnTry: 1,
				recvErr:      errors.New("read message: websocket: close 1006 (abnormal closure): unexpected EOF"),
				defaultResults: map[string][]byte{
					"initialize": []byte(`{"protocolVersion":"2024-11-05"}`),
					"tools/call": successResult,
				},
			}
			return firstTransport, nil
		}
		// Second dial: clean transport that succeeds.
		return &transportErrSequence{
			defaultResults: map[string][]byte{
				"initialize": []byte(`{"protocolVersion":"2024-11-05"}`),
				"tools/call": successResult,
			},
		}, nil
	})

	got, err := c.CallTool(context.Background(), "devbox", "devbox_quality_gate", map[string]any{"project": "loom-core"})
	if err != nil {
		t.Fatalf("CallTool after retry: %v", err)
	}
	if !strings.Contains(got, `"passed":true`) {
		t.Errorf("expected retried call to succeed, got body %q", got)
	}
	if dialCalls != 2 {
		t.Errorf("expected 2 dials (initial + retry), got %d", dialCalls)
	}
	if firstTransport == nil || !firstTransport.closed {
		t.Errorf("broken transport should have been closed during invalidation, closed=%v", firstTransport != nil && firstTransport.closed)
	}
}

func TestCallTool_RetriesOnceAfterBrokenPipeOnSend(t *testing.T) {
	// Simulates the third attempt in the canary log: Send returns
	// "broken pipe" on the cached (half-closed) connection. Retry
	// with a fresh dial succeeds.
	dialCalls := 0
	successResult := makeCallToolResult(t, map[string]any{"passed": true})

	c := newMCPHubClientWithDefaults(MCPHubConfig{
		HubURL:         "wss://stub",
		ConnectTimeout: 100 * time.Millisecond,
		CallTimeout:    500 * time.Millisecond,
	}, func(_ context.Context, _ string) (mcp.Transport, error) {
		dialCalls++
		if dialCalls == 1 {
			return &transportErrSequence{
				sendErrOnTry: 1,
				sendErr:      errors.New("write message: write tcp 10.42.7.5:57228->10.43.248.41:80: write: broken pipe"),
				defaultResults: map[string][]byte{
					"initialize": []byte(`{"protocolVersion":"2024-11-05"}`),
					"tools/call": successResult,
				},
			}, nil
		}
		return &transportErrSequence{
			defaultResults: map[string][]byte{
				"initialize": []byte(`{"protocolVersion":"2024-11-05"}`),
				"tools/call": successResult,
			},
		}, nil
	})

	if _, err := c.CallTool(context.Background(), "devbox", "devbox_quality_gate", nil); err != nil {
		t.Fatalf("CallTool after retry: %v", err)
	}
	if dialCalls != 2 {
		t.Errorf("expected 2 dials, got %d", dialCalls)
	}
}

func TestCallTool_DoesNotRetryJSONRPCErrors(t *testing.T) {
	// A tool-reported JSON-RPC error is NOT a transport failure and
	// must bubble immediately without burning a redial.
	dialCalls := 0
	c := newMCPHubClientWithDefaults(MCPHubConfig{
		HubURL:         "wss://stub",
		ConnectTimeout: 100 * time.Millisecond,
		CallTimeout:    500 * time.Millisecond,
	}, func(_ context.Context, _ string) (mcp.Transport, error) {
		dialCalls++
		ft := &fakeTransport{
			responses: map[string][]byte{
				"initialize": []byte(`{"protocolVersion":"2024-11-05"}`),
			},
			failOn: map[string]*mcp.Error{
				"tools/call": {Code: -32602, Message: "invalid args"},
			},
		}
		return ft, nil
	})

	if _, err := c.CallTool(context.Background(), "devbox", "tool", nil); err == nil {
		t.Fatal("expected JSON-RPC error to surface")
	}
	if dialCalls != 1 {
		t.Errorf("expected 1 dial (no retry on app-level error), got %d", dialCalls)
	}
}

func TestCallTool_StopsAfterOneRetry(t *testing.T) {
	// Both dials return broken transports — the second failure must
	// propagate (no infinite retry loop).
	dialCalls := 0
	c := newMCPHubClientWithDefaults(MCPHubConfig{
		HubURL:         "wss://stub",
		ConnectTimeout: 100 * time.Millisecond,
		CallTimeout:    500 * time.Millisecond,
	}, func(_ context.Context, _ string) (mcp.Transport, error) {
		dialCalls++
		return &transportErrSequence{
			recvErrOnTry: 1,
			recvErr:      errors.New("read message: websocket: close 1006 (abnormal closure): unexpected EOF"),
			defaultResults: map[string][]byte{
				"initialize": []byte(`{"protocolVersion":"2024-11-05"}`),
				"tools/call": []byte(`{}`),
			},
		}, nil
	})

	_, err := c.CallTool(context.Background(), "devbox", "tool", nil)
	if err == nil {
		t.Fatal("expected error after both attempts fail")
	}
	if dialCalls != 2 {
		t.Errorf("expected exactly 2 dials, got %d", dialCalls)
	}
}

func TestIsTransportError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"close 1006", errors.New("websocket: close 1006 (abnormal closure): unexpected EOF"), true},
		{"broken pipe", errors.New("write tcp x->y: write: broken pipe"), true},
		{"unexpected EOF", errors.New("read message: unexpected EOF"), true},
		{"connection reset", errors.New("read: connection reset by peer"), true},
		{"closed conn", errors.New("use of closed network connection"), true},
		{"transport closed", errors.New("transport closed"), true},
		{"i/o timeout", errors.New("read tcp: i/o timeout"), true},
		{"raw io.EOF", io.EOF, true},
		{"jsonrpc error", errors.New("mcphub: srv/tool: invalid args (code=-32602)"), false},
		{"tool reported error", errors.New("mcphub: srv/tool reported error: bad project"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransportError(tc.err); got != tc.want {
				t.Errorf("isTransportError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ----- Env helper -----

func TestMCPHubConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"LOOM_MCP_HUB_URL":        "wss://hub.example/ws",
		"LOOM_MCP_HUB_PROFILE":    "loom-mills-prod",
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
	if cfg.Profile != "loom-mills-prod" {
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

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	mcp "gitlab.flexinfer.ai/libs/mcp-go"
)

func TestRun_ArgumentValidation(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"test-server", "--profile", "codex", "--hub-url", "://bad"}, strings.NewReader(""), io.Discard, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit for invalid hub URL")
	}
}

func TestRun_ServerFirstFlagParsing(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req mcp.Message
		if err := json.Unmarshal(msg, &req); err != nil {
			return
		}

		resp := &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      req.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		}
		raw, _ := json.Marshal(resp)
		_ = conn.WriteMessage(websocket.TextMessage, raw)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	stdin, stdout, done, stderr := startWrapper(t, []string{"agent_context", "--profile", "codex", "--hub-url", wsURL})
	t.Cleanup(func() { _ = stdin.Close() })

	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":"req-1","method":"tools/list","params":{}}`+"\n")
	msg := readJSONLine(t, stdout, 2*time.Second)
	if msg.ID == nil || msg.ID != "req-1" {
		t.Fatalf("response id = %v, want req-1", msg.ID)
	}
	if !bytes.Contains(msg.Result, []byte(`"ok":true`)) {
		t.Fatalf("unexpected response result: %s", string(msg.Result))
	}

	_ = stdin.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("wrapper exited with code %d, stderr=%s", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper did not exit after stdin close")
	}
}

func TestRun_HeaderInjectionAndNotificationRelay(t *testing.T) {
	t.Setenv("MCP_HUB_TOKEN", "tok-test")
	t.Setenv("CF_ACCESS_CLIENT_ID", "cf-client")
	t.Setenv("CF_ACCESS_CLIENT_SECRET", "cf-secret")

	var authHeader, cfID, cfSecret string
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		cfID = r.Header.Get("CF-Access-Client-Id")
		cfSecret = r.Header.Get("CF-Access-Client-Secret")

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req mcp.Message
		if err := json.Unmarshal(msg, &req); err != nil {
			return
		}

		notif := &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			Method:  "notifications/test",
			Params:  json.RawMessage(`{"value":"n1"}`),
		}
		notifRaw, _ := json.Marshal(notif)
		_ = conn.WriteMessage(websocket.TextMessage, notifRaw)

		resp := &mcp.Message{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      req.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		}
		respRaw, _ := json.Marshal(resp)
		_ = conn.WriteMessage(websocket.TextMessage, respRaw)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	stdin, stdout, done, stderr := startWrapper(t, []string{"agent_context", "--hub-url", wsURL})
	t.Cleanup(func() { _ = stdin.Close() })

	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":"req-2","method":"tools/list","params":{}}`+"\n")

	first := readJSONLine(t, stdout, 2*time.Second)
	if first.Method != "notifications/test" {
		t.Fatalf("first outbound message method = %q, want notifications/test", first.Method)
	}

	second := readJSONLine(t, stdout, 2*time.Second)
	if second.ID == nil || second.ID != "req-2" {
		t.Fatalf("response id = %v, want req-2", second.ID)
	}

	if authHeader != "Bearer tok-test" {
		t.Fatalf("Authorization header = %q, want %q", authHeader, "Bearer tok-test")
	}
	if cfID != "cf-client" || cfSecret != "cf-secret" {
		t.Fatalf("cloudflare headers = (%q, %q), want (%q, %q)", cfID, cfSecret, "cf-client", "cf-secret")
	}

	_ = stdin.Close()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("wrapper exited with code %d, stderr=%s", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper did not exit after stdin close")
	}
}

func startWrapper(t *testing.T, args []string) (io.WriteCloser, *bufio.Reader, <-chan int, *bytes.Buffer) {
	t.Helper()

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderr := &bytes.Buffer{}
	done := make(chan int, 1)

	go func() {
		code := run(args, stdinReader, stdoutWriter, stderr)
		_ = stdoutWriter.Close()
		done <- code
	}()

	return stdinWriter, bufio.NewReader(stdoutReader), done, stderr
}

func readJSONLine(t *testing.T, r *bufio.Reader, timeout time.Duration) *mcp.Message {
	t.Helper()

	type result struct {
		line []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := r.ReadBytes('\n')
		ch <- result{line: line, err: err}
	}()

	select {
	case got := <-ch:
		if got.err != nil {
			t.Fatalf("read line: %v", got.err)
		}
		var msg mcp.Message
		if err := json.Unmarshal(bytes.TrimSpace(got.line), &msg); err != nil {
			t.Fatalf("unmarshal line %q: %v", string(got.line), err)
		}
		return &msg
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for wrapper output")
		return nil
	}
}

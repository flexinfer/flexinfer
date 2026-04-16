package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	releaseHub := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseHub:
		default:
			close(releaseHub)
		}
	})
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
		<-releaseHub
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
	close(releaseHub)
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
	releaseHub := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseHub:
		default:
			close(releaseHub)
		}
	})
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
		<-releaseHub
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
	close(releaseHub)
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("wrapper exited with code %d, stderr=%s", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper did not exit after stdin close")
	}
}

func TestRun_ReconnectsAndReplaysInitialization(t *testing.T) {
	releaseHub := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseHub:
		default:
			close(releaseHub)
		}
	})

	var connectionCount int32
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		switch atomic.AddInt32(&connectionCount, 1) {
		case 1:
			requireHubRequest(t, conn, "init-1", "initialize")
			writeHubResult(t, conn, "init-1", `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"hub","version":"test"}}`)
			requireHubNotification(t, conn, "notifications/initialized")
			requireHubRequest(t, conn, "req-1", "tools/list")
			writeHubResult(t, conn, "req-1", `{"ok":1}`)
			return
		case 2:
			requireHubRequest(t, conn, "init-1", "initialize")
			writeHubResult(t, conn, "init-1", `{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"hub","version":"test"}}`)
			requireHubNotification(t, conn, "notifications/initialized")
			requireHubRequest(t, conn, "req-2", "tools/list")
			writeHubResult(t, conn, "req-2", `{"ok":2}`)
			<-releaseHub
		default:
			t.Errorf("unexpected hub reconnect count: %d", connectionCount)
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	stdin, stdout, done, stderr := startWrapper(t, []string{"agent_context", "--profile", "codex", "--hub-url", wsURL})
	t.Cleanup(func() { _ = stdin.Close() })

	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":"init-1","method":"initialize","params":{}}`+"\n")
	initResp := readJSONLine(t, stdout, 2*time.Second)
	if initResp.ID != "init-1" {
		t.Fatalf("initialize response id = %v, want init-1", initResp.ID)
	}
	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`+"\n")
	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":"req-1","method":"tools/list","params":{}}`+"\n")
	first := readJSONLine(t, stdout, 2*time.Second)
	if !bytes.Contains(first.Result, []byte(`"ok":1`)) {
		t.Fatalf("first response result = %s, want ok=1", string(first.Result))
	}

	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":"req-2","method":"tools/list","params":{}}`+"\n")
	second := readJSONLine(t, stdout, 2*time.Second)
	if !bytes.Contains(second.Result, []byte(`"ok":2`)) {
		t.Fatalf("second response result = %s error = %+v, want ok=2", string(second.Result), second.Error)
	}

	_ = stdin.Close()
	close(releaseHub)
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("wrapper exited with code %d, stderr=%s", code, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wrapper did not exit after stdin close")
	}
}

func TestRun_HubRecvCloseRetriesRequestWithoutClosingWrapper(t *testing.T) {
	releaseHub := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseHub:
		default:
			close(releaseHub)
		}
	})

	var connectionCount int32
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		switch atomic.AddInt32(&connectionCount, 1) {
		case 1:
			requireHubRequest(t, conn, "req-1", "tools/list")
			return
		case 2:
			requireHubRequest(t, conn, "req-1", "tools/list")
			writeHubResult(t, conn, "req-1", `{"retried":true}`)
			requireHubRequest(t, conn, "req-2", "tools/list")
			writeHubResult(t, conn, "req-2", `{"ok":true}`)
			<-releaseHub
		default:
			t.Errorf("unexpected hub reconnect count: %d", atomic.LoadInt32(&connectionCount))
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	stdin, stdout, done, stderr := startWrapper(t, []string{"agent_context", "--profile", "codex", "--hub-url", wsURL})
	t.Cleanup(func() { _ = stdin.Close() })

	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":"req-1","method":"tools/list","params":{}}`+"\n")
	first := readJSONLine(t, stdout, 2*time.Second)
	if first.ID != "req-1" || !bytes.Contains(first.Result, []byte(`"retried":true`)) {
		t.Fatalf("first response = id:%v result:%s error:%+v, want retried req-1", first.ID, string(first.Result), first.Error)
	}

	_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":"req-2","method":"tools/list","params":{}}`+"\n")
	second := readJSONLine(t, stdout, 2*time.Second)
	if second.ID != "req-2" || !bytes.Contains(second.Result, []byte(`"ok":true`)) {
		t.Fatalf("second response = id:%v result:%s error:%+v, want successful req-2", second.ID, string(second.Result), second.Error)
	}

	_ = stdin.Close()
	close(releaseHub)
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

func requireHubRequest(t *testing.T, conn *websocket.Conn, id any, method string) {
	t.Helper()
	msg := readHubMessage(t, conn)
	if msg.ID != id || msg.Method != method {
		t.Fatalf("hub message = id:%v method:%q, want id:%v method:%q", msg.ID, msg.Method, id, method)
	}
}

func requireHubNotification(t *testing.T, conn *websocket.Conn, method string) {
	t.Helper()
	msg := readHubMessage(t, conn)
	if msg.ID != nil || msg.Method != method {
		t.Fatalf("hub notification = id:%v method:%q, want method:%q", msg.ID, msg.Method, method)
	}
}

func readHubMessage(t *testing.T, conn *websocket.Conn) *mcp.Message {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set hub read deadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read hub message: %v", err)
	}
	var msg mcp.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal hub message %q: %v", string(raw), err)
	}
	return &msg
}

func writeHubResult(t *testing.T, conn *websocket.Conn, id any, result string) {
	t.Helper()
	resp := &mcp.Message{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      id,
		Result:  json.RawMessage(result),
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal hub response: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("write hub response: %v", err)
	}
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

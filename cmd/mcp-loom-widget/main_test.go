package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// requestResponse roundtrips a single JSON-RPC request through Serve.
// Used by every test below so we exercise the actual stdio dispatch
// loop instead of the per-method handlers in isolation.
func requestResponse(t *testing.T, body string) map[string]any {
	t.Helper()
	srv := newServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	in := bytes.NewBufferString(body + "\n")
	out := &bytes.Buffer{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Serve(ctx, in, out); err != nil && err != io.EOF {
		t.Fatalf("Serve: %v", err)
	}

	line := strings.TrimSpace(out.String())
	if line == "" {
		return nil
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nraw=%q", err, line)
	}
	return resp
}

func TestInitializeReturnsServerInfoAndCapabilities(t *testing.T) {
	resp := requestResponse(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", resp["jsonrpc"])
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("missing result: %+v", resp)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != serverName {
		t.Errorf("serverInfo.name = %v, want %s", info["name"], serverName)
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities missing tools: %+v", caps)
	}
	if _, ok := caps["resources"]; !ok {
		t.Errorf("capabilities missing resources: %+v", caps)
	}
}

// TestToolsListEmitsUiResourceUri is the critical MCP Apps wire-format
// assertion: the tool declaration must carry _meta.ui.resourceUri so
// the host knows to render the widget when the tool is invoked.
func TestToolsListEmitsUiResourceUri(t *testing.T) {
	resp := requestResponse(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != toolName {
		t.Errorf("tool.name = %v, want %s", tool["name"], toolName)
	}
	meta, _ := tool["_meta"].(map[string]any)
	ui, _ := meta["ui"].(map[string]any)
	if ui["resourceUri"] != widgetURI {
		t.Errorf("tool._meta.ui.resourceUri = %v, want %s", ui["resourceUri"], widgetURI)
	}
}

// TestToolsCallEmitsUiResourceUri verifies the per-invocation pointer
// — some MCP Apps hosts use the call-time _meta rather than the
// definition-time one, so the server must populate both.
func TestToolsCallEmitsUiResourceUri(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"loom_fleet_show","arguments":{}}}`
	resp := requestResponse(t, body)
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("missing result: %+v", resp)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content count = %d, want 1", len(content))
	}
	c0, _ := content[0].(map[string]any)
	if c0["type"] != "text" {
		t.Errorf("content[0].type = %v, want text", c0["type"])
	}
	meta, _ := result["_meta"].(map[string]any)
	ui, _ := meta["ui"].(map[string]any)
	if ui["resourceUri"] != widgetURI {
		t.Errorf("result._meta.ui.resourceUri = %v, want %s", ui["resourceUri"], widgetURI)
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope"}}`
	resp := requestResponse(t, body)
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error, got %+v", resp)
	}
	if code, _ := errObj["code"].(float64); int(code) != -32602 {
		t.Errorf("error.code = %v, want -32602", errObj["code"])
	}
}

func TestResourcesListAdvertisesWidget(t *testing.T) {
	resp := requestResponse(t, `{"jsonrpc":"2.0","id":5,"method":"resources/list"}`)
	result, _ := resp["result"].(map[string]any)
	resources, _ := result["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("resources count = %d, want 1", len(resources))
	}
	r0, _ := resources[0].(map[string]any)
	if r0["uri"] != widgetURI {
		t.Errorf("resources[0].uri = %v, want %s", r0["uri"], widgetURI)
	}
	if r0["mimeType"] != widgetMimeType {
		t.Errorf("resources[0].mimeType = %v, want %s", r0["mimeType"], widgetMimeType)
	}
}

func TestResourcesReadReturnsEmbeddedHTML(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":6,"method":"resources/read","params":{"uri":"` + widgetURI + `"}}`
	resp := requestResponse(t, body)
	result, _ := resp["result"].(map[string]any)
	contents, _ := result["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents count = %d, want 1", len(contents))
	}
	c0, _ := contents[0].(map[string]any)
	if c0["mimeType"] != widgetMimeType {
		t.Errorf("contents[0].mimeType = %v, want %s", c0["mimeType"], widgetMimeType)
	}
	text, _ := c0["text"].(string)
	if !strings.Contains(text, "Loom Fleet") {
		t.Errorf("widget html missing Loom Fleet heading (first 80 chars): %q", text[:min(80, len(text))])
	}
	if !strings.Contains(text, "Slice 1b") {
		t.Errorf("widget html missing 1b-α placeholder marker")
	}
}

func TestResourcesReadUnknownURI(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{"uri":"ui://widget/wrong.html"}}`
	resp := requestResponse(t, body)
	if resp["error"] == nil {
		t.Errorf("expected error for unknown URI, got %+v", resp)
	}
}

func TestUnknownMethodReturnsMethodNotFound(t *testing.T) {
	resp := requestResponse(t, `{"jsonrpc":"2.0","id":8,"method":"nonexistent/method"}`)
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error, got %+v", resp)
	}
	if code, _ := errObj["code"].(float64); int(code) != -32601 {
		t.Errorf("error.code = %v, want -32601", errObj["code"])
	}
}

// TestNotificationsProduceNoResponse confirms that JSON-RPC
// notifications (no id) are accepted but emit nothing on stdout — the
// spec requires no-response for notifications.
func TestNotificationsProduceNoResponse(t *testing.T) {
	srv := newServer(slog.New(slog.NewTextHandler(io.Discard, nil)))
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}` + "\n")
	out := &bytes.Buffer{}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = srv.Serve(ctx, in, out)

	if out.Len() != 0 {
		t.Errorf("expected no output for notification, got %q", out.String())
	}
	if !srv.initialized {
		t.Errorf("notifications/initialized should flip initialized flag")
	}
}

// TestParseErrorReturnsCorrectCode hands a malformed line to dispatch
// and asserts the JSON-RPC 2.0 parse-error envelope.
func TestParseErrorReturnsCorrectCode(t *testing.T) {
	resp := requestResponse(t, `{this is not json`)
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error, got %+v", resp)
	}
	if code, _ := errObj["code"].(float64); int(code) != -32700 {
		t.Errorf("error.code = %v, want -32700", errObj["code"])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

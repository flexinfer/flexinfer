package main

import (
	"context"
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestProxy_ResourceTemplatesList_ReturnsProxyTemplates(t *testing.T) {
	msg, err := mcp.NewRequest(1, "resources/templates/list", map[string]any{})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}

	resp, err := handleProxyResourceTemplatesList(context.Background(), nil, msg)
	if err != nil {
		t.Fatalf("handleProxyResourceTemplatesList returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error != nil {
		t.Fatalf("expected no error, got: %+v", resp.Error)
	}

	var decoded map[string]any
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	v, ok := decoded["resourceTemplates"]
	if !ok {
		t.Fatalf("expected result.resourceTemplates key, got: %v", decoded)
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("expected resourceTemplates to be array, got %T", v)
	}
	if len(arr) == 0 {
		t.Fatalf("expected resourceTemplates to be non-empty")
	}

	want := map[string]bool{
		"loom_servers": true,
		"loom_tools":   true,
		"loom_health":  true,
		"loom_config":  true,
	}
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name != "" {
			delete(want, name)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing expected templates: %v", want)
	}
}

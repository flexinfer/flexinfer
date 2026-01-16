package main

import (
	"context"
	"encoding/json"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestProxy_ResourceTemplatesList_ReturnsEmptyList(t *testing.T) {
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
	if _, ok := v.([]any); !ok {
		t.Fatalf("expected resourceTemplates to be array, got %T", v)
	}
}

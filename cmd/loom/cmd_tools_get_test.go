package main

import "testing"

func TestBuildToolsGetParams_Success(t *testing.T) {
	params, err := buildToolsGetParams(" agent_context__search_nodes ")
	if err != nil {
		t.Fatalf("buildToolsGetParams error = %v", err)
	}
	got, ok := params["name"].(string)
	if !ok {
		t.Fatalf("name type = %T, want string", params["name"])
	}
	if got != "agent_context__search_nodes" {
		t.Fatalf("name = %q, want agent_context__search_nodes", got)
	}
}

func TestBuildToolsGetParams_Empty(t *testing.T) {
	_, err := buildToolsGetParams("   ")
	if err == nil {
		t.Fatal("expected error for empty tool name")
	}
}

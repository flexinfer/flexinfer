package main

import "testing"

func TestBuildToolsSearchParams_Defaults(t *testing.T) {
	params, err := buildToolsSearchParams("search", nil, "", 0, "")
	if err != nil {
		t.Fatalf("buildToolsSearchParams error = %v", err)
	}

	if got, ok := params["query"].(string); !ok || got != "search" {
		t.Fatalf("query = %v, want search", params["query"])
	}
	if got, ok := params["detail"].(string); !ok || got != "summary" {
		t.Fatalf("detail = %v, want summary", params["detail"])
	}
	if got, ok := params["limit"].(int); !ok || got != 20 {
		t.Fatalf("limit = %v, want 20", params["limit"])
	}
	if _, ok := params["servers"]; ok {
		t.Fatalf("servers should be omitted when unset: %#v", params["servers"])
	}
}

func TestBuildToolsSearchParams_InvalidDetail(t *testing.T) {
	_, err := buildToolsSearchParams("search", nil, "full", 10, "")
	if err == nil {
		t.Fatal("expected invalid detail error")
	}
}

func TestBuildToolsSearchParams_ServersAndCursor(t *testing.T) {
	params, err := buildToolsSearchParams("search", []string{"agent_context", "gitlab__"}, "schema", 500, "20")
	if err != nil {
		t.Fatalf("buildToolsSearchParams error = %v", err)
	}

	servers, ok := params["servers"].([]string)
	if !ok {
		t.Fatalf("servers type = %T, want []string", params["servers"])
	}
	if len(servers) != 2 || servers[0] != "agent_context" || servers[1] != "gitlab" {
		t.Fatalf("servers = %#v, want [agent_context gitlab]", servers)
	}

	if got, ok := params["detail"].(string); !ok || got != "schema" {
		t.Fatalf("detail = %v, want schema", params["detail"])
	}
	if got, ok := params["limit"].(int); !ok || got != 200 {
		t.Fatalf("limit = %v, want 200 clamp", params["limit"])
	}
	if got, ok := params["cursor"].(string); !ok || got != "20" {
		t.Fatalf("cursor = %v, want 20", params["cursor"])
	}
}

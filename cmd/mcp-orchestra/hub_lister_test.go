package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHubToolLister_ListTools(t *testing.T) {
	payload := hubToolsResponse{
		Tools: []hubToolEntry{
			{Name: "k8s__k8s_getPods", Description: "Get pods", Server: "k8s"},
			{Name: "flux__flux_logs", Description: "Flux logs", Server: "flux"},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loom/tools" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
	}))
	defer srv.Close()

	lister := NewHubToolLister(srv.URL)
	tools, err := lister.ListTools()
	if err != nil {
		t.Fatalf("ListTools() error: %v", err)
	}

	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "k8s__k8s_getPods" {
		t.Errorf("tools[0].Name = %q, want %q", tools[0].Name, "k8s__k8s_getPods")
	}
	if tools[1].Server != "flux" {
		t.Errorf("tools[1].Server = %q, want %q", tools[1].Server, "flux")
	}
}

func TestHubToolLister_ListTools_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	lister := NewHubToolLister(srv.URL)
	_, err := lister.ListTools()
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
}

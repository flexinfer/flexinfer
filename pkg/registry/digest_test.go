package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		name            string
		ref             string
		host, repo, tag string
		wantErr         bool
	}{
		{name: "harbor host with path and tag", ref: "registry.harbor.lan/flexinfer/runtime:master", host: "registry.harbor.lan", repo: "flexinfer/runtime", tag: "master"},
		{name: "host with port no tag", ref: "127.0.0.1:5000/foo/bar", host: "127.0.0.1:5000", repo: "foo/bar", tag: "latest"},
		{name: "dockerhub single segment", ref: "nginx", host: dockerHubHost, repo: "library/nginx", tag: "latest"},
		{name: "dockerhub single segment tagged", ref: "nginx:1.25", host: dockerHubHost, repo: "library/nginx", tag: "1.25"},
		{name: "dockerhub user repo", ref: "user/repo:tag", host: dockerHubHost, repo: "user/repo", tag: "tag"},
		{name: "localhost", ref: "localhost:5000/img", host: "localhost:5000", repo: "img", tag: "latest"},
		{name: "digest rejected", ref: "repo@sha256:abc", wantErr: true},
		{name: "empty rejected", ref: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseImageRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseImageRef(%q) expected error, got %+v", tt.ref, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseImageRef(%q) unexpected error: %v", tt.ref, err)
			}
			if got.Host != tt.host || got.Repo != tt.repo || got.Tag != tt.tag {
				t.Errorf("parseImageRef(%q) = %+v, want host=%s repo=%s tag=%s", tt.ref, got, tt.host, tt.repo, tt.tag)
			}
		})
	}
}

const testDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestResolveImageDigest_Direct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.Path == "/v2/flexinfer/runtime/manifests/master" {
			w.Header().Set("Docker-Content-Digest", testDigest)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	got, err := ResolveImageDigest(context.Background(), srv.Client(), host+"/flexinfer/runtime:master", ImageAuth{Insecure: true})
	if err != nil {
		t.Fatalf("ResolveImageDigest: %v", err)
	}
	if got != testDigest {
		t.Errorf("digest = %q, want %q", got, testDigest)
	}
}

func TestResolveImageDigest_BearerChallenge(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if r.URL.Query().Get("service") != "reg.example" {
				t.Errorf("token request missing service param: %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"token":"tok-123"}`)
		case r.Method == http.MethodHead && r.URL.Path == "/v2/library/nginx/manifests/latest":
			if r.Header.Get("Authorization") == "Bearer tok-123" {
				w.Header().Set("Docker-Content-Digest", testDigest)
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="reg.example",scope="repository:library/nginx:pull"`, srv.URL))
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	got, err := ResolveImageDigest(context.Background(), srv.Client(), host+"/library/nginx", ImageAuth{Insecure: true})
	if err != nil {
		t.Fatalf("ResolveImageDigest (bearer): %v", err)
	}
	if got != testDigest {
		t.Errorf("digest = %q, want %q", got, testDigest)
	}
}

func TestResolveImageDigest_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	if _, err := ResolveImageDigest(context.Background(), srv.Client(), host+"/missing/repo:tag", ImageAuth{Insecure: true}); err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

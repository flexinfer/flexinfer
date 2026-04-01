package projectmeta

import "testing"

func TestFromNamespace(t *testing.T) {
	tests := []struct {
		namespace string
		want      string
	}{
		{namespace: "loom-core/feat/orchestration", want: "loom-core"},
		{namespace: "services/loom-core", want: "services/loom-core"},
		{namespace: "services/loom-core/feat/orchestration", want: "services/loom-core"},
		{namespace: "platform/gitops/flux", want: "platform/gitops"},
		{namespace: "loom-core", want: "loom-core"},
		{namespace: "", want: ""},
		{namespace: "/broken", want: ""},
	}

	for _, tc := range tests {
		if got := FromNamespace(tc.namespace); got != tc.want {
			t.Fatalf("FromNamespace(%q) = %q, want %q", tc.namespace, got, tc.want)
		}
	}
}

func TestCanonical(t *testing.T) {
	if got := Canonical("services/loom-core", "loom-core/feat/orchestration"); got != "services/loom-core" {
		t.Fatalf("Canonical(explicit) = %q, want services/loom-core", got)
	}
	if got := Canonical("", "loom-core/feat/orchestration"); got != "loom-core" {
		t.Fatalf("Canonical(namespace) = %q, want loom-core", got)
	}
}

func TestFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "/Users/cblevins/workspace/services/loom-core", want: "services/loom-core"},
		{path: "/Users/cblevins/workspace/services/loom-core/.worktrees/mobile-ui", want: "services/loom-core"},
		{path: "platform/gitops/clusters/k3s", want: "platform/gitops"},
		{path: `C:\workspace\apps\loom\Sources\App.swift`, want: "apps/loom"},
		{path: "", want: ""},
		{path: "/tmp/not-a-workspace/path", want: ""},
	}

	for _, tc := range tests {
		if got := FromPath(tc.path); got != tc.want {
			t.Fatalf("FromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

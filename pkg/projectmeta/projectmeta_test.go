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

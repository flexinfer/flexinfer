package daemon

import (
	"testing"

	"github.com/crb2nu/loom/pkg/registry"
)

func TestRuntimeRegistryForTarget_NormalizesTargetSpecificSpecs(t *testing.T) {
	reg := &registry.Registry{
		Version: 1,
		Servers: []*registry.Server{
			{
				Name:       "agent_context",
				Categories: []string{"memory"},
				Common: &registry.TargetSpec{
					Command: "base-cmd",
					Args:    []any{"serve"},
					Env: map[string]string{
						"COMMON_ONLY": "1",
						"SHARED":      "common",
					},
					AlwaysAllow: []string{"agent_context"},
				},
				Targets: map[string]*registry.TargetSpec{
					"codex": {
						Command: "codex-cmd",
						Env: map[string]string{
							"TARGET_ONLY": "1",
							"SHARED":      "target",
						},
					},
				},
			},
		},
	}

	normalized, err := runtimeRegistryForTarget(reg, "codex")
	if err != nil {
		t.Fatalf("runtimeRegistryForTarget() error = %v", err)
	}

	if len(normalized.Servers) != 1 {
		t.Fatalf("normalized server count = %d, want 1", len(normalized.Servers))
	}
	server := normalized.Servers[0]
	if server.Targets != nil {
		t.Fatalf("normalized Targets should be nil, got %#v", server.Targets)
	}
	if server.Common == nil {
		t.Fatal("normalized Common spec is nil")
	}
	if got, want := server.Common.Command, "codex-cmd"; got != want {
		t.Fatalf("normalized command = %q, want %q", got, want)
	}
	if got, want := server.Common.Env["COMMON_ONLY"], "1"; got != want {
		t.Fatalf("COMMON_ONLY = %q, want %q", got, want)
	}
	if got, want := server.Common.Env["TARGET_ONLY"], "1"; got != want {
		t.Fatalf("TARGET_ONLY = %q, want %q", got, want)
	}
	if got, want := server.Common.Env["SHARED"], "target"; got != want {
		t.Fatalf("SHARED = %q, want %q", got, want)
	}

	spec, err := normalized.GetServerSpec("agent_context", "codex")
	if err != nil {
		t.Fatalf("normalized GetServerSpec() error = %v", err)
	}
	if got, want := spec.Env["SHARED"], "target"; got != want {
		t.Fatalf("normalized spec SHARED = %q, want %q", got, want)
	}
}

func TestRuntimeRegistryForTarget_ClonesCommonEnv(t *testing.T) {
	reg := &registry.Registry{
		Version: 1,
		Servers: []*registry.Server{
			{
				Name: "gitlab",
				Common: &registry.TargetSpec{
					Env: map[string]string{
						"TOKEN": "abc",
					},
				},
			},
		},
	}

	normalized, err := runtimeRegistryForTarget(reg, "codex")
	if err != nil {
		t.Fatalf("runtimeRegistryForTarget() error = %v", err)
	}

	normalized.Servers[0].Common.Env["TOKEN"] = "changed"
	if got, want := reg.Servers[0].Common.Env["TOKEN"], "abc"; got != want {
		t.Fatalf("original registry env mutated to %q, want %q", got, want)
	}
}

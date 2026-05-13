package generator

import (
	"encoding/json"
	"testing"
)

// TestComputeCodexHookTrust_KnownGoodHashes locks our hash computation
// against an end-to-end-verified hook configuration that Codex v0.129+
// actually executed when the corresponding trusted_hash was written into
// ~/.codex/config.toml [hooks.state]. Captured on 2026-05-12 from a live
// `codex exec` run where SessionStart + PostToolUse + UserPromptSubmit
// all fired (sentinel file received three timestamped lines).
//
// If this test breaks, Codex changed the canonical hash algorithm. Re-run
// the live test in scripts/ci/codex_hooks_smoke.sh to refresh the
// fixtures.
func TestComputeCodexHookTrust_KnownGoodHashes(t *testing.T) {
	const hooksJSON = `{
		"hooks": {
			"PostToolUse": [
				{"hooks": [{"type":"command","command":"date '+%Y-%m-%dT%H:%M:%S.%N PostToolUse fired' >> /tmp/codex-hook-probe.log"}]}
			],
			"SessionStart": [
				{"hooks": [{"type":"command","command":"date '+%Y-%m-%dT%H:%M:%S.%N SessionStart fired sid=' >> /tmp/codex-hook-probe.log"}]}
			],
			"UserPromptSubmit": [
				{"hooks": [{"type":"command","command":"date '+%Y-%m-%dT%H:%M:%S.%N UserPromptSubmit fired' >> /tmp/codex-hook-probe.log"}]}
			]
		}
	}`
	var parsed map[string]any
	if err := json.Unmarshal([]byte(hooksJSON), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	entries, err := ComputeCodexHookTrust("/tmp/known-good-input.json", parsed)
	if err != nil {
		t.Fatalf("ComputeCodexHookTrust: %v", err)
	}

	want := map[string]string{
		"/tmp/known-good-input.json:post_tool_use:0:0":      "sha256:9d405097392ec8a003bb3e21235ae6ad3a6b2e5b4e10ee82601a52f6eac07ccc",
		"/tmp/known-good-input.json:session_start:0:0":      "sha256:389f27b6c43d897c5c7c306d6783302f1a22f259fe178add0d6c4b144245cad0",
		"/tmp/known-good-input.json:user_prompt_submit:0:0": "sha256:78ce8c588512944a41944a50ced69e4614400cca47ba477d64ceacd3ceddcada",
	}
	got := make(map[string]string, len(entries))
	for _, e := range entries {
		got[e.Key] = e.TrustedHash
	}

	if len(got) != len(want) {
		t.Fatalf("entry count = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for k, expected := range want {
		if got[k] != expected {
			t.Errorf("key %q\n  got:  %s\n  want: %s", k, got[k], expected)
		}
	}
}

// TestComputeCodexHookTrust_NormalizesMissingTimeout verifies that a hook
// without a `timeout` field gets the codex-default 600s in the hashed
// identity. The normalization is per command_hook_hash in discovery.rs:
// `timeout_sec.unwrap_or(600).max(1)`.
func TestComputeCodexHookTrust_NormalizesMissingTimeout(t *testing.T) {
	noTimeout := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo hi"},
					},
				},
			},
		},
	}
	withTimeout600 := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo hi", "timeout": 600},
					},
				},
			},
		},
	}
	a, _ := ComputeCodexHookTrust("/p.json", noTimeout)
	b, _ := ComputeCodexHookTrust("/p.json", withTimeout600)
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("expected 1 entry each, got %d/%d", len(a), len(b))
	}
	if a[0].TrustedHash != b[0].TrustedHash {
		t.Errorf("missing timeout should normalize to 600:\n  no-timeout:  %s\n  timeout=600: %s",
			a[0].TrustedHash, b[0].TrustedHash)
	}
}

// TestComputeCodexHookTrust_MatcherChangesHash verifies that adding a
// matcher to a hook produces a distinct trusted_hash.
func TestComputeCodexHookTrust_MatcherChangesHash(t *testing.T) {
	withoutMatcher := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo hi"},
					},
				},
			},
		},
	}
	withMatcher := map[string]any{
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{
					"matcher": "Bash|Task",
					"hooks": []any{
						map[string]any{"type": "command", "command": "echo hi"},
					},
				},
			},
		},
	}
	a, _ := ComputeCodexHookTrust("/p.json", withoutMatcher)
	b, _ := ComputeCodexHookTrust("/p.json", withMatcher)
	if a[0].TrustedHash == b[0].TrustedHash {
		t.Errorf("matcher difference should change hash")
	}
}

// TestComputeCodexHookTrust_StableOrder confirms entries are sorted by
// event-name so the [hooks.state] table in config.toml has deterministic
// ordering across regens.
func TestComputeCodexHookTrust_StableOrder(t *testing.T) {
	multi := map[string]any{
		"hooks": map[string]any{
			"PostToolUse":  []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "a"}}}},
			"SessionStart": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "b"}}}},
		},
	}
	for i := 0; i < 5; i++ {
		entries, _ := ComputeCodexHookTrust("/p.json", multi)
		if entries[0].EventName != "PostToolUse" || entries[1].EventName != "SessionStart" {
			t.Fatalf("iter %d: unstable order: %s, %s", i, entries[0].EventName, entries[1].EventName)
		}
	}
}

package redact

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseTier(t *testing.T) {
	cases := map[string]struct {
		want Tier
		ok   bool
	}{
		"public":   {TierPublic, true},
		"redacted": {TierRedacted, true},
		"private":  {TierPrivate, true},
		"":         {"", false},
		"PUBLIC":   {"", false},
		"junk":     {"", false},
	}
	for in, c := range cases {
		t.Run(in, func(t *testing.T) {
			got, err := ParseTier(in)
			if c.ok {
				if err != nil || got != c.want {
					t.Errorf("ParseTier(%q) = (%v, %v), want (%v, nil)", in, got, err, c.want)
				}
			} else if err == nil {
				t.Errorf("ParseTier(%q) accepted invalid input", in)
			}
		})
	}
}

func TestRedact_Private_ReturnsInputUnchanged(t *testing.T) {
	in := map[string]any{"file_path": "/etc/passwd", "secret": "AKIAIOSFODNN7EXAMPLE"}
	got := Redact("Read", in, TierPrivate)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("private tier mutated input: got %v want %v", got, in)
	}
}

func TestRedact_Public_UnknownTool_Empty(t *testing.T) {
	in := map[string]any{"a": "1", "b": "2"}
	got := Redact("MysteryTool", in, TierPublic)
	if len(got) != 0 {
		t.Errorf("public/unknown should drop all keys, got %v", got)
	}
}

func TestRedact_Public_Read_KeepsPathBasenameOnly(t *testing.T) {
	in := map[string]any{"file_path": "/Users/cblevins/secret.env", "junk": "drop me"}
	got := Redact("Read", in, TierPublic)
	if got["file_path"] != "secret.env" {
		t.Errorf("Read.file_path basename: got %v", got["file_path"])
	}
	if _, ok := got["junk"]; ok {
		t.Errorf("Read.public should drop unknown fields, got %v", got)
	}
}

func TestRedact_Public_Bash_TruncatesCommand(t *testing.T) {
	in := map[string]any{"command": "echo hello && curl https://example.com/long/path?token=AKIAIOSFODNN7EXAMPLE"}
	got := Redact("Bash", in, TierPublic)
	cmd, _ := got["command"].(string)
	if !strings.HasSuffix(cmd, "…") {
		t.Errorf("Bash.command should have … suffix, got %q", cmd)
	}
	// Mask can extend or shrink the truncated portion; just bound it loosely.
	if len(cmd) > 60+4*len(RedactionMarker) {
		t.Errorf("Bash.command unreasonably long after mask: len=%d, %q", len(cmd), cmd)
	}
}

func TestRedact_Public_Bash_MasksSecretWithinTrunc(t *testing.T) {
	// Place secret at the start so it survives the 60-char trunc.
	in := map[string]any{"command": "AKIAIOSFODNN7EXAMPLE && echo done"}
	got := Redact("Bash", in, TierPublic)
	cmd, _ := got["command"].(string)
	if !strings.Contains(cmd, RedactionMarker) {
		t.Errorf("Bash.command secret should be masked even when truncated, got %q", cmd)
	}
}

func TestRedact_Redacted_Bash_MasksSecretsKeepsRest(t *testing.T) {
	in := map[string]any{"command": "echo hello && export TOKEN=glpat-abcdefghijklmnopqrstuv"}
	got := Redact("Bash", in, TierRedacted)
	cmd, _ := got["command"].(string)
	if !strings.Contains(cmd, "echo hello") {
		t.Errorf("redacted Bash should preserve non-secret content, got %q", cmd)
	}
	if !strings.Contains(cmd, RedactionMarker) {
		t.Errorf("redacted Bash should mask secrets, got %q", cmd)
	}
}

func TestRedact_AgentContextAdd_AlwaysDropsArgs(t *testing.T) {
	in := map[string]any{"entries": "lots of sensitive context"}
	for _, tier := range []Tier{TierPublic, TierRedacted} {
		got := Redact("agent_context_add", in, tier)
		if len(got) != 0 {
			t.Errorf("agent_context_add at %s should drop args, got %v", tier, got)
		}
	}
}

func TestRedact_NeverAddsKeys(t *testing.T) {
	in := map[string]any{"a": "1", "b": "2", "c": "3"}
	for _, tier := range []Tier{TierPublic, TierRedacted, TierPrivate} {
		got := Redact("UnknownTool", in, tier)
		for k := range got {
			if _, ok := in[k]; !ok {
				t.Errorf("Redact added new key %q for tier %s", k, tier)
			}
		}
	}
}

func TestRedact_Idempotent(t *testing.T) {
	in := map[string]any{
		"file_path": "/secret/path/file.env",
		"command":   "AKIAIOSFODNN7EXAMPLE",
		"other":     "innocent",
	}
	for _, tier := range []Tier{TierPublic, TierRedacted, TierPrivate} {
		once := Redact("Read", in, tier)
		twice := Redact("Read", once, tier)
		if !reflect.DeepEqual(once, twice) {
			t.Errorf("tier=%s not idempotent: once=%v twice=%v", tier, once, twice)
		}
	}
}

func TestRedact_NilArgs(t *testing.T) {
	if got := Redact("Read", nil, TierPublic); got == nil || len(got) != 0 {
		t.Errorf("public + nil args should be empty map, got %v", got)
	}
	if got := Redact("Read", nil, TierPrivate); got != nil {
		t.Errorf("private + nil args should pass through nil, got %v", got)
	}
}

func TestRedact_DoesNotMutateInput(t *testing.T) {
	in := map[string]any{"file_path": "/a/b/c.txt", "other": "x"}
	original := map[string]any{"file_path": "/a/b/c.txt", "other": "x"}
	_ = Redact("Read", in, TierPublic)
	if !reflect.DeepEqual(in, original) {
		t.Errorf("input mutated: got %v want %v", in, original)
	}
}

func TestSummary_Public_KnownTool(t *testing.T) {
	got := Summary("Read", "the file content here", TierPublic)
	if !strings.Contains(got, "size_bytes") {
		t.Errorf("Read public summary should be size-only, got %q", got)
	}
}

func TestSummary_Public_UnknownTool_Empty(t *testing.T) {
	got := Summary("Mystery", "result text", TierPublic)
	if got != "" {
		t.Errorf("public/unknown Summary should be empty, got %q", got)
	}
}

func TestSummary_Redacted_TruncsAndMasks(t *testing.T) {
	long := strings.Repeat("a", 300) + " AKIAIOSFODNN7EXAMPLE end"
	got := Summary("Read", long, TierRedacted)
	// 200-char trunc happens BEFORE masking, so the secret at position 300 is dropped.
	if len([]rune(strings.TrimSuffix(got, "…"))) > 200 {
		t.Errorf("redacted Summary exceeded 200 chars: len=%d", len(got))
	}
}

func TestSummary_Private_PassThrough(t *testing.T) {
	got := Summary("anything", "raw text", TierPrivate)
	if got != "raw text" {
		t.Errorf("private Summary should pass through, got %q", got)
	}
}

func TestSummary_NilResult(t *testing.T) {
	if got := Summary("Read", nil, TierPublic); got != "" {
		t.Errorf("nil result should produce empty summary, got %q", got)
	}
}

func BenchmarkRedact_LargeArgs(b *testing.B) {
	args := map[string]any{
		"file_path":  "/Users/cblevins/workspace/services/loom-core/pkg/telemetry/redact/redact.go",
		"command":    "go test -v -race ./pkg/telemetry/redact/... -run TestRedact",
		"old_string": strings.Repeat("foo bar baz ", 50),
		"new_string": strings.Repeat("qux quux corge ", 50),
		"junk1":      "AKIAIOSFODNN7EXAMPLE",
		"junk2":      "Bearer abcdefghijklmnopqrstuvwxyz",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Redact("Edit", args, TierRedacted)
	}
}

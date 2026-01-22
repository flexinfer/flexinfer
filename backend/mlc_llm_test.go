package backend

import (
	"strings"
	"testing"
)

func TestMLCLLMBackendArgs_ModeServerMapsToServe(t *testing.T) {
	b := &MLCLLMBackend{}

	spec := &ModelSpec{
		Model: "my-model",
		Config: map[string]interface{}{
			"mode": "server",
		},
	}

	args := b.Args(spec)
	if len(args) == 0 || args[0] != "serve" {
		t.Fatalf("expected args[0] to be %q, got %#v", "serve", args)
	}
}

func TestMLCLLMBackendArgs_DefaultModeIsServe(t *testing.T) {
	b := &MLCLLMBackend{}

	spec := &ModelSpec{
		Model: "my-model",
	}

	args := b.Args(spec)
	if len(args) == 0 || args[0] != "serve" {
		t.Fatalf("expected args[0] to be %q, got %#v", "serve", args)
	}
}

func TestMLCLLMBackendArgs_UsesMaxTotalSeqLengthOverrideKey(t *testing.T) {
	b := &MLCLLMBackend{}

	spec := &ModelSpec{
		Model: "my-model",
		Config: map[string]interface{}{
			"maxNumSequence":    2,
			"maxTotalSeqLength": 32768,
		},
	}

	args := b.Args(spec)
	joined := strings.Join(args, " ")
	if want := "max_total_seq_length=32768"; !strings.Contains(joined, want) {
		t.Fatalf("expected args to contain %q, got %#v", want, args)
	}
	if bad := "max_total_sequence_length="; strings.Contains(joined, bad) {
		t.Fatalf("expected args to not contain legacy key %q, got %#v", bad, args)
	}
}

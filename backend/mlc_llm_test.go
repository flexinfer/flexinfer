package backend

import "testing"

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


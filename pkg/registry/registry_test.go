package registry

import (
	"context"
	"testing"
)

// TestRegistryInterfaceCompliance ensures all registered adapters implement the interface.
func TestRegistryInterfaceCompliance(t *testing.T) {
	types := Types()
	if len(types) == 0 {
		t.Fatal("no registry types registered")
	}

	expectedTypes := map[string]bool{
		"oci":         false,
		"huggingface": false,
		"ollama":      false,
	}

	for _, typ := range types {
		if _, ok := expectedTypes[typ]; ok {
			expectedTypes[typ] = true
		}
	}

	for typ, found := range expectedTypes {
		if !found {
			t.Errorf("expected registry type %q not registered", typ)
		}
	}
}

func TestRegistryGet(t *testing.T) {
	for _, typ := range []string{"oci", "huggingface", "ollama"} {
		r, err := Get(typ)
		if err != nil {
			t.Errorf("Get(%q) returned error: %v", typ, err)
			continue
		}
		if r.Type() != typ {
			t.Errorf("Get(%q).Type() = %q, want %q", typ, r.Type(), typ)
		}
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	_, err := Get("nonexistent")
	if err == nil {
		t.Error("Get(\"nonexistent\") should return error")
	}
}

func TestOCIResolveWithoutURL(t *testing.T) {
	r := &OCIRegistry{}
	_, err := r.Resolve(context.Background(), "test-ref")
	if err == nil {
		t.Error("Resolve without oras should return error")
	}
}

func TestHuggingFaceType(t *testing.T) {
	r := &HuggingFaceRegistry{}
	if r.Type() != "huggingface" {
		t.Errorf("Type() = %q, want %q", r.Type(), "huggingface")
	}
}

func TestOllamaType(t *testing.T) {
	r := &OllamaRegistry{}
	if r.Type() != "ollama" {
		t.Errorf("Type() = %q, want %q", r.Type(), "ollama")
	}
}

func TestExtractDigestFromManifest(t *testing.T) {
	manifest := `{
		"schemaVersion": 2,
		"config": {
			"digest": "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
		}
	}`
	digest := extractDigestFromManifest(manifest)
	if !containsSubstr(digest, "sha256:") {
		t.Errorf("expected sha256 digest, got %q", digest)
	}
}

func containsSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (s[:len(sub)] == sub || containsSubstr(s[1:], sub))
}

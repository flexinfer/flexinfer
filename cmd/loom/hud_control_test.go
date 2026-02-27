package main

import (
	"encoding/hex"
	"path/filepath"
	"testing"
)

func TestEnsureMobileOperatorTokenFile(t *testing.T) {
	t.Parallel()

	tokenPath := filepath.Join(t.TempDir(), "loom", "mobile-operator-token")

	token1, err := ensureMobileOperatorTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ensureMobileOperatorTokenFile() first call error: %v", err)
	}
	if len(token1) != 64 {
		t.Fatalf("token length = %d, want 64", len(token1))
	}
	if _, err := hex.DecodeString(token1); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}

	token2, err := ensureMobileOperatorTokenFile(tokenPath)
	if err != nil {
		t.Fatalf("ensureMobileOperatorTokenFile() second call error: %v", err)
	}
	if token2 != token1 {
		t.Fatalf("token changed across calls: %q != %q", token2, token1)
	}
}

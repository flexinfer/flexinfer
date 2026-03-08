package main

import (
	"testing"
)

func TestLooksLikeSecretKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"token suffix", "GITHUB_TOKEN", true},
		{"key suffix", "API_KEY", true},
		{"secret suffix", "MY_SECRET", true},
		{"password suffix", "DB_PASSWORD", true},
		{"pat suffix", "GH_PAT", true},
		{"api key suffix", "STRIPE_API_KEY", true},
		{"api token suffix", "SLACK_API_TOKEN", true},
		{"access token suffix", "OAUTH_ACCESS_TOKEN", true},
		{"not secret", "DATABASE_URL", false},
		{"not secret 2", "LOG_LEVEL", false},
		{"empty", "", false},
		{"partial match", "TOKEN_BUCKET", false},
		{"lowercase not matched", "my_token", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := looksLikeSecretKey(tc.input)
			if got != tc.expected {
				t.Errorf("looksLikeSecretKey(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestExtractTemplateRefs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"env ref", "${env:MY_VAR}", []string{"MY_VAR"}},
		{"keychain ref", "${keychain:my-secret}", []string{"my-secret"}},
		{"secret ref", "${secret:db-pass}", []string{"db-pass"}},
		{"env with default", "${env:MY_VAR:-fallback}", []string{"MY_VAR"}},
		{"multiple refs", "a=${env:A} b=${env:B}", []string{"A", "B"}},
		{"mixed types", "${env:E}${keychain:K}${secret:S}", []string{"E", "K", "S"}},
		{"no refs", "plain string", nil},
		{"empty", "", nil},
		{"incomplete ref", "${env:MY_VAR", nil},
		{"empty key", "${env:}", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTemplateRefs(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("extractTemplateRefs(%q) = %v (len %d), want %v (len %d)",
					tc.input, got, len(got), tc.expected, len(tc.expected))
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tc.expected[i])
				}
			}
		})
	}
}

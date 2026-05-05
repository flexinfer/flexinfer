package redact

import (
	"strings"
	"testing"
)

// TestMaskSecrets_Patterns covers each built-in pattern with a positive case
// (must mask) and a negative case (must NOT mask).
func TestMaskSecrets_Patterns(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		shouldMask  bool
		mustContain string // when shouldMask=false, original substring must survive
	}{
		// AWS
		{"aws_positive", "key=AKIAIOSFODNN7EXAMPLE rest", true, ""},
		{"aws_negative_short", "AKIA12345 only 5 chars", false, "AKIA12345"},

		// GitLab PAT
		{"glpat_positive", "token=glpat-abcdefghijklmnopqrstuv x", true, ""},
		{"glpat_negative", "glpat-shorty", false, "glpat-shorty"},

		// GitHub PAT
		{"ghp_positive", "ghp_abcdefghijklmnopqrstuvwxyz1234567890 ok", true, ""},
		{"ghp_negative", "ghp_short", false, "ghp_short"},

		// GitHub OAuth
		{"gho_positive", "Authorization: gho_abcdefghijklmnopqrstuvwxyz1234567890", true, ""},
		{"gho_negative", "gho_short", false, "gho_short"},

		// Bearer
		{"bearer_positive", "Authorization: Bearer abcdefghijklmnopqrstuv", true, ""},
		{"bearer_negative", "Bearer short", false, "Bearer short"},

		// Basic
		{"basic_positive", "Authorization: Basic dXNlcjpwYXNzd29yZA==", true, ""},
		{"basic_negative", "Basic short", false, "Basic short"},

		// JWT
		{"jwt_positive", "tok=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NSJ9.abcdef rest", true, ""},
		{"jwt_negative", "not.a.jwt", false, "not.a.jwt"},

		// Form values
		{"password_eq", "config: password=hunter2 next=foo", true, ""},
		{"secret_colon", "secret:my_secret_value", true, ""},
		{"api_key_dash", "api-key=abc123 ok", true, ""},
		{"token_eq", "token=somevalue ok", true, ""},
		{"form_negative", "the password is in another file", false, "another file"},

		// Connection strings
		{"postgres_positive", "DATABASE_URL=postgres://user:pw@host/db", true, ""},
		{"mysql_positive", "mysql://root:secret@localhost/app", true, ""},
		{"redis_positive", "redis://default:abcdef@cache.lan:6379/0", true, ""},
		{"conn_negative", "postgres://localhost/db (no creds)", false, "no creds"},

		// Plain text untouched
		{"plain_text", "the quick brown fox jumps over the lazy dog", false, "lazy dog"},
		{"empty", "", false, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MaskSecrets(c.input)
			if c.shouldMask {
				if !strings.Contains(got, RedactionMarker) {
					t.Errorf("expected %q to be masked, got %q", c.input, got)
				}
			} else {
				if strings.Contains(got, RedactionMarker) {
					t.Errorf("expected %q to be untouched, got %q", c.input, got)
				}
				if c.mustContain != "" && !strings.Contains(got, c.mustContain) {
					t.Errorf("expected %q to survive in %q", c.mustContain, got)
				}
			}
		})
	}
}

// TestMaskSecrets_Idempotent ensures masking twice produces the same result as
// masking once — required so subscribers re-applying redaction don't corrupt
// already-redacted payloads.
func TestMaskSecrets_Idempotent(t *testing.T) {
	inputs := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"Bearer abcdefghijklmnopqrstuvwxyz",
		"normal text with no secrets",
		"",
		"mixed: AKIAIOSFODNN7EXAMPLE and password=foo and plain words",
	}
	for _, in := range inputs {
		once := MaskSecrets(in)
		twice := MaskSecrets(once)
		if once != twice {
			t.Errorf("not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

// TestMaskSecrets_MultipleSecrets_AllMasked ensures every secret in a single
// string is masked, not just the first.
func TestMaskSecrets_MultipleSecrets_AllMasked(t *testing.T) {
	in := "first AKIAIOSFODNN7EXAMPLE then ghp_abcdefghijklmnopqrstuvwxyz1234567890 last"
	got := MaskSecrets(in)
	if strings.Count(got, RedactionMarker) != 2 {
		t.Errorf("expected 2 redactions in %q, got %q", in, got)
	}
}

func BenchmarkMaskSecrets_Plain(b *testing.B) {
	s := strings.Repeat("the quick brown fox jumps over the lazy dog ", 10)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = MaskSecrets(s)
	}
}

func BenchmarkMaskSecrets_WithSecrets(b *testing.B) {
	s := "config: password=hunter2 token=glpat-abcdefghijklmnopqrstuv " +
		"and AKIAIOSFODNN7EXAMPLE in the middle plus Bearer abcdefghijklmnopqrstuvwxyz"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = MaskSecrets(s)
	}
}

package redact

import "regexp"

// RedactionMarker is substituted for any matched secret pattern. Exported so
// downstream tests can assert on the marker directly.
const RedactionMarker = "***REDACTED***"

// builtInPatterns matches well-known secret formats. Order matters only for
// performance — every pattern runs against the input independently. Add a new
// pattern by appending here and adding a positive + negative test case in
// patterns_test.go.
var builtInPatterns = []*regexp.Regexp{
	// AWS access key (AKIA + 16 base32 chars).
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),

	// GitLab personal access token (glpat- + 20 url-safe chars).
	regexp.MustCompile(`glpat-[0-9A-Za-z_-]{20,}`),

	// GitHub personal access token (ghp_ + 36 alphanum).
	regexp.MustCompile(`ghp_[0-9A-Za-z]{36}`),

	// GitHub OAuth token (gho_ + 36 alphanum).
	regexp.MustCompile(`gho_[0-9A-Za-z]{36}`),

	// HTTP Bearer auth header.
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/-]{20,}=*`),

	// HTTP Basic auth header.
	regexp.MustCompile(`(?i)Basic\s+[A-Za-z0-9+/]{12,}=*`),

	// JWT (3 base64url segments separated by dots, leading "eyJ" header).
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),

	// password=, secret=, api_key=, token= form values.
	regexp.MustCompile(`(?i)(password|secret|api[_-]?key|token)\s*[=:]\s*['"]?[^\s'"&]+`),

	// Connection strings with embedded credentials.
	regexp.MustCompile(`(?i)(postgres|postgresql|mysql|mongodb|redis|amqp)://[^:/@\s]+:[^@\s]+@`),
}

// MaskSecrets replaces every occurrence of any built-in secret pattern in s
// with RedactionMarker. Non-matching content is returned unchanged. Idempotent.
func MaskSecrets(s string) string {
	for _, re := range builtInPatterns {
		s = re.ReplaceAllString(s, RedactionMarker)
	}
	return s
}

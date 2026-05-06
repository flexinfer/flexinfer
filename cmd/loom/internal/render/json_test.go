package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSON_RoundTrip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	in := map[string]any{
		"name":  "loom",
		"count": 3,
	}
	if err := JSON(&buf, in); err != nil {
		t.Fatalf("JSON returned error: %v", err)
	}
	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("JSON output should end with newline, got %q", out)
	}
	if !strings.Contains(out, "  ") {
		t.Fatalf("JSON output should be indented, got %q", out)
	}

	got := map[string]any{}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if got["name"] != "loom" {
		t.Fatalf("name field round-trip mismatch: %v", got["name"])
	}
}

func TestJSON_NoHTMLEscaping(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	in := map[string]string{
		"html": "<a>&</a>",
	}
	if err := JSON(&buf, in); err != nil {
		t.Fatalf("JSON returned error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\\u003c") || strings.Contains(out, "\\u0026") {
		t.Fatalf("JSON should not HTML-escape, got %q", out)
	}
	if !strings.Contains(out, "<a>&</a>") {
		t.Fatalf("expected literal HTML in output, got %q", out)
	}
}

func TestJSONCompact_NoIndent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	in := map[string]int{"a": 1, "b": 2}
	if err := JSONCompact(&buf, in); err != nil {
		t.Fatalf("JSONCompact returned error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\n  ") {
		t.Fatalf("JSONCompact should not indent, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("JSONCompact should end with newline, got %q", out)
	}
}

func TestJSONCompact_NoHTMLEscaping(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := JSONCompact(&buf, "<&>"); err != nil {
		t.Fatalf("JSONCompact returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "<&>") {
		t.Fatalf("JSONCompact should pass HTML chars literally, got %q", buf.String())
	}
}

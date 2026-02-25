package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile_BasicParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	content := "FOO_ENVFILE=bar\nBAZ_ENVFILE=qux\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Ensure vars are unset before test.
	os.Unsetenv("FOO_ENVFILE")
	os.Unsetenv("BAZ_ENVFILE")
	defer os.Unsetenv("FOO_ENVFILE")
	defer os.Unsetenv("BAZ_ENVFILE")

	if err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	if got := os.Getenv("FOO_ENVFILE"); got != "bar" {
		t.Errorf("FOO_ENVFILE = %q, want %q", got, "bar")
	}
	if got := os.Getenv("BAZ_ENVFILE"); got != "qux" {
		t.Errorf("BAZ_ENVFILE = %q, want %q", got, "qux")
	}
}

func TestLoadFile_QuoteStripping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	content := `DOUBLE_ENVFILE="hello world"
SINGLE_ENVFILE='single quoted'
NO_QUOTE_ENVFILE=plain
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("DOUBLE_ENVFILE")
	os.Unsetenv("SINGLE_ENVFILE")
	os.Unsetenv("NO_QUOTE_ENVFILE")
	defer os.Unsetenv("DOUBLE_ENVFILE")
	defer os.Unsetenv("SINGLE_ENVFILE")
	defer os.Unsetenv("NO_QUOTE_ENVFILE")

	if err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	tests := []struct {
		key  string
		want string
	}{
		{"DOUBLE_ENVFILE", "hello world"},
		{"SINGLE_ENVFILE", "single quoted"},
		{"NO_QUOTE_ENVFILE", "plain"},
	}
	for _, tt := range tests {
		if got := os.Getenv(tt.key); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestLoadFile_CommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	content := `# This is a comment
REAL_ENVFILE=value

  # indented comment

ANOTHER_ENVFILE=ok
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("REAL_ENVFILE")
	os.Unsetenv("ANOTHER_ENVFILE")
	defer os.Unsetenv("REAL_ENVFILE")
	defer os.Unsetenv("ANOTHER_ENVFILE")

	if err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	if got := os.Getenv("REAL_ENVFILE"); got != "value" {
		t.Errorf("REAL_ENVFILE = %q, want %q", got, "value")
	}
	if got := os.Getenv("ANOTHER_ENVFILE"); got != "ok" {
		t.Errorf("ANOTHER_ENVFILE = %q, want %q", got, "ok")
	}
}

func TestLoadFile_NoOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	content := "EXISTING_ENVFILE=from_file\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Setenv("EXISTING_ENVFILE", "original")
	defer os.Unsetenv("EXISTING_ENVFILE")

	if err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	if got := os.Getenv("EXISTING_ENVFILE"); got != "original" {
		t.Errorf("EXISTING_ENVFILE = %q, want %q (should not override)", got, "original")
	}
}

func TestLoadFile_MissingFile(t *testing.T) {
	err := LoadFile("/nonexistent/path/to/file.env")
	if err != nil {
		t.Errorf("LoadFile() for missing file should return nil, got: %v", err)
	}
}

func TestLoadFile_EmptyValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.env")

	content := "EMPTY_ENVFILE=\nSPACED_ENVFILE=  \n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("EMPTY_ENVFILE")
	os.Unsetenv("SPACED_ENVFILE")
	defer os.Unsetenv("EMPTY_ENVFILE")
	defer os.Unsetenv("SPACED_ENVFILE")

	if err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile() error: %v", err)
	}

	// Empty value is set as empty string.
	if got, ok := os.LookupEnv("EMPTY_ENVFILE"); !ok || got != "" {
		t.Errorf("EMPTY_ENVFILE = %q (set=%v), want empty string", got, ok)
	}
}

func TestParseEnvLine(t *testing.T) {
	tests := []struct {
		line    string
		wantKey string
		wantVal string
		wantOK  bool
	}{
		{"KEY=value", "KEY", "value", true},
		{`KEY="quoted"`, "KEY", "quoted", true},
		{"KEY='single'", "KEY", "single", true},
		{"KEY = spaced", "KEY", "spaced", true},
		{"=nokey", "", "", false},
		{"noequals", "", "", false},
		{"KEY=val=ue", "KEY", "val=ue", true},
	}

	for _, tt := range tests {
		key, val, ok := parseEnvLine(tt.line)
		if ok != tt.wantOK || key != tt.wantKey || val != tt.wantVal {
			t.Errorf("parseEnvLine(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.line, key, val, ok, tt.wantKey, tt.wantVal, tt.wantOK)
		}
	}
}

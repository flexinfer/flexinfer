package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	tmp := t.TempDir()
	tests := []struct {
		name    string
		body    string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "basic key=value",
			body: "FOO=bar\nBAZ=qux\n",
			want: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "comments and blank lines ignored",
			body: "# comment\n\nFOO=bar\n# another\nBAZ=qux\n",
			want: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "double-quoted value strips quotes",
			body: `TOKEN="0d8a7f6a"` + "\n",
			want: map[string]string{"TOKEN": "0d8a7f6a"},
		},
		{
			name: "single-quoted value strips quotes",
			body: "TOKEN='abc123'\n",
			want: map[string]string{"TOKEN": "abc123"},
		},
		{
			name: "export prefix tolerated",
			body: "export FOO=bar\n",
			want: map[string]string{"FOO": "bar"},
		},
		{
			name: "empty value kept",
			body: "FOO=\n",
			want: map[string]string{"FOO": ""},
		},
		{
			name: "value with equals sign preserved",
			body: "URL=http://x?a=1&b=2\n",
			want: map[string]string{"URL": "http://x?a=1&b=2"},
		},
		{
			name:    "missing equals errors",
			body:    "FOO\n",
			wantErr: true,
		},
		{
			name:    "leading equals errors",
			body:    "=value\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(tmp, "test.env")
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			got, err := parseEnvFile(path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (parsed: %v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len: got %d want %d (got: %v)", len(got), len(tt.want), got)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("key %q: got %q want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestParseEnvFile_Missing(t *testing.T) {
	_, err := parseEnvFile(filepath.Join(t.TempDir(), "nope.env"))
	if err == nil {
		t.Fatal("expected error on missing file")
	}
}

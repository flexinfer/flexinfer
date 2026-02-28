package backend

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAddDirToTar(t *testing.T) {
	// Create test directory structure.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)

	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte("//js"), 0644)
	os.WriteFile(filepath.Join(dir, "test.pyc"), []byte("bytecode"), 0644)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	excludes := map[string]bool{
		".git":         true,
		"node_modules": true,
	}

	var totalBytes int64
	err := addDirToTar(tw, dir, "/workspace/services/project", excludes, &totalBytes, MaxSyncBytes)
	if err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()

	// Extract and verify contents.
	files := extractTarEntries(t, buf.Bytes())

	// Should have: project dir, src dir, go.mod, src/main.go.
	// Should NOT have: .git/*, node_modules/*, test.pyc (binary excluded).
	if _, ok := files["workspace/services/project/go.mod"]; !ok {
		t.Error("expected go.mod in tar")
	}
	if _, ok := files["workspace/services/project/src/main.go"]; !ok {
		t.Error("expected src/main.go in tar")
	}
	if _, ok := files["workspace/services/project/.git/HEAD"]; ok {
		t.Error(".git should be excluded")
	}
	if _, ok := files["workspace/services/project/node_modules/pkg/index.js"]; ok {
		t.Error("node_modules should be excluded")
	}
	if _, ok := files["workspace/services/project/test.pyc"]; ok {
		t.Error(".pyc files should be excluded")
	}
}

func TestAddDirToTar_MaxSizeExceeded(t *testing.T) {
	dir := t.TempDir()

	// Create a file larger than the limit.
	data := make([]byte, 1024)
	os.WriteFile(filepath.Join(dir, "big.txt"), data, 0644)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	var totalBytes int64
	err := addDirToTar(tw, dir, "/workspace", map[string]bool{}, &totalBytes, 512)
	if err == nil {
		t.Error("expected error for exceeding max size")
	}
	tw.Close()
	gw.Close()
}

func TestAddDirToTar_MultipleSourceDirs(t *testing.T) {
	// Simulate syncing project + dep.
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "services", "myproject")
	libDir := filepath.Join(tmpDir, "libs", "mylib")

	os.MkdirAll(projectDir, 0755)
	os.MkdirAll(libDir, 0755)
	os.WriteFile(filepath.Join(projectDir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(libDir, "lib.go"), []byte("package mylib"), 0644)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	excludes := map[string]bool{".git": true}
	var totalBytes int64

	dirs := []SyncDir{
		{LocalPath: projectDir, RemotePath: "/workspace/services/myproject"},
		{LocalPath: libDir, RemotePath: "/workspace/libs/mylib"},
	}

	for _, d := range dirs {
		err := addDirToTar(tw, d.LocalPath, d.RemotePath, excludes, &totalBytes, MaxSyncBytes)
		if err != nil {
			t.Fatal(err)
		}
	}

	tw.Close()
	gw.Close()

	files := extractTarEntries(t, buf.Bytes())

	if _, ok := files["workspace/services/myproject/main.go"]; !ok {
		t.Error("expected main.go from project")
	}
	if _, ok := files["workspace/libs/mylib/lib.go"]; !ok {
		t.Error("expected lib.go from lib")
	}
}

func TestIsBinaryExcluded(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"main.go", false},
		{"lib.py", false},
		{"test.pyc", true},
		{"test.pyo", true},
		{"lib.so", true},
		{"lib.dylib", true},
		{"main.test", true},
		{"README.md", false},
	}

	for _, tt := range tests {
		got := isBinaryExcluded(tt.name)
		if got != tt.want {
			t.Errorf("isBinaryExcluded(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// extractTarEntries decompresses and extracts a tar.gz, returning a map of
// path→content for all regular files.
func extractTarEntries(t *testing.T, data []byte) map[string]string {
	t.Helper()

	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	files := make(map[string]string)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch hdr.Typeflag {
		case tar.TypeReg:
			content, _ := io.ReadAll(tr)
			files[hdr.Name] = string(content)
		case tar.TypeDir:
			files[hdr.Name] = ""
		}
	}

	return files
}

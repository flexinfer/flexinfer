package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src.txt")
	dst := filepath.Join(tmpDir, "dst.txt")

	content := []byte("hello world")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatalf("failed to write src file: %v", err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	if !Exists(dst) {
		t.Errorf("dst file does not exist")
	}

	readContent, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst file: %v", err)
	}

	if string(readContent) != string(content) {
		t.Errorf("content mismatch: got %s, want %s", string(readContent), string(content))
	}
}

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "test.txt")

	if Exists(file) {
		t.Errorf("Exists returned true for non-existent file")
	}

	if err := os.WriteFile(file, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	if !Exists(file) {
		t.Errorf("Exists returned false for existing file")
	}
}

func TestCopySymlink(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a file and a symlink to it
	file := filepath.Join(tmpDir, "target.txt")
	if err := os.WriteFile(file, []byte("content"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	srcLink := filepath.Join(tmpDir, "link")
	if err := os.Symlink("target.txt", srcLink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	dstLink := filepath.Join(tmpDir, "link_copy")
	if err := CopySymlink(srcLink, dstLink); err != nil {
		t.Fatalf("CopySymlink failed: %v", err)
	}

	// Verify symlink was created
	info, err := os.Lstat(dstLink)
	if err != nil {
		t.Fatalf("failed to stat dst symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("dst is not a symlink")
	}

	// Verify symlink target is correct
	target, err := os.Readlink(dstLink)
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if target != "target.txt" {
		t.Errorf("symlink target mismatch: got %s, want target.txt", target)
	}
}

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		excludes []string
		want     bool
	}{
		{"dot path", ".", []string{"foo"}, false},
		{"prefix match", "sessions/abc", []string{"sessions"}, true},
		{"exact match", "readme.md", []string{"readme.md"}, true},
		{"glob match", "test.log", []string{"*.log"}, true},
		{"no match", "main.go", []string{"*.log", "vendor"}, false},
		{"empty excludes", "main.go", nil, false},
		{"empty path", "", []string{"foo"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldExclude(tc.path, tc.excludes)
			if got != tc.want {
				t.Errorf("shouldExclude(%q, %v) = %v, want %v", tc.path, tc.excludes, got, tc.want)
			}
		})
	}
}

func TestCopyDirWithSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	dstDir := filepath.Join(tmpDir, "dst")

	// Create source structure with symlinks
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Symlink to file
	if err := os.Symlink("file.txt", filepath.Join(srcDir, "link_to_file")); err != nil {
		t.Fatalf("failed to create symlink to file: %v", err)
	}

	// Symlink to directory
	if err := os.Symlink("subdir", filepath.Join(srcDir, "link_to_dir")); err != nil {
		t.Fatalf("failed to create symlink to dir: %v", err)
	}

	// Copy directory
	if err := CopyDir(srcDir, dstDir, nil); err != nil {
		t.Fatalf("CopyDir failed: %v", err)
	}

	// Verify symlink to file
	linkToFile := filepath.Join(dstDir, "link_to_file")
	info, err := os.Lstat(linkToFile)
	if err != nil {
		t.Fatalf("failed to stat link_to_file: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link_to_file is not a symlink")
	}
	target, _ := os.Readlink(linkToFile)
	if target != "file.txt" {
		t.Errorf("link_to_file target mismatch: got %s, want file.txt", target)
	}

	// Verify symlink to directory
	linkToDir := filepath.Join(dstDir, "link_to_dir")
	info, err = os.Lstat(linkToDir)
	if err != nil {
		t.Fatalf("failed to stat link_to_dir: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link_to_dir is not a symlink")
	}
	target, _ = os.Readlink(linkToDir)
	if target != "subdir" {
		t.Errorf("link_to_dir target mismatch: got %s, want subdir", target)
	}
}

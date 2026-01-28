package pathsec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidatePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a subdirectory for testing
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		root    string
		wantErr bool
	}{
		{
			name:    "valid subpath",
			path:    filepath.Join(tmpDir, "foo"),
			root:    tmpDir,
			wantErr: false,
		},
		{
			name:    "root itself",
			path:    tmpDir,
			root:    tmpDir,
			wantErr: false,
		},
		{
			name:    "valid nested subpath",
			path:    filepath.Join(tmpDir, "a", "b", "c"),
			root:    tmpDir,
			wantErr: false,
		},
		{
			name:    "traversal attempt",
			path:    filepath.Join(tmpDir, "..", "etc", "passwd"),
			root:    tmpDir,
			wantErr: true,
		},
		{
			name:    "absolute escape",
			path:    "/etc/passwd",
			root:    tmpDir,
			wantErr: true,
		},
		{
			name:    "double dot traversal",
			path:    tmpDir + "/../../../etc/passwd",
			root:    tmpDir,
			wantErr: true,
		},
		{
			name:    "different root entirely",
			path:    "/tmp/other",
			root:    tmpDir,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePath(tt.path, tt.root)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePath_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not reliable on Windows")
	}

	tmpDir := t.TempDir()

	// Create a file inside the allowed directory
	insideFile := filepath.Join(tmpDir, "inside.txt")
	if err := os.WriteFile(insideFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create inside file: %v", err)
	}

	// Create symlink pointing outside
	outsideFile := "/etc/hosts"
	symlinkPath := filepath.Join(tmpDir, "sneaky")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Skip("cannot create symlink - permission denied")
	}

	err := ValidatePath(symlinkPath, tmpDir)
	if err == nil {
		t.Error("expected error for symlink escaping boundary, got nil")
	}

	// Symlink to file inside should be OK
	goodSymlink := filepath.Join(tmpDir, "good")
	if err := os.Symlink(insideFile, goodSymlink); err != nil {
		t.Fatalf("failed to create good symlink: %v", err)
	}

	err = ValidatePath(goodSymlink, tmpDir)
	if err != nil {
		t.Errorf("expected no error for symlink within boundary, got %v", err)
	}
}

func TestValidateFileSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a small file
	smallFile := filepath.Join(tmpDir, "small.txt")
	if err := os.WriteFile(smallFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create small file: %v", err)
	}

	// Create a larger file
	largeFile := filepath.Join(tmpDir, "large.txt")
	largeContent := make([]byte, 1024)
	if err := os.WriteFile(largeFile, largeContent, 0644); err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}

	tests := []struct {
		name     string
		path     string
		maxBytes int64
		wantErr  bool
	}{
		{
			name:     "small file within limit",
			path:     smallFile,
			maxBytes: 100,
			wantErr:  false,
		},
		{
			name:     "small file at exact limit",
			path:     smallFile,
			maxBytes: 5, // "hello" is 5 bytes
			wantErr:  false,
		},
		{
			name:     "small file exceeds limit",
			path:     smallFile,
			maxBytes: 3,
			wantErr:  true,
		},
		{
			name:     "large file exceeds limit",
			path:     largeFile,
			maxBytes: 100,
			wantErr:  true,
		},
		{
			name:     "large file within limit",
			path:     largeFile,
			maxBytes: 2048,
			wantErr:  false,
		},
		{
			name:     "nonexistent file",
			path:     filepath.Join(tmpDir, "nonexistent.txt"),
			maxBytes: 100,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFileSize(tt.path, tt.maxBytes)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateFileSize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCleanPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "simple relative path",
			path:    "foo/bar",
			wantErr: false,
		},
		{
			name:    "path with dots",
			path:    "foo/../bar",
			wantErr: false,
		},
		{
			name:    "path with double slashes",
			path:    "foo//bar",
			wantErr: false,
		},
		{
			name:    "absolute path",
			path:    "/tmp/test",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CleanPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("CleanPath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				// Result should be absolute
				if !filepath.IsAbs(result) {
					t.Errorf("CleanPath() result should be absolute, got %q", result)
				}
				// Result should be clean
				if result != filepath.Clean(result) {
					t.Errorf("CleanPath() result should be clean, got %q", result)
				}
			}
		})
	}
}

func TestIsSubpath(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{
			name:   "direct child",
			parent: "/tmp",
			child:  "/tmp/foo",
			want:   true,
		},
		{
			name:   "nested child",
			parent: "/tmp",
			child:  "/tmp/foo/bar/baz",
			want:   true,
		},
		{
			name:   "same path",
			parent: "/tmp",
			child:  "/tmp",
			want:   true,
		},
		{
			name:   "not a child",
			parent: "/tmp",
			child:  "/var/log",
			want:   false,
		},
		{
			name:   "sibling",
			parent: "/tmp/foo",
			child:  "/tmp/bar",
			want:   false,
		},
		{
			name:   "parent of parent",
			parent: "/tmp/foo",
			child:  "/tmp",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsSubpath(tt.parent, tt.child)
			if err != nil {
				t.Fatalf("IsSubpath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("IsSubpath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContainsTraversal(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "clean path",
			path: "foo/bar/baz",
			want: false,
		},
		{
			name: "double dot",
			path: "foo/../bar",
			want: true,
		},
		{
			name: "leading double dot",
			path: "../foo",
			want: true,
		},
		{
			name: "null byte",
			path: "foo\x00bar",
			want: true,
		},
		{
			name: "single dot is ok",
			path: "./foo",
			want: false,
		},
		{
			name: "absolute path is ok",
			path: "/foo/bar",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainsTraversal(tt.path); got != tt.want {
				t.Errorf("ContainsTraversal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSafeJoin(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		base    string
		elem    string
		wantErr bool
	}{
		{
			name:    "simple join",
			base:    tmpDir,
			elem:    "foo",
			wantErr: false,
		},
		{
			name:    "nested join",
			base:    tmpDir,
			elem:    "foo/bar",
			wantErr: false,
		},
		{
			name:    "traversal attempt",
			base:    tmpDir,
			elem:    "../../../etc/passwd",
			wantErr: true,
		},
		{
			name:    "hidden traversal",
			base:    tmpDir,
			elem:    "foo/../../bar",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := SafeJoin(tt.base, tt.elem)
			if (err != nil) != tt.wantErr {
				t.Errorf("SafeJoin() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				// Verify result is within base
				if err := ValidatePath(result, tt.base); err != nil {
					t.Errorf("SafeJoin() result escapes base: %v", err)
				}
			}
		})
	}
}

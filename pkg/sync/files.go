package sync

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CopyDir copies a directory recursively, respecting excludes.
func CopyDir(src, dst string, excludes []string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if shouldExclude(relPath, excludes) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		dstPath := filepath.Join(dst, relPath)

		// Handle symlinks - recreate them in destination
		if info.Mode()&os.ModeSymlink != 0 {
			return CopySymlink(path, dstPath)
		}

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		return CopyFile(path, dstPath)
	})
}

// CopyFile copies a single file.
func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	// Create parent dir if needed
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}

// CopySymlink copies a symlink by recreating it at the destination.
func CopySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}

	// Create parent dir if needed
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// Remove existing symlink/file if present
	_ = os.Remove(dst)

	return os.Symlink(target, dst)
}

// shouldExclude checks if a path matches any exclude pattern.
func shouldExclude(path string, excludes []string) bool {
	if path == "." {
		return false
	}
	for _, ex := range excludes {
		// Simple prefix match for directories (e.g. "sessions")
		if strings.HasPrefix(path, ex) {
			return true
		}
		// Exact match
		if path == ex {
			return true
		}
		// Glob match (simple)
		if matched, _ := filepath.Match(ex, path); matched {
			return true
		}
	}
	return false
}

// Exists checks if a file or directory exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir checks if a path is a directory.
func IsDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

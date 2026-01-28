// Package pathsec provides path security utilities for validating and
// sanitizing filesystem paths to prevent path traversal attacks.
package pathsec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath ensures path is within the allowed boundary after symlink resolution.
// It returns an error if the path escapes the allowed root directory.
func ValidatePath(path, allowedRoot string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Resolve the path, handling non-existent components
	absPath, err = resolveWithMissingComponents(absPath)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	cleanRoot, err := filepath.Abs(allowedRoot)
	if err != nil {
		return fmt.Errorf("invalid root: %w", err)
	}

	// Resolve root symlinks too
	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot resolve root: %w", err)
	}
	if err == nil {
		cleanRoot = realRoot
	}

	// Check path is within boundary
	if !strings.HasPrefix(absPath, cleanRoot+string(filepath.Separator)) && absPath != cleanRoot {
		return fmt.Errorf("path %q escapes allowed boundary %q", path, allowedRoot)
	}
	return nil
}

// resolveWithMissingComponents resolves a path that may have non-existent components.
// It resolves symlinks for the existing portion and appends the missing components.
func resolveWithMissingComponents(absPath string) (string, error) {
	// Try to resolve the full path first
	realPath, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return realPath, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	// Path doesn't exist - find the existing parent and append missing components
	var missingParts []string
	currentPath := absPath

	for {
		realPath, err := filepath.EvalSymlinks(currentPath)
		if err == nil {
			// Found an existing path - combine with missing parts
			for i := len(missingParts) - 1; i >= 0; i-- {
				realPath = filepath.Join(realPath, missingParts[i])
			}
			return realPath, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}

		// Track this missing component and try parent
		missingParts = append(missingParts, filepath.Base(currentPath))
		parent := filepath.Dir(currentPath)
		if parent == currentPath {
			// Reached root without finding existing path
			// Return the original cleaned path
			return absPath, nil
		}
		currentPath = parent
	}
}

// ValidateFileSize checks that file doesn't exceed maxBytes.
// Returns an error if the file is too large or cannot be stat'd.
func ValidateFileSize(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	if info.Size() > maxBytes {
		return fmt.Errorf("file size %d exceeds limit %d", info.Size(), maxBytes)
	}
	return nil
}

// CleanPath cleans and makes path absolute.
// It removes redundant separators and resolves . and .. elements.
func CleanPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	return filepath.Abs(cleaned)
}

// IsSubpath checks if child path is within parent directory.
// Unlike ValidatePath, this does not resolve symlinks.
func IsSubpath(parent, child string) (bool, error) {
	absParent, err := filepath.Abs(parent)
	if err != nil {
		return false, fmt.Errorf("invalid parent: %w", err)
	}

	absChild, err := filepath.Abs(child)
	if err != nil {
		return false, fmt.Errorf("invalid child: %w", err)
	}

	// Ensure parent ends with separator for proper prefix matching
	if !strings.HasSuffix(absParent, string(filepath.Separator)) {
		absParent += string(filepath.Separator)
	}

	return strings.HasPrefix(absChild, absParent) || absChild == strings.TrimSuffix(absParent, string(filepath.Separator)), nil
}

// ContainsTraversal checks if a path contains directory traversal patterns.
// This is a quick check that doesn't require filesystem access.
func ContainsTraversal(path string) bool {
	// Check for common traversal patterns
	if strings.Contains(path, "..") {
		return true
	}
	// Check for null bytes (path truncation attack)
	if strings.Contains(path, "\x00") {
		return true
	}
	return false
}

// SafeJoin joins base and elem paths, ensuring the result stays within base.
// Returns an error if the joined path would escape base.
func SafeJoin(base, elem string) (string, error) {
	// First check for obvious traversal
	if ContainsTraversal(elem) {
		return "", fmt.Errorf("path element contains traversal pattern")
	}

	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("invalid base: %w", err)
	}

	joined := filepath.Join(absBase, elem)
	cleaned := filepath.Clean(joined)

	// Verify the result is still within base
	if err := ValidatePath(cleaned, absBase); err != nil {
		return "", err
	}

	return cleaned, nil
}

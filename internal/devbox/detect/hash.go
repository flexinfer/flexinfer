package detect

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// depFiles are the dependency files whose content determines the fingerprint hash.
var depFiles = []string{
	"go.mod", "go.sum",
	"pyproject.toml", "uv.lock", "poetry.lock",
	"package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json",
	"Cargo.toml", "Cargo.lock",
	".devbox.yaml",
}

// computeHash computes a SHA-256 hash of all dependency files present in the project.
func computeHash(projectDir string, _ *EnvFingerprint) (string, error) {
	h := sha256.New()

	// Sort file names for deterministic hashing
	files := make([]string, len(depFiles))
	copy(files, depFiles)
	sort.Strings(files)

	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue // skip missing files
		}
		// Write file name as separator to avoid collisions
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}

	return fmt.Sprintf("%x", h.Sum(nil))[:12], nil
}

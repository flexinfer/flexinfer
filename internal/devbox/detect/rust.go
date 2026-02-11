package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// detectRust checks for Rust project indicators and populates the fingerprint.
func detectRust(projectDir string, fp *EnvFingerprint) {
	cargoPath := filepath.Join(projectDir, "Cargo.toml")
	data, err := os.ReadFile(cargoPath)
	if err != nil {
		return
	}

	spec := LanguageSpec{
		Language:   "rust",
		Version:    parseRustEdition(string(data)),
		DepFile:    "Cargo.toml",
		DepManager: "cargo",
	}

	if fileExists(filepath.Join(projectDir, "Cargo.lock")) {
		spec.LockFile = "Cargo.lock"
	}

	fp.Languages = append(fp.Languages, spec)
}

// parseRustEdition extracts the Rust edition from Cargo.toml.
func parseRustEdition(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "edition") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) < 2 {
				continue
			}
			return strings.Trim(parts[1], ` "'`)
		}
	}
	return ""
}

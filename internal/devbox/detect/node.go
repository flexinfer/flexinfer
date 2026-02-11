package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// detectNode checks for Node.js project indicators and populates the fingerprint.
func detectNode(projectDir string, fp *EnvFingerprint) {
	pkgPath := filepath.Join(projectDir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		return
	}

	spec := LanguageSpec{
		Language:   "node",
		Version:    parseNodeVersion(string(data)),
		DepFile:    "package.json",
		DepManager: detectNodeDepManager(projectDir),
	}

	switch spec.DepManager {
	case "pnpm":
		if fileExists(filepath.Join(projectDir, "pnpm-lock.yaml")) {
			spec.LockFile = "pnpm-lock.yaml"
		}
	case "yarn":
		if fileExists(filepath.Join(projectDir, "yarn.lock")) {
			spec.LockFile = "yarn.lock"
		}
	default:
		if fileExists(filepath.Join(projectDir, "package-lock.json")) {
			spec.LockFile = "package-lock.json"
		}
	}

	fp.Languages = append(fp.Languages, spec)
}

// parseNodeVersion extracts the Node.js version from package.json engines field.
func parseNodeVersion(content string) string {
	var pkg struct {
		Engines map[string]string `json:"engines"`
	}
	if err := json.Unmarshal([]byte(content), &pkg); err != nil {
		return ""
	}
	if v, ok := pkg.Engines["node"]; ok {
		return strings.Trim(v, ">=^~ ")
	}
	return ""
}

// detectNodeDepManager determines which Node package manager is in use.
func detectNodeDepManager(projectDir string) string {
	if fileExists(filepath.Join(projectDir, "pnpm-lock.yaml")) {
		return "pnpm"
	}
	if fileExists(filepath.Join(projectDir, "yarn.lock")) {
		return "yarn"
	}
	return "npm"
}

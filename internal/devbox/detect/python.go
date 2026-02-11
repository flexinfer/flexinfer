package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// detectPython checks for Python project indicators and populates the fingerprint.
func detectPython(projectDir string, fp *EnvFingerprint) {
	pyprojectPath := filepath.Join(projectDir, "pyproject.toml")
	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		// Fall back to requirements.txt
		if fileExists(filepath.Join(projectDir, "requirements.txt")) {
			spec := LanguageSpec{
				Language:   "python",
				DepFile:    "requirements.txt",
				DepManager: "pip",
			}
			fp.Languages = append(fp.Languages, spec)
		}
		return
	}

	spec := LanguageSpec{
		Language:   "python",
		Version:    parsePythonVersion(string(data)),
		DepFile:    "pyproject.toml",
		DepManager: detectPythonDepManager(projectDir),
	}

	switch spec.DepManager {
	case "uv":
		if fileExists(filepath.Join(projectDir, "uv.lock")) {
			spec.LockFile = "uv.lock"
		}
	case "poetry":
		if fileExists(filepath.Join(projectDir, "poetry.lock")) {
			spec.LockFile = "poetry.lock"
		}
	}

	fp.Languages = append(fp.Languages, spec)
}

// parsePythonVersion extracts python version from pyproject.toml.
// Looks for requires-python = ">=3.11" or similar patterns.
func parsePythonVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "requires-python") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) < 2 {
				continue
			}
			ver := strings.Trim(parts[1], ` "'><=~!`)
			return ver
		}
	}
	return ""
}

// detectPythonDepManager determines which Python dependency manager is in use.
func detectPythonDepManager(projectDir string) string {
	if fileExists(filepath.Join(projectDir, "uv.lock")) {
		return "uv"
	}
	if fileExists(filepath.Join(projectDir, "poetry.lock")) {
		return "poetry"
	}
	// Check pyproject.toml for [tool.poetry] section
	data, err := os.ReadFile(filepath.Join(projectDir, "pyproject.toml"))
	if err == nil && strings.Contains(string(data), "[tool.poetry]") {
		return "poetry"
	}
	return "uv"
}

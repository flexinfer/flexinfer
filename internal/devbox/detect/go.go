package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// detectGo checks for Go project indicators and populates the fingerprint.
func detectGo(projectDir string, fp *EnvFingerprint) {
	goModPath := filepath.Join(projectDir, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return
	}

	spec := LanguageSpec{
		Language:   "go",
		Version:    parseGoVersion(string(data)),
		DepFile:    "go.mod",
		DepManager: "go",
	}

	if fileExists(filepath.Join(projectDir, "go.sum")) {
		spec.LockFile = "go.sum"
	}

	spec.Tools = detectGoTools(projectDir)
	fp.Languages = append(fp.Languages, spec)
}

// parseGoVersion extracts the Go version from go.mod content.
func parseGoVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") {
			return strings.TrimPrefix(line, "go ")
		}
	}
	return ""
}

// detectGoTools looks for common Go tools referenced in the Makefile.
func detectGoTools(projectDir string) []string {
	data, err := os.ReadFile(filepath.Join(projectDir, "Makefile"))
	if err != nil {
		return nil
	}
	content := string(data)
	var tools []string
	toolMap := map[string]string{
		"golangci-lint": "github.com/golangci/golangci-lint/cmd/golangci-lint",
		"goimports":     "golang.org/x/tools/cmd/goimports",
		"gosec":         "github.com/securego/gosec/v2/cmd/gosec",
	}
	for name := range toolMap {
		if strings.Contains(content, name) {
			tools = append(tools, name)
		}
	}
	return tools
}

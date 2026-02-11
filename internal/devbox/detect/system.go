package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// detectSystemDeps extracts system-level dependencies from Dockerfiles and Makefiles.
func detectSystemDeps(projectDir string, fp *EnvFingerprint) {
	deps := extractFromDockerfile(projectDir)
	fp.SystemDeps = appendUnique(fp.SystemDeps, deps...)
}

// extractFromDockerfile parses apt-get install lines from existing Dockerfiles.
func extractFromDockerfile(projectDir string) []string {
	for _, name := range []string{"Dockerfile", "Dockerfile.dev"} {
		path := filepath.Join(projectDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return parseAptPackages(string(data))
	}
	return nil
}

// parseAptPackages extracts package names from apt-get install lines in Dockerfile content.
func parseAptPackages(content string) []string {
	var packages []string
	lines := strings.Split(content, "\n")

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, "apt-get install") && !strings.Contains(line, "apk add") {
			continue
		}

		// Collect continuation lines
		full := line
		for strings.HasSuffix(strings.TrimSpace(full), "\\") && i+1 < len(lines) {
			i++
			full = strings.TrimSuffix(strings.TrimSpace(full), "\\") + " " + strings.TrimSpace(lines[i])
		}

		packages = append(packages, extractPackageNames(full)...)
	}
	return packages
}

// extractPackageNames pulls package names from an apt-get install or apk add command.
func extractPackageNames(line string) []string {
	var packages []string
	// Find the install command and extract what follows
	var start int
	if idx := strings.Index(line, "apt-get install"); idx >= 0 {
		start = idx + len("apt-get install")
	} else if idx := strings.Index(line, "apk add"); idx >= 0 {
		start = idx + len("apk add")
	} else {
		return nil
	}

	parts := strings.Fields(line[start:])
	for _, p := range parts {
		p = strings.TrimSpace(p)
		// Skip flags and common non-package tokens
		if p == "" || strings.HasPrefix(p, "-") || strings.HasPrefix(p, "&&") ||
			strings.HasPrefix(p, "||") || strings.HasPrefix(p, ";") ||
			strings.HasPrefix(p, "\\") || strings.HasPrefix(p, "#") ||
			p == "RUN" || p == "rm" || p == "rf" {
			continue
		}
		packages = append(packages, p)
	}
	return packages
}

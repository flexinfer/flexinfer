package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// Fingerprint analyzes a project directory and returns its environment fingerprint.
// If a .devcontainer/devcontainer.json is present, it takes priority over auto-detection.
func Fingerprint(projectDir string) (*EnvFingerprint, error) {
	fp := &EnvFingerprint{
		ProjectDir:  projectDir,
		ProjectName: filepath.Base(projectDir),
		Languages:   make([]LanguageSpec, 0, 4),
		EnvVars:     make(map[string]string),
	}

	// Check for devcontainer.json first — takes priority for image/build config
	dc, err := loadDevContainer(projectDir)
	if err == nil && dc != nil {
		fp.DevContainer = dc
		for k, v := range dc.ContainerEnv {
			fp.EnvVars[k] = v
		}
	}

	// Always detect languages for metadata even with devcontainer
	detectGo(projectDir, fp)
	detectPython(projectDir, fp)
	detectNode(projectDir, fp)
	detectRust(projectDir, fp)
	detectSystemDeps(projectDir, fp)

	manifest, err := loadManifest(projectDir)
	if err == nil && manifest != nil {
		fp.Overrides = manifest
		fp.SystemDeps = appendUnique(fp.SystemDeps, manifest.SystemDeps...)
		for k, v := range manifest.Env {
			fp.EnvVars[k] = v
		}
	}

	detectBuildTargets(projectDir, fp)

	hash, err := computeHash(projectDir, fp)
	if err != nil {
		return nil, err
	}
	fp.Hash = hash

	return fp, nil
}

// detectBuildTargets looks for a Makefile and extracts target names.
func detectBuildTargets(projectDir string, fp *EnvFingerprint) {
	data, err := os.ReadFile(filepath.Join(projectDir, "Makefile"))
	if err != nil {
		return
	}
	targets := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) == 0 || line[0] == '#' || line[0] == '\t' || line[0] == ' ' || line[0] == '.' {
			continue
		}
		if idx := strings.Index(line, ":"); idx > 0 {
			target := strings.TrimSpace(line[:idx])
			if isSimpleTarget(target) {
				targets = append(targets, target)
			}
		}
	}
	fp.BuildTargets = targets
}

// isSimpleTarget returns true if the target name is a simple word (no variables or patterns).
func isSimpleTarget(t string) bool {
	for _, c := range t {
		if c == '$' || c == '%' || c == '/' {
			return false
		}
	}
	return len(t) > 0
}

// fileExists checks if a file exists at the given path.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// appendUnique appends items to a slice, skipping duplicates.
func appendUnique(dst []string, items ...string) []string {
	seen := make(map[string]bool, len(dst))
	for _, v := range dst {
		seen[v] = true
	}
	for _, item := range items {
		if !seen[item] {
			dst = append(dst, item)
			seen[item] = true
		}
	}
	return dst
}

package dockerfile

import (
	"bytes"
	"fmt"

	"github.com/crb2nu/loom/internal/devbox/detect"
)

// Generate produces a Dockerfile from an environment fingerprint.
func Generate(fp *detect.EnvFingerprint) ([]byte, error) {
	if len(fp.Languages) == 0 {
		return nil, fmt.Errorf("no languages detected in %s", fp.ProjectDir)
	}

	// Override base image from manifest
	if fp.Overrides != nil && fp.Overrides.BaseImage != "" {
		return generateCustomBase(fp)
	}

	// Single language → use dedicated template
	if len(fp.Languages) == 1 {
		return generateSingle(fp.Languages[0], fp)
	}

	// Multi-language → compose
	return generateMulti(fp)
}

// generateSingle renders a single-language Dockerfile.
func generateSingle(spec detect.LanguageSpec, fp *detect.EnvFingerprint) ([]byte, error) {
	var buf bytes.Buffer

	switch spec.Language {
	case "go":
		data := buildGoData(spec, fp)
		if err := goTemplate.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("go template: %w", err)
		}
	case "python":
		data := buildPythonData(spec, fp)
		if err := pythonTemplate.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("python template: %w", err)
		}
	case "node":
		data := buildNodeData(spec, fp)
		if err := nodeTemplate.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("node template: %w", err)
		}
	case "rust":
		data := templateData{
			Hash:          fp.Hash,
			SystemDeps:    fp.SystemDeps,
			Env:           fp.EnvVars,
			SetupCommands: setupCommands(fp),
		}
		if err := rustTemplate.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("rust template: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported language: %s", spec.Language)
	}

	return buf.Bytes(), nil
}

// generateMulti renders a multi-language Dockerfile.
func generateMulti(fp *detect.EnvFingerprint) ([]byte, error) {
	data := buildMultiData(fp)
	var buf bytes.Buffer
	if err := multiTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("multi template: %w", err)
	}
	return buf.Bytes(), nil
}

// generateCustomBase renders a Dockerfile with a user-specified base image.
func generateCustomBase(fp *detect.EnvFingerprint) ([]byte, error) {
	data := multiTemplateData{
		Hash:          fp.Hash,
		BaseImage:     fp.Overrides.BaseImage,
		SystemDeps:    fp.SystemDeps,
		Env:           fp.EnvVars,
		SetupCommands: setupCommands(fp),
	}

	// Try to detect the package manager from the base image name
	if containsAny(fp.Overrides.BaseImage, "alpine") {
		data.PackageManager = "apk add --no-cache"
		data.PackageInstallCmd = "git make bash curl"
	} else {
		data.PackageManager = "apt-get update && apt-get install -y --no-install-recommends"
		data.PackageInstallCmd = "git make curl ca-certificates"
	}

	var buf bytes.Buffer
	if err := multiTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("custom base template: %w", err)
	}
	return buf.Bytes(), nil
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

package dockerfile

import "github.com/crb2nu/loom/internal/devbox/detect"

// templateData holds parameters for Go templates.
type templateData struct {
	Hash       string
	SystemDeps []string
	Env        map[string]string

	// Go-specific
	GoVersion string
	Tools     []string

	// Python-specific
	PythonVersion string

	// Node-specific
	NodeVersion string

	// Shared
	DepManager    string
	SetupCommands []string
}

// multiTemplateData holds parameters for multi-language Dockerfile generation.
type multiTemplateData struct {
	Hash                 string
	BaseImage            string
	PackageManager       string
	PackageInstallCmd    string
	SystemDeps           []string
	ExtraPackageManagers []string
	DepSteps             []string
	SetupCommands        []string
	Env                  map[string]string
}

// buildGoData creates template data from a Go LanguageSpec.
func buildGoData(spec detect.LanguageSpec, fp *detect.EnvFingerprint) templateData {
	version := spec.Version
	if version == "" {
		version = "1.25"
	}

	var tools []string
	for _, t := range spec.Tools {
		switch t {
		case "golangci-lint":
			tools = append(tools, "github.com/golangci/golangci-lint/cmd/golangci-lint")
		case "goimports":
			tools = append(tools, "golang.org/x/tools/cmd/goimports")
		case "gosec":
			tools = append(tools, "github.com/securego/gosec/v2/cmd/gosec")
		}
	}

	return templateData{
		Hash:          fp.Hash,
		GoVersion:     version,
		SystemDeps:    fp.SystemDeps,
		Tools:         tools,
		Env:           fp.EnvVars,
		SetupCommands: setupCommands(fp),
	}
}

// buildPythonData creates template data from a Python LanguageSpec.
func buildPythonData(spec detect.LanguageSpec, fp *detect.EnvFingerprint) templateData {
	version := spec.Version
	if version == "" {
		version = "3.11"
	}

	return templateData{
		Hash:          fp.Hash,
		PythonVersion: version,
		SystemDeps:    fp.SystemDeps,
		DepManager:    spec.DepManager,
		Env:           fp.EnvVars,
		SetupCommands: setupCommands(fp),
	}
}

// buildNodeData creates template data from a Node LanguageSpec.
func buildNodeData(spec detect.LanguageSpec, fp *detect.EnvFingerprint) templateData {
	version := spec.Version
	if version == "" {
		version = "20"
	}

	return templateData{
		Hash:          fp.Hash,
		NodeVersion:   version,
		SystemDeps:    fp.SystemDeps,
		DepManager:    spec.DepManager,
		Env:           fp.EnvVars,
		SetupCommands: setupCommands(fp),
	}
}

// buildMultiData creates template data for a multi-language project.
func buildMultiData(fp *detect.EnvFingerprint) multiTemplateData {
	data := multiTemplateData{
		Hash:          fp.Hash,
		SystemDeps:    fp.SystemDeps,
		Env:           fp.EnvVars,
		SetupCommands: setupCommands(fp),
	}

	// Use Go as the base if present (Alpine-based), otherwise Debian slim
	var hasGo, hasNode, hasPython bool
	for _, lang := range fp.Languages {
		switch lang.Language {
		case "go":
			hasGo = true
		case "node":
			hasNode = true
		case "python":
			hasPython = true
		}
	}

	if hasGo {
		goVer := "1.25"
		for _, l := range fp.Languages {
			if l.Language == "go" && l.Version != "" {
				goVer = l.Version
				break
			}
		}
		data.BaseImage = "golang:" + goVer + "-alpine"
		data.PackageManager = "apk add --no-cache"
		data.PackageInstallCmd = "ca-certificates git make bash curl"

		data.DepSteps = append(data.DepSteps, "COPY go.mod go.sum* ./\nRUN go mod download || true")

		if hasNode {
			data.PackageInstallCmd += " nodejs npm"
			for _, l := range fp.Languages {
				if l.Language == "node" && l.DepManager == "pnpm" {
					data.ExtraPackageManagers = append(data.ExtraPackageManagers,
						"corepack enable && corepack prepare pnpm@latest --activate")
					break
				}
			}
			data.DepSteps = append(data.DepSteps, nodeDepStep(fp))
		}
		if hasPython {
			data.PackageInstallCmd += " python3 py3-pip"
			data.DepSteps = append(data.DepSteps, pythonDepStep(fp))
		}
	} else {
		data.BaseImage = "debian:bookworm-slim"
		data.PackageManager = "apt-get update && apt-get install -y --no-install-recommends"
		data.PackageInstallCmd = "git make curl ca-certificates build-essential"

		if hasNode {
			data.PackageInstallCmd += " nodejs npm"
			data.DepSteps = append(data.DepSteps, nodeDepStep(fp))
		}
		if hasPython {
			data.PackageInstallCmd += " python3 python3-pip python3-venv"
			data.DepSteps = append(data.DepSteps, pythonDepStep(fp))
		}
	}

	return data
}

// nodeDepStep returns Dockerfile lines for Node.js dependency installation.
func nodeDepStep(fp *detect.EnvFingerprint) string {
	for _, l := range fp.Languages {
		if l.Language == "node" {
			switch l.DepManager {
			case "pnpm":
				return "COPY package.json pnpm-lock.yaml* ./\nRUN pnpm install --frozen-lockfile 2>/dev/null || pnpm install || true"
			case "yarn":
				return "COPY package.json yarn.lock* ./\nRUN yarn install --frozen-lockfile 2>/dev/null || yarn install || true"
			default:
				return "COPY package.json package-lock.json* ./\nRUN npm ci 2>/dev/null || npm install || true"
			}
		}
	}
	return ""
}

// pythonDepStep returns Dockerfile lines for Python dependency installation.
func pythonDepStep(fp *detect.EnvFingerprint) string {
	for _, l := range fp.Languages {
		if l.Language == "python" {
			switch l.DepManager {
			case "uv":
				return "RUN pip install --no-cache-dir uv\nCOPY pyproject.toml uv.lock* ./\nRUN uv sync --frozen 2>/dev/null || uv sync || true"
			case "poetry":
				return "RUN pip install --no-cache-dir poetry\nCOPY pyproject.toml poetry.lock* ./\nRUN poetry install --no-interaction --no-root 2>/dev/null || true"
			default:
				return "COPY requirements.txt* ./\nRUN pip install --no-cache-dir -r requirements.txt 2>/dev/null || true"
			}
		}
	}
	return ""
}

// setupCommands returns setup commands from manifest overrides.
func setupCommands(fp *detect.EnvFingerprint) []string {
	if fp.Overrides == nil {
		return nil
	}
	return fp.Overrides.Setup
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "0.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp,
		shutdownTracer,
		err := mcpotel.
		InitTracer(ctx, "mcp-release",
			logger,
		)
	if err !=
		nil {
		logger.
			Warn("OTel tracer init failed",

				"error", err)
	}
	defer func() {
		_ = shutdownTracer(ctx)
	}()
	tracer := mcpotel.
		Tracer(tp, "mcp-release")

	logger.Info("starting server", "name", "mcp-release", "version", version)

	server := mcp.NewServer("mcp-release", version)
	server.SetInstructions("Multi-channel release orchestration. Tools: release_validate, release_changelog, release_status")

	server.AddTool(mcp.Tool{
		Name:        "release_validate",
		Description: "Validate version alignment across package.json, Cargo.toml, and tauri.conf.json in a Tauri project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project_dir": map[string]any{
					"type":        "string",
					"description": "Path to the project root directory",
				},
			},
			Required: []string{"project_dir"},
		},
	}, mcpotel.TracedToolHandler(tracer, "release_validate", handleValidate))

	server.AddTool(mcp.Tool{
		Name:        "release_changelog",
		Description: "Generate a changelog from git history between two tags",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project_dir": map[string]any{
					"type":        "string",
					"description": "Path to the git repository",
				},
				"from_tag": map[string]any{
					"type":        "string",
					"description": "Start tag (exclusive)",
				},
				"to_tag": map[string]any{
					"type":        "string",
					"description": "End tag (inclusive). Defaults to HEAD.",
				},
			},
			Required: []string{"project_dir", "from_tag"},
		},
	}, mcpotel.TracedToolHandler(tracer, "release_changelog", handleChangelog))

	server.AddTool(mcp.Tool{
		Name:        "release_status",
		Description: "Check release status across distribution channels (GitHub Releases, itch.io)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project_dir": map[string]any{
					"type":        "string",
					"description": "Path to the project root (reads version from package.json)",
				},
				"github_repo": map[string]any{
					"type":        "string",
					"description": "GitHub repo in owner/name format (e.g., streamslate/streamslate)",
				},
				"itchio_project": map[string]any{
					"type":        "string",
					"description": "itch.io project identifier (e.g., caedus90/streamslate)",
				},
			},
			Required: []string{"project_dir"},
		},
	}, mcpotel.TracedToolHandler(tracer, "release_status", handleStatus))

	return server.Run(ctx)
}

type packageJSON struct {
	Version string `json:"version"`
}

type tauriConf struct {
	Package struct {
		Version string `json:"version"`
	} `json:"package"`
}

func readVersion(projectDir string) (map[string]string, error) {
	versions := make(map[string]string)

	// package.json
	pkgData, err := os.ReadFile(filepath.Join(projectDir, "package.json"))
	if err == nil {
		var pkg packageJSON
		if json.Unmarshal(pkgData, &pkg) == nil {
			versions["package.json"] = pkg.Version
		}
	}

	// Cargo.toml
	cargoData, err := os.ReadFile(filepath.Join(projectDir, "src-tauri", "Cargo.toml"))
	if err == nil {
		for _, line := range strings.Split(string(cargoData), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "version") && strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					v := strings.TrimSpace(parts[1])
					v = strings.Trim(v, "\"")
					versions["Cargo.toml"] = v
					break
				}
			}
		}
	}

	// tauri.conf.json
	tauriData, err := os.ReadFile(filepath.Join(projectDir, "src-tauri", "tauri.conf.json"))
	if err == nil {
		var conf tauriConf
		if json.Unmarshal(tauriData, &conf) == nil && conf.Package.Version != "" {
			versions["tauri.conf.json"] = conf.Package.Version
		}
	}

	return versions, nil
}

func handleValidate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	projectDir := v.Required("project_dir")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	versions, err := readVersion(projectDir)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to read versions: %w", err)), nil
	}

	if len(versions) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no version files found in %s", projectDir)), nil
	}

	// Check alignment
	aligned := true
	var firstVersion string
	for _, ver := range versions {
		if firstVersion == "" {
			firstVersion = ver
		} else if ver != firstVersion {
			aligned = false
			break
		}
	}

	return mcp.JSONResult(map[string]any{
		"aligned":  aligned,
		"version":  firstVersion,
		"versions": versions,
	})
}

func handleChangelog(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	projectDir := v.Required("project_dir")
	fromTag := v.Required("from_tag")
	toTag := v.String("to_tag", "HEAD")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rangeSpec := fmt.Sprintf("%s..%s", fromTag, toTag)
	cmd := exec.CommandContext(ctx, "git", "log", rangeSpec, "--pretty=format:%h %s", "--no-merges")
	cmd.Dir = projectDir

	output, err := cmd.Output()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("git log failed: %w", err)), nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	commits := make([]map[string]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) == 2 {
			commits = append(commits, map[string]string{
				"hash":    parts[0],
				"message": parts[1],
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"from":    fromTag,
		"to":      toTag,
		"count":   len(commits),
		"commits": commits,
	})
}

func handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	projectDir := v.Required("project_dir")
	githubRepo := v.String("github_repo", "")
	itchioProject := v.String("itchio_project", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result := map[string]any{}

	// Local version
	versions, _ := readVersion(projectDir)
	if ver, ok := versions["package.json"]; ok {
		result["local_version"] = ver
	}

	// GitHub release status
	if githubRepo != "" {
		ghCtx, ghCancel := context.WithTimeout(ctx, 15*time.Second)
		defer ghCancel()

		cmd := exec.CommandContext(ghCtx, "gh", "release", "view", "--repo", githubRepo, "--json", "tagName,isDraft,isPrerelease,publishedAt")
		output, err := cmd.Output()
		if err == nil {
			var release map[string]any
			if json.Unmarshal(output, &release) == nil {
				result["github"] = release
			}
		} else {
			result["github"] = map[string]any{"error": "failed to query GitHub release"}
		}
	}

	// itch.io status
	if itchioProject != "" {
		butlerPath, err := exec.LookPath("butler")
		if err == nil {
			itchCtx, itchCancel := context.WithTimeout(ctx, 15*time.Second)
			defer itchCancel()

			cmd := exec.CommandContext(itchCtx, butlerPath, "status", itchioProject)
			output, err := cmd.Output()
			if err == nil {
				result["itchio"] = strings.TrimSpace(string(output))
			} else {
				result["itchio"] = map[string]any{"error": "butler status failed"}
			}
		} else {
			result["itchio"] = map[string]any{"error": "butler not installed"}
		}
	}

	return mcp.JSONResult(result)
}

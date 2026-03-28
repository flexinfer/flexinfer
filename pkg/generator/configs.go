package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/validator"
)

func normalizeLoomBinary(loomBinary string) string {
	if strings.TrimSpace(loomBinary) == "" {
		return "loom"
	}
	return strings.TrimSpace(loomBinary)
}

func isExecutableFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	return info.Mode()&0o111 != 0
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

const (
	defaultHubWrapperCommand      = "mcp-hub-wrapper"
	hubWrapperOverrideEnv         = "LOOM_MCP_HUB_WRAPPER"
	hubWrapperHealthCheckTimeout  = 3 * time.Second
	hubWrapperLegacyRelativePath  = "scripts/mcp/hub_wrapper.sh"
	hubWrapperWorkspaceBinaryPath = "services/loom-core/bin/mcp-hub-wrapper"
)

var (
	hubWrapperLookPath      = exec.LookPath
	hubWrapperCommandRunner = exec.CommandContext
)

// GenerateConfigs generates MCP client configurations.
// registryPath is used to determine the repo root for resolving ${repo} tokens.
func GenerateConfigs(reg *registry.Registry, outputDir string, targets []string, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	return GenerateConfigsWithPath(reg, "", outputDir, targets, hubMode, hubURL, loomMode, loomBinary, resolveSecrets)
}

func inferRegistryRoot(registryPath string) string {
	if registryPath == "" {
		return ""
	}
	// Typical layout: <root>/mcp/context/registry.yaml
	// We want <root> as the base for resolving relative paths like scripts/...
	contextDir := filepath.Dir(registryPath) // .../mcp/context
	mcpDir := filepath.Dir(contextDir)       // .../mcp
	if filepath.Base(contextDir) == "context" && filepath.Base(mcpDir) == "mcp" {
		return filepath.Dir(mcpDir) // .../<root>
	}
	return filepath.Dir(registryPath)
}

// InferWorkspaceRoot walks up from candidate looking for the workspace root
// (identified by a services/loom-core subdirectory). Returns candidate as
// fallback if no match is found.
func InferWorkspaceRoot(candidate string) string {
	if candidate == "" {
		return ""
	}
	try := func(dir string) bool {
		if dir == "" {
			return false
		}
		if _, err := os.Stat(filepath.Join(dir, "services", "loom-core")); err != nil {
			return false
		}
		return true
	}
	if try(candidate) {
		return candidate
	}

	// Walk upwards a few levels to handle cases where the registry lives under
	// platform/gitops but ${repo} should point at the monorepo root.
	dir := candidate
	for range 6 {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		if try(dir) {
			return dir
		}
	}
	return candidate
}

// GenerateConfigsWithPath generates MCP client configurations with an explicit registry path.
// GenerateParams bundles the parameters that all config generators share.
// Reduces 10-parameter function signatures to a single struct.
type GenerateParams struct {
	Reg            *registry.Registry
	OutputDir      string
	Target         string
	Profile        *PlatformProfile
	HubMode        bool
	HubURL         string
	LoomMode       bool
	LoomBinary     string
	WorkspaceRoot  string
	RegistryRoot   string
	ResolveSecrets bool
}

// buildTargets resolves the MCP server specs for this target.
func (p *GenerateParams) buildTargets() (map[string]*registry.TargetSpec, error) {
	return buildTargetMap(p.Reg, p.Target, p.Profile, p.HubMode, p.HubURL, p.LoomMode, p.LoomBinary, p.WorkspaceRoot, p.RegistryRoot, p.ResolveSecrets)
}

// destDir returns the platform-specific output directory.
func (p *GenerateParams) destDir() string {
	return filepath.Join(p.OutputDir, p.Target)
}

// filePerm returns the output file permission (restrictive when secrets are resolved).
func (p *GenerateParams) filePerm() os.FileMode {
	if p.ResolveSecrets {
		fmt.Fprintf(os.Stderr, "Note: resolved secret templates for %s (file contains sensitive values)\n", p.Target)
		return 0600
	}
	return 0644
}

func GenerateConfigsWithPath(reg *registry.Registry, registryPath string, outputDir string, targets []string, hubMode bool, hubURL string, loomMode bool, loomBinary string, resolveSecrets bool) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if len(targets) == 0 || targets[0] == "all" {
		targets = AllPlatformNames()
	}

	// Resolve repo root from registry path
	workspaceRoot := InferWorkspaceRoot(registry.GetRepoRoot(registryPath))
	registryRoot := inferRegistryRoot(registryPath)

	for _, target := range targets {
		profile, profileErr := GetPlatformProfile(target)
		if profileErr != nil {
			return fmt.Errorf("unknown target %q: %w", target, profileErr)
		}

		params := &GenerateParams{
			Reg:            reg,
			OutputDir:      outputDir,
			Target:         target,
			Profile:        profile,
			HubMode:        hubMode,
			HubURL:         hubURL,
			LoomMode:       loomMode,
			LoomBinary:     loomBinary,
			WorkspaceRoot:  workspaceRoot,
			RegistryRoot:   registryRoot,
			ResolveSecrets: resolveSecrets,
		}

		var err error
		switch profile.ConfigFormat {
		case "json":
			err = generateJSONConfig(params)
		case "toml":
			err = generateTomlConfig(params)
		default:
			err = fmt.Errorf("unsupported config format %q for %s", profile.ConfigFormat, target)
		}
		if err != nil {
			return fmt.Errorf("generate %s: %w", target, err)
		}

		// Generate lifecycle hook configs for platforms that support them.
		if err := generateHooksConfig(reg, outputDir, target, profile, loomBinary); err != nil {
			return fmt.Errorf("generate hooks for %s: %w", target, err)
		}
	}

	// Emit sandbox policy advisory file if defined in registry.
	if reg.SandboxPolicy != nil {
		if err := emitSandboxPolicy(reg.SandboxPolicy, outputDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: sandbox policy emission failed: %v\n", err)
		}
	}

	// Validate generated configs
	homeDir, _ := os.UserHomeDir()
	v := validator.New(workspaceRoot, homeDir)
	results, err := v.ValidateGenerated(outputDir, targets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: validation check failed: %v\n", err)
	} else {
		for _, result := range results {
			if result.HasErrors() || result.HasWarnings() {
				for _, verr := range result.Errors {
					// Skip plaintext secret warnings when secrets were intentionally resolved
					if resolveSecrets && strings.Contains(verr.Message, "plaintext secret") {
						continue
					}
					if verr.Severity == validator.SeverityError {
						fmt.Fprintf(os.Stderr, "ERROR [%s] %s: %s\n", result.Target, verr.Field, verr.Message)
					} else {
						fmt.Fprintf(os.Stderr, "WARN  [%s] %s: %s\n", result.Target, verr.Field, verr.Message)
					}
				}
			}
		}
	}

	return nil
}

func sortStrings(s []string) {
	sort.Strings(s)
}

// registryPlatformPerms returns the PlatformPermission for a given platform,
// or nil if the registry has no entry.
func registryPlatformPerms(reg *registry.Registry, platform string) *registry.PlatformPermission {
	if reg == nil || reg.PlatformPermissions == nil {
		return nil
	}
	return reg.PlatformPermissions[platform]
}

func coerceStringSlice(v any) []string {
	switch vv := v.(type) {
	case nil:
		return nil
	case []string:
		return vv
	case []any:
		out := make([]string, 0, len(vv))
		for _, x := range vv {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

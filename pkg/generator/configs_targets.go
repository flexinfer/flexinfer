package generator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/templatevars"
)

func buildTargetMap(reg *registry.Registry, target string, profile *PlatformProfile, hubMode bool, hubURL string, loomMode bool, loomBinary string, workspaceRoot string, registryRoot string, resolveSecrets bool) (map[string]*registry.TargetSpec, error) {
	if loomMode {
		cmd := normalizeLoomBinary(loomBinary)
		args := []any{"proxy"}
		// Apply proxy args from profile (agent-hint, tool-profile, max-tools).
		if profile != nil {
			lp := profile.LoomProxy
			if lp.AgentHint != "" {
				args = append(args, "--agent-hint", lp.AgentHint)
			}
			if lp.ToolProfile != "" {
				args = append(args, "--tool-profile", lp.ToolProfile)
			}
			if lp.MaxTools > 0 {
				args = append(args, "--max-tools", fmt.Sprintf("%d", lp.MaxTools))
			}
		}
		return map[string]*registry.TargetSpec{
			"loom": {
				Description: "Loom MCP proxy - unified access to all servers",
				Command:     cmd,
				Args:        args,
				Hint:        "network",
				Timeout:     600,
				AlwaysAllow: []string{"*"},
				Type:        "stdio",
			},
		}, nil
	}

	resolved := make(map[string]*registry.TargetSpec)
	repoPath := workspaceRoot // Use provided workspace root instead of cwd
	hubWrapper := ""

	if hubMode {
		var err error
		hubWrapper, err = resolveHubWrapper(workspaceRoot, registryRoot)
		if err != nil {
			return nil, fmt.Errorf("resolve hub wrapper: %w", err)
		}
	}

	// Create expander lazily if secrets resolution is requested
	var expander *templatevars.Expander
	if resolveSecrets {
		expander = templatevars.New(
			templatevars.WithRegistry(reg),
			templatevars.WithLazySecrets(),
		)
	}

	// Load catalog state to filter disabled servers
	catalogState, _ := registry.LoadCatalogState()

	for _, server := range reg.Servers {
		if catalogState != nil && catalogState.IsDisabled(server.Name) {
			continue
		}

		spec, err := reg.GetServerSpec(server.Name, target)
		if err != nil {
			continue // Skip if not found (shouldn't happen with GetServerSpec logic)
		}

		// Resolve path tokens (${repo}, ${HOME})
		spec.Command = ResolveCommand(spec.Command, repoPath, registryRoot, "local")
		resolvedArgs := ResolveArgs(spec.Args, repoPath, registryRoot, "local")
		spec.Args = make([]any, len(resolvedArgs))
		for i, v := range resolvedArgs {
			spec.Args[i] = v
		}
		newEnv := make(map[string]string)
		for k, v := range spec.Env {
			newEnv[k] = ResolveTokens(v, repoPath, "local")
		}
		spec.Env = newEnv

		// Resolve secret templates if requested (for platforms that can't resolve at runtime)
		if resolveSecrets && expander != nil {
			spec.Env = expander.ExpandMap(spec.Env)
			for i, arg := range spec.Args {
				if s, ok := arg.(string); ok {
					spec.Args[i] = expander.Expand(s)
				}
			}
		}

		if hubMode && !server.IsLocalOnly() {
			// Convert to hub mode
			spec = convertToHubMode(spec, server.Name, hubURL, target, hubWrapper)
		}

		if spec.Command != "" {
			resolved[server.Name] = spec
		}
	}
	return resolved, nil
}

func convertToHubMode(spec *registry.TargetSpec, serverName, hubURL, profile string, wrapper string) *registry.TargetSpec {
	return &registry.TargetSpec{
		Description: spec.Description,
		Command:     strings.TrimSpace(wrapper),
		Args:        []any{serverName, "--profile", profile, "--hub-url", hubURL},
		Env:         spec.Env,
		Hint:        spec.Hint,
		Timeout:     spec.Timeout,
		AlwaysAllow: spec.AlwaysAllow,
		Type:        spec.Type,
	}
}

func resolveHubWrapper(workspaceRoot string, registryRoot string) (string, error) {
	candidates := hubWrapperCandidates(workspaceRoot, registryRoot)
	seen := make(map[string]struct{}, len(candidates))
	failures := make([]string, 0, len(candidates))

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		resolved, err := resolveWrapperExecutable(candidate)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", candidate, err))
			continue
		}

		if err := probeHubWrapper(resolved); err != nil {
			failures = append(failures, fmt.Sprintf("%s (%v)", resolved, err))
			continue
		}
		return resolved, nil
	}

	return "", fmt.Errorf("no healthy hub wrapper found (candidates tried: %s)", strings.Join(failures, "; "))
}

func hubWrapperCandidates(workspaceRoot string, registryRoot string) []string {
	candidates := []string{}

	if override := strings.TrimSpace(os.Getenv(hubWrapperOverrideEnv)); override != "" {
		candidates = append(candidates, override)
	}

	if workspaceRoot != "" {
		candidates = append(candidates, filepath.Join(workspaceRoot, hubWrapperWorkspaceBinaryPath))
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", defaultHubWrapperCommand))
	}

	candidates = append(candidates, defaultHubWrapperCommand)

	legacy := resolvePathLike(hubWrapperLegacyRelativePath, workspaceRoot, registryRoot, "local")
	if legacy != "" {
		candidates = append(candidates, legacy)
	}

	return candidates
}

func resolveWrapperExecutable(candidate string) (string, error) {
	if strings.Contains(candidate, string(filepath.Separator)) || filepath.IsAbs(candidate) {
		if !isExecutableFile(candidate) {
			return "", fmt.Errorf("not executable")
		}
		return candidate, nil
	}
	resolved, err := hubWrapperLookPath(candidate)
	if err != nil {
		return "", err
	}
	if !isExecutableFile(resolved) {
		return "", fmt.Errorf("resolved path is not executable: %s", resolved)
	}
	return resolved, nil
}

func probeHubWrapper(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), hubWrapperHealthCheckTimeout)
	defer cancel()

	cmd := hubWrapperCommandRunner(ctx, path, "--help")
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/pathsec"
)

const (
	defaultBrandKitCLI  = "/Users/cblevins/workspace/libs/banner-kit/bin/banner-kit"
	defaultWorkspaceDir = "/Users/cblevins/workspace"
	maxRepos            = 200
	maxOutputBytes      = 24 * 1024
	commandTimeout      = 60 * time.Second
)

type brandParams struct {
	Root   string
	Kind   string
	Config string
	Repos  []string
	Verify bool
	All    bool
	Limit  int
	Asset  string
}

type cliResult struct {
	Command  []string `json:"command"`
	Root     string   `json:"root"`
	Kind     string   `json:"kind,omitempty"`
	Repos    []string `json:"repos,omitempty"`
	ExitCode int      `json:"exit_code"`
	JSON     any      `json:"json,omitempty"`
	Stdout   string   `json:"stdout,omitempty"`
	Stderr   string   `json:"stderr,omitempty"`
}

type repoSummary struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Kind         string `json:"kind,omitempty"`
	HasReadme    bool   `json:"has_readme"`
	HasAssetsDir bool   `json:"has_assets_dir"`
	HasBanner    bool   `json:"has_banner"`
	HasIcon      bool   `json:"has_icon"`
}

type inspectResult struct {
	Repo              repoSummary `json:"repo"`
	ManifestPath      string      `json:"manifest_path,omitempty"`
	Manifest          any         `json:"manifest,omitempty"`
	PlannedCLICommand []string    `json:"planned_cli_command"`
}

func configuredCLI() string {
	return env.String("BRAND_KIT_CLI", defaultBrandKitCLI)
}

func defaultRoot() string {
	return env.String("BRAND_KIT_DEFAULT_ROOT", defaultWorkspaceDir)
}

func validateCLI(cli string) error {
	if strings.TrimSpace(cli) == "" {
		return mcperror.NotConfigured("BRAND_KIT_CLI", "set BRAND_KIT_CLI to the banner-kit executable")
	}
	if strings.ContainsRune(cli, filepath.Separator) {
		info, err := os.Stat(cli)
		if err != nil {
			return mcperror.NotConfigured("BRAND_KIT_CLI", fmt.Sprintf("banner-kit executable not found at %s", cli))
		}
		if info.IsDir() {
			return mcperror.NotConfigured("BRAND_KIT_CLI", fmt.Sprintf("%s is a directory, expected an executable", cli))
		}
		return nil
	}
	if _, err := exec.LookPath(cli); err != nil {
		return mcperror.NotConfigured("BRAND_KIT_CLI", fmt.Sprintf("%s was not found in PATH", cli))
	}
	return nil
}

func resolveRoot(root, kind string) (string, error) {
	base := strings.TrimSpace(root)
	if base == "" {
		base = defaultRoot()
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("invalid root: %w", err)
	}

	if err := pathsec.ValidatePath(absBase, defaultRoot()); err != nil {
		return "", fmt.Errorf("root not allowed: %w", err)
	}

	if kind == "library" || kind == "service" {
		bucket := map[string]string{"library": "libs", "service": "services"}[kind]
		if filepath.Base(absBase) != bucket {
			candidate := filepath.Join(absBase, bucket)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				absBase = candidate
			}
		}
	}

	return absBase, nil
}

func validateRepos(repos []string) error {
	for _, repo := range repos {
		if repo == "" {
			return fmt.Errorf("repo names must not be empty")
		}
		if filepath.IsAbs(repo) || strings.Contains(repo, "..") || strings.ContainsAny(repo, `/\:`) {
			return fmt.Errorf("repo %q must be a simple repository name", repo)
		}
	}
	return nil
}

func runBrandKit(ctx context.Context, p brandParams, action string, extra ...string) (cliResult, error) {
	cli := configuredCLI()
	if err := validateCLI(cli); err != nil {
		return cliResult{}, err
	}
	if err := validateRepos(p.Repos); err != nil {
		return cliResult{}, err
	}
	root, err := resolveRoot(p.Root, p.Kind)
	if err != nil {
		return cliResult{}, err
	}

	cmdArgs := []string{}
	if action != "" && action != "generate" {
		cmdArgs = append(cmdArgs, action)
	}
	cmdArgs = append(cmdArgs, "--json", "--root", root)
	if p.Kind != "" {
		cmdArgs = append(cmdArgs, "--kind", p.Kind)
	}
	if p.Config != "" {
		cmdArgs = append(cmdArgs, "--config", p.Config)
	}
	cmdArgs = append(cmdArgs, extra...)
	cmdArgs = append(cmdArgs, p.Repos...)

	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, cli, cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()

	out := trimOutput(stdout.String())
	errOut := trimOutput(stderr.String())
	res := cliResult{
		Command:  append([]string{cli}, cmdArgs...),
		Root:     root,
		Kind:     p.Kind,
		Repos:    append([]string{}, p.Repos...),
		ExitCode: exitCode(err),
		Stdout:   out,
		Stderr:   errOut,
	}
	if parsed, ok := parseJSON(out); ok {
		res.JSON = parsed
		res.Stdout = ""
	}

	if runCtx.Err() != nil {
		return res, mcperror.OperationFailed("banner-kit command", runCtx.Err())
	}
	if err != nil && res.JSON == nil {
		return res, mcperror.OperationFailed("banner-kit command", fmt.Errorf("%w: %s", err, firstNonEmpty(errOut, out)))
	}
	return res, nil
}

func inspectRepo(p brandParams, repo string) (inspectResult, error) {
	if err := validateRepos([]string{repo}); err != nil {
		return inspectResult{}, err
	}
	root, err := resolveRoot(p.Root, p.Kind)
	if err != nil {
		return inspectResult{}, err
	}
	summary, err := summarizeRepo(root, p.Kind, repo)
	if err != nil {
		return inspectResult{}, err
	}
	result := inspectResult{
		Repo:              summary,
		PlannedCLICommand: buildPlannedCommand(p, "lint", repo),
	}
	for _, name := range []string{"brand.json", "banner-kit.json", ".brand.json"} {
		candidate := filepath.Join(summary.Path, name)
		data, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		var parsed any
		if json.Unmarshal(data, &parsed) == nil {
			result.ManifestPath = candidate
			result.Manifest = parsed
			break
		}
	}
	return result, nil
}

func listRepos(p brandParams) (map[string]any, error) {
	root, err := resolveRoot(p.Root, p.Kind)
	if err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit <= 0 || limit > maxRepos {
		limit = maxRepos
	}

	repos, err := discoverRepos(root, p.Kind, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"root":  root,
		"kind":  p.Kind,
		"count": len(repos),
		"repos": repos,
	}, nil
}

func discoverRepos(root, kind string, limit int) ([]repoSummary, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("list root: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var repos []repoSummary
	for _, entry := range entries {
		if len(repos) >= limit {
			break
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		summary, err := summarizeRepo(root, kind, entry.Name())
		if err == nil {
			repos = append(repos, summary)
		}
	}
	return repos, nil
}

func summarizeRepo(root, kind, repo string) (repoSummary, error) {
	repoPath := filepath.Join(root, repo)
	if err := pathsec.ValidatePath(repoPath, root); err != nil {
		return repoSummary{}, err
	}
	info, err := os.Stat(repoPath)
	if err != nil {
		return repoSummary{}, err
	}
	if !info.IsDir() {
		return repoSummary{}, fmt.Errorf("%s is not a directory", repoPath)
	}
	return repoSummary{
		Name:         repo,
		Path:         repoPath,
		Kind:         kind,
		HasReadme:    fileExists(filepath.Join(repoPath, "README.md")),
		HasAssetsDir: dirExists(filepath.Join(repoPath, "assets")),
		HasBanner:    fileExists(filepath.Join(repoPath, "assets", "banner.png")),
		HasIcon:      fileExists(filepath.Join(repoPath, "assets", "icon.png")),
	}, nil
}

func buildPlannedCommand(p brandParams, action string, repos ...string) []string {
	root, _ := resolveRoot(p.Root, p.Kind)
	args := []string{configuredCLI(), action, "--json", "--root", root}
	if p.Kind != "" {
		args = append(args, "--kind", p.Kind)
	}
	if p.Config != "" {
		args = append(args, "--config", p.Config)
	}
	args = append(args, repos...)
	return args
}

func trimOutput(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + "...<truncated>"
}

func parseJSON(s string) (any, bool) {
	var parsed any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return nil, false
	}
	return parsed, true
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return "no output"
}

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ensureRepoRoot(ctx context.Context, cfg Config, logger *slog.Logger) error {
	if strings.TrimSpace(cfg.RepoRoot) == "" {
		return nil
	}
	if repoRootReady(cfg.RepoRoot) {
		return nil
	}
	if strings.TrimSpace(cfg.GitLabToken) == "" ||
		strings.TrimSpace(cfg.GitLabAPIURL) == "" ||
		strings.TrimSpace(cfg.GitLabProject) == "" {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git executable unavailable: %w", err)
	}
	repoURL, host, err := gitRepoURL(cfg.GitLabAPIURL, cfg.GitLabProject)
	if err != nil {
		return err
	}
	parent := filepath.Dir(cfg.RepoRoot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create repo parent: %w", err)
	}
	home, err := os.MkdirTemp(parent, ".git-home-*")
	if err != nil {
		return fmt.Errorf("create git home: %w", err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	netrc := fmt.Sprintf("machine %s login oauth2 password %s\n", host, cfg.GitLabToken)
	if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte(netrc), 0o600); err != nil {
		return fmt.Errorf("write git credentials: %w", err)
	}

	if _, err := os.Stat(filepath.Join(cfg.RepoRoot, ".git")); err == nil {
		if err := runGit(ctx, home, "", "config", "--global", "--add", "safe.directory", cfg.RepoRoot); err != nil {
			return err
		}
		if err := runGit(ctx, home, cfg.RepoRoot, "remote", "set-url", "origin", repoURL); err != nil {
			return err
		}
		if err := runGit(ctx, home, cfg.RepoRoot, "fetch", "--depth=1", "origin", "main"); err != nil {
			return err
		}
		if err := runGit(ctx, home, cfg.RepoRoot, "checkout", "main"); err != nil {
			return err
		}
		if err := runGit(ctx, home, cfg.RepoRoot, "merge", "--ff-only", "origin/main"); err != nil {
			if logger != nil {
				logger.Warn("repo root fast-forward skipped; local checkout may contain uncommitted Mills artifacts", "repo_root", cfg.RepoRoot, "error", err)
			}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat repo git dir: %w", err)
	}

	if err := os.RemoveAll(cfg.RepoRoot); err != nil {
		return fmt.Errorf("clear incomplete repo root: %w", err)
	}
	return runGit(ctx, home, "", "clone", "--depth=1", "--branch", "main", repoURL, cfg.RepoRoot)
}

func repoRootReady(repoRoot string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		return false
	}
	loomDir := filepath.Join(repoRoot, ".loom")
	info, err := os.Stat(loomDir)
	if err != nil || !info.IsDir() {
		return false
	}
	f, err := os.CreateTemp(loomDir, ".repo-root-check-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func gitRepoURL(apiURL, project string) (repoURL string, host string, err error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return "", "", errors.New("git repo url: project required")
	}
	if strings.IndexFunc(project, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		return "", "", fmt.Errorf("git repo url: project path required, got numeric project %q", project)
	}
	unescapedProject, err := url.PathUnescape(project)
	if err != nil {
		return "", "", fmt.Errorf("git repo url: decode project: %w", err)
	}
	base := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	base = strings.TrimSuffix(base, "/api/v4")
	u, err := url.Parse(base)
	if err != nil {
		return "", "", fmt.Errorf("git repo url: parse api url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("git repo url: api url must include scheme and host")
	}
	u.User = nil
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.Trim(strings.TrimSuffix(unescapedProject, ".git"), "/") + ".git"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), u.Hostname(), nil
}

func runGit(ctx context.Context, home, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "HOME="+home, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

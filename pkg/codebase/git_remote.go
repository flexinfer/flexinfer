package codebase

import (
	"context"
	"net/url"
	"os/exec"
	"strings"
)

// ParseGitRemoteProject extracts "owner/repo" from a git remote URL.
// Supports SSH (git@host:owner/repo.git) and HTTPS (https://host/owner/repo.git).
// Returns empty string if the URL cannot be parsed.
func ParseGitRemoteProject(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}

	var path string

	// SSH format: git@host:owner/repo.git
	if i := strings.Index(rawURL, "://"); i < 0 && strings.Contains(rawURL, ":") {
		// Split on the first colon after the @.
		parts := strings.SplitN(rawURL, ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			return ""
		}
		path = parts[1]
	} else {
		// HTTPS (or other scheme) format.
		u, err := url.Parse(rawURL)
		if err != nil || u.Path == "" {
			return ""
		}
		path = u.Path
	}

	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")

	if path == "" {
		return ""
	}

	// Must contain at least one slash (owner/repo).
	if !strings.Contains(path, "/") {
		return ""
	}

	return path
}

// DetectPipelineProject runs "git config --get remote.origin.url" in dir
// and parses the result. Returns "" if unavailable.
func DetectPipelineProject(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "config", "--get", "remote.origin.url")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return ParseGitRemoteProject(strings.TrimSpace(string(out)))
}

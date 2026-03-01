package codebase

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

const gitCommandTimeout = 2 * time.Second

func deriveRepoID(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}

	gitRoot := absRoot
	if out, err := runGitOutput(absRoot, "rev-parse", "--show-toplevel"); err == nil {
		if s := strings.TrimSpace(out); s != "" {
			gitRoot = s
		}
	}

	if out, err := runGitOutput(gitRoot, "config", "--get", "remote.origin.url"); err == nil {
		remote := strings.TrimSpace(out)
		if remote != "" {
			remote = strings.TrimSuffix(remote, ".git")
			return schema.ShortSHA256Hex(remote), nil
		}
	}

	return schema.ShortSHA256Hex(gitRoot), nil
}

func runGitOutput(repoPath string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	cmdArgs := append([]string{"-C", repoPath}, args...)
	out, err := exec.CommandContext(ctx, "git", cmdArgs...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// normalizeStringSlice trims whitespace and lowercases each element,
// dropping any that are empty after trimming.
func normalizeStringSlice(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, strings.ToLower(s))
		}
	}
	return out
}

func lexicalTokens(q string) []string {
	q = strings.ToLower(q)
	var b strings.Builder
	b.Grow(len(q))
	for _, r := range q {
		switch {
		case unicode.IsLetter(r), unicode.IsNumber(r):
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	raw := strings.Fields(b.String())
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		if len(t) < 3 || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

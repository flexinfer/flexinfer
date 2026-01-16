package codebase

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

type gitMeta struct {
	commit string
	author string
}

func detectGitRoot(ctx context.Context, absRoot string) (string, bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", absRoot, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return root, true
}

func annotateChunksWithGitMetadata(ctx context.Context, gitRoot string, absPath string, chunks []schema.Chunk) error {
	if gitRoot == "" || len(chunks) == 0 {
		return nil
	}

	rel, err := filepath.Rel(gitRoot, absPath)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return nil
	}
	rel = filepath.ToSlash(rel)

	wanted := map[int]bool{}
	for _, ch := range chunks {
		if ch.StartLine > 0 {
			wanted[ch.StartLine] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	metaByLine, err := gitBlamePorcelainForLines(ctx, gitRoot, rel, wanted)
	if err != nil {
		return err
	}

	for i := range chunks {
		m, ok := metaByLine[chunks[i].StartLine]
		if !ok {
			continue
		}
		chunks[i].GitCommit = m.commit
		chunks[i].GitBlame = m.author
	}
	return nil
}

func gitBlamePorcelainForLines(ctx context.Context, gitRoot string, relPath string, wanted map[int]bool) (map[int]gitMeta, error) {
	if len(wanted) == 0 {
		return map[int]gitMeta{}, nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", gitRoot, "blame", "--line-porcelain", "HEAD", "--", relPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	meta, parseErr := parseGitBlamePorcelainForLines(stdout, wanted)
	_, _ = io.ReadAll(stderr)

	waitErr := cmd.Wait()
	if parseErr != nil {
		return nil, parseErr
	}
	if waitErr != nil {
		return map[int]gitMeta{}, nil
	}
	return meta, nil
}

func parseGitBlamePorcelainForLines(r io.Reader, wanted map[int]bool) (map[int]gitMeta, error) {
	out := map[int]gitMeta{}

	var (
		currentCommit string
		currentAuthor string
		lineNum       int
	)

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "\t") {
			lineNum++
			if wanted[lineNum] {
				out[lineNum] = gitMeta{
					commit: shortGitSHA(currentCommit),
					author: currentAuthor,
				}
			}
			continue
		}
		if strings.HasPrefix(line, "author ") {
			currentAuthor = strings.TrimSpace(strings.TrimPrefix(line, "author "))
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 1 && looksLikeGitSHA(fields[0]) {
			currentCommit = fields[0]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func shortGitSHA(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 8 {
		return s[:8]
	}
	return s
}

func looksLikeGitSHA(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

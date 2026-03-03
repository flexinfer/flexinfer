package index

import (
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type IgnoreMatcher struct {
	rules      []ignoreRule
	hasNegate  bool
	root       string
	fastPrefix map[string]struct{}
}

type ignoreRule struct {
	pattern   string
	negate    bool
	directory bool
}

func NewIgnoreMatcher(root string, extraPatterns []string) *IgnoreMatcher {
	root = filepath.Clean(root)
	m := &IgnoreMatcher{
		root: root,
		fastPrefix: map[string]struct{}{
			".git":         {},
			".worktrees":   {},
			"node_modules": {},
			"dist":         {},
			"build":        {},
			"vendor":       {},
			".venv":        {},
			"__pycache__":  {},
		},
	}

	// Mark as having a negated pattern up front so fast-prefix pruning does not
	// incorrectly hide paths that should be unignored by extra patterns.
	for _, p := range extraPatterns {
		if _, negate, ok := parseGitignorePattern(p); ok && negate {
			m.hasNegate = true
			break
		}
	}

	for _, p := range DefaultExcludeGlobs() {
		m.addPattern("", p, false)
	}

	if err := filepath.WalkDir(m.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel := filepath.ToSlash(m.relativePath(p))
		if rel == "" || rel == "." {
			return nil
		}
		if d.IsDir() {
			if m.shouldFastSkip(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ".gitignore" {
			return nil
		}
		base := filepath.ToSlash(filepath.Dir(rel))
		if base == "." {
			base = ""
		}
		_ = m.addGitignore(base, p)
		return nil
	}); err != nil {
		return m
	}

	m.addPatterns(extraPatterns, "")

	return m
}

func (m *IgnoreMatcher) IsIgnored(path string, isDir bool) bool {
	if strings.TrimSpace(path) == "" || path == "." {
		return false
	}

	path = filepath.ToSlash(path)
	if isDir && !strings.HasSuffix(path, "/") {
		path += "/"
	}
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")

	if !m.hasNegate && m.shouldFastSkip(path) {
		return true
	}

	ignored := false
	for _, rule := range m.rules {
		if rule.directory && !isDir {
			continue
		}
		if !doublestar.PathMatchUnvalidated(rule.pattern, path) {
			continue
		}
		ignored = !rule.negate
	}
	return ignored
}

func (m *IgnoreMatcher) shouldFastSkip(path string) bool {
	if m == nil {
		return false
	}
	segments := strings.Split(filepath.ToSlash(strings.TrimSuffix(path, "/")), "/")
	for _, segment := range segments {
		if _, ok := m.fastPrefix[segment]; ok {
			return true
		}
	}
	return false
}

func (m *IgnoreMatcher) addPatterns(patterns []string, base string) {
	for _, p := range patterns {
		raw, negate, ok := parseGitignorePattern(p)
		if !ok {
			continue
		}
		m.addPattern(base, raw, negate)
	}
}

func (m *IgnoreMatcher) addPattern(base, pattern string, negate bool) {
	rule := ruleFromPattern(base, pattern, negate)
	if rule == nil {
		return
	}
	if !doublestar.ValidatePathPattern(rule.pattern) {
		return
	}
	if negate {
		m.hasNegate = true
	}
	m.rules = append(m.rules, *rule)
}

func (m *IgnoreMatcher) addGitignore(baseDir, absPath string) error {
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if _, _, ok := parseGitignorePattern(line); !ok {
			continue
		}
		m.addPatterns([]string{line}, baseDir)
	}
	return nil
}

func parseGitignorePattern(line string) (string, bool, bool) {
	line = strings.TrimRight(line, "\r")
	if line == "" {
		return "", false, false
	}

	line = trimUnescapedLeadingSpace(line)
	if line == "" {
		return "", false, false
	}

	if strings.HasPrefix(line, "\\#") {
		line = strings.TrimPrefix(line, "\\")
	} else if strings.HasPrefix(line, "#") {
		return "", false, false
	}

	negate := false
	if strings.HasPrefix(line, "\\!") {
		line = strings.TrimPrefix(line, "\\")
	} else if strings.HasPrefix(line, "!") {
		line = strings.TrimPrefix(line, "!")
		line = trimUnescapedLeadingSpace(line)
		negate = true
	}

	line = trimUnescapedTrailingSpace(line)
	if line == "" {
		return "", false, false
	}
	if strings.HasPrefix(line, "\\ ") || strings.HasPrefix(line, "\\\t") {
		line = strings.TrimPrefix(line, "\\")
	}

	return line, negate, true
}

func trimUnescapedLeadingSpace(line string) string {
	for len(line) > 0 {
		ch := line[0]
		if ch != ' ' && ch != '\t' {
			return line
		}
		idx := 0
		if isEscaped(line, idx) {
			return line
		}
		line = line[1:]
	}
	return ""
}

func trimUnescapedTrailingSpace(line string) string {
	end := len(line)
	for end > 0 {
		ch := line[end-1]
		if ch != ' ' && ch != '\t' {
			break
		}
		if isEscaped(line, end-1) {
			break
		}
		end--
	}
	return line[:end]
}

func isEscaped(s string, idx int) bool {
	count := 0
	for i := idx - 1; i >= 0; i-- {
		if s[i] != '\\' {
			break
		}
		count++
	}
	return count%2 == 1
}

func ruleFromPattern(base string, pattern string, negate bool) *ignoreRule {
	if pattern == "" {
		return nil
	}

	p := pattern
	directory := false
	if strings.HasSuffix(p, "/") {
		directory = true
		p = strings.TrimSuffix(p, "/")
	}

	anchored := false
	if strings.HasPrefix(p, "/") {
		anchored = true
		p = strings.TrimPrefix(p, "/")
	}

	p = filepath.ToSlash(path.Clean(p))
	p = strings.TrimSuffix(p, "/")
	if p == "." {
		p = ""
	}
	if p == "" {
		return nil
	}

	hasSlash := strings.Contains(p, "/")
	if !anchored && !hasSlash {
		p = path.Join(base, "**", p)
	} else if base != "" {
		p = path.Join(base, p)
	}
	p = path.Clean(p)
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	if directory && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if p == "/" || p == "**/" {
		return nil
	}

	return &ignoreRule{pattern: p, negate: negate, directory: directory}
}

func (m *IgnoreMatcher) relativePath(p string) string {
	rel, err := filepath.Rel(m.root, p)
	if err != nil {
		return p
	}
	return rel
}

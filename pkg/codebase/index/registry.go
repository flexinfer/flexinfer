package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/codebase/index/goindex"
	"github.com/crb2nu/loom/pkg/codebase/schema"
)

type Registry struct {
	maxFileBytes int64
	indexers     map[string]Indexer
}

type Indexer interface {
	Language() string
	Extensions() []string
	IndexFile(ctx context.Context, absRoot, absPath, repoID string) ([]schema.Chunk, error)
}

func NewRegistry(maxFileBytes int64) *Registry {
	r := &Registry{
		maxFileBytes: maxFileBytes,
		indexers:     make(map[string]Indexer),
	}
	r.Register(goindex.New())
	return r
}

func (r *Registry) Register(ix Indexer) {
	r.indexers[ix.Language()] = ix
}

func (r *Registry) CollectFiles(absRoot string, languages []string, exclude []string) ([]string, error) {
	wantLang := make(map[string]bool, len(languages))
	for _, l := range languages {
		wantLang[strings.ToLower(l)] = true
	}

	wantExt := make(map[string]bool)
	for lang := range wantLang {
		ix := r.indexers[lang]
		if ix == nil {
			return nil, fmt.Errorf("unsupported language: %s", lang)
		}
		for _, ext := range ix.Extensions() {
			wantExt[ext] = true
		}
	}

	defaultExcludes := []string{
		".git/**",
		"node_modules/**",
		"vendor/**",
		"dist/**",
		"build/**",
		"**/.venv/**",
		"**/__pycache__/**",
	}
	allExcludes := append(defaultExcludes, exclude...)

	var files []string
	err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if matchesAnyGlob(rel+"/", allExcludes) {
				return filepath.SkipDir
			}
			return nil
		}

		if matchesAnyGlob(rel, allExcludes) {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if !wantExt[ext] {
			return nil
		}

		info, err := d.Info()
		if err == nil && r.maxFileBytes > 0 && info.Size() > r.maxFileBytes {
			return nil
		}

		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func (r *Registry) IndexFile(ctx context.Context, absRoot, absPath, repoID string) ([]schema.Chunk, error) {
	ext := strings.ToLower(filepath.Ext(absPath))
	for _, ix := range r.indexers {
		for _, e := range ix.Extensions() {
			if e == ext {
				return ix.IndexFile(ctx, absRoot, absPath, repoID)
			}
		}
	}
	return nil, nil
}

func matchesAnyGlob(path string, globs []string) bool {
	for _, g := range globs {
		if ok := globMatch(g, path); ok {
			return true
		}
	}
	return false
}

// globMatch is a small wrapper around filepath.Match-like semantics but operating on slash paths.
// Supports patterns like "dir/**" by prefix matching when "**" appears.
func globMatch(pattern, path string) bool {
	p := filepath.ToSlash(pattern)
	s := filepath.ToSlash(path)
	if strings.Contains(p, "**") {
		parts := strings.Split(p, "**")
		if len(parts) == 2 {
			return strings.HasPrefix(s, strings.TrimSuffix(parts[0], "/"))
		}
	}
	ok, _ := filepath.Match(p, s)
	return ok
}

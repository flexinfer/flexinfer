package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/codebase/index/goindex"
	"github.com/crb2nu/loom/pkg/codebase/index/pyindex"
	"github.com/crb2nu/loom/pkg/codebase/index/rsindex"
	"github.com/crb2nu/loom/pkg/codebase/index/tsindex"
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
	r.Register(tsindex.NewTypeScript())
	r.Register(tsindex.NewJavaScript())
	r.Register(pyindex.New())
	r.Register(rsindex.New())
	return r
}

func DefaultExcludeGlobs() []string {
	return []string{
		".git/**",
		".worktrees/**",
		"node_modules/**",
		"vendor/**",
		"dist/**",
		"build/**",
		"**/.venv/**",
		"**/__pycache__/**",
	}
}

func (r *Registry) Register(ix Indexer) {
	r.indexers[ix.Language()] = ix
}

func (r *Registry) SupportedLanguages() []string {
	langs := make([]string, 0, len(r.indexers))
	for k := range r.indexers {
		langs = append(langs, k)
	}
	sort.Strings(langs)
	return langs
}

func (r *Registry) ExtensionsForLanguages(languages []string) (map[string]bool, error) {
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

	return wantExt, nil
}

func (r *Registry) CollectFiles(absRoot string, languages []string, exclude []string) ([]string, error) {
	wantExt, err := r.ExtensionsForLanguages(languages)
	if err != nil {
		return nil, err
	}

	matcher := NewIgnoreMatcher(absRoot, exclude)

	var files []string
	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
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
			if matcher.IsIgnored(rel+"/", true) {
				return filepath.SkipDir
			}
			return nil
		}

		if matcher.IsIgnored(rel, false) {
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

// contentIndexer optionally avoids re-reading file bytes inside indexers.
type contentIndexer interface {
	IndexFileFromContent(ctx context.Context, absRoot, absPath, repoID string, content []byte) ([]schema.Chunk, error)
}

func (r *Registry) IndexFileFromContent(
	ctx context.Context,
	absRoot,
	absPath,
	repoID string,
	content []byte,
) ([]schema.Chunk, error) {
	ext := strings.ToLower(filepath.Ext(absPath))
	for _, ix := range r.indexers {
		for _, e := range ix.Extensions() {
			if e == ext {
				if ci, ok := ix.(contentIndexer); ok {
					return ci.IndexFileFromContent(ctx, absRoot, absPath, repoID, content)
				}
				return ix.IndexFile(ctx, absRoot, absPath, repoID)
			}
		}
	}
	return nil, nil
}

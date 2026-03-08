package codebase

import (
	"context"
	"fmt"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))
	chunkTypes := normalizeStringSlice(validate.StringSliceFromArgs(args, "chunk_types"))

	total, err := s.qdrant.Count(ctx, qdrant.Filter(repoID, "", nil, nil))
	if err != nil {
		return nil, err
	}

	langs := languages
	if len(langs) == 0 {
		langs = s.indexers.SupportedLanguages()
	}
	ctypes := chunkTypes
	if len(ctypes) == 0 {
		ctypes = []string{"function", "method", "class", "module", "import", "variable", "block"}
	}

	byLang := map[string]int{}
	for _, l := range langs {
		n, err := s.qdrant.Count(ctx, qdrant.Filter(repoID, "", []string{l}, nil))
		if err != nil {
			return nil, err
		}
		byLang[l] = n
	}

	byType := map[string]int{}
	for _, t := range ctypes {
		n, err := s.qdrant.Count(ctx, qdrant.Filter(repoID, "", nil, []string{t}))
		if err != nil {
			return nil, err
		}
		byType[t] = n
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":       repoID,
		"collection":    s.cfg.QdrantCollection,
		"total_chunks":  total,
		"by_language":   byLang,
		"by_chunk_type": byType,
	})
}

func (s *Service) HandleDeleteRepo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", "")
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required")
	}
	if !validate.BoolFromArgs(args, "confirm", false) {
		return mcp.JSONResult(map[string]any{
			"ok":      false,
			"error":   "confirm=true is required",
			"repo_id": repoID,
		})
	}
	dryRun := validate.BoolFromArgs(args, "dry_run", false)
	if dryRun {
		count, err := s.qdrant.Count(ctx, qdrant.Filter(repoID, "", nil, nil))
		if err != nil {
			return nil, err
		}
		return mcp.JSONResult(map[string]any{
			"ok":           true,
			"dry_run":      true,
			"repo_id":      repoID,
			"would_delete": count,
		})
	}

	if err := s.qdrant.DeleteRepo(ctx, repoID); err != nil {
		return nil, err
	}
	return mcp.JSONResult(map[string]any{"ok": true, "repo_id": repoID})
}

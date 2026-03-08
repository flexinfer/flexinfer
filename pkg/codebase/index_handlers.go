package codebase

import (
	"context"
	"fmt"
	"strings"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleIndexStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	root := validate.StringFromArgs(args, "root", ".")
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if repoID == "" {
		derived, derr := deriveRepoID(root)
		if derr != nil {
			return nil, derr
		}
		repoID = derived
	}

	langs := s.indexers.SupportedLanguages()
	if normalized := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages")); len(normalized) > 0 {
		langs = normalized
	}

	exclude := validate.StringSliceFromArgs(args, "exclude")

	fullRefresh := validate.BoolFromArgs(args, "full_refresh", true)
	gitMetadata := validate.BoolFromArgs(args, "git_metadata", s.cfg.GitMetadataDefault)
	embeddings := validate.BoolFromArgs(args, "embeddings", !s.cfg.DisableEmbeddingsDefault)

	jobID := schema.ShortSHA256Hex(fmt.Sprintf("%s:%d", repoID, time.Now().UnixNano()))
	jobCtx, cancel := context.WithCancel(ctx)

	job := &indexJob{
		id:     jobID,
		cancel: cancel,
		status: "running",
		stats: schema.IndexStats{
			RepoID:    repoID,
			Root:      root,
			StartedAt: time.Now(),
		},
	}

	s.jobsMu.Lock()
	s.jobs[jobID] = job
	s.jobsMu.Unlock()

	go s.runIndexJob(jobCtx, jobID, repoID, root, langs, exclude, fullRefresh, gitMetadata, embeddings)

	return mcp.JSONResult(map[string]any{
		"job_id":       jobID,
		"repo_id":      repoID,
		"git_metadata": gitMetadata,
		"embeddings":   embeddings,
	})
}

func (s *Service) HandleIndexPoll(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	jobID := validate.StringFromArgs(args, "job_id", "")
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("job_id is required")
	}

	s.jobsMu.RLock()
	job := s.jobs[jobID]
	s.jobsMu.RUnlock()
	if job == nil {
		return mcp.JSONResult(map[string]any{
			"found":  false,
			"job_id": jobID,
		})
	}

	return mcp.JSONResult(map[string]any{
		"found":  true,
		"job_id": job.id,
		"status": job.status,
		"error":  job.err,
		"stats":  job.stats,
	})
}

func (s *Service) HandleIndexCancel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	jobID := validate.StringFromArgs(args, "job_id", "")
	if strings.TrimSpace(jobID) == "" {
		return nil, fmt.Errorf("job_id is required")
	}

	s.jobsMu.RLock()
	job := s.jobs[jobID]
	s.jobsMu.RUnlock()
	if job == nil {
		return mcp.JSONResult(map[string]any{"ok": false, "error": "job not found"})
	}

	job.cancel()
	return mcp.JSONResult(map[string]any{"ok": true})
}

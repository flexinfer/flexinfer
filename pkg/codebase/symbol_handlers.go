package codebase

import (
	"context"
	"fmt"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleGetDefinition(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol := validate.StringFromArgs(args, "symbol", "")
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath := validate.StringFromArgs(args, "file_path", "")

	limit := validate.IntFromArgs(args, "limit", s.cfg.ScrollLimit)
	if limit <= 0 {
		limit = s.cfg.ScrollLimit
	}

	includeContent := validate.BoolFromArgs(args, "include_content", false)

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))

	ch, err := s.qdrant.FindChunkByName(ctx, repoID, symbol, filePath, languages, limit)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return mcp.JSONResult(map[string]any{
			"found":     false,
			"repo_id":   repoID,
			"symbol":    symbol,
			"file_path": filePath,
		})
	}

	if !includeContent {
		ch.Content = ""
	}

	return mcp.JSONResult(map[string]any{
		"found":      true,
		"repo_id":    repoID,
		"symbol":     symbol,
		"file_path":  filePath,
		"definition": ch,
	})
}

func (s *Service) HandleGetReferences(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol := validate.StringFromArgs(args, "symbol", "")
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath := validate.StringFromArgs(args, "file_path", "")

	limit := validate.IntFromArgs(args, "limit", s.cfg.ScrollLimit)
	if limit <= 0 {
		limit = s.cfg.ScrollLimit
	}

	includeDefinitions := validate.BoolFromArgs(args, "include_definitions", true)
	includeCallers := validate.BoolFromArgs(args, "include_callers", true)
	includeModules := validate.BoolFromArgs(args, "include_modules", false)
	includeContent := validate.BoolFromArgs(args, "include_content", false)

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))

	var (
		definitions []schema.Chunk
		callers     []schema.CallerInfo
	)

	if includeDefinitions {
		chunks, err := s.qdrant.FindChunksByName(ctx, repoID, symbol, filePath, languages, limit)
		if err != nil {
			return nil, err
		}
		for _, ch := range chunks {
			if !includeModules && ch.ChunkType == "module" {
				continue
			}
			if !includeContent {
				ch.Content = ""
			}
			definitions = append(definitions, ch)
		}
	}

	if includeCallers {
		var err error
		if strings.TrimSpace(filePath) != "" {
			callers, err = s.qdrant.FindCallersInFile(ctx, repoID, filePath, symbol, limit)
		} else {
			callers, err = s.qdrant.FindCallers(ctx, repoID, symbol, limit)
		}
		if err != nil {
			return nil, err
		}
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":       repoID,
		"symbol":        symbol,
		"file_path":     filePath,
		"definitions":   definitions,
		"callers":       callers,
		"include_defs":  includeDefinitions,
		"include_calls": includeCallers,
	})
}

func (s *Service) HandleGetContext(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	filePath := validate.StringFromArgs(args, "file_path", "")
	if strings.TrimSpace(filePath) == "" {
		return nil, fmt.Errorf("file_path is required")
	}

	line := validate.IntFromArgs(args, "line_number", 0)
	if line <= 0 {
		return nil, fmt.Errorf("line_number must be > 0")
	}

	includeCallers := validate.BoolFromArgs(args, "include_callers", true)
	includeCallees := validate.BoolFromArgs(args, "include_callees", true)

	relatedLimit := validate.IntFromArgs(args, "related_limit", 5)
	if relatedLimit <= 0 {
		relatedLimit = 5
	}

	includeContent := validate.BoolFromArgs(args, "include_content", false)

	ctxInfo, err := s.qdrant.GetFileContext(ctx, repoID, filePath, line, relatedLimit)
	if err != nil {
		return nil, err
	}
	if ctxInfo == nil || ctxInfo.Chunk == nil {
		return mcp.JSONResult(map[string]any{
			"found":     false,
			"repo_id":   repoID,
			"file_path": filePath,
			"line":      line,
		})
	}

	if includeCallees {
		for _, call := range ctxInfo.Chunk.Calls {
			ctxInfo.Callees = append(ctxInfo.Callees, schema.CalleeInfo{
				Name:       call,
				IsExternal: strings.Contains(call, "."),
			})
		}
	}

	if includeCallers && ctxInfo.Chunk.Name != "" {
		callers, err := s.qdrant.FindCallers(ctx, repoID, ctxInfo.Chunk.Name, s.cfg.ScrollLimit)
		if err != nil {
			return nil, err
		}
		ctxInfo.Callers = callers
	}

	if !includeContent {
		ctxInfo.Chunk.Content = ""
		for i := range ctxInfo.RelatedChunks {
			ctxInfo.RelatedChunks[i].Content = ""
		}
	}

	return mcp.JSONResult(map[string]any{
		"found":   true,
		"context": ctxInfo,
	})
}

func (s *Service) HandleFindCallers(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol := validate.StringFromArgs(args, "symbol", "")
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath := validate.StringFromArgs(args, "file_path", "")

	limit := validate.IntFromArgs(args, "limit", s.cfg.ScrollLimit)
	if limit <= 0 {
		limit = s.cfg.ScrollLimit
	}

	var callers []schema.CallerInfo
	var err error
	if strings.TrimSpace(filePath) != "" {
		callers, err = s.qdrant.FindCallersInFile(ctx, repoID, filePath, symbol, limit)
	} else {
		callers, err = s.qdrant.FindCallers(ctx, repoID, symbol, limit)
	}
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":   repoID,
		"symbol":    symbol,
		"file_path": filePath,
		"callers":   callers,
	})
}

func (s *Service) HandleFindCallees(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol := validate.StringFromArgs(args, "symbol", "")
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath := validate.StringFromArgs(args, "file_path", "")

	limit := validate.IntFromArgs(args, "limit", s.cfg.ScrollLimit)
	if limit <= 0 {
		limit = s.cfg.ScrollLimit
	}

	ch, err := s.qdrant.FindChunkByName(ctx, repoID, symbol, filePath, nil, limit)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return mcp.JSONResult(map[string]any{
			"found":     false,
			"repo_id":   repoID,
			"symbol":    symbol,
			"file_path": filePath,
		})
	}

	callees := make([]schema.CalleeInfo, 0, len(ch.Calls))
	for _, call := range ch.Calls {
		callees = append(callees, schema.CalleeInfo{
			Name:       call,
			IsExternal: strings.Contains(call, "."),
		})
	}

	return mcp.JSONResult(map[string]any{
		"found":     true,
		"repo_id":   repoID,
		"symbol":    symbol,
		"file_path": filePath,
		"callees":   callees,
	})
}

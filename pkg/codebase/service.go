package codebase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/codebase/index"
	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/httpclient"
)

type Service struct {
	cfg Config

	qdrant *qdrant.Client
	embed  *embed.MorphClient

	indexers *index.Registry

	jobsMu sync.RWMutex
	jobs   map[string]*indexJob

	watchMu   sync.RWMutex
	watchJobs map[string]*watchJob
}

type indexJob struct {
	id string

	cancel context.CancelFunc

	status string
	err    string

	stats schema.IndexStats
}

func NewServiceFromEnv() (*Service, error) {
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		return nil, err
	}

	hc := httpclient.NewDefault()

	svc := &Service{
		cfg:       cfg,
		qdrant:    qdrant.NewClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.QdrantCollection, cfg.QdrantDistance),
		embed:     embed.NewMorphClient(hc, cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel),
		jobs:      make(map[string]*indexJob),
		watchJobs: make(map[string]*watchJob),
		indexers: index.NewRegistry(
			cfg.MaxFileBytes,
		),
	}

	return svc, nil
}

func (s *Service) HandleIndexStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	root := "."
	if v, ok := args["root"].(string); ok && strings.TrimSpace(v) != "" {
		root = v
	}

	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if repoID == "" {
		derived, derr := deriveRepoID(root)
		if derr != nil {
			return nil, derr
		}
		repoID = derived
	}

	langs := s.indexers.SupportedLanguages()
	if raw, ok := args["languages"].([]any); ok && len(raw) > 0 {
		var out []string
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.ToLower(strings.TrimSpace(s)))
			}
		}
		if len(out) > 0 {
			langs = out
		}
	}

	var exclude []string
	if raw, ok := args["exclude"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				exclude = append(exclude, s)
			}
		}
	}

	fullRefresh := true
	if v, ok := args["full_refresh"].(bool); ok {
		fullRefresh = v
	}

	gitMetadata := s.cfg.GitMetadataDefault
	if v, ok := args["git_metadata"].(bool); ok {
		gitMetadata = v
	}

	embeddings := !s.cfg.DisableEmbeddingsDefault
	if v, ok := args["embeddings"].(bool); ok {
		embeddings = v
	}

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

func (s *Service) HandleStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	var languages []string
	if raw, ok := args["languages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				languages = append(languages, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}
	var chunkTypes []string
	if raw, ok := args["chunk_types"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				chunkTypes = append(chunkTypes, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

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
	repoID, _ := args["repo_id"].(string)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required")
	}
	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return mcp.JSONResult(map[string]any{
			"ok":      false,
			"error":   "confirm=true is required",
			"repo_id": repoID,
		})
	}
	dryRun, _ := args["dry_run"].(bool)
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

func (s *Service) HandleIndexPoll(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	jobID, _ := args["job_id"].(string)
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
	jobID, _ := args["job_id"].(string)
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

func (s *Service) HandleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	limit := 10
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		limit = int(v)
	}
	if v, ok := args["limit"].(int); ok && v > 0 {
		limit = v
	}

	includeContent := false
	if v, ok := args["include_content"].(bool); ok {
		includeContent = v
	}

	rerank := "none"
	if v, ok := args["rerank"].(string); ok && strings.TrimSpace(v) != "" {
		rerank = strings.ToLower(strings.TrimSpace(v))
	}
	lexicalWeight := 0.15
	switch v := args["lexical_weight"].(type) {
	case float64:
		lexicalWeight = v
	case int:
		lexicalWeight = float64(v)
	}
	if lexicalWeight < 0 {
		lexicalWeight = 0
	}
	if lexicalWeight > 1 {
		lexicalWeight = 1
	}

	var languages []string
	if raw, ok := args["languages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				languages = append(languages, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}
	var chunkTypes []string
	if raw, ok := args["chunk_types"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				chunkTypes = append(chunkTypes, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

	vec, err := s.embed.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	results, err := s.qdrant.Search(ctx, repoID, vec, limit, languages, chunkTypes, true)
	if err != nil {
		return nil, err
	}

	if rerank == "hybrid" && lexicalWeight > 0 {
		toks := lexicalTokens(query)
		if len(toks) > 0 {
			type scored struct {
				res      schema.SearchResult
				combined float64
			}
			scoredResults := make([]scored, 0, len(results))
			for _, r := range results {
				text := strings.ToLower(r.Chunk.Signature + "\n" + r.Chunk.Docstring + "\n" + r.Chunk.Content)
				hits := 0
				for _, tok := range toks {
					if strings.Contains(text, tok) {
						hits++
					}
				}
				lex := float64(hits) / float64(len(toks))
				combined := r.Score*(1-lexicalWeight) + lex*lexicalWeight
				scoredResults = append(scoredResults, scored{res: r, combined: combined})
			}
			sort.SliceStable(scoredResults, func(i, j int) bool {
				return scoredResults[i].combined > scoredResults[j].combined
			})
			results = results[:0]
			for _, sr := range scoredResults {
				results = append(results, sr.res)
			}
		}
	}

	if !includeContent {
		for i := range results {
			results[i].Chunk.Content = ""
		}
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":        repoID,
		"query":          query,
		"rerank":         rerank,
		"lexical_weight": lexicalWeight,
		"results":        results,
	})
}

func (s *Service) HandleGetDefinition(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol, _ := args["symbol"].(string)
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath, _ := args["file_path"].(string)

	limit := s.cfg.ScrollLimit
	switch v := args["limit"].(type) {
	case float64:
		if int(v) > 0 {
			limit = int(v)
		}
	case int:
		if v > 0 {
			limit = v
		}
	}

	includeContent := false
	if v, ok := args["include_content"].(bool); ok {
		includeContent = v
	}

	var languages []string
	if raw, ok := args["languages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				languages = append(languages, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

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
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol, _ := args["symbol"].(string)
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath, _ := args["file_path"].(string)

	limit := s.cfg.ScrollLimit
	switch v := args["limit"].(type) {
	case float64:
		if int(v) > 0 {
			limit = int(v)
		}
	case int:
		if v > 0 {
			limit = v
		}
	}

	includeDefinitions := true
	if v, ok := args["include_definitions"].(bool); ok {
		includeDefinitions = v
	}
	includeCallers := true
	if v, ok := args["include_callers"].(bool); ok {
		includeCallers = v
	}
	includeModules := false
	if v, ok := args["include_modules"].(bool); ok {
		includeModules = v
	}
	includeContent := false
	if v, ok := args["include_content"].(bool); ok {
		includeContent = v
	}

	var languages []string
	if raw, ok := args["languages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				languages = append(languages, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

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
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	filePath, _ := args["file_path"].(string)
	if strings.TrimSpace(filePath) == "" {
		return nil, fmt.Errorf("file_path is required")
	}

	line := 0
	switch v := args["line_number"].(type) {
	case float64:
		line = int(v)
	case int:
		line = v
	}
	if line <= 0 {
		return nil, fmt.Errorf("line_number must be > 0")
	}

	includeCallers := true
	if v, ok := args["include_callers"].(bool); ok {
		includeCallers = v
	}
	includeCallees := true
	if v, ok := args["include_callees"].(bool); ok {
		includeCallees = v
	}

	relatedLimit := 5
	switch v := args["related_limit"].(type) {
	case float64:
		if int(v) > 0 {
			relatedLimit = int(v)
		}
	case int:
		if v > 0 {
			relatedLimit = v
		}
	}

	includeContent := false
	if v, ok := args["include_content"].(bool); ok {
		includeContent = v
	}

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
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol, _ := args["symbol"].(string)
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath, _ := args["file_path"].(string)

	limit := s.cfg.ScrollLimit
	switch v := args["limit"].(type) {
	case float64:
		if int(v) > 0 {
			limit = int(v)
		}
	case int:
		if v > 0 {
			limit = v
		}
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
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol, _ := args["symbol"].(string)
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath, _ := args["file_path"].(string)

	limit := s.cfg.ScrollLimit
	switch v := args["limit"].(type) {
	case float64:
		if int(v) > 0 {
			limit = int(v)
		}
	case int:
		if v > 0 {
			limit = v
		}
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

func (s *Service) HandleTextSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	limit := 10
	switch v := args["limit"].(type) {
	case float64:
		if int(v) > 0 {
			limit = int(v)
		}
	case int:
		if v > 0 {
			limit = v
		}
	}
	if limit > 200 {
		limit = 200
	}

	maxScan := 2000
	switch v := args["max_scan"].(type) {
	case float64:
		if int(v) > 0 {
			maxScan = int(v)
		}
	case int:
		if v > 0 {
			maxScan = v
		}
	}
	if maxScan > 50_000 {
		maxScan = 50_000
	}

	caseSensitive := false
	if v, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = v
	}

	includeContent := false
	if v, ok := args["include_content"].(bool); ok {
		includeContent = v
	}

	filePath, _ := args["file_path"].(string)

	var languages []string
	if raw, ok := args["languages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				languages = append(languages, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

	var chunkTypes []string
	if raw, ok := args["chunk_types"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				chunkTypes = append(chunkTypes, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

	filter := qdrant.Filter(repoID, filePath, languages, chunkTypes)
	chunks, err := s.qdrant.ScrollChunks(ctx, filter, maxScan)
	if err != nil {
		return nil, err
	}

	toks := lexicalTokens(query)
	// If query is too short for tokenization, fall back to a literal substring match.
	if len(toks) == 0 {
		toks = []string{strings.ToLower(query)}
	}

	type scored struct {
		chunk schema.Chunk
		score float64
		hits  int
	}
	scoredChunks := make([]scored, 0, len(chunks))

	var q string
	if caseSensitive {
		q = query
	} else {
		q = strings.ToLower(query)
	}

	for _, ch := range chunks {
		text := ch.Signature + "\n" + ch.Docstring + "\n" + ch.Content
		if !caseSensitive {
			text = strings.ToLower(text)
		}

		// Prefer exact substring match if possible.
		exact := 0
		if q != "" && strings.Contains(text, q) {
			exact = 1
		}

		hits := 0
		for _, tok := range toks {
			if tok != "" && strings.Contains(text, tok) {
				hits++
			}
		}
		if hits == 0 && exact == 0 {
			continue
		}

		score := float64(hits) / float64(len(toks))
		if exact > 0 {
			score = minFloat64(1.0, score+0.25)
		}
		scoredChunks = append(scoredChunks, scored{chunk: ch, score: score, hits: hits})
	}

	sort.SliceStable(scoredChunks, func(i, j int) bool {
		if scoredChunks[i].score == scoredChunks[j].score {
			if scoredChunks[i].hits == scoredChunks[j].hits {
				if scoredChunks[i].chunk.FilePath == scoredChunks[j].chunk.FilePath {
					return scoredChunks[i].chunk.StartLine < scoredChunks[j].chunk.StartLine
				}
				return scoredChunks[i].chunk.FilePath < scoredChunks[j].chunk.FilePath
			}
			return scoredChunks[i].hits > scoredChunks[j].hits
		}
		return scoredChunks[i].score > scoredChunks[j].score
	})

	results := make([]schema.SearchResult, 0, minInt(limit, len(scoredChunks)))
	for i := 0; i < len(scoredChunks) && len(results) < limit; i++ {
		ch := scoredChunks[i].chunk
		if !includeContent {
			ch.Content = ""
		}
		results = append(results, schema.SearchResult{
			Score: scoredChunks[i].score,
			Chunk: ch,
		})
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":         repoID,
		"query":           query,
		"file_path":       filePath,
		"case_sensitive":  caseSensitive,
		"scanned_chunks":  len(chunks),
		"matched_chunks":  len(scoredChunks),
		"max_scan":        maxScan,
		"limit":           limit,
		"languages":       languages,
		"chunk_types":     chunkTypes,
		"include_content": includeContent,
		"results":         results,
	})
}

type graphNode struct {
	Symbol     string        `json:"symbol"`
	External   bool          `json:"external,omitempty"`
	Definition *schema.Chunk `json:"definition,omitempty"`
}

type graphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	CallExpr string `json:"call_expr,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Line     int    `json:"line,omitempty"`
}

func (s *Service) HandleCallGraph(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol, _ := args["symbol"].(string)
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath, _ := args["file_path"].(string)

	direction := "out"
	if v, ok := args["direction"].(string); ok && strings.TrimSpace(v) != "" {
		direction = strings.ToLower(strings.TrimSpace(v))
	}
	if direction != "out" && direction != "in" && direction != "both" {
		return nil, fmt.Errorf("direction must be one of: out, in, both")
	}

	depth := 2
	switch v := args["depth"].(type) {
	case float64:
		if int(v) >= 0 {
			depth = int(v)
		}
	case int:
		if v >= 0 {
			depth = v
		}
	}
	if depth > 10 {
		depth = 10
	}

	limit := s.cfg.ScrollLimit
	switch v := args["limit"].(type) {
	case float64:
		if int(v) > 0 {
			limit = int(v)
		}
	case int:
		if v > 0 {
			limit = v
		}
	}

	maxNodes := 200
	switch v := args["max_nodes"].(type) {
	case float64:
		if int(v) > 0 {
			maxNodes = int(v)
		}
	case int:
		if v > 0 {
			maxNodes = v
		}
	}
	if maxNodes > 2000 {
		maxNodes = 2000
	}

	includeExternal := true
	if v, ok := args["include_external"].(bool); ok {
		includeExternal = v
	}

	var languages []string
	if raw, ok := args["languages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				languages = append(languages, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

	nodes := map[string]*graphNode{}
	addNode := func(sym string, external bool) {
		if strings.TrimSpace(sym) == "" {
			return
		}
		if nodes[sym] == nil {
			nodes[sym] = &graphNode{Symbol: sym, External: external}
		} else if !external {
			nodes[sym].External = false
		}
	}

	edges := []graphEdge{}
	addEdge := func(e graphEdge) {
		if strings.TrimSpace(e.From) == "" || strings.TrimSpace(e.To) == "" {
			return
		}
		edges = append(edges, e)
	}

	seen := map[string]bool{}
	frontier := []string{symbol}
	seen[symbol] = true
	addNode(symbol, false)

	attachDefinition := func(sym string, fp string) *schema.Chunk {
		ch, err := s.qdrant.FindChunkByName(ctx, repoID, sym, fp, languages, limit)
		if err != nil || ch == nil {
			return nil
		}
		ch.Content = ""
		return ch
	}

	if def := attachDefinition(symbol, filePath); def != nil {
		nodes[symbol].Definition = def
	}

	for level := 0; level < depth; level++ {
		if len(frontier) == 0 {
			break
		}

		next := make([]string, 0, len(frontier)*2)
		for _, cur := range frontier {
			if direction == "out" || direction == "both" {
				def := nodes[cur].Definition
				if def == nil {
					def = attachDefinition(cur, "")
					nodes[cur].Definition = def
				}
				if def != nil {
					for _, call := range def.Calls {
						tok := qdrant.NormalizeCallToken(call)
						if tok == "" {
							continue
						}
						if tok == cur {
							continue
						}
						if includeExternal {
							addNode(tok, strings.Contains(call, ".") || strings.Contains(call, "::"))
						} else {
							addNode(tok, false)
						}
						addEdge(graphEdge{From: cur, To: tok, Kind: "calls", CallExpr: call})
						if len(seen) < maxNodes && !seen[tok] {
							seen[tok] = true
							next = append(next, tok)
						}
					}
				}
			}

			if direction == "in" || direction == "both" {
				callers, err := s.qdrant.FindCallers(ctx, repoID, cur, limit)
				if err != nil {
					continue
				}
				for _, c := range callers {
					if strings.TrimSpace(c.FunctionName) == "" {
						continue
					}
					addNode(c.FunctionName, false)
					addEdge(graphEdge{
						From:     c.FunctionName,
						To:       cur,
						Kind:     "calls",
						CallExpr: c.CallExpr,
						FilePath: c.FilePath,
						Line:     c.LineNumber,
					})
					if len(seen) < maxNodes && !seen[c.FunctionName] {
						seen[c.FunctionName] = true
						next = append(next, c.FunctionName)
					}
				}
			}
		}

		frontier = next
	}

	outNodes := make([]graphNode, 0, len(nodes))
	for _, n := range nodes {
		outNodes = append(outNodes, *n)
	}
	sort.Slice(outNodes, func(i, j int) bool { return outNodes[i].Symbol < outNodes[j].Symbol })

	return mcp.JSONResult(map[string]any{
		"repo_id":   repoID,
		"symbol":    symbol,
		"file_path": filePath,
		"direction": direction,
		"depth":     depth,
		"max_nodes": maxNodes,
		"nodes":     outNodes,
		"edges":     edges,
		"languages": languages,
		"truncated": len(seen) >= maxNodes,
	})
}

type moduleGraphNode struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // file|import
	FilePath string `json:"file_path,omitempty"`
	Import   string `json:"import,omitempty"`
}

type moduleGraphEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"` // imports
	ImportRaw  string `json:"import_raw,omitempty"`
	ResolvedTo string `json:"resolved_to,omitempty"` // file_path when resolved
}

func (s *Service) HandleModuleGraph(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	maxFiles := 512
	switch v := args["max_files"].(type) {
	case float64:
		if int(v) > 0 {
			maxFiles = int(v)
		}
	case int:
		if v > 0 {
			maxFiles = v
		}
	}
	if maxFiles > 10_000 {
		maxFiles = 10_000
	}

	maxEdges := 4000
	switch v := args["max_edges"].(type) {
	case float64:
		if int(v) > 0 {
			maxEdges = int(v)
		}
	case int:
		if v > 0 {
			maxEdges = v
		}
	}
	if maxEdges > 100_000 {
		maxEdges = 100_000
	}

	includeExternal := true
	if v, ok := args["include_external"].(bool); ok {
		includeExternal = v
	}

	var languages []string
	if raw, ok := args["languages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				languages = append(languages, strings.ToLower(strings.TrimSpace(s)))
			}
		}
	}

	modules, err := s.qdrant.ListModules(ctx, repoID, maxFiles)
	if err != nil {
		return nil, err
	}

	if len(languages) > 0 {
		want := map[string]bool{}
		for _, l := range languages {
			want[l] = true
		}
		filtered := modules[:0]
		for _, m := range modules {
			if want[strings.ToLower(m.Language)] {
				filtered = append(filtered, m)
			}
		}
		modules = filtered
	}

	fileSet := map[string]bool{}
	for _, m := range modules {
		if strings.TrimSpace(m.FilePath) != "" {
			fileSet[m.FilePath] = true
		}
	}

	resolveRelativeJSImport := func(fromFile string, raw string) (string, bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" || !strings.HasPrefix(raw, ".") {
			return "", false
		}
		dir := path.Dir(fromFile)
		if dir == "." {
			dir = ""
		}
		base := path.Clean(path.Join(dir, raw))
		cands := []string{
			base,
			base + ".ts",
			base + ".tsx",
			base + ".js",
			base + ".jsx",
			path.Join(base, "index.ts"),
			path.Join(base, "index.tsx"),
			path.Join(base, "index.js"),
			path.Join(base, "index.jsx"),
		}
		for _, c := range cands {
			if fileSet[c] {
				return c, true
			}
		}
		return "", false
	}

	resolvePythonImport := func(raw string) (string, bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", false
		}
		if strings.ContainsAny(raw, "/\\") {
			return "", false
		}
		base := strings.ReplaceAll(raw, ".", "/")
		cands := []string{
			base + ".py",
			base + ".pyi",
			path.Join(base, "__init__.py"),
			path.Join(base, "__init__.pyi"),
		}
		for _, c := range cands {
			if fileSet[c] {
				return c, true
			}
		}
		return "", false
	}

	nodes := map[string]moduleGraphNode{}
	addNode := func(n moduleGraphNode) {
		if n.ID == "" {
			return
		}
		if _, ok := nodes[n.ID]; !ok {
			nodes[n.ID] = n
		}
	}

	edges := make([]moduleGraphEdge, 0, 0)

	for _, m := range modules {
		from := m.FilePath
		if strings.TrimSpace(from) == "" {
			continue
		}
		addNode(moduleGraphNode{ID: "file:" + from, Kind: "file", FilePath: from})

		for _, imp := range m.Imports {
			if strings.TrimSpace(imp) == "" {
				continue
			}
			toID := "import:" + imp
			resolved := ""

			switch strings.ToLower(m.Language) {
			case "typescript", "javascript":
				if r, ok := resolveRelativeJSImport(from, imp); ok {
					resolved = r
					toID = "file:" + r
					addNode(moduleGraphNode{ID: toID, Kind: "file", FilePath: r})
				}
			case "python":
				if r, ok := resolvePythonImport(imp); ok {
					resolved = r
					toID = "file:" + r
					addNode(moduleGraphNode{ID: toID, Kind: "file", FilePath: r})
				}
			}

			if resolved == "" {
				if !includeExternal {
					continue
				}
				addNode(moduleGraphNode{ID: toID, Kind: "import", Import: imp})
			}

			edges = append(edges, moduleGraphEdge{
				From:       "file:" + from,
				To:         toID,
				Kind:       "imports",
				ImportRaw:  imp,
				ResolvedTo: resolved,
			})
			if len(edges) >= maxEdges {
				break
			}
		}
		if len(edges) >= maxEdges {
			break
		}
	}

	outNodes := make([]moduleGraphNode, 0, len(nodes))
	for _, n := range nodes {
		outNodes = append(outNodes, n)
	}
	sort.Slice(outNodes, func(i, j int) bool { return outNodes[i].ID < outNodes[j].ID })

	return mcp.JSONResult(map[string]any{
		"repo_id":            repoID,
		"max_files":          maxFiles,
		"max_edges":          maxEdges,
		"include_external":   includeExternal,
		"languages":          languages,
		"nodes":              outNodes,
		"edges":              edges,
		"truncated_by_files": len(modules) >= maxFiles,
		"truncated_by_edges": len(edges) >= maxEdges,
	})
}

func (s *Service) runIndexJob(
	ctx context.Context,
	jobID string,
	repoID string,
	root string,
	languages []string,
	exclude []string,
	fullRefresh bool,
	gitMetadata bool,
	embeddings bool,
) {
	defer func() {
		// keep job state for debugging; future: add TTL cleanup
	}()

	absRoot, err := filepath.Abs(root)
	if err != nil {
		s.setJobFailed(jobID, fmt.Sprintf("resolve root: %v", err))
		return
	}

	gitRoot := ""
	if gitMetadata {
		if gr, ok := detectGitRoot(ctx, absRoot); ok {
			gitRoot = gr
		} else {
			gitMetadata = false
		}
	}

	if fullRefresh {
		exists, existsErr := s.qdrant.CollectionExists(ctx)
		if existsErr != nil {
			s.setJobFailed(jobID, fmt.Sprintf("qdrant collection check: %v", existsErr))
			return
		}
		if exists {
			if deleteRepoErr := s.qdrant.DeleteRepo(ctx, repoID); deleteRepoErr != nil {
				s.setJobFailed(jobID, fmt.Sprintf("qdrant delete repo: %v", deleteRepoErr))
				return
			}
		}
	}

	files, err := s.indexers.CollectFiles(absRoot, languages, exclude)
	if err != nil {
		s.setJobFailed(jobID, fmt.Sprintf("collect files: %v", err))
		return
	}

	s.jobsMu.Lock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.FilesTotal = len(files)
	}
	s.jobsMu.Unlock()

	type pendingChunk struct {
		chunk schema.Chunk
		text  string
	}

	var (
		pending     []pendingChunk
		ensured     bool
		vectorSize  int
		embedBatch  = s.cfg.EmbedBatchSize
		upsertBatch = s.cfg.UpsertBatchSize
	)

	if !embeddings {
		exists, size, err := s.qdrant.GetCollectionVectorSize(ctx)
		if err != nil {
			s.setJobFailed(jobID, fmt.Sprintf("qdrant collection info: %v", err))
			return
		}
		if exists {
			if size <= 0 {
				s.setJobFailed(jobID, "qdrant collection vector size unknown")
				return
			}
			vectorSize = size
			ensured = true
		} else {
			vectorSize = 1
			if err := s.qdrant.EnsureCollection(ctx, vectorSize); err != nil {
				s.setJobFailed(jobID, fmt.Sprintf("qdrant ensure collection: %v", err))
				return
			}
			ensured = true
		}
	}

	dummyVec := func() []float64 {
		v := make([]float64, vectorSize)
		if len(v) > 0 {
			v[0] = 1
		}
		return v
	}

	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		var vectors [][]float64
		if embeddings {
			texts := make([]string, 0, len(pending))
			for _, p := range pending {
				texts = append(texts, p.text)
			}
			var embedErr error
			vectors, embedErr = s.embed.EmbedDocuments(ctx, texts)
			if embedErr != nil {
				return embedErr
			}
			if !ensured {
				if len(vectors) == 0 || len(vectors[0]) == 0 {
					return fmt.Errorf("embedding returned empty vector")
				}
				vectorSize = len(vectors[0])
				if ensureErr := s.qdrant.EnsureCollection(ctx, vectorSize); ensureErr != nil {
					return ensureErr
				}
				ensured = true
			}
		} else {
			vectors = make([][]float64, len(pending))
			for i := range vectors {
				vectors[i] = dummyVec()
			}
		}

		points := make([]qdrant.Point, 0, len(pending))
		for i, p := range pending {
			points = append(points, qdrant.Point{
				ID:      p.chunk.ID,
				Vector:  vectors[i],
				Payload: qdrant.ChunkToPayload(p.chunk, true),
			})
		}
		for i := 0; i < len(points); i += upsertBatch {
			end := i + upsertBatch
			if end > len(points) {
				end = len(points)
			}
			if upsertErr := s.qdrant.Upsert(ctx, points[i:end], true); upsertErr != nil {
				return upsertErr
			}
		}
		pending = pending[:0]
		return nil
	}

	for _, file := range files {
		select {
		case <-ctx.Done():
			s.setJobCanceled(jobID)
			return
		default:
		}

		rel, err := filepath.Rel(absRoot, file)
		if err != nil {
			s.incrementJobError(jobID, fmt.Sprintf("rel path: %v", err))
			continue
		}
		relSlash := filepath.ToSlash(rel)

		if !fullRefresh {
			b, readErr := os.ReadFile(file)
			if readErr != nil {
				s.incrementJobError(jobID, fmt.Sprintf("read file for hash %s: %v", relSlash, readErr))
				s.incrementFilesDone(jobID, 0)
				continue
			}
			hash := schema.ContentHash(string(b))

			prev, ok, hashErr := s.qdrant.GetModuleContentHash(ctx, repoID, relSlash)
			if hashErr != nil {
				s.incrementJobError(jobID, fmt.Sprintf("module hash lookup %s: %v", relSlash, hashErr))
			} else if ok && prev == hash {
				s.incrementFilesSkipped(jobID)
				continue
			}
		}

		if deleteFileErr := s.qdrant.DeleteFile(ctx, repoID, relSlash); deleteFileErr != nil {
			// If collection doesn't exist yet, deletion can fail; treat as non-fatal before first ensure.
			if !ensured && errors.Is(deleteFileErr, qdrant.ErrCollectionNotFound) {
				// ignore
			} else {
				s.incrementJobError(jobID, fmt.Sprintf("delete file: %v", deleteFileErr))
			}
		}

		chunks, err := s.indexers.IndexFile(ctx, absRoot, file, repoID)
		if err != nil {
			s.incrementJobError(jobID, fmt.Sprintf("index %s: %v", rel, err))
			s.incrementFilesDone(jobID, 0)
			continue
		}

		if gitMetadata {
			if err := annotateChunksWithGitMetadata(ctx, gitRoot, file, chunks); err != nil {
				s.incrementJobError(jobID, fmt.Sprintf("git metadata %s: %v", relSlash, err))
			}
		}

		for _, ch := range chunks {
			text := ch.Content
			if ch.Docstring != "" {
				text = ch.Docstring + "\n\n" + text
			}
			pending = append(pending, pendingChunk{chunk: ch, text: text})
		}

		s.incrementFilesDone(jobID, len(chunks))

		if len(pending) >= embedBatch {
			if err := flush(); err != nil {
				s.setJobFailed(jobID, fmt.Sprintf("flush chunks: %v", err))
				return
			}
		}
	}

	if err := flush(); err != nil {
		s.setJobFailed(jobID, fmt.Sprintf("final flush chunks: %v", err))
		return
	}

	s.setJobDone(jobID)
	_ = vectorSize
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func (s *Service) setJobFailed(jobID, msg string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "failed"
		job.err = msg
		job.stats.FinishedAt = time.Now()
	}
}

func (s *Service) setJobDone(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "done"
		job.stats.FinishedAt = time.Now()
	}
}

func (s *Service) setJobCanceled(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.status = "canceled"
		job.stats.FinishedAt = time.Now()
	}
}

func (s *Service) incrementFilesDone(jobID string, chunks int) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.FilesDone++
		job.stats.ChunksTotal += chunks
	}
}

func (s *Service) incrementFilesSkipped(jobID string) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.FilesDone++
		job.stats.FilesSkipped++
	}
}

func (s *Service) incrementJobError(jobID, msg string) {
	_, _ = fmt.Fprintln(os.Stderr, msg)
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	if job := s.jobs[jobID]; job != nil {
		job.stats.Errors++
		job.err = msg
	}
}

func deriveRepoID(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}

	gitRoot := absRoot
	if out, err := exec.Command("git", "-C", absRoot, "rev-parse", "--show-toplevel").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			gitRoot = s
		}
	}

	if out, err := exec.Command("git", "-C", gitRoot, "config", "--get", "remote.origin.url").Output(); err == nil {
		remote := strings.TrimSpace(string(out))
		if remote != "" {
			remote = strings.TrimSuffix(remote, ".git")
			return schema.ShortSHA256Hex(remote), nil
		}
	}

	return schema.ShortSHA256Hex(gitRoot), nil
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

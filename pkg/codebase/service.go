package codebase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/chunker"
	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/codebase/index"
	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/validate"
)

// Embedding provider defaults. These are used as fallbacks when the
// configured provider differs from the base config's default values.
const (
	defaultFlexInferURL   = "http://localhost:8080"
	defaultFlexInferModel = "BAAI/bge-large-en-v1.5"
	defaultOllamaURL      = "http://localhost:11434"
	defaultOllamaModel    = "nomic-embed-text"
	defaultMorphBaseURL   = "https://api.morphllm.com/v1"
	defaultMorphModel     = "morph-embedding-v3"
)

type Service struct {
	cfg Config

	qdrant *qdrant.Client
	embed  embed.Embedder

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

	// Select embedder based on configuration
	var embedder embed.Embedder
	if cfg.DisableEmbeddingsDefault {
		// Use dummy embedder when embeddings are disabled
		embedder = embed.NewDummyEmbedder(1)
	} else {
		switch cfg.EmbedProvider {
		case "flexinfer":
			// FlexInfer TEI backend (OpenAI-compatible)
			baseURL := cfg.EmbedBaseURL
			if baseURL == "" || baseURL == defaultMorphBaseURL {
				baseURL = env.String("FLEXINFER_URL", defaultFlexInferURL) + "/v1"
			}
			model := cfg.EmbedModel
			if model == "" || model == defaultMorphModel {
				model = defaultFlexInferModel
			}
			embedder = embed.NewFlexInferClient(hc, baseURL, cfg.EmbedAPIKey, model)
		case "ollama":
			// Ollama local embeddings
			baseURL := cfg.EmbedBaseURL
			if baseURL == "" || baseURL == defaultMorphBaseURL {
				baseURL = env.String("OLLAMA_BASE_URL", defaultOllamaURL)
			}
			model := cfg.EmbedModel
			if model == "" || model == defaultMorphModel {
				model = defaultOllamaModel
			}
			embedder = embed.NewOllamaClient(hc, baseURL, model)
		case "dummy", "none":
			// Explicit dummy mode
			embedder = embed.NewDummyEmbedder(1)
		default:
			// Default to Morph/OpenAI-compatible API
			embedder = embed.NewMorphClient(hc, cfg.EmbedBaseURL, cfg.EmbedAPIKey, cfg.EmbedModel)
		}
	}

	svc := &Service{
		cfg:       cfg,
		qdrant:    qdrant.NewClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.QdrantCollection, cfg.QdrantDistance),
		embed:     embedder,
		jobs:      make(map[string]*indexJob),
		watchJobs: make(map[string]*watchJob),
		indexers: index.NewRegistry(
			cfg.MaxFileBytes,
		),
	}

	return svc, nil
}

// NewServiceWithEmbedder creates a service with a custom embedder.
func NewServiceWithEmbedder(cfg Config, embedder embed.Embedder) (*Service, error) {
	hc := httpclient.NewDefault()

	svc := &Service{
		cfg:       cfg,
		qdrant:    qdrant.NewClient(hc, cfg.QdrantURL, cfg.QdrantAPIKey, cfg.QdrantCollection, cfg.QdrantDistance),
		embed:     embedder,
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

func (s *Service) HandleStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
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
	repoID, _ := args["repo_id"].(string)
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

	limit := validate.IntFromArgs(args, "limit", 10)
	if limit <= 0 {
		limit = 10
	}

	includeContent := validate.BoolFromArgs(args, "include_content", false)

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

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))
	chunkTypes := normalizeStringSlice(validate.StringSliceFromArgs(args, "chunk_types"))

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

	limit := validate.IntFromArgs(args, "limit", 10)
	if limit <= 0 {
		limit = 10
	}
	if limit > 200 {
		limit = 200
	}

	maxScan := validate.IntFromArgs(args, "max_scan", 2000)
	if maxScan <= 0 {
		maxScan = 2000
	}
	if maxScan > 50_000 {
		maxScan = 50_000
	}

	caseSensitive := validate.BoolFromArgs(args, "case_sensitive", false)
	includeContent := validate.BoolFromArgs(args, "include_content", false)

	filePath, _ := args["file_path"].(string)

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))
	chunkTypes := normalizeStringSlice(validate.StringSliceFromArgs(args, "chunk_types"))

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

func normalizeRenderFormat(v any) (string, error) {
	render := "none"
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		render = strings.ToLower(strings.TrimSpace(s))
	}
	switch render {
	case "none", "mermaid", "dot":
		return render, nil
	default:
		return "", fmt.Errorf("render must be one of: none, mermaid, dot")
	}
}

func escapeMermaidLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func escapeDotLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func renderCallGraph(render string, nodes []graphNode, edges []graphEdge) string {
	idBySymbol := make(map[string]string, len(nodes))
	for i := range nodes {
		idBySymbol[nodes[i].Symbol] = fmt.Sprintf("n%d", i)
	}

	var b strings.Builder
	switch render {
	case "mermaid":
		b.WriteString("graph TD\n")
		for i := range nodes {
			id := idBySymbol[nodes[i].Symbol]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString("[\"")
			b.WriteString(escapeMermaidLabel(nodes[i].Symbol))
			b.WriteString("\"]\n")
		}
		for _, e := range edges {
			from := idBySymbol[e.From]
			to := idBySymbol[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" --> ")
			b.WriteString(to)
			b.WriteByte('\n')
		}
	case "dot":
		b.WriteString("digraph G {\n")
		for i := range nodes {
			id := idBySymbol[nodes[i].Symbol]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString(" [label=\"")
			b.WriteString(escapeDotLabel(nodes[i].Symbol))
			b.WriteString("\"];\n")
		}
		for _, e := range edges {
			from := idBySymbol[e.From]
			to := idBySymbol[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" -> ")
			b.WriteString(to)
			b.WriteString(";\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
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

	depth := validate.IntFromArgs(args, "depth", 2)
	if depth < 0 {
		depth = 2
	}
	if depth > 10 {
		depth = 10
	}

	limit := validate.IntFromArgs(args, "limit", s.cfg.ScrollLimit)
	if limit <= 0 {
		limit = s.cfg.ScrollLimit
	}

	maxNodes := validate.IntFromArgs(args, "max_nodes", 200)
	if maxNodes <= 0 {
		maxNodes = 200
	}
	if maxNodes > 2000 {
		maxNodes = 2000
	}

	includeExternal := validate.BoolFromArgs(args, "include_external", true)

	render, err := normalizeRenderFormat(args["render"])
	if err != nil {
		return nil, err
	}

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))

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

	rendered := ""
	if render != "none" {
		rendered = renderCallGraph(render, outNodes, edges)
	}

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
		"render":    render,
		"rendered":  rendered,
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

func renderModuleGraph(render string, nodes []moduleGraphNode, edges []moduleGraphEdge) string {
	idByNode := make(map[string]string, len(nodes))
	labelByNode := make(map[string]string, len(nodes))
	for i := range nodes {
		id := fmt.Sprintf("n%d", i)
		idByNode[nodes[i].ID] = id
		switch nodes[i].Kind {
		case "file":
			labelByNode[nodes[i].ID] = nodes[i].FilePath
		case "import":
			labelByNode[nodes[i].ID] = nodes[i].Import
		default:
			labelByNode[nodes[i].ID] = nodes[i].ID
		}
	}

	var b strings.Builder
	switch render {
	case "mermaid":
		b.WriteString("graph TD\n")
		for i := range nodes {
			nid := nodes[i].ID
			id := idByNode[nid]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString("[\"")
			b.WriteString(escapeMermaidLabel(labelByNode[nid]))
			b.WriteString("\"]\n")
		}
		for _, e := range edges {
			from := idByNode[e.From]
			to := idByNode[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" --> ")
			b.WriteString(to)
			b.WriteByte('\n')
		}
	case "dot":
		b.WriteString("digraph G {\n")
		for i := range nodes {
			nid := nodes[i].ID
			id := idByNode[nid]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString(" [label=\"")
			b.WriteString(escapeDotLabel(labelByNode[nid]))
			b.WriteString("\"];\n")
		}
		for _, e := range edges {
			from := idByNode[e.From]
			to := idByNode[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" -> ")
			b.WriteString(to)
			b.WriteString(";\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func (s *Service) HandleModuleGraph(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	maxFiles := validate.IntFromArgs(args, "max_files", 512)
	if maxFiles <= 0 {
		maxFiles = 512
	}
	if maxFiles > 10_000 {
		maxFiles = 10_000
	}

	maxEdges := validate.IntFromArgs(args, "max_edges", 4000)
	if maxEdges <= 0 {
		maxEdges = 4000
	}
	if maxEdges > 100_000 {
		maxEdges = 100_000
	}

	includeExternal := validate.BoolFromArgs(args, "include_external", true)

	render, err := normalizeRenderFormat(args["render"])
	if err != nil {
		return nil, err
	}

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))

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

	edges := make([]moduleGraphEdge, 0)

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

	rendered := ""
	if render != "none" {
		rendered = renderModuleGraph(render, outNodes, edges)
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":            repoID,
		"max_files":          maxFiles,
		"max_edges":          maxEdges,
		"include_external":   includeExternal,
		"languages":          languages,
		"nodes":              outNodes,
		"edges":              edges,
		"render":             render,
		"rendered":           rendered,
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
		chunk  schema.Chunk
		text   string
		vector []float64
	}
	type indexedFile struct {
		rel       string
		chunks    []schema.Chunk
		fileCache map[string][]float64
		skipped   bool
		err       error
	}

	var (
		pending     []pendingChunk
		ensured     bool
		vectorSize  int
		embedBatch  = s.cfg.EmbedBatchSize
		upsertBatch = s.cfg.UpsertBatchSize
	)

	embedModel := ""
	if embeddings {
		embedModel = s.embed.Model()
	}

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
		if embeddings {
			var (
				texts   []string
				indices []int
			)
			for i, p := range pending {
				if len(p.vector) > 0 {
					continue
				}
				texts = append(texts, p.text)
				indices = append(indices, i)
			}

			if len(texts) > 0 {
				vectors, embedErr := s.embed.EmbedDocuments(ctx, texts)
				if embedErr != nil {
					return embedErr
				}
				if len(vectors) != len(texts) {
					return fmt.Errorf("embedding returned %d vectors for %d texts", len(vectors), len(texts))
				}
				for j, idx := range indices {
					pending[idx].vector = vectors[j]
				}
			}

			if !ensured {
				for _, p := range pending {
					if len(p.vector) > 0 {
						vectorSize = len(p.vector)
						break
					}
				}
				if vectorSize <= 0 {
					return fmt.Errorf("embedding returned empty vector")
				}
				if ensureErr := s.ensureCollectionForVector(ctx, vectorSize, fullRefresh); ensureErr != nil {
					return ensureErr
				}
				ensured = true
			}
		} else {
			for i := range pending {
				pending[i].vector = dummyVec()
			}
		}

		points := make([]qdrant.Point, 0, len(pending))
		for _, p := range pending {
			if len(p.vector) == 0 {
				return fmt.Errorf("missing vector for chunk %s", p.chunk.ID)
			}
			points = append(points, qdrant.Point{
				ID:      p.chunk.ID,
				Vector:  p.vector,
				Payload: qdrant.ChunkToPayload(p.chunk, true, embedModel),
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

	workerJobs := make(chan string, max(1, min(len(files), s.cfg.IndexConcurrency*4)))
	results := make(chan indexedFile, max(1, min(len(files), s.cfg.IndexConcurrency*4)))
	workers := s.cfg.IndexConcurrency
	if workers <= 0 {
		workers = 4
	}

	var wg sync.WaitGroup
	sendResult := func(r indexedFile) {
		select {
		case results <- r:
		case <-ctx.Done():
		}
	}

	worker := func() {
		defer wg.Done()
		for absPath := range workerJobs {
			select {
			case <-ctx.Done():
				return
			default:
			}

			rel, err := filepath.Rel(absRoot, absPath)
			if err != nil {
				sendResult(indexedFile{err: fmt.Errorf("rel path: %v", err)})
				continue
			}
			relSlash := filepath.ToSlash(rel)
			content, err := os.ReadFile(absPath)
			if err != nil {
				sendResult(indexedFile{rel: relSlash, err: fmt.Errorf("read file %s: %v", relSlash, err)})
				continue
			}

			if !fullRefresh {
				hash := schema.ContentHash(string(content))
				prev, ok, hashErr := s.qdrant.GetModuleContentHash(ctx, repoID, relSlash)
				if hashErr != nil {
					s.incrementJobError(jobID, fmt.Sprintf("module hash lookup %s: %v", relSlash, hashErr))
				} else if ok && prev == hash {
					sendResult(indexedFile{rel: relSlash, skipped: true})
					continue
				}
			}

			var fileCache map[string][]float64
			if embeddings && !fullRefresh {
				cache, cacheErr := s.qdrant.GetFileEmbeddingCache(ctx, repoID, relSlash, s.embed.Model(), 4096)
				if cacheErr != nil {
					s.incrementJobError(jobID, fmt.Sprintf("embedding cache %s: %v", relSlash, cacheErr))
				} else {
					fileCache = cache
				}
			}

			if deleteFileErr := s.qdrant.DeleteFile(ctx, repoID, relSlash); deleteFileErr != nil {
				if !ensured && errors.Is(deleteFileErr, qdrant.ErrCollectionNotFound) {
					// ignore
				} else {
					s.incrementJobError(jobID, fmt.Sprintf("delete file: %v", deleteFileErr))
				}
			}

			chunks, indexErr := s.indexers.IndexFileFromContent(ctx, absRoot, absPath, repoID, content)
			if indexErr != nil {
				sendResult(indexedFile{rel: relSlash, err: fmt.Errorf("index %s: %v", relSlash, indexErr)})
				continue
			}
			if gitMetadata {
				if err := annotateChunksWithGitMetadata(ctx, gitRoot, absPath, chunks); err != nil {
					s.incrementJobError(jobID, fmt.Sprintf("git metadata %s: %v", relSlash, err))
				}
			}

			// Split large chunks into overlapping windows
			chunks = chunker.SplitLargeChunks(chunks, chunker.Config{
				MaxTokens:     s.cfg.ChunkMaxTokens,
				OverlapTokens: s.cfg.ChunkOverlapTokens,
				MinTokens:     s.cfg.ChunkMinTokens,
			})

			for i := range chunks {
				chunker.EnrichChunkIdentifiers(&chunks[i])
			}

			sendResult(indexedFile{
				rel:       relSlash,
				chunks:    chunks,
				fileCache: fileCache,
			})
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}

	enqueued := 0
enqueueLoop:
	for _, absPath := range files {
		select {
		case <-ctx.Done():
			break enqueueLoop
		case workerJobs <- absPath:
			enqueued++
		}
	}
	close(workerJobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	if enqueued == 0 {
		s.setJobDone(jobID)
		return
	}

	for received := 0; received < enqueued; {
		select {
		case <-ctx.Done():
			s.setJobCanceled(jobID)
			for range results {
			}
			return
		case r, ok := <-results:
			if !ok {
				s.setJobDone(jobID)
				return
			}
			received++
			if r.err != nil {
				s.incrementJobError(jobID, r.err.Error())
				s.incrementFilesDone(jobID, 0)
				continue
			}
			if r.skipped {
				s.incrementFilesSkipped(jobID)
				continue
			}

			for _, ch := range r.chunks {
				text := ch.Content
				if ch.Docstring != "" {
					text = ch.Docstring + "\n\n" + text
				}
				var vec []float64
				if embeddings && r.fileCache != nil {
					if v, ok := r.fileCache[ch.ContentHash]; ok && len(v) > 0 {
						vec = v
					}
				}
				pending = append(pending, pendingChunk{chunk: ch, text: text, vector: vec})
			}
			s.incrementFilesDone(jobID, len(r.chunks))

			if len(pending) >= embedBatch {
				if err := flush(); err != nil {
					s.setJobFailed(jobID, fmt.Sprintf("flush chunks: %v", err))
					return
				}
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

func (s *Service) ensureCollectionForVector(ctx context.Context, vectorSize int, allowRecreate bool) error {
	if err := s.qdrant.EnsureCollection(ctx, vectorSize); err == nil {
		return nil
	} else if !allowRecreate || !qdrant.IsVectorSizeMismatch(err) {
		return err
	}

	remaining, countErr := s.qdrant.Count(ctx, nil)
	if countErr != nil {
		return fmt.Errorf("qdrant vector size mismatch and collection count failed: %w", countErr)
	}
	if remaining > 0 {
		return fmt.Errorf(
			"qdrant vector size mismatch and collection is not empty (%d points); refusing automatic recreate",
			remaining,
		)
	}
	if recreateErr := s.qdrant.RecreateCollection(ctx, vectorSize); recreateErr != nil {
		return fmt.Errorf("qdrant recreate collection after vector size mismatch: %w", recreateErr)
	}
	return nil
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

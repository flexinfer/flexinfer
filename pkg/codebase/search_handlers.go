package codebase

import (
	"context"
	"fmt"
	"sort"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/validate"
)

func (s *Service) HandleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	query := validate.StringFromArgs(args, "query", "")
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	limit := validate.IntFromArgs(args, "limit", 10)
	if limit <= 0 {
		limit = 10
	}

	includeContent := validate.BoolFromArgs(args, "include_content", false)

	rerank := strings.ToLower(validate.StringFromArgs(args, "rerank", "none"))
	lexicalWeight := validate.Float64FromArgs(args, "lexical_weight", 0.15)
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

func (s *Service) HandleTextSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := validate.StringFromArgs(args, "repo_id", s.cfg.RepoIDDefault)
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	query := validate.StringFromArgs(args, "query", "")
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

	filePath := validate.StringFromArgs(args, "file_path", "")

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

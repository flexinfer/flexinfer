package bridge

import (
	"fmt"
)

// --- Codebase DTOs ---

// CodebaseStatsInfo describes the current state of a codebase index.
type CodebaseStatsInfo struct {
	TotalFiles   int            `json:"total_files"`
	TotalSymbols int            `json:"total_symbols"`
	Languages    map[string]int `json:"languages"`
	LastIndexed  string         `json:"last_indexed"`
	IndexStatus  string         `json:"index_status"`
}

// CodebaseSearchResult represents a single search hit from the codebase index.
type CodebaseSearchResult struct {
	FilePath string  `json:"file_path"`
	Symbol   string  `json:"symbol"`
	Kind     string  `json:"kind"`
	Line     int     `json:"line"`
	Score    float64 `json:"score"`
	Snippet  string  `json:"snippet"`
}

// CodebaseIndexJob represents an in-progress or completed indexing job.
type CodebaseIndexJob struct {
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

// --- Codebase bridge methods ---

// CodebaseStats fetches the current codebase index statistics from the
// codebase-memory MCP server.
func (a *AgentBridge) CodebaseStats() (*CodebaseStatsInfo, error) {
	raw, err := a.client.CallTool("codebase_memory__codebase_stats", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("codebase stats: %w", err)
	}
	var stats CodebaseStatsInfo
	if err := UnmarshalToolResult(raw, &stats); err != nil {
		return nil, fmt.Errorf("unmarshal codebase stats: %w", err)
	}
	return &stats, nil
}

// CodebaseSearch performs a semantic search across the codebase index.
func (a *AgentBridge) CodebaseSearch(query string, limit int) ([]CodebaseSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	raw, err := a.client.CallTool("codebase_memory__codebase_search", map[string]any{
		"query": query,
		"limit": limit,
	})
	if err != nil {
		return nil, fmt.Errorf("codebase search: %w", err)
	}
	var result struct {
		Results []CodebaseSearchResult `json:"results"`
	}
	if err := UnmarshalToolResult(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal codebase search: %w", err)
	}
	return result.Results, nil
}

// CodebaseTextSearch performs a text (grep-like) search across the codebase index.
func (a *AgentBridge) CodebaseTextSearch(query string, limit int) ([]CodebaseSearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	raw, err := a.client.CallTool("codebase_memory__codebase_text_search", map[string]any{
		"query": query,
		"limit": limit,
	})
	if err != nil {
		return nil, fmt.Errorf("codebase text search: %w", err)
	}
	var result struct {
		Results []CodebaseSearchResult `json:"results"`
	}
	if err := UnmarshalToolResult(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal codebase text search: %w", err)
	}
	return result.Results, nil
}

// CodebaseIndexStart triggers a new indexing job for the given path.
func (a *AgentBridge) CodebaseIndexStart(path string) (*CodebaseIndexJob, error) {
	raw, err := a.client.CallTool("codebase_memory__codebase_index_start", map[string]any{
		"path": path,
	})
	if err != nil {
		return nil, fmt.Errorf("codebase index start: %w", err)
	}
	var job CodebaseIndexJob
	if err := UnmarshalToolResult(raw, &job); err != nil {
		return nil, fmt.Errorf("unmarshal codebase index start: %w", err)
	}
	return &job, nil
}

// CodebaseIndexPoll checks the status of an in-progress indexing job.
func (a *AgentBridge) CodebaseIndexPoll(jobID string) (*CodebaseIndexJob, error) {
	raw, err := a.client.CallTool("codebase_memory__codebase_index_poll", map[string]any{
		"job_id": jobID,
	})
	if err != nil {
		return nil, fmt.Errorf("codebase index poll: %w", err)
	}
	var job CodebaseIndexJob
	if err := UnmarshalToolResult(raw, &job); err != nil {
		return nil, fmt.Errorf("unmarshal codebase index poll: %w", err)
	}
	return &job, nil
}

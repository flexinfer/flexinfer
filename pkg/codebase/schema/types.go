package schema

import "time"

const Version = "v1"

type Chunk struct {
	ID string `json:"id"`

	RepoID    string `json:"repo_id"`
	FilePath  string `json:"file_path"`
	Language  string `json:"language"`
	ChunkType string `json:"chunk_type"`

	StartLine   int `json:"start_line"`
	EndLine     int `json:"end_line"`
	StartColumn int `json:"start_column"`
	EndColumn   int `json:"end_column"`

	Name       string   `json:"name,omitempty"`
	Signature  string   `json:"signature,omitempty"`
	Docstring  string   `json:"docstring,omitempty"`
	ParentName string   `json:"parent_name,omitempty"`
	ParentType string   `json:"parent_type,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	Calls      []string `json:"calls,omitempty"`
	Defs       []string `json:"definitions,omitempty"`

	TokenCount  int       `json:"token_count"`
	IndexedAt   time.Time `json:"indexed_at"`
	SchemaVer   string    `json:"schema_version"`
	ContentHash string    `json:"content_hash"`
	Content     string    `json:"content,omitempty"`
}

type SearchResult struct {
	Score float64 `json:"score"`
	Chunk Chunk   `json:"chunk"`
}

type CallerInfo struct {
	FilePath     string `json:"file_path"`
	FunctionName string `json:"function_name"`
	LineNumber   int    `json:"line_number"`
	CallExpr     string `json:"call_expression"`
}

type CalleeInfo struct {
	Name       string `json:"name"`
	IsExternal bool   `json:"is_external"`
}

type ContextInfo struct {
	Chunk         *Chunk       `json:"chunk,omitempty"`
	RelatedChunks []Chunk      `json:"related_chunks,omitempty"`
	Callers       []CallerInfo `json:"callers,omitempty"`
	Callees       []CalleeInfo `json:"callees,omitempty"`
	Imports       []string     `json:"imports,omitempty"`
}

type IndexStats struct {
	RepoID      string    `json:"repo_id"`
	Root        string    `json:"root"`
	FilesTotal  int       `json:"files_total"`
	FilesDone   int       `json:"files_done"`
	FilesSkipped int      `json:"files_skipped"`
	ChunksTotal int       `json:"chunks_total"`
	Errors      int       `json:"errors"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
}

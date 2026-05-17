package schema

import "time"

const Version = "v1"

type Chunk struct {
	ID string `json:"id"`

	RepoID    string `json:"repo_id"`
	FilePath  string `json:"file_path"`
	Language  string `json:"language"`
	ChunkType string `json:"chunk_type"`

	GitCommit string `json:"git_commit,omitempty"`
	GitBlame  string `json:"git_blame,omitempty"`

	StartLine   int `json:"start_line"`
	EndLine     int `json:"end_line"`
	StartColumn int `json:"start_column"`
	EndColumn   int `json:"end_column"`

	Name        string   `json:"name,omitempty"`
	Signature   string   `json:"signature,omitempty"`
	Docstring   string   `json:"docstring,omitempty"`
	ParentName  string   `json:"parent_name,omitempty"`
	ParentType  string   `json:"parent_type,omitempty"`
	Imports     []string `json:"imports,omitempty"`
	Calls       []string `json:"calls,omitempty"`
	Defs        []string `json:"definitions,omitempty"`
	Identifiers []string `json:"identifiers,omitempty"` // Extracted identifiers for hybrid search

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
	RepoID       string `json:"repo_id"`
	Root         string `json:"root"`
	FilesTotal   int    `json:"files_total"`
	FilesDone    int    `json:"files_done"`
	FilesSkipped int    `json:"files_skipped"`
	ChunksTotal  int    `json:"chunks_total"`
	Errors       int    `json:"errors"`
	// FlushWarnings counts non-fatal failures of the trailing
	// Qdrant Flush call at end of the indexing run. Prior writes are
	// still durable via Qdrant's WAL fsync when this happens; the
	// counter exists so operators can observe transport flakiness
	// without conflating it with hard indexing errors.
	FlushWarnings    int             `json:"flush_warnings,omitempty"`
	LastFlushWarning string          `json:"last_flush_warning,omitempty"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       time.Time       `json:"finished_at,omitempty"`
	Stages           IndexStageStats `json:"stages,omitempty"`
}

type WatchStats struct {
	RepoID string `json:"repo_id"`
	Root   string `json:"root"`

	EventsTotal int `json:"events_total"`
	FilesQueued int `json:"files_queued"`

	FilesIndexed   int `json:"files_indexed"`
	FilesSkipped   int `json:"files_skipped"`
	FilesDeleted   int `json:"files_deleted"`
	ChunksUpserted int `json:"chunks_upserted"`

	Errors int `json:"errors"`

	StartedAt   time.Time       `json:"started_at"`
	StoppedAt   time.Time       `json:"stopped_at,omitempty"`
	LastEventAt time.Time       `json:"last_event_at,omitempty"`
	Stages      WatchStageStats `json:"stages,omitempty"`
}

type StageStat struct {
	Runs          int   `json:"runs,omitempty"`
	Items         int   `json:"items,omitempty"`
	DurationNanos int64 `json:"duration_nanos,omitempty"`
}

type IndexStageStats struct {
	FileCollect          StageStat `json:"file_collect,omitempty"`
	FileRead             StageStat `json:"file_read,omitempty"`
	PreflightLookup      StageStat `json:"preflight_lookup,omitempty"`
	UnchangedHashLookup  StageStat `json:"unchanged_hash_lookup,omitempty"`
	EmbeddingCacheLookup StageStat `json:"embedding_cache_lookup,omitempty"`
	DeleteBeforeUpsert   StageStat `json:"delete_before_upsert,omitempty"`
	ParseIndex           StageStat `json:"parse_index,omitempty"`
	GitMetadata          StageStat `json:"git_metadata,omitempty"`
	ChunkSplitEnrich     StageStat `json:"chunk_split_enrich,omitempty"`
	Embedding            StageStat `json:"embedding,omitempty"`
	QdrantUpsert         StageStat `json:"qdrant_upsert,omitempty"`
	Total                StageStat `json:"total,omitempty"`
}

type WatchStageStats struct {
	FileRead             StageStat `json:"file_read,omitempty"`
	PreflightLookup      StageStat `json:"preflight_lookup,omitempty"`
	UnchangedHashLookup  StageStat `json:"unchanged_hash_lookup,omitempty"`
	EmbeddingCacheLookup StageStat `json:"embedding_cache_lookup,omitempty"`
	DeleteBeforeUpsert   StageStat `json:"delete_before_upsert,omitempty"`
	ParseIndex           StageStat `json:"parse_index,omitempty"`
	GitMetadata          StageStat `json:"git_metadata,omitempty"`
	ChunkSplitEnrich     StageStat `json:"chunk_split_enrich,omitempty"`
	Embedding            StageStat `json:"embedding,omitempty"`
	QdrantUpsert         StageStat `json:"qdrant_upsert,omitempty"`
	Total                StageStat `json:"total,omitempty"`
}

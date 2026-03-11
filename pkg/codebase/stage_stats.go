package codebase

import (
	"time"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

type indexStage string

const (
	indexStageFileCollect          indexStage = "file_collect"
	indexStageFileRead             indexStage = "file_read"
	indexStagePreflightLookup      indexStage = "preflight_lookup"
	indexStageUnchangedHashLookup  indexStage = "unchanged_hash_lookup"
	indexStageEmbeddingCacheLookup indexStage = "embedding_cache_lookup"
	indexStageDeleteBeforeUpsert   indexStage = "delete_before_upsert"
	indexStageParseIndex           indexStage = "parse_index"
	indexStageGitMetadata          indexStage = "git_metadata"
	indexStageChunkSplitEnrich     indexStage = "chunk_split_enrich"
	indexStageEmbedding            indexStage = "embedding"
	indexStageQdrantUpsert         indexStage = "qdrant_upsert"
	indexStageTotal                indexStage = "total"
)

// watchStage constants omitted — watch uses mergeWatchStageStats directly.

func stageSample(d time.Duration, items int) schema.StageStat {
	return schema.StageStat{Runs: 1, Items: items, DurationNanos: d.Nanoseconds()}
}

func addStageStat(dst *schema.StageStat, sample schema.StageStat) {
	dst.Runs += sample.Runs
	dst.Items += sample.Items
	dst.DurationNanos += sample.DurationNanos
}

func mergeIndexStageStats(dst *schema.IndexStageStats, src schema.IndexStageStats) {
	addStageStat(&dst.FileCollect, src.FileCollect)
	addStageStat(&dst.FileRead, src.FileRead)
	addStageStat(&dst.PreflightLookup, src.PreflightLookup)
	addStageStat(&dst.UnchangedHashLookup, src.UnchangedHashLookup)
	addStageStat(&dst.EmbeddingCacheLookup, src.EmbeddingCacheLookup)
	addStageStat(&dst.DeleteBeforeUpsert, src.DeleteBeforeUpsert)
	addStageStat(&dst.ParseIndex, src.ParseIndex)
	addStageStat(&dst.GitMetadata, src.GitMetadata)
	addStageStat(&dst.ChunkSplitEnrich, src.ChunkSplitEnrich)
	addStageStat(&dst.Embedding, src.Embedding)
	addStageStat(&dst.QdrantUpsert, src.QdrantUpsert)
	addStageStat(&dst.Total, src.Total)
}

func mergeWatchStageStats(dst *schema.WatchStageStats, src schema.WatchStageStats) {
	addStageStat(&dst.FileRead, src.FileRead)
	addStageStat(&dst.PreflightLookup, src.PreflightLookup)
	addStageStat(&dst.UnchangedHashLookup, src.UnchangedHashLookup)
	addStageStat(&dst.EmbeddingCacheLookup, src.EmbeddingCacheLookup)
	addStageStat(&dst.DeleteBeforeUpsert, src.DeleteBeforeUpsert)
	addStageStat(&dst.ParseIndex, src.ParseIndex)
	addStageStat(&dst.GitMetadata, src.GitMetadata)
	addStageStat(&dst.ChunkSplitEnrich, src.ChunkSplitEnrich)
	addStageStat(&dst.Embedding, src.Embedding)
	addStageStat(&dst.QdrantUpsert, src.QdrantUpsert)
	addStageStat(&dst.Total, src.Total)
}

func (s *Service) addIndexStageStat(jobID string, stage indexStage, d time.Duration, items int) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	job := s.jobs[jobID]
	if job == nil {
		return
	}
	sample := stageSample(d, items)
	switch stage {
	case indexStageFileCollect:
		addStageStat(&job.stats.Stages.FileCollect, sample)
	case indexStageFileRead:
		addStageStat(&job.stats.Stages.FileRead, sample)
	case indexStagePreflightLookup:
		addStageStat(&job.stats.Stages.PreflightLookup, sample)
	case indexStageUnchangedHashLookup:
		addStageStat(&job.stats.Stages.UnchangedHashLookup, sample)
	case indexStageEmbeddingCacheLookup:
		addStageStat(&job.stats.Stages.EmbeddingCacheLookup, sample)
	case indexStageDeleteBeforeUpsert:
		addStageStat(&job.stats.Stages.DeleteBeforeUpsert, sample)
	case indexStageParseIndex:
		addStageStat(&job.stats.Stages.ParseIndex, sample)
	case indexStageGitMetadata:
		addStageStat(&job.stats.Stages.GitMetadata, sample)
	case indexStageChunkSplitEnrich:
		addStageStat(&job.stats.Stages.ChunkSplitEnrich, sample)
	case indexStageEmbedding:
		addStageStat(&job.stats.Stages.Embedding, sample)
	case indexStageQdrantUpsert:
		addStageStat(&job.stats.Stages.QdrantUpsert, sample)
	case indexStageTotal:
		addStageStat(&job.stats.Stages.Total, sample)
	}
}

func (s *Service) mergeIndexStageStats(jobID string, stats schema.IndexStageStats) {
	s.jobsMu.Lock()
	defer s.jobsMu.Unlock()
	job := s.jobs[jobID]
	if job == nil {
		return
	}
	mergeIndexStageStats(&job.stats.Stages, stats)
}

func (s *Service) mergeWatchStageStats(watchID string, stats schema.WatchStageStats) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	job := s.watchJobs[watchID]
	if job == nil {
		return
	}
	mergeWatchStageStats(&job.stats.Stages, stats)
}

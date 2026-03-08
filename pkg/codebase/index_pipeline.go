package codebase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/crb2nu/loom/pkg/codebase/chunker"
	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
)

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

	go func() {
		defer close(workerJobs)
		for _, absPath := range files {
			select {
			case <-ctx.Done():
				return
			case workerJobs <- absPath:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
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

	if ctx.Err() != nil {
		s.setJobCanceled(jobID)
		return
	}

	if err := flush(); err != nil {
		s.setJobFailed(jobID, fmt.Sprintf("final flush chunks: %v", err))
		return
	}

	s.setJobDone(jobID)
	_ = vectorSize
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

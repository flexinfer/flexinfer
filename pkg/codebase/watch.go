package codebase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/fsnotify/fsnotify"

	"github.com/crb2nu/loom/pkg/codebase/chunker"
	"github.com/crb2nu/loom/pkg/codebase/index"
	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/validate"
)

type watchJob struct {
	id     string
	cancel context.CancelFunc

	status string
	err    string

	stats schema.WatchStats
}

type watchTask struct {
	absPath string
	relPath string
	op      string // upsert|delete
}

func (s *Service) HandleWatchStart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	debounce := 750 * time.Millisecond
	if ms := validate.IntFromArgs(args, "debounce_ms", 0); ms > 0 {
		debounce = time.Duration(ms) * time.Millisecond
	}
	if debounce < 100*time.Millisecond {
		debounce = 100 * time.Millisecond
	}

	gitMetadata := validate.BoolFromArgs(args, "git_metadata", s.cfg.GitMetadataDefault)
	embeddings := validate.BoolFromArgs(args, "embeddings", !s.cfg.DisableEmbeddingsDefault)

	watchID := schema.ShortSHA256Hex(fmt.Sprintf("%s:%d", repoID, time.Now().UnixNano()))
	jobCtx, cancel := context.WithCancel(ctx)

	job := &watchJob{
		id:     watchID,
		cancel: cancel,
		status: "running",
		stats: schema.WatchStats{
			RepoID:    repoID,
			Root:      root,
			StartedAt: time.Now(),
		},
	}

	s.watchMu.Lock()
	s.watchJobs[watchID] = job
	s.watchMu.Unlock()

	go s.runWatchJob(jobCtx, watchID, repoID, root, langs, exclude, debounce, gitMetadata, embeddings)

	return mcp.JSONResult(map[string]any{
		"watch_id":     watchID,
		"repo_id":      repoID,
		"git_metadata": gitMetadata,
		"embeddings":   embeddings,
	})
}

func (s *Service) HandleWatchPoll(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	watchID := validate.StringFromArgs(args, "watch_id", "")
	if watchID == "" {
		return nil, fmt.Errorf("watch_id is required")
	}

	s.watchMu.RLock()
	job := s.watchJobs[watchID]
	s.watchMu.RUnlock()
	if job == nil {
		return mcp.JSONResult(map[string]any{
			"found":    false,
			"watch_id": watchID,
		})
	}

	return mcp.JSONResult(map[string]any{
		"found":    true,
		"watch_id": job.id,
		"status":   job.status,
		"error":    job.err,
		"stats":    job.stats,
	})
}

func (s *Service) HandleWatchStop(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	watchID := validate.StringFromArgs(args, "watch_id", "")
	if watchID == "" {
		return nil, fmt.Errorf("watch_id is required")
	}

	s.watchMu.RLock()
	job := s.watchJobs[watchID]
	s.watchMu.RUnlock()
	if job == nil {
		return mcp.JSONResult(map[string]any{"ok": false, "error": "watch job not found"})
	}

	job.cancel()
	return mcp.JSONResult(map[string]any{"ok": true})
}

func (s *Service) runWatchJob(
	ctx context.Context,
	watchID string,
	repoID string,
	root string,
	languages []string,
	exclude []string,
	debounce time.Duration,
	gitMetadata bool,
	embeddings bool,
) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		s.setWatchFailed(watchID, fmt.Sprintf("resolve root: %v", err))
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

	vectorSize := 0
	if !embeddings {
		exists, size, err := s.qdrant.GetCollectionVectorSize(ctx)
		if err != nil {
			s.setWatchFailed(watchID, fmt.Sprintf("qdrant collection info: %v", err))
			return
		}
		if exists {
			if size <= 0 {
				s.setWatchFailed(watchID, "qdrant collection vector size unknown")
				return
			}
			vectorSize = size
		} else {
			vectorSize = 1
			if err := s.qdrant.EnsureCollection(ctx, vectorSize); err != nil {
				s.setWatchFailed(watchID, fmt.Sprintf("qdrant ensure collection: %v", err))
				return
			}
		}
	}

	wantExt, err := s.indexers.ExtensionsForLanguages(languages)
	if err != nil {
		s.setWatchFailed(watchID, err.Error())
		return
	}

	ignoreMatcher := index.NewIgnoreMatcher(absRoot, exclude)

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.setWatchFailed(watchID, fmt.Sprintf("fsnotify: %v", err))
		return
	}
	defer fsWatcher.Close()

	addDir := func(dir string) error {
		if err := fsWatcher.Add(dir); err != nil {
			return err
		}
		return nil
	}

	// Watch all directories under root (recursively), skipping excluded paths.
	if err := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return addDir(path)
		}
		if ignoreMatcher.IsIgnored(rel+"/", true) {
			return filepath.SkipDir
		}
		if err := addDir(path); err != nil {
			// Best-effort; keep watching other dirs.
			s.incrementWatchError(watchID, fmt.Sprintf("watch dir %s: %v", rel, err))
			return nil
		}
		return nil
	}); err != nil {
		s.setWatchFailed(watchID, fmt.Sprintf("walk root: %v", err))
		return
	}

	tasks := make(chan watchTask, 2048)
	var wg sync.WaitGroup
	workers := s.cfg.IndexConcurrency
	if workers <= 0 {
		workers = 4
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case t, ok := <-tasks:
					if !ok {
						return
					}
					if err := s.applyWatchTask(ctx, watchID, repoID, absRoot, gitRoot, gitMetadata, embeddings, vectorSize, t); err != nil {
						s.incrementWatchError(watchID, err.Error())
					}
				}
			}
		}()
	}

	type pendingInfo struct {
		at time.Time
		op string
	}
	pending := map[string]pendingInfo{}
	var pendingMu sync.Mutex

	enqueue := func(absPath, relPath, op string) {
		pendingMu.Lock()
		pending[absPath] = pendingInfo{at: time.Now(), op: op}
		pendingMu.Unlock()
		s.incrementWatchEvent(watchID)
	}

	flush := func() {
		now := time.Now()
		var ready []watchTask
		pendingMu.Lock()
		for absPath, info := range pending {
			if now.Sub(info.at) < debounce {
				continue
			}
			delete(pending, absPath)
			rel, relErr := filepath.Rel(absRoot, absPath)
			if relErr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			ready = append(ready, watchTask{absPath: absPath, relPath: rel, op: info.op})
		}
		pendingMu.Unlock()

		if len(ready) == 0 {
			return
		}
		s.incrementWatchQueued(watchID, len(ready))
		for _, t := range ready {
			select {
			case <-ctx.Done():
				return
			case tasks <- t:
			default:
				s.incrementWatchError(watchID, "task queue full; dropping updates")
				return
			}
		}
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(tasks)
			wg.Wait()
			s.setWatchStopped(watchID)
			return

		case evt, ok := <-fsWatcher.Events:
			if !ok {
				close(tasks)
				wg.Wait()
				s.setWatchStopped(watchID)
				return
			}
			// Try to watch new directories as they appear.
			if evt.Has(fsnotify.Create) {
				if st, statErr := os.Stat(evt.Name); statErr == nil && st.IsDir() {
					_ = filepath.WalkDir(evt.Name, func(p string, d os.DirEntry, err error) error {
						if err != nil || !d.IsDir() {
							return nil
						}
						rel, relErr := filepath.Rel(absRoot, p)
						if relErr == nil {
							if ignoreMatcher.IsIgnored(filepath.ToSlash(rel)+"/", true) {
								return filepath.SkipDir
							}
						}
						_ = addDir(p)
						return nil
					})
					continue
				}
			}

			rel, relErr := filepath.Rel(absRoot, evt.Name)
			if relErr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "../") || rel == ".." {
				continue
			}
			if ignoreMatcher.IsIgnored(rel, false) {
				continue
			}

			ext := strings.ToLower(filepath.Ext(evt.Name))
			if ext == "" || !wantExt[ext] {
				continue
			}

			if evt.Has(fsnotify.Remove) || evt.Has(fsnotify.Rename) {
				enqueue(evt.Name, rel, "delete")
				continue
			}
			if evt.Has(fsnotify.Write) || evt.Has(fsnotify.Create) {
				enqueue(evt.Name, rel, "upsert")
				continue
			}

		case err, ok := <-fsWatcher.Errors:
			if ok && err != nil {
				s.incrementWatchError(watchID, fmt.Sprintf("fsnotify error: %v", err))
			}

		case <-ticker.C:
			flush()
		}
	}
}

func (s *Service) applyWatchTask(ctx context.Context, watchID, repoID, absRoot, gitRoot string, gitMetadata bool, embeddings bool, vectorSize int, t watchTask) error {
	stages := schema.WatchStageStats{}
	switch t.op {
	case "delete":
		deleteStart := time.Now()
		if err := s.qdrant.DeleteFile(ctx, repoID, t.relPath); err != nil && !errors.Is(err, qdrant.ErrCollectionNotFound) {
			return fmt.Errorf("delete %s: %v", t.relPath, err)
		}
		stages.DeleteBeforeUpsert = stageSample(time.Since(deleteStart), 1)
		s.mergeWatchStageStats(watchID, stages)
		s.incrementWatchDeleted(watchID)
		return nil
	default:
		if _, err := os.Stat(t.absPath); err != nil {
			deleteStart := time.Now()
			if err := s.qdrant.DeleteFile(ctx, repoID, t.relPath); err != nil && !errors.Is(err, qdrant.ErrCollectionNotFound) {
				return fmt.Errorf("delete missing %s: %v", t.relPath, err)
			}
			stages.DeleteBeforeUpsert = stageSample(time.Since(deleteStart), 1)
			s.mergeWatchStageStats(watchID, stages)
			s.incrementWatchDeleted(watchID)
			return nil
		}

		readStart := time.Now()
		b, err := os.ReadFile(t.absPath)
		if err != nil {
			return fmt.Errorf("read %s: %v", t.relPath, err)
		}
		stages.FileRead = stageSample(time.Since(readStart), 1)
		if s.cfg.MaxFileBytes > 0 && int64(len(b)) > s.cfg.MaxFileBytes {
			s.mergeWatchStageStats(watchID, stages)
			return nil
		}

		fileHash := schema.ContentHashBytes(b)
		preflightStart := time.Now()
		preflight, preflightErr := s.qdrant.GetFilePreflight(ctx, repoID, t.relPath, s.cfg.EmbedModel, 4096)
		stages.PreflightLookup = stageSample(time.Since(preflightStart), 1)
		stages.UnchangedHashLookup = stageSample(0, 1)
		if embeddings {
			stages.EmbeddingCacheLookup = stageSample(0, 1)
		}
		if preflightErr == nil && preflight.ModuleFound && preflight.ModuleContentHash == fileHash {
			s.mergeWatchStageStats(watchID, stages)
			s.incrementWatchSkipped(watchID)
			return nil
		}

		var fileCache map[string][]float64
		if preflightErr != nil {
			s.incrementWatchError(watchID, fmt.Sprintf("preflight %s: %v", t.relPath, preflightErr))
		} else if embeddings {
			fileCache = preflight.EmbeddingCache
		}

		deleteStart := time.Now()
		if delErr := s.qdrant.DeleteFile(ctx, repoID, t.relPath); delErr != nil && !errors.Is(delErr, qdrant.ErrCollectionNotFound) {
			return fmt.Errorf("delete before upsert %s: %v", t.relPath, delErr)
		}
		stages.DeleteBeforeUpsert = stageSample(time.Since(deleteStart), 1)

		parseStart := time.Now()
		chunks, err := s.indexers.IndexFileFromContent(ctx, absRoot, t.absPath, repoID, b)
		if err != nil {
			return fmt.Errorf("index %s: %v", t.relPath, err)
		}
		stages.ParseIndex = stageSample(time.Since(parseStart), 1)
		if len(chunks) == 0 {
			s.mergeWatchStageStats(watchID, stages)
			return nil
		}

		if gitMetadata {
			gitStart := time.Now()
			if err := annotateChunksWithGitMetadata(ctx, gitRoot, t.absPath, chunks); err != nil {
				s.incrementWatchError(watchID, fmt.Sprintf("git metadata %s: %v", t.relPath, err))
			}
			stages.GitMetadata = stageSample(time.Since(gitStart), len(chunks))
		}

		chunkStart := time.Now()
		chunks = chunker.SplitLargeChunks(chunks, chunker.Config{
			MaxTokens:     s.cfg.ChunkMaxTokens,
			OverlapTokens: s.cfg.ChunkOverlapTokens,
			MinTokens:     s.cfg.ChunkMinTokens,
		})
		for i := range chunks {
			chunker.EnrichChunkIdentifiers(&chunks[i])
		}
		stages.ChunkSplitEnrich = stageSample(time.Since(chunkStart), len(chunks))

		points := make([]qdrant.Point, 0, len(chunks))
		embedModel := ""
		if embeddings {
			embedModel = s.cfg.EmbedModel
		}
		if embeddings {
			vectors := make([][]float64, len(chunks))
			var (
				texts   []string
				indices []int
			)
			for i, ch := range chunks {
				if fileCache != nil {
					if v, ok := fileCache[ch.ContentHash]; ok && len(v) > 0 {
						vectors[i] = v
						continue
					}
				}
				text := ch.Content
				if ch.Docstring != "" {
					text = ch.Docstring + "\n\n" + text
				}
				texts = append(texts, text)
				indices = append(indices, i)
			}

			if len(texts) > 0 {
				embedStart := time.Now()
				embedded, err := s.embed.EmbedDocuments(ctx, texts)
				if err != nil {
					return fmt.Errorf("embed %s: %v", t.relPath, err)
				}
				stages.Embedding = stageSample(time.Since(embedStart), len(texts))
				if len(embedded) != len(texts) {
					return fmt.Errorf("embed %s: returned %d vectors for %d texts", t.relPath, len(embedded), len(texts))
				}
				for j, idx := range indices {
					vectors[idx] = embedded[j]
				}
			}

			size := 0
			for _, v := range vectors {
				if len(v) > 0 {
					size = len(v)
					break
				}
			}
			if size <= 0 {
				return fmt.Errorf("embed %s: empty vector", t.relPath)
			}
			if err := s.ensureCollectionForVector(ctx, size, false); err != nil {
				return fmt.Errorf("ensure collection: %v", err)
			}

			for i := range chunks {
				if len(vectors[i]) == 0 {
					return fmt.Errorf("embed %s: missing vector", t.relPath)
				}
				points = append(points, qdrant.Point{
					ID:      chunks[i].ID,
					Vector:  vectors[i],
					Payload: qdrant.ChunkToPayload(chunks[i], true, embedModel),
				})
			}
		} else {
			if vectorSize <= 0 {
				return fmt.Errorf("no-embeddings mode requires known qdrant vector size")
			}
			dummy := make([]float64, vectorSize)
			dummy[0] = 1
			for i := range chunks {
				points = append(points, qdrant.Point{
					ID:      chunks[i].ID,
					Vector:  dummy,
					Payload: qdrant.ChunkToPayload(chunks[i], true, embedModel),
				})
			}
		}

		upsertStart := time.Now()
		for i := 0; i < len(points); i += s.cfg.UpsertBatchSize {
			end := i + s.cfg.UpsertBatchSize
			if end > len(points) {
				end = len(points)
			}
			if err := s.qdrant.Upsert(ctx, points[i:end], true); err != nil {
				return fmt.Errorf("upsert %s: %v", t.relPath, err)
			}
		}
		stages.QdrantUpsert = stageSample(time.Since(upsertStart), len(points))

		s.mergeWatchStageStats(watchID, stages)
		s.incrementWatchIndexed(watchID, len(chunks))
		return nil
	}
}

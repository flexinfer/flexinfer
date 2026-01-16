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

	"github.com/bmatcuk/doublestar/v4"
	"github.com/fsnotify/fsnotify"

	"github.com/crb2nu/loom/pkg/codebase/index"
	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
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

	debounce := 750 * time.Millisecond
	switch v := args["debounce_ms"].(type) {
	case float64:
		if int(v) > 0 {
			debounce = time.Duration(int(v)) * time.Millisecond
		}
	case int:
		if v > 0 {
			debounce = time.Duration(v) * time.Millisecond
		}
	}
	if debounce < 100*time.Millisecond {
		debounce = 100 * time.Millisecond
	}

	gitMetadata := s.cfg.GitMetadataDefault
	if v, ok := args["git_metadata"].(bool); ok {
		gitMetadata = v
	}

	embeddings := !s.cfg.DisableEmbeddingsDefault
	if v, ok := args["embeddings"].(bool); ok {
		embeddings = v
	}

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
	watchID, _ := args["watch_id"].(string)
	if strings.TrimSpace(watchID) == "" {
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
	watchID, _ := args["watch_id"].(string)
	if strings.TrimSpace(watchID) == "" {
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

	allExcludes := append(index.DefaultExcludeGlobs(), exclude...)

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
		if indexGlobMatchAny(rel+"/", allExcludes) {
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
							if indexGlobMatchAny(filepath.ToSlash(rel)+"/", allExcludes) {
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
			if indexGlobMatchAny(rel, allExcludes) {
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
	switch t.op {
	case "delete":
		if err := s.qdrant.DeleteFile(ctx, repoID, t.relPath); err != nil && !errors.Is(err, qdrant.ErrCollectionNotFound) {
			return fmt.Errorf("delete %s: %v", t.relPath, err)
		}
		s.incrementWatchDeleted(watchID)
		return nil
	default:
		// Best-effort: if file doesn't exist anymore, treat as delete.
		if _, err := os.Stat(t.absPath); err != nil {
			if err := s.qdrant.DeleteFile(ctx, repoID, t.relPath); err != nil && !errors.Is(err, qdrant.ErrCollectionNotFound) {
				return fmt.Errorf("delete missing %s: %v", t.relPath, err)
			}
			s.incrementWatchDeleted(watchID)
			return nil
		}

		b, err := os.ReadFile(t.absPath)
		if err != nil {
			return fmt.Errorf("read %s: %v", t.relPath, err)
		}
		if s.cfg.MaxFileBytes > 0 && int64(len(b)) > s.cfg.MaxFileBytes {
			return nil
		}
		fileHash := schema.ContentHash(string(b))
		prev, ok, err := s.qdrant.GetModuleContentHash(ctx, repoID, t.relPath)
		if err == nil && ok && prev == fileHash {
			s.incrementWatchSkipped(watchID)
			return nil
		}

		if delErr := s.qdrant.DeleteFile(ctx, repoID, t.relPath); delErr != nil && !errors.Is(delErr, qdrant.ErrCollectionNotFound) {
			return fmt.Errorf("delete before upsert %s: %v", t.relPath, delErr)
		}

		chunks, err := s.indexers.IndexFile(ctx, absRoot, t.absPath, repoID)
		if err != nil {
			return fmt.Errorf("index %s: %v", t.relPath, err)
		}
		if len(chunks) == 0 {
			return nil
		}

		if gitMetadata {
			if err := annotateChunksWithGitMetadata(ctx, gitRoot, t.absPath, chunks); err != nil {
				// Non-fatal; index results remain useful without git info.
				s.incrementWatchError(watchID, fmt.Sprintf("git metadata %s: %v", t.relPath, err))
			}
		}

		points := make([]qdrant.Point, 0, len(chunks))
		if embeddings {
			texts := make([]string, 0, len(chunks))
			for _, ch := range chunks {
				text := ch.Content
				if ch.Docstring != "" {
					text = ch.Docstring + "\n\n" + text
				}
				texts = append(texts, text)
			}
			vectors, err := s.embed.EmbedDocuments(ctx, texts)
			if err != nil {
				return fmt.Errorf("embed %s: %v", t.relPath, err)
			}
			if len(vectors) == 0 || len(vectors[0]) == 0 {
				return fmt.Errorf("embed %s: empty vector", t.relPath)
			}
			if err := s.qdrant.EnsureCollection(ctx, len(vectors[0])); err != nil {
				return fmt.Errorf("ensure collection: %v", err)
			}

			for i := range chunks {
				points = append(points, qdrant.Point{
					ID:      chunks[i].ID,
					Vector:  vectors[i],
					Payload: qdrant.ChunkToPayload(chunks[i], true),
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
					Payload: qdrant.ChunkToPayload(chunks[i], true),
				})
			}
		}
		for i := 0; i < len(points); i += s.cfg.UpsertBatchSize {
			end := i + s.cfg.UpsertBatchSize
			if end > len(points) {
				end = len(points)
			}
			if err := s.qdrant.Upsert(ctx, points[i:end], true); err != nil {
				return fmt.Errorf("upsert %s: %v", t.relPath, err)
			}
		}

		s.incrementWatchIndexed(watchID, len(chunks))
		return nil
	}
}

func indexGlobMatchAny(path string, globs []string) bool {
	for _, g := range globs {
		if ok := indexGlobMatch(g, path); ok {
			return true
		}
	}
	return false
}

func indexGlobMatch(pattern, path string) bool {
	// Reuse index glob semantics (doublestar).
	ok, err := doublestar.PathMatch(filepath.ToSlash(pattern), filepath.ToSlash(path))
	return err == nil && ok
}

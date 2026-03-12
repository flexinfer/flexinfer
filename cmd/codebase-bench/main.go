package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase"
	"github.com/crb2nu/loom/pkg/codebase/schema"
)

type benchConfig struct {
	Root             string `json:"root"`
	FixtureRoot      string `json:"fixture_root"`
	RepoID           string `json:"repo_id"`
	Scenario         string `json:"scenario"`
	Runs             int    `json:"runs"`
	WarmupRuns       int    `json:"warmup_runs"`
	Embeddings       bool   `json:"embeddings"`
	GitMetadata      bool   `json:"git_metadata"`
	IndexConcurrency int    `json:"index_concurrency"`
	EmbedBatchSize   int    `json:"embed_batch_size"`
	UpsertBatchSize  int    `json:"upsert_batch_size"`
	PollIntervalMS   int    `json:"poll_interval_ms"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
}

type memorySnapshot struct {
	AllocBytes      uint64 `json:"alloc_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	NumGC           uint32 `json:"num_gc"`
}

type memoryDelta struct {
	AllocBytesDelta      int64  `json:"alloc_bytes_delta"`
	TotalAllocBytesDelta uint64 `json:"total_alloc_bytes_delta"`
	HeapAllocBytesDelta  int64  `json:"heap_alloc_bytes_delta"`
	NumGCDelta           int64  `json:"num_gc_delta"`
}

type watchLatency struct {
	FilePath       string `json:"file_path"`
	LatencyMillis  int64  `json:"latency_millis"`
	FilesIndexed   int    `json:"files_indexed"`
	FilesSkipped   int    `json:"files_skipped"`
	ChunksUpserted int    `json:"chunks_upserted"`
}

type scenarioRun struct {
	Scenario       string             `json:"scenario"`
	RunIndex       int                `json:"run_index"`
	Warmup         bool               `json:"warmup"`
	StartedAt      time.Time          `json:"started_at"`
	FinishedAt     time.Time          `json:"finished_at"`
	DurationMillis int64              `json:"duration_millis"`
	MemoryBefore   memorySnapshot     `json:"memory_before"`
	MemoryAfter    memorySnapshot     `json:"memory_after"`
	MemoryDelta    memoryDelta        `json:"memory_delta"`
	IndexStats     *schema.IndexStats `json:"index_stats,omitempty"`
	WatchStats     *schema.WatchStats `json:"watch_stats,omitempty"`
	WatchLatencies []watchLatency     `json:"watch_latencies,omitempty"`
}

type artifact struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Config      benchConfig   `json:"config"`
	Runs        []scenarioRun `json:"runs"`
	Summary     any           `json:"summary"`
}

type scenarioSummary struct {
	Scenario             string `json:"scenario"`
	MeasuredRuns         int    `json:"measured_runs"`
	MedianDurationMillis int64  `json:"median_duration_millis"`
	MinDurationMillis    int64  `json:"min_duration_millis"`
	MaxDurationMillis    int64  `json:"max_duration_millis"`
}

type indexStartResponse struct {
	JobID string `json:"job_id"`
}

type indexPollResponse struct {
	Found  bool              `json:"found"`
	JobID  string            `json:"job_id"`
	Status string            `json:"status"`
	Error  string            `json:"error"`
	Stats  schema.IndexStats `json:"stats"`
}

type watchStartResponse struct {
	WatchID string `json:"watch_id"`
}

type watchPollResponse struct {
	Found   bool              `json:"found"`
	WatchID string            `json:"watch_id"`
	Status  string            `json:"status"`
	Error   string            `json:"error"`
	Stats   schema.WatchStats `json:"stats"`
}

func main() {
	var cfg benchConfig
	var outputDir string

	flag.StringVar(&cfg.Root, "root", ".", "repository root to benchmark for full/incremental scenarios")
	flag.StringVar(&cfg.FixtureRoot, "fixture-root", "pkg/codebase/testdata/mixedrepo", "fixture repo root for watch scenario")
	flag.StringVar(&cfg.RepoID, "repo-id", "", "repo id override")
	flag.StringVar(&cfg.Scenario, "scenario", "all", "scenario to run: full, incremental, watch, or all")
	flag.IntVar(&cfg.Runs, "runs", 5, "measured runs per scenario")
	flag.IntVar(&cfg.WarmupRuns, "warmup-runs", 1, "warmup runs per scenario")
	flag.BoolVar(&cfg.Embeddings, "embeddings", false, "enable embeddings")
	flag.BoolVar(&cfg.GitMetadata, "git-metadata", false, "enable git metadata")
	flag.IntVar(&cfg.IndexConcurrency, "index-concurrency", 4, "index worker concurrency")
	flag.IntVar(&cfg.EmbedBatchSize, "embed-batch-size", 64, "embedding batch size")
	flag.IntVar(&cfg.UpsertBatchSize, "upsert-batch-size", 64, "upsert batch size")
	flag.IntVar(&cfg.PollIntervalMS, "poll-interval-ms", 250, "poll interval in milliseconds")
	flag.IntVar(&cfg.TimeoutSeconds, "timeout-seconds", 600, "per-scenario timeout in seconds")
	flag.StringVar(&outputDir, "output-dir", "artifacts/codebase-bench", "directory for JSON artifacts")
	flag.Parse()

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fatalf("create output dir: %v", err)
	}

	applyEnv(cfg)

	runs, err := execute(cfg)
	if err != nil {
		fatalf("%v", err)
	}

	art := artifact{
		GeneratedAt: time.Now().UTC(),
		Config:      cfg,
		Runs:        runs,
		Summary:     summarizeRuns(runs),
	}

	ts := time.Now().UTC().Format("20060102-150405")
	path := filepath.Join(outputDir, fmt.Sprintf("codebase-bench-%s.json", ts))
	body, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		fatalf("marshal artifact: %v", err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		fatalf("write artifact: %v", err)
	}

	fmt.Printf("wrote %s\n", path)
}

func execute(cfg benchConfig) ([]scenarioRun, error) {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	cfg.Root = root

	fixtureRoot, err := filepath.Abs(cfg.FixtureRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve fixture root: %w", err)
	}
	cfg.FixtureRoot = fixtureRoot

	ctx := context.Background()
	svc, err := codebase.NewServiceFromEnv()
	if err != nil {
		return nil, fmt.Errorf("init codebase service: %w", err)
	}

	scenarios := expandScenarios(cfg.Scenario)
	runs := make([]scenarioRun, 0, len(scenarios)*(cfg.Runs+cfg.WarmupRuns))
	for _, scenario := range scenarios {
		switch scenario {
		case "full":
			for i := 0; i < cfg.WarmupRuns+cfg.Runs; i++ {
				run, err := runIndexScenario(ctx, svc, cfg, scenario, i, i < cfg.WarmupRuns, true)
				if err != nil {
					return runs, err
				}
				runs = append(runs, run)
			}
		case "incremental":
			if _, err := runIndexScenario(ctx, svc, cfg, "incremental-prime", 0, true, true); err != nil {
				return runs, fmt.Errorf("prime incremental state: %w", err)
			}
			for i := 0; i < cfg.WarmupRuns+cfg.Runs; i++ {
				run, err := runIndexScenario(ctx, svc, cfg, scenario, i, i < cfg.WarmupRuns, false)
				if err != nil {
					return runs, err
				}
				runs = append(runs, run)
			}
		case "watch":
			for i := 0; i < cfg.WarmupRuns+cfg.Runs; i++ {
				run, err := runWatchScenario(ctx, svc, cfg, i, i < cfg.WarmupRuns)
				if err != nil {
					return runs, err
				}
				runs = append(runs, run)
			}
		default:
			return runs, fmt.Errorf("unsupported scenario %q", scenario)
		}
	}

	return runs, nil
}

func runIndexScenario(ctx context.Context, svc *codebase.Service, cfg benchConfig, scenario string, idx int, warmup bool, fullRefresh bool) (scenarioRun, error) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	before := readMem()
	started := time.Now().UTC()
	startRes, err := svc.HandleIndexStart(runCtx, map[string]any{
		"root":         cfg.Root,
		"repo_id":      cfg.RepoID,
		"full_refresh": fullRefresh,
		"git_metadata": cfg.GitMetadata,
		"embeddings":   cfg.Embeddings,
	})
	if err != nil {
		return scenarioRun{}, fmt.Errorf("%s start: %w", scenario, err)
	}
	var startedResp indexStartResponse
	if err := decodeToolJSON(startRes, &startedResp); err != nil {
		return scenarioRun{}, fmt.Errorf("%s decode start: %w", scenario, err)
	}

	pollTick := time.NewTicker(time.Duration(cfg.PollIntervalMS) * time.Millisecond)
	defer pollTick.Stop()

	var final indexPollResponse
	for {
		select {
		case <-runCtx.Done():
			return scenarioRun{}, fmt.Errorf("%s timed out: %w", scenario, runCtx.Err())
		case <-pollTick.C:
			pollRes, err := svc.HandleIndexPoll(runCtx, map[string]any{"job_id": startedResp.JobID})
			if err != nil {
				return scenarioRun{}, fmt.Errorf("%s poll: %w", scenario, err)
			}
			if err := decodeToolJSON(pollRes, &final); err != nil {
				return scenarioRun{}, fmt.Errorf("%s decode poll: %w", scenario, err)
			}
			if final.Status == "done" || final.Status == "failed" || final.Status == "canceled" {
				if final.Status != "done" {
					return scenarioRun{}, fmt.Errorf("%s ended with status=%s error=%s", scenario, final.Status, final.Error)
				}
				finished := time.Now().UTC()
				after := readMem()
				return scenarioRun{
					Scenario:       scenario,
					RunIndex:       idx,
					Warmup:         warmup,
					StartedAt:      started,
					FinishedAt:     finished,
					DurationMillis: finished.Sub(started).Milliseconds(),
					MemoryBefore:   before,
					MemoryAfter:    after,
					MemoryDelta:    diffMem(before, after),
					IndexStats:     &final.Stats,
				}, nil
			}
		}
	}
}

func runWatchScenario(ctx context.Context, svc *codebase.Service, cfg benchConfig, idx int, warmup bool) (scenarioRun, error) {
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	tempRoot, err := os.MkdirTemp("", "codebase-watch-bench-*")
	if err != nil {
		return scenarioRun{}, fmt.Errorf("create temp fixture root: %w", err)
	}
	defer os.RemoveAll(tempRoot)
	if err := copyDir(cfg.FixtureRoot, tempRoot); err != nil {
		return scenarioRun{}, fmt.Errorf("copy fixture: %w", err)
	}

	before := readMem()
	started := time.Now().UTC()
	startRes, err := svc.HandleWatchStart(runCtx, map[string]any{
		"root":         tempRoot,
		"repo_id":      watchRepoID(cfg),
		"git_metadata": false,
		"embeddings":   cfg.Embeddings,
		"debounce_ms":  150,
	})
	if err != nil {
		return scenarioRun{}, fmt.Errorf("watch start: %w", err)
	}
	var startedResp watchStartResponse
	if err := decodeToolJSON(startRes, &startedResp); err != nil {
		return scenarioRun{}, fmt.Errorf("decode watch start: %w", err)
	}
	defer func() {
		_, _ = svc.HandleWatchStop(context.Background(), map[string]any{"watch_id": startedResp.WatchID})
	}()

	time.Sleep(500 * time.Millisecond)
	latencies := make([]watchLatency, 0, 2)
	for _, relPath := range []string{"main.go", "src/index.ts"} {
		path := filepath.Join(tempRoot, filepath.FromSlash(relPath))
		editStarted := time.Now()
		if err := appendLine(path, fmt.Sprintf("// bench update %d %d", idx, time.Now().UnixNano())); err != nil {
			return scenarioRun{}, fmt.Errorf("edit %s: %w", relPath, err)
		}
		stats, err := waitForWatchAdvance(runCtx, svc, startedResp.WatchID, len(latencies)+1, cfg.PollIntervalMS)
		if err != nil {
			return scenarioRun{}, err
		}
		latencies = append(latencies, watchLatency{
			FilePath:       relPath,
			LatencyMillis:  time.Since(editStarted).Milliseconds(),
			FilesIndexed:   stats.FilesIndexed,
			FilesSkipped:   stats.FilesSkipped,
			ChunksUpserted: stats.ChunksUpserted,
		})
	}

	stopRes, err := svc.HandleWatchStop(runCtx, map[string]any{"watch_id": startedResp.WatchID})
	if err != nil {
		return scenarioRun{}, fmt.Errorf("watch stop: %w", err)
	}
	var _ map[string]any
	_ = stopRes

	finalStats, err := waitForWatchStopped(runCtx, svc, startedResp.WatchID, cfg.PollIntervalMS)
	if err != nil {
		return scenarioRun{}, err
	}

	finished := time.Now().UTC()
	after := readMem()
	return scenarioRun{
		Scenario:       "watch",
		RunIndex:       idx,
		Warmup:         warmup,
		StartedAt:      started,
		FinishedAt:     finished,
		DurationMillis: finished.Sub(started).Milliseconds(),
		MemoryBefore:   before,
		MemoryAfter:    after,
		MemoryDelta:    diffMem(before, after),
		WatchStats:     &finalStats,
		WatchLatencies: latencies,
	}, nil
}

func waitForWatchAdvance(ctx context.Context, svc *codebase.Service, watchID string, wantIndexed int, pollMS int) (schema.WatchStats, error) {
	ticker := time.NewTicker(time.Duration(pollMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return schema.WatchStats{}, fmt.Errorf("watch advance timed out: %w", ctx.Err())
		case <-ticker.C:
			res, err := svc.HandleWatchPoll(ctx, map[string]any{"watch_id": watchID})
			if err != nil {
				return schema.WatchStats{}, fmt.Errorf("watch poll: %w", err)
			}
			var poll watchPollResponse
			if err := decodeToolJSON(res, &poll); err != nil {
				return schema.WatchStats{}, fmt.Errorf("decode watch poll: %w", err)
			}
			if poll.Status == "failed" {
				return schema.WatchStats{}, fmt.Errorf("watch failed: %s", poll.Error)
			}
			if poll.Stats.FilesIndexed >= wantIndexed {
				return poll.Stats, nil
			}
		}
	}
}

func waitForWatchStopped(ctx context.Context, svc *codebase.Service, watchID string, pollMS int) (schema.WatchStats, error) {
	ticker := time.NewTicker(time.Duration(pollMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return schema.WatchStats{}, fmt.Errorf("watch stop timed out: %w", ctx.Err())
		case <-ticker.C:
			res, err := svc.HandleWatchPoll(ctx, map[string]any{"watch_id": watchID})
			if err != nil {
				return schema.WatchStats{}, fmt.Errorf("watch poll: %w", err)
			}
			var poll watchPollResponse
			if err := decodeToolJSON(res, &poll); err != nil {
				return schema.WatchStats{}, fmt.Errorf("decode watch poll: %w", err)
			}
			if poll.Status == "stopped" || poll.Status == "failed" {
				if poll.Status == "failed" {
					return schema.WatchStats{}, fmt.Errorf("watch failed: %s", poll.Error)
				}
				return poll.Stats, nil
			}
		}
	}
}

func decodeToolJSON(res *mcp.CallToolResult, out any) error {
	if res == nil || len(res.Content) == 0 {
		return fmt.Errorf("empty tool result")
	}
	if res.IsError {
		return toolResultError(res)
	}

	text, err := firstToolText(res)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(text), out); err == nil {
		return nil
	}

	jsonBytes, err := mcp.DecodeTOONToJSON(text)
	if err != nil {
		return fmt.Errorf("decode tool text: %w", err)
	}
	if err := json.Unmarshal(jsonBytes, out); err != nil {
		return fmt.Errorf("unmarshal decoded tool text: %w", err)
	}
	return nil
}

func firstToolText(res *mcp.CallToolResult) (string, error) {
	for _, content := range res.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			return content.Text, nil
		}
	}
	return "", fmt.Errorf("tool result did not contain text content")
}

func toolResultError(res *mcp.CallToolResult) error {
	for _, content := range res.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			return errors.New(strings.TrimSpace(content.Text))
		}
	}
	return errors.New("tool returned error")
}

func expandScenarios(raw string) []string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" || raw == "all" {
		return []string{"full", "incremental", "watch"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func watchRepoID(cfg benchConfig) string {
	if repoID := strings.TrimSpace(cfg.RepoID); repoID != "" {
		return repoID + "-watch"
	}
	fixture := strings.TrimSpace(cfg.FixtureRoot)
	if fixture == "" {
		return "watch-fixture"
	}
	base := strings.TrimSpace(filepath.Base(fixture))
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "watch-fixture"
	}
	return base + "-watch"
}

func applyEnv(cfg benchConfig) {
	_ = os.Setenv("CODEBASE_DISABLE_EMBEDDINGS", fmt.Sprintf("%t", !cfg.Embeddings))
	_ = os.Setenv("CODEBASE_GIT_METADATA", fmt.Sprintf("%t", cfg.GitMetadata))
	_ = os.Setenv("CODEBASE_INDEX_CONCURRENCY", fmt.Sprintf("%d", cfg.IndexConcurrency))
	_ = os.Setenv("CODEBASE_EMBED_BATCH_SIZE", fmt.Sprintf("%d", cfg.EmbedBatchSize))
	_ = os.Setenv("CODEBASE_UPSERT_BATCH_SIZE", fmt.Sprintf("%d", cfg.UpsertBatchSize))
}

func readMem() memorySnapshot {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return memorySnapshot{
		AllocBytes:      ms.Alloc,
		TotalAllocBytes: ms.TotalAlloc,
		HeapAllocBytes:  ms.HeapAlloc,
		NumGC:           ms.NumGC,
	}
}

func diffMem(before, after memorySnapshot) memoryDelta {
	return memoryDelta{
		AllocBytesDelta:      int64(after.AllocBytes) - int64(before.AllocBytes),
		TotalAllocBytesDelta: after.TotalAllocBytes - before.TotalAllocBytes,
		HeapAllocBytesDelta:  int64(after.HeapAllocBytes) - int64(before.HeapAllocBytes),
		NumGCDelta:           int64(after.NumGC) - int64(before.NumGC),
	}
}

func summarizeRuns(runs []scenarioRun) []scenarioSummary {
	grouped := map[string][]int64{}
	for _, run := range runs {
		if run.Warmup {
			continue
		}
		grouped[run.Scenario] = append(grouped[run.Scenario], run.DurationMillis)
	}
	keys := make([]string, 0, len(grouped))
	for scenario := range grouped {
		keys = append(keys, scenario)
	}
	sort.Strings(keys)
	summary := make([]scenarioSummary, 0, len(keys))
	for _, scenario := range keys {
		durations := grouped[scenario]
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		summary = append(summary, scenarioSummary{
			Scenario:             scenario,
			MeasuredRuns:         len(durations),
			MedianDurationMillis: medianInt64(durations),
			MinDurationMillis:    durations[0],
			MaxDurationMillis:    durations[len(durations)-1],
		})
	}
	return summary
}

func medianInt64(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}

func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n%s\n", line)
	return err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

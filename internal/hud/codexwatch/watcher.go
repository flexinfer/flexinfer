package codexwatch

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Options govern the Watcher's discovery + tailing behavior.
type Options struct {
	// SessionsDir is the root that Codex Desktop writes JSONL files
	// under (default ~/.codex/sessions). The watcher scans the
	// current YYYY/MM/DD directory only; older dirs are ignored to
	// avoid replaying months of historical sessions.
	SessionsDir string

	// FromAll, when true, also tails historical files. Useful for
	// debugging; flooded on first run. Default false (only new files
	// observed after Start, plus today's directory at startup).
	FromAll bool

	// PollInterval governs per-tailer poll frequency. Default 500ms.
	PollInterval time.Duration

	// DiscoveryInterval governs how often the watcher rescans for new
	// session files. Default 2s.
	DiscoveryInterval time.Duration

	// IdleTimeout: a tailer without growth for this long emits
	// session.end and exits. Default 30min.
	IdleTimeout time.Duration

	// MaxLifetime caps a single tailer goroutine. Default 4h.
	MaxLifetime time.Duration

	// Logger nil falls back to slog.Default().
	Logger *slog.Logger
}

// Watcher orchestrates discovery + per-file tailers. Safe for use by a
// single caller (the daemon owns it). One Watcher manages all session
// files under SessionsDir; it does not multiplex across multiple users.
type Watcher struct {
	opts      Options
	publisher Publisher
	logger    *slog.Logger

	mu      sync.Mutex
	tailers map[string]*tailer // path → active tailer
	done    map[string]bool    // path → tailer already exited terminally; do not re-adopt
}

// NewWatcher constructs a Watcher targeting publisher. Returns an error
// only if SessionsDir is missing AND cannot be discovered from $HOME;
// the directory not existing yet is fine — discovery is retried.
func NewWatcher(publisher Publisher, opts Options) (*Watcher, error) {
	if publisher == nil {
		return nil, fmt.Errorf("codexwatch: publisher required")
	}
	if opts.SessionsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve sessions dir: %w", err)
		}
		opts.SessionsDir = filepath.Join(home, ".codex", "sessions")
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 500 * time.Millisecond
	}
	if opts.DiscoveryInterval == 0 {
		opts.DiscoveryInterval = 2 * time.Second
	}
	if opts.IdleTimeout == 0 {
		opts.IdleTimeout = 30 * time.Minute
	}
	if opts.MaxLifetime == 0 {
		opts.MaxLifetime = 4 * time.Hour
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Watcher{
		opts:      opts,
		publisher: publisher,
		logger:    opts.Logger,
		tailers:   map[string]*tailer{},
		done:      map[string]bool{},
	}, nil
}

// Run blocks until ctx is cancelled. Periodically scans for new session
// files under SessionsDir/YYYY/MM/DD (and the prior date for
// midnight-rollover races) and spawns a tailer per new file.
func (w *Watcher) Run(ctx context.Context) {
	tick := time.NewTicker(w.opts.DiscoveryInterval)
	defer tick.Stop()

	// Initial discovery is synchronous so the caller has a
	// deterministic startup; subsequent rescans poll on the ticker.
	w.discover(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			w.discover(ctx)
		}
	}
}

// discover scans the current + prior date directory (UTC) for new
// rollout-*.jsonl files. When FromAll is set, the entire SessionsDir
// tree is scanned regardless of date.
func (w *Watcher) discover(ctx context.Context) {
	roots := w.candidateRoots()
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue // dir may not exist yet; quiet skip
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			path := filepath.Join(root, name)
			w.maybeAdopt(ctx, path)
		}
	}
}

// candidateRoots returns the directories the discoverer should scan
// this tick. For the steady-state case that's today + yesterday (UTC);
// for FromAll it walks the entire SessionsDir tree.
func (w *Watcher) candidateRoots() []string {
	if w.opts.FromAll {
		var out []string
		_ = filepath.WalkDir(w.opts.SessionsDir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				out = append(out, p)
			}
			return nil
		})
		return out
	}
	now := time.Now().UTC()
	yest := now.Add(-24 * time.Hour)
	return []string{
		dateDir(w.opts.SessionsDir, now),
		dateDir(w.opts.SessionsDir, yest),
	}
}

// maybeAdopt registers a tailer for path if none is already running and
// the path has not already produced a terminal session.end. The "done"
// set is what stops the discoverer from re-adopting a file whose tailer
// already exited on idle-timeout or max-lifetime — the file is still on
// disk, so a naive rescan would duplicate session.start.
func (w *Watcher) maybeAdopt(ctx context.Context, path string) {
	w.mu.Lock()
	if _, ok := w.tailers[path]; ok {
		w.mu.Unlock()
		return
	}
	if w.done[path] {
		w.mu.Unlock()
		return
	}
	cfg := tailerConfig{
		pollInterval: w.opts.PollInterval,
		idleTimeout:  w.opts.IdleTimeout,
		maxLifetime:  w.opts.MaxLifetime,
		startAtEnd:   !w.opts.FromAll,
	}
	t := newTailer(path, cfg, w.publisher, w.logger)
	w.tailers[path] = t
	w.mu.Unlock()

	go func() {
		t.Run(ctx)
		w.mu.Lock()
		delete(w.tailers, path)
		// Only mark "done" if the tailer ever observed a session — an
		// empty file that no Codex thread ever wrote to should remain
		// adoptable so a freshly-started Codex session on that path
		// (rare; ids are uuids) can still be observed.
		if t.state != nil && t.state.SessionID != "" {
			w.done[path] = true
		}
		w.mu.Unlock()
	}()
}

// dateDir composes the Codex per-day directory for t: SessionsDir/YYYY/MM/DD.
func dateDir(root string, t time.Time) string {
	return filepath.Join(root,
		fmt.Sprintf("%04d", t.Year()),
		fmt.Sprintf("%02d", int(t.Month())),
		fmt.Sprintf("%02d", t.Day()),
	)
}

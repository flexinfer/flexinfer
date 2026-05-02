package crossrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
)

// Loader watches a single `repos.yaml` file, validates it, and reflects
// each successful parse into an in-memory snapshot atomically. It mirrors
// the fsnotify hot-reload pattern in `pkg/hive/squads/loader.go`: a single
// background goroutine watches the *parent* directory (so atomic
// rename-based writers — kubectl ConfigMap mounts, `mv tmp dst`, IDE
// "safe save" — all surface as Create/Rename on the target file).
//
// Bad YAML during reload is non-destructive: the previous good snapshot
// survives, OnError fires, and the watcher keeps running. The first parse
// (in NewLoader) is fatal — a misconfigured registry should fail loud at
// startup rather than silently routing every cross-repo run to fallback.
type Loader struct {
	path    string // absolute, resolved
	dir     string // parent dir of path; what fsnotify actually watches
	target  string // base name of path; matched against fsnotify events
	opts    LoaderOptions
	log     *slog.Logger
	current atomic.Pointer[Registry]
	watcher *fsnotify.Watcher
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	mu       sync.Mutex // guards onChange registration
	onChange []func(*Registry)
}

// LoaderOptions tunes loader construction. All fields are optional.
type LoaderOptions struct {
	// SkipWatch disables fsnotify; callers must invoke Reload manually.
	// Useful for tests that drive reload synchronously.
	SkipWatch bool

	// OnError is called with reload errors (parse/validate). nil drops
	// errors silently. The constructor's first-parse error is *not*
	// routed through OnError — it returns synchronously instead.
	OnError func(error)
}

// NewLoader opens the registry file, performs an initial parse + validate,
// and (unless SkipWatch is set) starts a background fsnotify loop on the
// parent directory. The first parse must succeed: a missing file or
// invalid YAML at startup returns an error and starts no watcher.
//
// Subsequent reload errors are non-fatal: the prior good snapshot is
// retained, OnError fires, and the watcher keeps running.
func NewLoader(ctx context.Context, path string, log *slog.Logger, opts LoaderOptions) (*Loader, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("crossrepo: path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("crossrepo: abs %q: %w", path, err)
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	l := &Loader{
		path:   abs,
		dir:    filepath.Dir(abs),
		target: filepath.Base(abs),
		opts:   opts,
		log:    log,
	}
	// Initial parse must succeed.
	reg, err := l.parse()
	if err != nil {
		return nil, err
	}
	l.current.Store(reg)
	l.log.Info("crossrepo registry loaded", "path", abs, "repos", len(reg.Spec.Repos))

	if opts.SkipWatch {
		return l, nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("crossrepo: fsnotify: %w", err)
	}
	if err := w.Add(l.dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("crossrepo: watch %s: %w", l.dir, err)
	}
	l.watcher = w
	watchCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.wg.Add(1)
	go l.watchLoop(watchCtx)
	return l, nil
}

// Snapshot returns a defensive copy of the current registry's repo list.
// Lock-free; safe to call from hot paths.
func (l *Loader) Snapshot() []RepoEntry {
	if l == nil {
		return nil
	}
	reg := l.current.Load()
	return reg.Repos()
}

// Current returns a pointer to the most recently loaded Registry. The
// returned pointer is safe to read (Repos slice is shared, treat as
// immutable). For mutation callers should prefer Snapshot.
func (l *Loader) Current() *Registry {
	if l == nil {
		return nil
	}
	return l.current.Load()
}

// Find looks up a repo by name in the current snapshot.
func (l *Loader) Find(name string) (RepoEntry, bool) {
	if l == nil {
		return RepoEntry{}, false
	}
	reg := l.current.Load()
	return reg.Find(name)
}

// Subscribe registers a callback fired after every successful reload (not
// on initial load). The callback runs on the watcher goroutine; it must
// not block long.
func (l *Loader) Subscribe(f func(*Registry)) {
	if f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onChange = append(l.onChange, f)
}

// Reload re-reads + validates the registry. On success it atomically
// replaces the current snapshot and fires subscribers. On parse/validate
// error it returns the error and leaves the prior snapshot intact —
// callers (and the watcher loop) treat that as the "last good config"
// behavior.
func (l *Loader) Reload() error {
	reg, err := l.parse()
	if err != nil {
		return err
	}
	l.current.Store(reg)
	l.fireOnChange(reg)
	l.log.Info("crossrepo registry reloaded", "path", l.path, "repos", len(reg.Spec.Repos))
	return nil
}

// Close stops the watcher and releases fsnotify resources.
func (l *Loader) Close() error {
	if l == nil {
		return nil
	}
	if l.cancel != nil {
		l.cancel()
	}
	l.wg.Wait()
	if l.watcher != nil {
		return l.watcher.Close()
	}
	return nil
}

// parse reads the registry file from disk and validates it. On success
// the registry has its defaults applied. Errors are returned unwrapped
// from this layer so callers can chain with %w.
func (l *Loader) parse() (*Registry, error) {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return nil, fmt.Errorf("crossrepo: read %s: %w", l.path, err)
	}
	reg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	return reg, nil
}

func (l *Loader) fireOnChange(reg *Registry) {
	l.mu.Lock()
	subs := append([]func(*Registry){}, l.onChange...)
	l.mu.Unlock()
	for _, f := range subs {
		f(reg)
	}
}

func (l *Loader) watchLoop(ctx context.Context) {
	defer l.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			if !l.isTargetEvent(ev) {
				continue
			}
			if err := l.Reload(); err != nil {
				if l.opts.OnError != nil {
					l.opts.OnError(err)
				}
				l.log.Warn("crossrepo reload error (last-good retained)",
					"path", l.path, "err", err.Error())
			}
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			if l.opts.OnError != nil {
				l.opts.OnError(err)
			}
			l.log.Warn("crossrepo watcher error", "err", err.Error())
		}
	}
}

// isTargetEvent narrows the parent-directory watch to events that touch
// our specific registry file. Atomic rename-based writers fire Create or
// Rename on the destination path.
func (l *Loader) isTargetEvent(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	return filepath.Base(ev.Name) == l.target
}

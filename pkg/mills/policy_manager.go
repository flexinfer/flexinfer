package mills

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"
)

// PolicyManager owns the active Policy and supports atomic hot-reload from a
// YAML file. Readers call Current() with no lock; the value is replaced
// atomically so in-flight runs always see a coherent snapshot. A bad reload
// (parse or validate error) keeps the previous policy active and surfaces the
// error via the OnError callback.
type PolicyManager struct {
	path     string
	current  atomic.Pointer[Policy]
	watcher  *fsnotify.Watcher
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	onChange []func(old, new *Policy)
	onError  func(error)
	mu       sync.Mutex // guards onChange registration only
}

// PolicyManagerOptions tunes manager construction. All fields are optional.
type PolicyManagerOptions struct {
	// OnError is invoked with reload errors. If nil, errors are dropped.
	OnError func(error)
	// SkipWatch disables fsnotify entirely; Reload() must be called manually.
	// Useful for tests and for ConfigMap mounts that re-inject the file by
	// path replacement (which fsnotify can miss without watch_root tricks).
	SkipWatch bool
}

// NewPolicyManager loads the policy at path, validates it, and (unless
// SkipWatch is set) installs an fsnotify watch on the file's parent directory
// so it survives ConfigMap remounts.
func NewPolicyManager(ctx context.Context, path string, opts PolicyManagerOptions) (*PolicyManager, error) {
	if path == "" {
		return nil, errors.New("mills: policy path required")
	}
	p, err := LoadPolicy(path)
	if err != nil {
		return nil, err
	}
	m := &PolicyManager{path: path, onError: opts.OnError}
	m.current.Store(p)

	if opts.SkipWatch {
		return m, nil
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("mills: fsnotify: %w", err)
	}
	// Watch the parent directory so atomic-rename and ConfigMap-style mounts
	// (which replace the symlink chain) trigger reloads.
	if err := w.Add(filepath.Dir(path)); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("mills: watch %s: %w", path, err)
	}
	m.watcher = w

	watchCtx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.wg.Add(1)
	go m.watchLoop(watchCtx)
	return m, nil
}

// Current returns the active policy. Lock-free; safe to call from hot paths.
func (m *PolicyManager) Current() *Policy {
	if m == nil {
		return nil
	}
	return m.current.Load()
}

// Subscribe registers a callback fired after every successful reload. The
// callback runs synchronously on the watcher goroutine, so it must not block
// for long.
func (m *PolicyManager) Subscribe(f func(old, new *Policy)) {
	if f == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = append(m.onChange, f)
}

// Reload re-reads the policy file and atomically swaps the active value.
// Safe to call from tests or as a manual fallback when SkipWatch is true.
func (m *PolicyManager) Reload() error {
	p, err := LoadPolicy(m.path)
	if err != nil {
		return err
	}
	old := m.current.Swap(p)
	m.fireOnChange(old, p)
	return nil
}

func (m *PolicyManager) fireOnChange(old, new *Policy) {
	m.mu.Lock()
	subs := append([]func(*Policy, *Policy){}, m.onChange...)
	m.mu.Unlock()
	for _, f := range subs {
		f(old, new)
	}
}

// Close stops the watcher goroutine and releases fsnotify resources.
func (m *PolicyManager) Close() error {
	if m == nil {
		return nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	if m.watcher != nil {
		return m.watcher.Close()
	}
	return nil
}

func (m *PolicyManager) watchLoop(ctx context.Context) {
	defer m.wg.Done()
	target := filepath.Clean(m.path)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-m.watcher.Events:
			if !ok {
				return
			}
			// Only react to events for our exact target file. Many editors do
			// rename-write so we look at Create / Write / Rename events.
			if filepath.Clean(ev.Name) != target {
				continue
			}
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if err := m.Reload(); err != nil && m.onError != nil {
				m.onError(err)
			}
		case err, ok := <-m.watcher.Errors:
			if !ok {
				return
			}
			if m.onError != nil {
				m.onError(err)
			}
		}
	}
}

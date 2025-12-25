// Package sync provides file watching for automatic configuration sync.
package sync

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchEventType indicates what kind of change occurred.
type WatchEventType int

const (
	EventRegistryChanged WatchEventType = iota
	EventProfileChanged
)

// WatchEvent represents a file system change event.
type WatchEvent struct {
	Type     WatchEventType
	Profile  string // For profile changes
	Path     string
	Time     time.Time
}

// Watcher monitors configuration files for changes.
type Watcher struct {
	manager     *Manager
	fsWatcher   *fsnotify.Watcher
	debounce    time.Duration
	onChange    chan WatchEvent
	logger      *slog.Logger
	repoRoot    string
	registryPath string

	mu          sync.Mutex
	pending     map[string]time.Time // Debounce tracking
	done        chan struct{}
}

// WatcherConfig configures the file watcher.
type WatcherConfig struct {
	Manager      *Manager
	RepoRoot     string
	RegistryPath string
	Debounce     time.Duration
	Logger       *slog.Logger
}

// NewWatcher creates a new file watcher.
func NewWatcher(cfg WatcherConfig) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if cfg.Debounce == 0 {
		cfg.Debounce = 500 * time.Millisecond
	}

	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	w := &Watcher{
		manager:      cfg.Manager,
		fsWatcher:    fsWatcher,
		debounce:     cfg.Debounce,
		onChange:     make(chan WatchEvent, 10),
		logger:       cfg.Logger,
		repoRoot:     cfg.RepoRoot,
		registryPath: cfg.RegistryPath,
		pending:      make(map[string]time.Time),
		done:         make(chan struct{}),
	}

	return w, nil
}

// Start begins watching for file changes.
func (w *Watcher) Start() error {
	// Watch registry file
	if w.registryPath != "" {
		if err := w.fsWatcher.Add(filepath.Dir(w.registryPath)); err != nil {
			w.logger.Warn("failed to watch registry directory", "path", w.registryPath, "error", err)
		} else {
			w.logger.Debug("watching registry", "path", w.registryPath)
		}
	}

	// Watch profile directories
	if w.manager != nil && w.repoRoot != "" {
		for _, profile := range w.manager.List() {
			p := w.manager.Get(profile)
			if p == nil {
				continue
			}
			profileDir := filepath.Join(w.repoRoot, p.RepoDir)
			if _, err := os.Stat(profileDir); err == nil {
				if err := w.fsWatcher.Add(profileDir); err != nil {
					w.logger.Warn("failed to watch profile directory", "profile", profile, "path", profileDir, "error", err)
				} else {
					w.logger.Debug("watching profile", "profile", profile, "path", profileDir)
				}
			}
		}
	}

	// Start event processing
	go w.processEvents()

	return nil
}

// Stop stops the watcher.
func (w *Watcher) Stop() error {
	close(w.done)
	return w.fsWatcher.Close()
}

// Events returns the channel of watch events.
func (w *Watcher) Events() <-chan WatchEvent {
	return w.onChange
}

// processEvents handles fsnotify events with debouncing.
func (w *Watcher) processEvents() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleFSEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			w.logger.Error("watcher error", "error", err)

		case <-ticker.C:
			w.processPending()
		}
	}
}

// handleFSEvent processes a single filesystem event.
func (w *Watcher) handleFSEvent(event fsnotify.Event) {
	// Only care about writes and creates
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
		return
	}

	w.mu.Lock()
	w.pending[event.Name] = time.Now()
	w.mu.Unlock()
}

// processPending checks for debounced events ready to emit.
func (w *Watcher) processPending() {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	for path, lastChange := range w.pending {
		if now.Sub(lastChange) < w.debounce {
			continue // Still debouncing
		}

		delete(w.pending, path)

		// Determine event type
		event := w.classifyEvent(path)
		if event != nil {
			select {
			case w.onChange <- *event:
			default:
				w.logger.Warn("event channel full, dropping event", "path", path)
			}
		}
	}
}

// classifyEvent determines what kind of event occurred.
func (w *Watcher) classifyEvent(path string) *WatchEvent {
	// Check if this is the registry file
	if w.registryPath != "" && filepath.Base(path) == filepath.Base(w.registryPath) {
		return &WatchEvent{
			Type: EventRegistryChanged,
			Path: path,
			Time: time.Now(),
		}
	}

	// Check if this is a profile directory
	if w.manager != nil {
		for _, profile := range w.manager.List() {
			p := w.manager.Get(profile)
			if p == nil {
				continue
			}
			profileDir := filepath.Join(w.repoRoot, p.RepoDir)
			if isSubPath(path, profileDir) {
				return &WatchEvent{
					Type:    EventProfileChanged,
					Profile: profile,
					Path:    path,
					Time:    time.Now(),
				}
			}
		}
	}

	return nil
}

// isSubPath checks if path is under dir.
func isSubPath(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return len(rel) > 0 && rel[0] != '.'
}

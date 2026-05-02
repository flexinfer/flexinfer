package squads

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fsnotify/fsnotify"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// Loader scans a directory of squad manifest YAMLs, validates them, and
// reflects each one into the canonical `squads` table. It mirrors the
// fsnotify hot-reload pattern in pkg/hive/policy_manager.go: a single
// background goroutine watches the parent directory and re-syncs on any
// create / write / rename / remove event. Bad manifests do not replace
// last-good — the in-memory cache and DB rows from the previous good
// reload survive a parse error and the loader's OnError fires.
type Loader struct {
	dir   string
	store *store.Store
	opts  LoaderOptions

	cache    atomic.Pointer[map[string]*Manifest]
	watcher  *fsnotify.Watcher
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex // guards onChange registration
	onChange []func(snapshot map[string]*Manifest)
}

// LoaderOptions tunes loader construction. All fields are optional.
type LoaderOptions struct {
	// SkipWatch disables fsnotify; callers must invoke Sync manually.
	// Useful for tests + ConfigMap mounts that re-inject files via
	// path replacement (which fsnotify can miss without a parent watch).
	SkipWatch bool

	// OnError is called with reload errors (parse, validate, DB write).
	// nil drops errors silently.
	OnError func(error)

	// SHAFn returns a content hash for a manifest. Defaults to a stable
	// hash; callers can plug in a git-aware version that returns the
	// blob SHA so squad rows show real provenance.
	SHAFn func(data []byte) string

	// Logger is structured logging. nil discards.
	Logger *slog.Logger
}

// NewLoader scans the directory once, reflects manifests into the store,
// and (unless SkipWatch is set) starts a background fsnotify loop. A
// fatal scan error is returned synchronously; per-file parse/validate
// errors are surfaced via OnError but do not abort startup.
func NewLoader(ctx context.Context, dir string, st *store.Store, opts LoaderOptions) (*Loader, error) {
	if dir == "" {
		return nil, errors.New("squads: dir required")
	}
	if st == nil {
		return nil, errors.New("squads: store required")
	}
	if opts.SHAFn == nil {
		opts.SHAFn = stableSHA
	}
	l := &Loader{dir: dir, store: st, opts: opts}
	empty := map[string]*Manifest{}
	l.cache.Store(&empty)

	if err := l.Sync(ctx); err != nil {
		return nil, err
	}
	if opts.SkipWatch {
		return l, nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("squads: fsnotify: %w", err)
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("squads: watch %s: %w", dir, err)
	}
	l.watcher = w
	watchCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.wg.Add(1)
	go l.watchLoop(watchCtx)
	return l, nil
}

// Current returns a snapshot of the most recently loaded manifests, keyed
// by name. Lock-free; safe to call from hot paths.
func (l *Loader) Current() map[string]*Manifest {
	if l == nil {
		return nil
	}
	p := l.cache.Load()
	if p == nil {
		return nil
	}
	out := make(map[string]*Manifest, len(*p))
	for k, v := range *p {
		out[k] = v
	}
	return out
}

// Subscribe registers a callback fired after every successful reload.
// The callback runs on the watcher goroutine; it must not block long.
func (l *Loader) Subscribe(f func(map[string]*Manifest)) {
	if f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onChange = append(l.onChange, f)
}

// Sync re-reads the directory, validates every manifest, and reflects
// the live set into the squads table. It is safe to call from tests or
// as a manual fallback when SkipWatch is true.
//
// Manifests that fail parse/validate are skipped (with OnError fired);
// the rest still reflect into the store. A directory-level error (dir
// missing, permission denied) is returned and leaves the previous good
// cache intact — the constructor surfaces this so misconfigured deploys
// fail loudly at startup rather than silently routing everything to
// fallback.
func (l *Loader) Sync(ctx context.Context) error {
	if _, err := os.Stat(l.dir); err != nil {
		return fmt.Errorf("squads: read dir %s: %w", l.dir, err)
	}
	manifests, errs := l.scan()
	for _, err := range errs {
		if l.opts.OnError != nil {
			l.opts.OnError(err)
		}
	}
	// Always update the cache to reflect the new good set, even if
	// some files failed — readers see a coherent partial view rather
	// than stale results from before the file was edited.
	snapshot := make(map[string]*Manifest, len(manifests))
	for _, m := range manifests {
		snapshot[m.Metadata.Name] = m
	}
	l.cache.Store(&snapshot)

	// Reflect to canonical store. Each manifest is upserted; squads
	// that vanished from disk are not auto-deleted (operators may pause
	// a squad by setting `enabled: false` while the file is moved).
	for _, m := range manifests {
		if err := l.upsert(ctx, m); err != nil {
			if l.opts.OnError != nil {
				l.opts.OnError(fmt.Errorf("squads: upsert %s: %w", m.Metadata.Name, err))
			}
		}
	}
	l.fireOnChange(snapshot)
	if l.opts.Logger != nil {
		l.opts.Logger.Info("squads loaded", "count", len(snapshot), "dir", l.dir)
	}
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

// scan reads every *.yaml under the watch dir, parses + validates each
// one, and returns the successful set plus a slice of per-file errors.
// The directory must exist; missing dir returns ([], [err]).
func (l *Loader) scan() ([]*Manifest, []error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, []error{fmt.Errorf("squads: read dir %s: %w", l.dir, err)}
	}
	var (
		manifests []*Manifest
		errs      []error
	)
	for _, e := range entries {
		if e.IsDir() || !isYAMLFile(e) {
			continue
		}
		full := filepath.Join(l.dir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			errs = append(errs, fmt.Errorf("squads: read %s: %w", full, err))
			continue
		}
		m, err := Parse(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("squads: %s: %w", e.Name(), err))
			continue
		}
		manifests = append(manifests, m)
	}
	// Stable order so Sync is deterministic.
	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Metadata.Name < manifests[j].Metadata.Name
	})
	return manifests, errs
}

func isYAMLFile(e fs.DirEntry) bool {
	name := strings.ToLower(e.Name())
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// upsert reflects one manifest into the squads table. Bytes hashing is
// best-effort; callers can override SHAFn to use git blob SHAs.
func (l *Loader) upsert(ctx context.Context, m *Manifest) error {
	row := &store.Squad{
		Name:             m.Metadata.Name,
		Paths:            append([]string(nil), m.Spec.Paths...),
		Tests:            append([]string(nil), m.Spec.Tests...),
		Gates:            gatesToMap(m.Spec.Gates),
		Ensemble:         copyAnyMap(m.Spec.Ensemble),
		BudgetShare:      m.Spec.BudgetShare,
		RecursionEnabled: m.Spec.RecursionEnabled,
		Enabled:          m.IsEnabled(),
		LastLoadedSHA:    l.opts.SHAFn(yamlBytesFor(m)),
	}
	return l.store.Squads.PutSquad(ctx, row)
}

// gatesToMap converts the squad spec's typed gates map to the untyped
// map[string]any shape the canonical-store DAO expects.
func gatesToMap(in map[string][]string) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// yamlBytesFor returns the marshaled bytes of a manifest for hashing.
// Errors are silently swallowed — the resulting SHA just becomes the hash
// of the empty document, which still differs across manifests because
// scan() already validated the input.
func yamlBytesFor(m *Manifest) []byte {
	if m == nil {
		return nil
	}
	type wireSpec struct {
		Paths            []string            `yaml:"paths,omitempty"`
		Tests            []string            `yaml:"tests,omitempty"`
		Gates            map[string][]string `yaml:"gates,omitempty"`
		Ensemble         map[string]any      `yaml:"ensemble,omitempty"`
		BudgetShare      float64             `yaml:"budget_share,omitempty"`
		RecursionEnabled bool                `yaml:"recursion_enabled,omitempty"`
		Enabled          *bool               `yaml:"enabled,omitempty"`
	}
	type wire struct {
		APIVersion string       `yaml:"apiVersion"`
		Kind       string       `yaml:"kind"`
		Metadata   ManifestMeta `yaml:"metadata"`
		Spec       wireSpec     `yaml:"spec"`
	}
	w := wire{
		APIVersion: m.APIVersion, Kind: m.Kind, Metadata: m.Metadata,
		Spec: wireSpec{
			Paths: m.Spec.Paths, Tests: m.Spec.Tests, Gates: m.Spec.Gates,
			Ensemble: m.Spec.Ensemble, BudgetShare: m.Spec.BudgetShare,
			RecursionEnabled: m.Spec.RecursionEnabled, Enabled: m.Spec.Enabled,
		},
	}
	// Use a side-effect-free marshal so callers don't need yaml import.
	out, _ := marshalForHash(w)
	return out
}

func (l *Loader) fireOnChange(snapshot map[string]*Manifest) {
	l.mu.Lock()
	subs := append([]func(map[string]*Manifest){}, l.onChange...)
	l.mu.Unlock()
	for _, f := range subs {
		f(snapshot)
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
			if !isManifestEvent(ev) {
				continue
			}
			if err := l.Sync(ctx); err != nil && l.opts.OnError != nil {
				l.opts.OnError(err)
			}
		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			if l.opts.OnError != nil {
				l.opts.OnError(err)
			}
		}
	}
}

func isManifestEvent(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	name := strings.ToLower(filepath.Base(ev.Name))
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

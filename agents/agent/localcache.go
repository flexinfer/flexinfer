package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// Local cache inventory.
//
// A `strategy: Local` Model stages its weights into <base>/<namespace>/<name>
// on a GPU node's disk, and nothing in Kubernetes owns that directory. Debug
// canaries of one 27B artifact left eight 18 GB copies plus older orphans
// (about 190 GB) on the 7900 XTX root, and the DiskPressure trip they fed
// (2026-11-06 22:52Z) evicted every pod on the card for 17 hours; the node's
// disk alert fired, but nothing said whose bytes they were. The controller's
// on-delete policy (controllers/model_cache_cleanup.go) handles future
// deletions; this inventory makes the directories that are already orphaned,
// or retained on purpose, visible. It reads and never deletes.
//
// Every refresh lists the Models of every namespace, walks the base and
// exports flexinfer_node_local_cache_bytes and
// flexinfer_node_local_cache_orphans. Layout rules:
//
//   - <base>/<ns> is a namespace directory when at least one Model lives in
//     namespace <ns>. Each directory under it is one entry, an orphan unless a
//     Model <ns>/<name> exists. Loose files there are not counted.
//   - Anything else directly under the base, a directory or a regular file,
//     is one entry with an empty namespace and is an orphan: legacy flat
//     caches (<base>/qwen35-27b-opus-distill on the XTX, 25 GB, no CR since
//     March), other tools' stores (<base>/ollama), the directory of a
//     namespace whose Models are all gone, and ModelCache nodeLocal
//     directories (<base>/<name>; none exist in the fleet, every ModelCache
//     is SharedPVC as of 2026-11-07).
//   - Symlinks are neither followed nor counted.
//
// "Not an orphan" means only that a Model of that namespace/name exists, not
// that it uses this node or this strategy: a Model that moved nodes or left
// `strategy: Local` keeps its old directory listed as owned.
//
// Sizes are du's: allocated blocks of every entry in the tree, each inode
// once, symlinks not followed. A directory's size is cached against its
// mtime and re-measured when the mtime changes or the cached size is older
// than localCacheRemeasureAfter. The stage renames each finished file into
// the top directory, which moves its mtime, but a download growing under
// .cache/huggingface/download does not, so the age bound keeps an in-flight
// stage from reading stale for long.
//
// The inventory runs in its own goroutine so a slow walk (a sick disk) or a
// slow API server never delays the GPU probe loop or the health sentinel.

const (
	// DefaultLocalCacheRoot is the controller's default Local cache base
	// (controllers/model_cache_cleanup.go localCacheBasePath).
	DefaultLocalCacheRoot = "/var/lib/flexinfer/models"
	// localCacheRemeasureAfter bounds how long a cached directory size is
	// trusted while the directory's own mtime has not changed.
	localCacheRemeasureAfter = 10 * time.Minute
	localCacheListTimeout    = 10 * time.Second
)

// ModelKeyLister lists the namespace/name of every Model in the cluster.
type ModelKeyLister interface {
	ModelKeys(ctx context.Context) (map[types.NamespacedName]struct{}, error)
}

// modelKeySource lists Models across all namespaces. The agent
// ServiceAccount needs cluster-wide get/list on models.ai.flexinfer, which
// the chart grants only while agent.localCacheInventory.enabled is true.
type modelKeySource struct {
	c client.Reader
}

func (s modelKeySource) ModelKeys(ctx context.Context) (map[types.NamespacedName]struct{}, error) {
	var list aiv1alpha2.ModelList
	if err := s.c.List(ctx, &list); err != nil {
		return nil, err
	}
	out := make(map[types.NamespacedName]struct{}, len(list.Items))
	for i := range list.Items {
		out[types.NamespacedName{Namespace: list.Items[i].Namespace, Name: list.Items[i].Name}] = struct{}{}
	}
	return out, nil
}

// LocalCacheEntry is one inventoried cache directory (or a stray file
// directly under the base).
type LocalCacheEntry struct {
	// Namespace is empty for an entry directly under the base.
	Namespace string
	Name      string
	Bytes     int64
	Orphan    bool
}

// fileID identifies an inode, for counting hard links once.
type fileID struct {
	dev, ino uint64
}

type cachedSize struct {
	modTime    time.Time
	measuredAt time.Time
	bytes      int64
}

type localCacheSeries struct {
	namespace, model, orphan string
}

// LocalCacheInventory sizes the Local cache directories on the agent's node
// and joins them with the Model list. Create it with NewLocalCacheInventory
// and call Run in its own goroutine. It is not safe for concurrent use.
type LocalCacheInventory struct {
	root   string
	node   string
	models ModelKeyLister
	log    logr.Logger

	// usage measures one lstat result (diskUsage in production).
	usage          func(fs.FileInfo) (bytes int64, id fileID, multiLinked bool)
	now            func() time.Time
	remeasureAfter time.Duration

	sizes        map[string]cachedSize // by path relative to root
	known        map[types.NamespacedName]struct{}
	haveModels   bool
	listFailures int
	exported     map[localCacheSeries]struct{}
}

// NewLocalCacheInventory returns an inventory of root (the Local cache base
// as mounted in the agent container) that reads Models with the agent's
// in-cluster ServiceAccount.
func (a *Agent) NewLocalCacheInventory(root string, log logr.Logger) (*LocalCacheInventory, error) {
	c, err := newModelReader()
	if err != nil {
		return nil, err
	}
	return newLocalCacheInventory(root, a.nodeName, modelKeySource{c: c}, log), nil
}

func newLocalCacheInventory(root, node string, models ModelKeyLister, log logr.Logger) *LocalCacheInventory {
	return &LocalCacheInventory{
		root:           filepath.Clean(root),
		node:           node,
		models:         models,
		log:            log,
		usage:          diskUsage,
		now:            time.Now,
		remeasureAfter: localCacheRemeasureAfter,
		sizes:          map[string]cachedSize{},
		exported:       map[localCacheSeries]struct{}{},
	}
}

// Run refreshes the inventory now and then every interval until ctx is done.
// Failures are logged when they start and when they clear, not every cycle.
func (inv *LocalCacheInventory) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	inv.log.Info("Local cache inventory enabled", "root", inv.root, "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr string
	for {
		_, err := inv.Refresh(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil && err.Error() != lastErr:
			inv.log.Error(err, "Local cache inventory refresh failed")
			lastErr = err.Error()
		case err == nil && lastErr != "":
			inv.log.Info("Local cache inventory refresh recovered")
			lastErr = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Refresh lists the Models, walks the root and exports the result. Before the
// first successful Model listing it exports nothing: a directory whose owner
// cannot be checked is not reported as an orphan. After that a failed listing
// reuses the last good one. An unreadable root clears every series, so the
// metrics go absent instead of going stale.
func (inv *LocalCacheInventory) Refresh(ctx context.Context) ([]LocalCacheEntry, error) {
	lctx, cancel := context.WithTimeout(ctx, localCacheListTimeout)
	keys, err := inv.models.ModelKeys(lctx)
	cancel()
	switch {
	case err == nil:
		if inv.listFailures > 0 && inv.haveModels {
			inv.log.Info("Local cache inventory: Model listing recovered", "failures", inv.listFailures)
		}
		inv.listFailures = 0
		inv.known, inv.haveModels = keys, true
	case !inv.haveModels:
		inv.listFailures++
		return nil, fmt.Errorf("list Models: %w", err)
	default:
		inv.listFailures++
		if inv.listFailures == 1 {
			inv.log.Info("Local cache inventory: cannot list Models, judging orphans by the last good listing", "error", err.Error())
		}
	}

	entries, err := inv.scan(inv.known)
	if err != nil {
		inv.clear()
		return nil, fmt.Errorf("read Local cache root %s: %w", inv.root, err)
	}
	inv.export(entries)
	return entries, nil
}

// scan walks the root two levels deep and sizes every entry.
func (inv *LocalCacheInventory) scan(models map[types.NamespacedName]struct{}) ([]LocalCacheEntry, error) {
	namespaces := map[string]bool{}
	for k := range models {
		namespaces[k.Namespace] = true
	}
	top, err := os.ReadDir(inv.root)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []LocalCacheEntry
	for _, d := range top {
		name := d.Name()
		if d.IsDir() && namespaces[name] {
			children, err := os.ReadDir(filepath.Join(inv.root, name))
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					inv.log.Error(err, "Cannot read Local cache namespace directory", "path", filepath.Join(inv.root, name))
				}
				continue
			}
			for _, c := range children {
				if !c.IsDir() {
					continue // loose files and symlinks
				}
				rel := filepath.Join(name, c.Name())
				bytes, ok := inv.size(rel, c)
				if !ok {
					continue
				}
				seen[rel] = true
				_, owned := models[types.NamespacedName{Namespace: name, Name: c.Name()}]
				out = append(out, LocalCacheEntry{Namespace: name, Name: c.Name(), Bytes: bytes, Orphan: !owned})
			}
			continue
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			continue // symlinks, sockets, devices
		}
		bytes, ok := inv.size(name, d)
		if !ok {
			continue
		}
		seen[name] = true
		out = append(out, LocalCacheEntry{Name: name, Bytes: bytes, Orphan: true})
	}
	for rel := range inv.sizes {
		if !seen[rel] {
			delete(inv.sizes, rel)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// size returns an entry's allocated bytes, from the cache while the
// directory's mtime is unchanged and the measurement is younger than
// remeasureAfter. ok is false when the entry vanished or cannot be read.
func (inv *LocalCacheInventory) size(rel string, d fs.DirEntry) (bytes int64, ok bool) {
	info, err := d.Info() // lstat, taken before the walk so a change during it re-measures next time
	if err != nil {
		return 0, false
	}
	if !info.IsDir() {
		b, _, _ := inv.usage(info)
		return b, true
	}
	now := inv.now()
	if c, hit := inv.sizes[rel]; hit && c.modTime.Equal(info.ModTime()) && now.Sub(c.measuredAt) < inv.remeasureAfter {
		return c.bytes, true
	}
	b, err := inv.measure(filepath.Join(inv.root, rel))
	if err != nil {
		delete(inv.sizes, rel)
		if !errors.Is(err, fs.ErrNotExist) {
			inv.log.Error(err, "Cannot measure Local cache directory", "path", filepath.Join(inv.root, rel))
		}
		return 0, false
	}
	inv.sizes[rel] = cachedSize{modTime: info.ModTime(), measuredAt: now, bytes: b}
	return b, true
}

// measure is `du -s`: the allocated bytes of every entry under path, hard
// links counted once, symlinks not followed. Entries that vanish or cannot
// be read mid-walk (a stage renaming a download into place, a cleanup Job)
// are skipped; only an unreadable path itself fails.
func (inv *LocalCacheInventory) measure(path string) (int64, error) {
	var total int64
	linked := map[fileID]struct{}{}
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == path {
				return err
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		b, id, multi := inv.usage(info)
		if multi {
			if _, dup := linked[id]; dup {
				return nil
			}
			linked[id] = struct{}{}
		}
		total += b
		return nil
	})
	return total, err
}

// export replaces the node's series with entries and logs each newly seen
// orphan once.
func (inv *LocalCacheInventory) export(entries []LocalCacheEntry) {
	next := make(map[localCacheSeries]struct{}, len(entries))
	orphans := 0
	for _, e := range entries {
		s := localCacheSeries{namespace: e.Namespace, model: e.Name, orphan: strconv.FormatBool(e.Orphan)}
		next[s] = struct{}{}
		metrics.NodeLocalCacheBytes.WithLabelValues(inv.node, s.namespace, s.model, s.orphan).Set(float64(e.Bytes))
		if !e.Orphan {
			continue
		}
		orphans++
		if _, known := inv.exported[s]; !known {
			inv.log.Info("Orphaned Local cache directory: no Model owns it",
				"path", filepath.Join(inv.root, e.Namespace, e.Name), "bytes", e.Bytes)
		}
	}
	for s := range inv.exported {
		if _, keep := next[s]; !keep {
			metrics.NodeLocalCacheBytes.DeleteLabelValues(inv.node, s.namespace, s.model, s.orphan)
		}
	}
	inv.exported = next
	metrics.NodeLocalCacheOrphans.WithLabelValues(inv.node).Set(float64(orphans))
}

// clear deletes every series this inventory exported.
func (inv *LocalCacheInventory) clear() {
	for s := range inv.exported {
		metrics.NodeLocalCacheBytes.DeleteLabelValues(inv.node, s.namespace, s.model, s.orphan)
	}
	inv.exported = map[localCacheSeries]struct{}{}
	metrics.NodeLocalCacheOrphans.DeleteLabelValues(inv.node)
}

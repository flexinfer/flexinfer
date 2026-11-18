package agent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// apparentUsage counts regular files by their length and everything else as
// zero, so expected sizes are exact sums; it keeps diskUsage's inode identity
// for the hard-link rule.
func apparentUsage(info fs.FileInfo) (int64, fileID, bool) {
	_, id, multi := diskUsage(info)
	if !info.Mode().IsRegular() {
		return 0, id, multi
	}
	return info.Size(), id, multi
}

func writeSized(t *testing.T, path string, n int) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	writeFile(t, path, strings.Repeat("x", n))
}

func cacheModel(ns, name string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
}

func newTestInventory(t *testing.T, root, node string, models ModelKeyLister) *LocalCacheInventory {
	t.Helper()
	inv := newLocalCacheInventory(root, node, models, logr.Discard())
	inv.usage = apparentUsage
	t.Cleanup(inv.clear)
	return inv
}

// localCacheSeriesOf returns the node's flexinfer_node_local_cache_bytes
// series as "namespace/model orphan=<bool>" → value.
func localCacheSeriesOf(t *testing.T, node string) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	for _, m := range collectFor(t, metrics.NodeLocalCacheBytes, node) {
		l := labelMap(m)
		out[l["namespace"]+"/"+l["model"]+" orphan="+l["orphan"]] = m.GetGauge().GetValue()
	}
	return out
}

func localCacheOrphansOf(t *testing.T, node string) (float64, bool) {
	t.Helper()
	ms := collectFor(t, metrics.NodeLocalCacheOrphans, node)
	if len(ms) == 0 {
		return 0, false
	}
	return ms[0].GetGauge().GetValue(), true
}

func collectFor(t *testing.T, c prometheus.Collector, node string) []*dto.Metric {
	t.Helper()
	ch := make(chan prometheus.Metric, 256)
	c.Collect(ch)
	close(ch)
	var out []*dto.Metric
	for m := range ch {
		var d dto.Metric
		require.NoError(t, m.Write(&d))
		if labelMap(&d)["node"] == node {
			out = append(out, &d)
		}
	}
	return out
}

func labelMap(m *dto.Metric) map[string]string {
	l := map[string]string{}
	for _, p := range m.GetLabel() {
		l[p.GetName()] = p.GetValue()
	}
	return l
}

// The 2026-11-07 XTX tree in miniature: lane caches under flexinfer-system,
// a flat legacy cache and an ollama store directly under the base.
func TestLocalCacheInventory_JoinAndExport(t *testing.T) {
	root := t.TempDir()
	const ns, node = "flexinfer-system", "node-join"
	writeSized(t, filepath.Join(root, ns, "qwen38-27b-xtx-warm-canary", "config.json"), 100)
	writeSized(t, filepath.Join(root, ns, "qwen38-27b-xtx-warm-canary", "model-00001-of-00007.safetensors"), 4000)
	writeSized(t, filepath.Join(root, ns, "qwen38-27b-xtx-warm-canary", ".cache", "huggingface", "download", "x.incomplete"), 500)
	writeSized(t, filepath.Join(root, ns, "qwen38-27b-so-canary", "model.safetensors"), 3000)
	writeSized(t, filepath.Join(root, ns, "loose.txt"), 77) // not a cache directory
	require.NoError(t, os.Symlink(filepath.Join(root, ns, "qwen38-27b-so-canary"), filepath.Join(root, ns, "so-link")))
	writeSized(t, filepath.Join(root, "tenant-a", "chat", "w.safetensors"), 200)
	writeSized(t, filepath.Join(root, "qwen35-27b-opus-distill", "model-00001-of-00007.safetensors"), 2500)
	writeSized(t, filepath.Join(root, "qwen35-27b-opus-distill", "config.json"), 50)
	writeSized(t, filepath.Join(root, "ollama", "blobs", "sha256-abc"), 700)
	writeSized(t, filepath.Join(root, "ollama", "manifests", "m"), 10)
	writeSized(t, filepath.Join(root, "retired-ns", "old-lane", "w.safetensors"), 900)
	writeSized(t, filepath.Join(root, "stray.tar"), 1234)
	require.NoError(t, os.Symlink(filepath.Join(root, "ollama"), filepath.Join(root, "shortcut")))

	scheme := runtime.NewScheme()
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		cacheModel(ns, "qwen38-27b-xtx-warm-canary"),
		cacheModel(ns, "no-directory-here"),
		cacheModel("tenant-a", "chat"),
	).Build()
	inv := newTestInventory(t, root, node, modelKeySource{c: c})

	entries, err := inv.Refresh(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []LocalCacheEntry{
		{Name: "ollama", Bytes: 710, Orphan: true},
		{Name: "qwen35-27b-opus-distill", Bytes: 2550, Orphan: true},
		{Name: "retired-ns", Bytes: 900, Orphan: true}, // a namespace with no Models left
		{Name: "stray.tar", Bytes: 1234, Orphan: true},
		{Namespace: ns, Name: "qwen38-27b-so-canary", Bytes: 3000, Orphan: true},
		{Namespace: ns, Name: "qwen38-27b-xtx-warm-canary", Bytes: 4600},
		{Namespace: "tenant-a", Name: "chat", Bytes: 200},
	}, entries)
	assert.Equal(t, map[string]float64{
		"/ollama orphan=true":                           710,
		"/qwen35-27b-opus-distill orphan=true":          2550,
		"/retired-ns orphan=true":                       900,
		"/stray.tar orphan=true":                        1234,
		ns + "/qwen38-27b-so-canary orphan=true":        3000,
		ns + "/qwen38-27b-xtx-warm-canary orphan=false": 4600,
		"tenant-a/chat orphan=false":                    200,
	}, localCacheSeriesOf(t, node))
	orphans, ok := localCacheOrphansOf(t, node)
	require.True(t, ok)
	assert.Equal(t, 5.0, orphans)

	// Deleting a Model flips its directory to orphan and drops the owned
	// series. tenant-a loses its last Model, so its directory is no longer a
	// namespace directory and is reported whole from the base.
	require.NoError(t, c.Delete(context.Background(), cacheModel(ns, "qwen38-27b-xtx-warm-canary")))
	require.NoError(t, c.Delete(context.Background(), cacheModel("tenant-a", "chat")))
	// Removing a directory drops its series.
	require.NoError(t, os.RemoveAll(filepath.Join(root, ns, "qwen38-27b-so-canary")))
	require.NoError(t, os.RemoveAll(filepath.Join(root, "stray.tar")))

	_, err = inv.Refresh(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{
		"/ollama orphan=true":                          710,
		"/qwen35-27b-opus-distill orphan=true":         2550,
		"/retired-ns orphan=true":                      900,
		"/tenant-a orphan=true":                        200,
		ns + "/qwen38-27b-xtx-warm-canary orphan=true": 4600,
	}, localCacheSeriesOf(t, node))
	orphans, _ = localCacheOrphansOf(t, node)
	assert.Equal(t, 5.0, orphans)
	assert.NotContains(t, inv.sizes, filepath.Join(ns, "qwen38-27b-so-canary"), "a vanished directory leaves the size cache")
}

// A directory is re-walked only when its mtime moves or its size is older
// than remeasureAfter; growth in a nested directory alone stays cached.
func TestLocalCacheInventory_SizeCachedByMtime(t *testing.T) {
	root := t.TempDir()
	const ns, node = "flexinfer-system", "node-mtime"
	dir := filepath.Join(root, ns, "lane")
	nested := filepath.Join(dir, ".cache", "huggingface", "download")
	writeSized(t, filepath.Join(dir, "a.safetensors"), 1000)
	mkdirAll(t, nested)
	stamp := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(dir, stamp, stamp))

	now := stamp
	inv := newTestInventory(t, root, node, &fakeModelKeys{keys: keysOf(ns + "/lane")})
	inv.now = func() time.Time { return now }
	size := func() float64 {
		t.Helper()
		_, err := inv.Refresh(context.Background())
		require.NoError(t, err)
		return localCacheSeriesOf(t, node)[ns+"/lane orphan=false"]
	}

	assert.Equal(t, 1000.0, size())

	// A download growing below the top directory does not move its mtime.
	writeSized(t, filepath.Join(nested, "b.incomplete"), 500)
	require.NoError(t, os.Chtimes(dir, stamp, stamp))
	now = now.Add(inv.remeasureAfter - time.Second)
	assert.Equal(t, 1000.0, size(), "unchanged mtime: cached size")

	now = now.Add(time.Second)
	assert.Equal(t, 1500.0, size(), "cached size expired: re-measured")

	// The stage renames a finished file into the top directory: new mtime.
	writeSized(t, filepath.Join(nested, "c.incomplete"), 250)
	require.NoError(t, os.Rename(filepath.Join(nested, "c.incomplete"), filepath.Join(dir, "c.safetensors")))
	later := stamp.Add(time.Minute)
	require.NoError(t, os.Chtimes(dir, later, later))
	assert.Equal(t, 1750.0, size(), "mtime moved: re-measured")
}

type fakeModelKeys struct {
	keys map[types.NamespacedName]struct{}
	err  error
}

func (f *fakeModelKeys) ModelKeys(context.Context) (map[types.NamespacedName]struct{}, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[types.NamespacedName]struct{}, len(f.keys))
	for k := range f.keys {
		out[k] = struct{}{}
	}
	return out, nil
}

func keysOf(names ...string) map[types.NamespacedName]struct{} {
	out := map[types.NamespacedName]struct{}{}
	for _, n := range names {
		ns, name, _ := strings.Cut(n, "/")
		out[types.NamespacedName{Namespace: ns, Name: name}] = struct{}{}
	}
	return out
}

// Before the first good Model listing nothing is exported (an owner that
// cannot be checked is not an orphan); after it, a failed listing reuses the
// last good one.
func TestLocalCacheInventory_ModelListFailure(t *testing.T) {
	root := t.TempDir()
	const ns, node = "flexinfer-system", "node-listfail"
	writeSized(t, filepath.Join(root, ns, "lane", "w"), 10)
	models := &fakeModelKeys{err: errors.New("connection refused")}
	inv := newTestInventory(t, root, node, models)

	_, err := inv.Refresh(context.Background())
	require.Error(t, err)
	assert.Empty(t, localCacheSeriesOf(t, node))
	_, ok := localCacheOrphansOf(t, node)
	assert.False(t, ok, "no orphan count before a Model listing succeeded")

	models.err, models.keys = nil, keysOf(ns+"/lane")
	_, err = inv.Refresh(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{ns + "/lane orphan=false": 10}, localCacheSeriesOf(t, node))

	models.err = errors.New("connection refused")
	writeSized(t, filepath.Join(root, ns, "canary", "w"), 20)
	_, err = inv.Refresh(context.Background())
	require.NoError(t, err, "the last good listing still judges orphans")
	assert.Equal(t, map[string]float64{
		ns + "/lane orphan=false":  10,
		ns + "/canary orphan=true": 20,
	}, localCacheSeriesOf(t, node))
	assert.Equal(t, 1, inv.listFailures, "one failure counted since the good listing")
}

// An unreadable root clears the node's series instead of leaving them stale.
func TestLocalCacheInventory_UnreadableRootClears(t *testing.T) {
	root := t.TempDir()
	const ns, node = "flexinfer-system", "node-noroot"
	writeSized(t, filepath.Join(root, ns, "canary", "w"), 10)
	inv := newTestInventory(t, root, node, &fakeModelKeys{keys: keysOf(ns + "/lane")})

	_, err := inv.Refresh(context.Background())
	require.NoError(t, err)
	require.Len(t, localCacheSeriesOf(t, node), 1)

	require.NoError(t, os.RemoveAll(root))
	_, err = inv.Refresh(context.Background())
	require.Error(t, err)
	assert.Empty(t, localCacheSeriesOf(t, node))
	_, ok := localCacheOrphansOf(t, node)
	assert.False(t, ok)
}

// The Model join reads every namespace, not only the agent's.
func TestModelKeySource_AllNamespaces(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	var c client.Reader = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		cacheModel("flexinfer-system", "a"),
		cacheModel("tenant-a", "b"),
	).Build()
	got, err := modelKeySource{c: c}.ModelKeys(context.Background())
	require.NoError(t, err)
	assert.Equal(t, keysOf("flexinfer-system/a", "tenant-a/b"), got)
}

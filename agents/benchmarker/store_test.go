package benchmarker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func sampleRecord() BenchmarkRecord {
	return BenchmarkRecord{
		ModelName:        "test-model",
		Backend:          "ollama",
		NodeName:         "node1",
		Namespace:        "default",
		ConfigMapName:    "bench-result",
		GlobalConfigMap:  defaultBenchmarkResultsConfigMap,
		TokensPerSecond:  42.5,
		CompletionTokens: 100,
		Duration:         2 * time.Second,
		Samples:          5,
		BatchSize:        16,
		Iterations:       5,
		WarmupIterations: 2,
		MinDuration:      30 * time.Second,
		Timestamp:        time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC),
	}
}

func TestConfigMapStore_Save_CreatesNew(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()
	store := NewConfigMapStore(clientset)

	record := sampleRecord()
	err := store.Save(context.Background(), record)
	require.NoError(t, err)

	cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), "bench-result", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, "test-model", cm.Data["model"])
	assert.Equal(t, "ollama", cm.Data["backend"])
	assert.Equal(t, "42.5", cm.Data["tokensPerSecond"])
	assert.Equal(t, "100", cm.Data["completionTokens"])
	assert.Equal(t, "5", cm.Data["samples"])
	assert.Equal(t, "16", cm.Data["batchSize"])
	assert.NotEmpty(t, cm.Data["timestamp"])
}

func TestConfigMapStore_Save_UpdatesExisting(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()

	// Pre-create a ConfigMap with old data.
	_, err := clientset.CoreV1().ConfigMaps("default").Create(context.Background(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "bench-result", Namespace: "default"},
		Data:       map[string]string{"tokensPerSecond": "10"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	store := NewConfigMapStore(clientset)
	record := sampleRecord()
	err = store.Save(context.Background(), record)
	require.NoError(t, err)

	cm, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), "bench-result", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "42.5", cm.Data["tokensPerSecond"])
}

func TestConfigMapStore_Save_WritesGlobalResult(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node1",
			Labels: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
				"flexinfer.ai/gpu.vram":   "24Gi",
				"flexinfer.ai/gpu.count":  "1",
				"flexinfer.ai/gpu.int4":   "true",
			},
		},
	}
	_, err := clientset.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
	require.NoError(t, err)

	store := NewConfigMapStore(clientset)
	record := sampleRecord()
	err = store.Save(context.Background(), record)
	require.NoError(t, err)

	global, err := clientset.CoreV1().ConfigMaps("default").Get(context.Background(), defaultBenchmarkResultsConfigMap, metav1.GetOptions{})
	require.NoError(t, err)

	deviceClass := deviceClassFromNode(node)
	key := benchmarkKey("ollama", "test-model", deviceClass)
	assert.Equal(t, "42.5", global.Data[key])
	assert.Contains(t, global.Data["meta_"+key], `"tokensPerSecond":42.5`)
}

// fakeStore is a test double for ResultStore.
type fakeStore struct {
	saved   []BenchmarkRecord
	failErr error
}

func (f *fakeStore) Save(_ context.Context, r BenchmarkRecord) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.saved = append(f.saved, r)
	return nil
}

func TestMultiStore_FansOutToAll(t *testing.T) {
	t.Parallel()
	s1 := &fakeStore{}
	s2 := &fakeStore{}
	multi := NewMultiStore(s1, s2)

	record := sampleRecord()
	err := multi.Save(context.Background(), record)
	require.NoError(t, err)

	assert.Len(t, s1.saved, 1)
	assert.Len(t, s2.saved, 1)
	assert.Equal(t, record.ModelName, s1.saved[0].ModelName)
	assert.Equal(t, record.ModelName, s2.saved[0].ModelName)
}

func TestMultiStore_StopsOnFirstError(t *testing.T) {
	t.Parallel()
	errBoom := errors.New("boom")
	s1 := &fakeStore{failErr: errBoom}
	s2 := &fakeStore{}
	multi := NewMultiStore(s1, s2)

	err := multi.Save(context.Background(), sampleRecord())
	assert.ErrorIs(t, err, errBoom)
	// Second store should not have been called.
	assert.Len(t, s2.saved, 0)
}

func TestMultiStore_EmptyStores(t *testing.T) {
	t.Parallel()
	multi := NewMultiStore()
	err := multi.Save(context.Background(), sampleRecord())
	require.NoError(t, err)
}

func TestSaveRecord_PersistsMeasuredResult(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()
	store := &fakeStore{}
	b := &Benchmarker{
		kubeClient: clientset,
		namespace:  "default",
		resultsCM:  defaultBenchmarkResultsConfigMap,
		store:      store,
	}

	record := sampleRecord()
	record.ConfigMapName = ""
	record.GlobalConfigMap = ""

	err := b.SaveRecord(context.Background(), &record, "gauntlet-result")
	require.NoError(t, err)

	require.Len(t, store.saved, 1)
	assert.Equal(t, "gauntlet-result", store.saved[0].ConfigMapName)
	assert.Equal(t, defaultBenchmarkResultsConfigMap, store.saved[0].GlobalConfigMap)
	assert.Equal(t, record.TokensPerSecond, store.saved[0].TokensPerSecond)
}

func TestSaveRecord_RejectsNilRecord(t *testing.T) {
	t.Parallel()
	b := &Benchmarker{}

	err := b.SaveRecord(context.Background(), nil, "gauntlet-result")
	assert.ErrorContains(t, err, "benchmark record is nil")
}

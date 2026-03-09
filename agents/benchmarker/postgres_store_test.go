package benchmarker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func pgRecord() BenchmarkRecord {
	return BenchmarkRecord{
		ModelName:        "qwen3-14b",
		Backend:          "vllm",
		NodeName:         "gpu-node-1",
		Namespace:        "ai",
		ConfigMapName:    "bench-result",
		GlobalConfigMap:  defaultBenchmarkResultsConfigMap,
		TokensPerSecond:  73.2,
		CompletionTokens: 250,
		Duration:         3400 * time.Millisecond,
		Samples:          5,
		BatchSize:        128,
		Iterations:       5,
		WarmupIterations: 2,
		MinDuration:      30 * time.Second,
		Timestamp:        time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
	}
}

func TestPostgresStore_Save_InsertsRecord(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := &PostgresStore{db: db, kubeClient: nil}

	mock.ExpectExec(`INSERT INTO benchmarks`).
		WithArgs(
			sqlmock.AnyArg(), // id (uuid)
			"qwen3-14b",      // model
			"vllm",           // backend
			"unknown",        // device_class (no kube client)
			73.2,             // tokens_per_second
			250,              // completion_tokens
			3.4,              // duration_seconds
			5,                // samples
			128,              // batch_size
			time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC), // timestamp
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Save(context.Background(), pgRecord())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Save_DeviceClassFallback(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// No kubeClient → device_class should be "unknown"
	store := &PostgresStore{db: db, kubeClient: nil}

	mock.ExpectExec(`INSERT INTO benchmarks`).
		WithArgs(
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			"unknown", // device_class fallback
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Save(context.Background(), pgRecord())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Save_DeviceClassFromNode(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	clientset := fake.NewSimpleClientset()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
				"flexinfer.ai/gpu.vram":   "24Gi",
				"flexinfer.ai/gpu.count":  "1",
				"flexinfer.ai/gpu.int4":   "true",
			},
		},
	}
	_, err = clientset.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{})
	require.NoError(t, err)

	store := &PostgresStore{db: db, kubeClient: clientset}

	expectedDeviceClass := "vendor=AMD,arch=gfx1100,vram=24Gi,count=1,int4=true"

	mock.ExpectExec(`INSERT INTO benchmarks`).
		WithArgs(
			sqlmock.AnyArg(),    // id
			"qwen3-14b",         // model
			"vllm",              // backend
			expectedDeviceClass, // device_class from node labels
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = store.Save(context.Background(), pgRecord())
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Save_DBError(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	store := &PostgresStore{db: db, kubeClient: nil}

	dbErr := fmt.Errorf("connection refused")
	mock.ExpectExec(`INSERT INTO benchmarks`).
		WillReturnError(dbErr)

	err = store.Save(context.Background(), pgRecord())
	require.Error(t, err)
	assert.ErrorContains(t, err, "connection refused")
	assert.ErrorContains(t, err, "failed to insert benchmark result into postgres")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPostgresStore_Close(t *testing.T) {
	t.Parallel()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	store := &PostgresStore{db: db, kubeClient: nil}

	mock.ExpectClose()

	err = store.Close()
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

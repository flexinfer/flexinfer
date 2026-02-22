package benchmarker

import (
	"context"
	"time"
)

// BenchmarkRecord holds all the data produced by a benchmark run,
// which can be saved to one or more persistence stores.
type BenchmarkRecord struct {
	ModelName        string
	Backend          string
	NodeName         string
	Namespace        string
	ConfigMapName    string // Target ConfigMap for this specific deployment's result
	GlobalConfigMap  string // Global results ConfigMap name
	TokensPerSecond  float64
	CompletionTokens int
	Duration         time.Duration
	Samples          int
	BatchSize        int
	Iterations       int
	WarmupIterations int
	MinDuration      time.Duration
	Timestamp        time.Time
}

// ResultStore defines how benchmark results are saved.
type ResultStore interface {
	Save(ctx context.Context, record BenchmarkRecord) error
}

// MultiStore saves results to multiple stores sequentially.
type MultiStore struct {
	stores []ResultStore
}

// NewMultiStore creates a store that delegates to all provided stores.
func NewMultiStore(stores ...ResultStore) ResultStore {
	return &MultiStore{stores: stores}
}

// Save calls Save on all underlying stores.
func (m *MultiStore) Save(ctx context.Context, record BenchmarkRecord) error {
	for _, store := range m.stores {
		if err := store.Save(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

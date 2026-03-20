package hud

import (
	"context"
	"time"

	"github.com/crb2nu/loom/internal/spawn"
)

// SpawnStore wraps spawn.FileStore to preserve the original HUD API while
// delegating persistence to the extracted spawn package.
type SpawnStore struct {
	inner *spawn.FileStore
}

// NewSpawnStore creates a SpawnStore backed by a spawn.FileStore.
func NewSpawnStore(dir string) (*SpawnStore, error) {
	fs, err := spawn.NewFileStore(dir)
	if err != nil {
		return nil, err
	}
	return &SpawnStore{inner: fs}, nil
}

// Save persists a spawn state.
func (s *SpawnStore) Save(state *SpawnState) error {
	return s.inner.Save(context.Background(), state)
}

// Load reads all persisted spawn states.
func (s *SpawnStore) Load() ([]*SpawnState, error) {
	return s.inner.LoadAll(context.Background())
}

// Delete removes a persisted spawn state.
func (s *SpawnStore) Delete(spawnID string) error {
	return s.inner.Delete(context.Background(), spawnID)
}

// PruneCompleted removes terminal spawn states older than maxAge.
func (s *SpawnStore) PruneCompleted(maxAge time.Duration) error {
	return s.inner.PruneCompleted(context.Background(), maxAge)
}

// isTerminal delegates to spawn.IsTerminal.
func isTerminal(status SpawnStatus) bool {
	return spawn.IsTerminal(status)
}

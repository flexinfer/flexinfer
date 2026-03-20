package hud

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SpawnStore persists spawn state to disk for recovery after restart.
type SpawnStore struct {
	dir string // e.g. ~/.config/loom/spawns/
}

// NewSpawnStore creates a SpawnStore, ensuring the directory exists.
func NewSpawnStore(dir string) (*SpawnStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("spawn store directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create spawn store dir: %w", err)
	}
	return &SpawnStore{dir: dir}, nil
}

// Save persists a spawn state to disk as <spawn_id>.json.
func (s *SpawnStore) Save(state *SpawnState) error {
	if state == nil {
		return fmt.Errorf("cannot save nil spawn state")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal spawn state: %w", err)
	}
	path := s.path(state.SpawnID)
	// Write atomically via temp file + rename to avoid partial reads.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write spawn state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename spawn state: %w", err)
	}
	return nil
}

// Load reads all persisted spawn states from disk.
func (s *SpawnStore) Load() ([]*SpawnState, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read spawn store dir: %w", err)
	}
	var states []*SpawnState
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		// Skip temp files from incomplete writes.
		if strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue // skip unreadable files
		}
		var st SpawnState
		if err := json.Unmarshal(data, &st); err != nil {
			continue // skip malformed files
		}
		states = append(states, &st)
	}
	return states, nil
}

// Delete removes the persisted state file for a spawn.
func (s *SpawnStore) Delete(spawnID string) error {
	path := s.path(spawnID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete spawn state %s: %w", spawnID, err)
	}
	return nil
}

// PruneCompleted removes persisted state files for spawns in terminal states
// (completed, failed, stopped) that ended more than maxAge ago.
func (s *SpawnStore) PruneCompleted(maxAge time.Duration) error {
	states, err := s.Load()
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-maxAge)
	for _, st := range states {
		if !isTerminal(st.Status) {
			continue
		}
		if st.EndedAt != nil && st.EndedAt.Before(cutoff) {
			if err := s.Delete(st.SpawnID); err != nil {
				return err
			}
		}
	}
	return nil
}

// path returns the file path for a spawn state file.
func (s *SpawnStore) path(spawnID string) string {
	return filepath.Join(s.dir, spawnID+".json")
}

// isTerminal returns true if the status represents a terminal spawn state.
func isTerminal(status SpawnStatus) bool {
	switch status {
	case SpawnStatusCompleted, SpawnStatusFailed, SpawnStatusStopped:
		return true
	default:
		return false
	}
}

// defaultSpawnStoreDir returns the default spawn store directory.
func defaultSpawnStoreDir() string {
	if cfgDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(cfgDir, "loom", "spawns")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "spawns")
}

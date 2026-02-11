// Package state provides JSON-file persistence for devbox sandbox state.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store manages persistent state for devbox sandboxes.
type Store struct {
	path string
	mu   sync.RWMutex
	data StoreData
}

// StoreData is the persisted state structure.
type StoreData struct {
	Sandboxes map[string]*Entry `json:"sandboxes"`
}

// Entry describes a single sandbox environment.
type Entry struct {
	ProjectDir      string    `json:"project_dir"`
	ContainerID     string    `json:"container_id"`
	ImageTag        string    `json:"image_tag"`
	FingerprintHash string    `json:"fingerprint_hash"`
	Backend         string    `json:"backend"`
	Status          string    `json:"status"` // building, ready, running, stopped, error
	LastUsed        time.Time `json:"last_used"`
	CreatedAt       time.Time `json:"created_at"`
	Error           string    `json:"error,omitempty"`
}

// NewStore creates a store at the given path, loading existing data if present.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	s := &Store{
		path: filepath.Join(dir, "state.json"),
		data: StoreData{Sandboxes: make(map[string]*Entry)},
	}

	data, err := os.ReadFile(s.path)
	if err == nil {
		_ = json.Unmarshal(data, &s.data)
	}
	if s.data.Sandboxes == nil {
		s.data.Sandboxes = make(map[string]*Entry)
	}

	return s, nil
}

// Get returns the entry for a project, or nil if not found.
func (s *Store) Get(project string) *Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Sandboxes[project]
}

// Set stores an entry for a project and persists to disk.
func (s *Store) Set(project string, entry *Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sandboxes[project] = entry
	return s.save()
}

// Delete removes an entry for a project and persists to disk.
func (s *Store) Delete(project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Sandboxes, project)
	return s.save()
}

// List returns all sandbox entries.
func (s *Store) List() map[string]*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]*Entry, len(s.data.Sandboxes))
	for k, v := range s.data.Sandboxes {
		result[k] = v
	}
	return result
}

// TouchLastUsed updates the LastUsed timestamp for a project.
func (s *Store) TouchLastUsed(project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.data.Sandboxes[project]; ok {
		entry.LastUsed = time.Now()
		return s.save()
	}
	return nil
}

// IdleEntries returns entries whose LastUsed is older than the given duration.
func (s *Store) IdleEntries(idleTimeout time.Duration) map[string]*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := time.Now().Add(-idleTimeout)
	result := make(map[string]*Entry)
	for k, v := range s.data.Sandboxes {
		if v.Status == "running" && v.LastUsed.Before(cutoff) {
			result[k] = v
		}
	}
	return result
}

// save persists state to disk. Must be called with mu held.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

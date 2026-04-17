package agentcontext

import (
	"context"
	"sync"
	"time"
)

// =========================================================================
// Pinned raw-blob store (F2)
//
// When the scheduler replaces a batch of entries with an LLM-synthesized
// summary, we still want the raw blobs available for a bounded window so
// callers can retrieve the untouched originals (e.g. for audits or recovery).
// =========================================================================

// PinnedStore tracks entry IDs whose raw content should be retained past a
// compaction event until a configured expiry time. Implementations should be
// safe for concurrent use.
type PinnedStore interface {
	// Pin marks entryIDs as pinned until the given time (inclusive).
	Pin(ctx context.Context, entryIDs []string, until time.Time) error

	// IsPinned reports whether the given entryID currently has a live pin.
	IsPinned(ctx context.Context, entryID string) (bool, error)

	// Purge removes any pins whose expiry has already passed.
	Purge(ctx context.Context) error
}

// MemoryPinnedStore is an in-memory PinnedStore keyed on entryID -> unpinAt.
// Entries are considered unpinned once time.Now() >= unpinAt.
type MemoryPinnedStore struct {
	mu      sync.Mutex
	unpinAt map[string]time.Time
}

// NewMemoryPinnedStore returns an empty in-memory PinnedStore.
func NewMemoryPinnedStore() *MemoryPinnedStore {
	return &MemoryPinnedStore{unpinAt: make(map[string]time.Time)}
}

// Pin marks the given entry IDs as pinned until the specified time.
func (m *MemoryPinnedStore) Pin(_ context.Context, entryIDs []string, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range entryIDs {
		if id == "" {
			continue
		}
		// If a later expiry already exists, keep the longer one.
		if existing, ok := m.unpinAt[id]; ok && existing.After(until) {
			continue
		}
		m.unpinAt[id] = until
	}
	return nil
}

// IsPinned reports whether the given entry is currently pinned.
func (m *MemoryPinnedStore) IsPinned(_ context.Context, entryID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	until, ok := m.unpinAt[entryID]
	if !ok {
		return false, nil
	}
	if !time.Now().Before(until) {
		// Lazy purge of this single key.
		delete(m.unpinAt, entryID)
		return false, nil
	}
	return true, nil
}

// Purge removes any pins whose expiry has passed.
func (m *MemoryPinnedStore) Purge(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, until := range m.unpinAt {
		if !now.Before(until) {
			delete(m.unpinAt, id)
		}
	}
	return nil
}

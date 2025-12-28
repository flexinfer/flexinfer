// Package secrets provides a pluggable secret store for loom.
// It supports multiple backends (env, keychain, file, 1Password) and provides
// a unified interface for secret management.
package secrets

import (
	"fmt"
	"sync"
)

// Backend is the interface that all secret store backends must implement.
type Backend interface {
	// Get retrieves a secret value by key. Returns empty string if not found.
	Get(key string) (string, error)

	// Set stores a secret value. Not all backends support this (e.g., env).
	Set(key, value string) error

	// Delete removes a secret. Not all backends support this.
	Delete(key string) error

	// List returns all available secret keys. Not all backends support this.
	List() ([]string, error)

	// Name returns the backend name for logging/display.
	Name() string

	// ReadOnly returns true if this backend doesn't support writes.
	ReadOnly() bool
}

// ErrNotFound is returned when a secret is not found in any backend.
var ErrNotFound = fmt.Errorf("secret not found")

// ErrReadOnly is returned when attempting to write to a read-only backend.
var ErrReadOnly = fmt.Errorf("backend is read-only")

// Manager coordinates multiple secret backends.
// It searches backends in priority order for reads and uses the primary
// backend for writes.
type Manager struct {
	backends []Backend // Searched in order for reads
	primary  Backend   // Used for writes (first writable backend)
	mu       sync.RWMutex
}

// NewManager creates a new secret manager with the given backends.
// Backends are searched in the order provided. The first writable backend
// becomes the primary for writes.
func NewManager(backends ...Backend) *Manager {
	m := &Manager{
		backends: backends,
	}

	// Find first writable backend for primary
	for _, b := range backends {
		if !b.ReadOnly() {
			m.primary = b
			break
		}
	}

	return m
}

// Get retrieves a secret, searching backends in priority order.
// Returns the value and which backend it came from.
func (m *Manager) Get(key string) (string, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, b := range m.backends {
		val, err := b.Get(key)
		if err != nil {
			continue // Try next backend
		}
		if val != "" {
			return val, b.Name(), nil
		}
	}

	return "", "", ErrNotFound
}

// GetValue is a convenience method that just returns the value.
func (m *Manager) GetValue(key string) string {
	val, _, _ := m.Get(key)
	return val
}

// Set stores a secret in the primary backend.
func (m *Manager) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.primary == nil {
		return fmt.Errorf("no writable backend configured")
	}

	return m.primary.Set(key, value)
}

// Delete removes a secret from the primary backend.
func (m *Manager) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.primary == nil {
		return fmt.Errorf("no writable backend configured")
	}

	return m.primary.Delete(key)
}

// List returns all secret keys from all backends.
func (m *Manager) List() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]bool)
	var keys []string

	for _, b := range m.backends {
		bKeys, err := b.List()
		if err != nil {
			continue // Skip backends that don't support listing
		}
		for _, k := range bKeys {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}

	return keys, nil
}

// Backends returns the configured backends for inspection.
func (m *Manager) Backends() []Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backends
}

// PrimaryBackend returns the primary (writable) backend.
func (m *Manager) PrimaryBackend() Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.primary
}

// DefaultManager creates a manager with the default backend configuration:
// 1. Environment variables (highest priority, read-only)
// 2. macOS Keychain (if available)
// 3. Encrypted file store (fallback, writable)
func DefaultManager() (*Manager, error) {
	var backends []Backend

	// Environment variables - highest priority, allows runtime override
	backends = append(backends, NewEnvBackend())

	// macOS Keychain - if available
	if kb, err := NewKeychainBackend(); err == nil {
		backends = append(backends, kb)
	}

	// 1Password CLI - if configured (uses default vault)
	if op, err := NewOnePasswordBackend(""); err == nil {
		backends = append(backends, op)
	}

	// Encrypted file store - fallback
	if fb, err := NewFileBackend(""); err == nil {
		backends = append(backends, fb)
	}

	if len(backends) == 0 {
		return nil, fmt.Errorf("no secret backends available")
	}

	return NewManager(backends...), nil
}

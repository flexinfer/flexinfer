package proxy

import "sync"

// TypedSyncMap is a type-safe wrapper around sync.Map that eliminates
// unchecked type assertions which can panic at runtime.
type TypedSyncMap[K comparable, V any] struct {
	m sync.Map
}

// Load returns the value stored in the map for a key, or the zero value if no
// value is present. The ok result indicates whether value was found in the map.
func (m *TypedSyncMap[K, V]) Load(key K) (V, bool) {
	val, ok := m.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

// Store sets the value for a key.
func (m *TypedSyncMap[K, V]) Store(key K, val V) {
	m.m.Store(key, val)
}

// LoadOrStore returns the existing value for the key if present. Otherwise, it
// stores and returns the given value. The loaded result is true if the value was
// loaded, false if stored.
func (m *TypedSyncMap[K, V]) LoadOrStore(key K, val V) (V, bool) {
	actual, loaded := m.m.LoadOrStore(key, val)
	return actual.(V), loaded
}

// Delete deletes the value for a key.
func (m *TypedSyncMap[K, V]) Delete(key K) {
	m.m.Delete(key)
}

// Range calls f sequentially for each key and value present in the map. If f
// returns false, range stops the iteration.
func (m *TypedSyncMap[K, V]) Range(f func(K, V) bool) {
	m.m.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}

// Clear removes all entries from the map by replacing the inner sync.Map.
func (m *TypedSyncMap[K, V]) Clear() {
	m.m = sync.Map{}
}

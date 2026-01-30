// Package routing provides request routing strategies for multi-replica models.
package routing

import (
	"fmt"
	"hash/crc32"
	"sort"
	"sync"
)

// HashRing implements consistent hashing for routing requests to pods.
// It maintains a ring of virtual nodes (vnodes) to distribute load evenly
// and minimize redistribution when pods are added/removed.
type HashRing struct {
	mu      sync.RWMutex
	vnodes  int               // Number of virtual nodes per real node
	ring    []uint32          // Sorted list of vnode hashes
	nodeMap map[uint32]string // vnode hash -> node address
	nodes   map[string]bool   // Set of node addresses
}

// NewHashRing creates a new consistent hash ring.
// vnodes controls how many virtual nodes are created per real node.
// Higher values give more even distribution but use more memory.
// Recommended: 100-200 for typical workloads.
func NewHashRing(vnodes int) *HashRing {
	if vnodes <= 0 {
		vnodes = 150
	}
	return &HashRing{
		vnodes:  vnodes,
		ring:    make([]uint32, 0),
		nodeMap: make(map[uint32]string),
		nodes:   make(map[string]bool),
	}
}

// Add adds a node (pod address) to the hash ring.
func (h *HashRing) Add(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.nodes[node] {
		return // Already exists
	}

	h.nodes[node] = true

	// Add virtual nodes
	for i := 0; i < h.vnodes; i++ {
		vnode := h.hashKey(node, i)
		h.ring = append(h.ring, vnode)
		h.nodeMap[vnode] = node
	}

	// Sort the ring
	sort.Slice(h.ring, func(i, j int) bool {
		return h.ring[i] < h.ring[j]
	})
}

// Remove removes a node from the hash ring.
func (h *HashRing) Remove(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.nodes[node] {
		return // Doesn't exist
	}

	delete(h.nodes, node)

	// Remove virtual nodes
	newRing := make([]uint32, 0, len(h.ring)-h.vnodes)
	for _, vnode := range h.ring {
		if h.nodeMap[vnode] != node {
			newRing = append(newRing, vnode)
		} else {
			delete(h.nodeMap, vnode)
		}
	}
	h.ring = newRing
}

// Get returns the node responsible for the given key.
// Returns empty string if the ring is empty.
func (h *HashRing) Get(key string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.ring) == 0 {
		return ""
	}

	hash := h.hash(key)

	// Binary search for the first vnode >= hash
	idx := sort.Search(len(h.ring), func(i int) bool {
		return h.ring[i] >= hash
	})

	// Wrap around if we've gone past the end
	if idx >= len(h.ring) {
		idx = 0
	}

	return h.nodeMap[h.ring[idx]]
}

// GetN returns up to n distinct nodes for the given key, for replication.
// The first node is the primary, subsequent nodes can be used for failover.
func (h *HashRing) GetN(key string, n int) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.ring) == 0 {
		return nil
	}

	hash := h.hash(key)

	// Binary search for the first vnode >= hash
	idx := sort.Search(len(h.ring), func(i int) bool {
		return h.ring[i] >= hash
	})

	if idx >= len(h.ring) {
		idx = 0
	}

	// Collect distinct nodes
	seen := make(map[string]bool)
	result := make([]string, 0, n)

	for i := 0; i < len(h.ring) && len(result) < n; i++ {
		nodeIdx := (idx + i) % len(h.ring)
		node := h.nodeMap[h.ring[nodeIdx]]
		if !seen[node] {
			seen[node] = true
			result = append(result, node)
		}
	}

	return result
}

// Nodes returns a copy of all nodes in the ring.
func (h *HashRing) Nodes() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]string, 0, len(h.nodes))
	for node := range h.nodes {
		result = append(result, node)
	}
	return result
}

// Size returns the number of nodes in the ring.
func (h *HashRing) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.nodes)
}

// SetNodes replaces all nodes in the ring with the given set.
// This is useful for bulk updates from endpoint watches.
func (h *HashRing) SetNodes(nodes []string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Find nodes to remove and add
	newNodes := make(map[string]bool)
	for _, node := range nodes {
		newNodes[node] = true
	}

	// Remove old nodes
	for node := range h.nodes {
		if !newNodes[node] {
			delete(h.nodes, node)
			// Remove virtual nodes
			newRing := make([]uint32, 0, len(h.ring)-h.vnodes)
			for _, vnode := range h.ring {
				if h.nodeMap[vnode] != node {
					newRing = append(newRing, vnode)
				} else {
					delete(h.nodeMap, vnode)
				}
			}
			h.ring = newRing
		}
	}

	// Add new nodes
	for _, node := range nodes {
		if !h.nodes[node] {
			h.nodes[node] = true
			for i := 0; i < h.vnodes; i++ {
				vnode := h.hashKey(node, i)
				h.ring = append(h.ring, vnode)
				h.nodeMap[vnode] = node
			}
		}
	}

	// Sort the ring
	sort.Slice(h.ring, func(i, j int) bool {
		return h.ring[i] < h.ring[j]
	})
}

// hash computes the hash of a key.
func (h *HashRing) hash(key string) uint32 {
	return crc32.ChecksumIEEE([]byte(key))
}

// hashKey computes the hash of a virtual node.
func (h *HashRing) hashKey(node string, idx int) uint32 {
	// Use a format that spreads vnodes evenly across the ring.
	// Prepending the index helps distribute virtual nodes more uniformly.
	key := fmt.Sprintf("%d:%s", idx, node)
	return crc32.ChecksumIEEE([]byte(key))
}

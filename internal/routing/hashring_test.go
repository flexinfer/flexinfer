package routing

import (
	"fmt"
	"testing"
)

func TestHashRing_AddRemove(t *testing.T) {
	ring := NewHashRing(100)

	// Initially empty
	if ring.Size() != 0 {
		t.Errorf("expected empty ring, got %d nodes", ring.Size())
	}

	// Add nodes
	ring.Add("10.0.0.1:8000")
	ring.Add("10.0.0.2:8000")
	ring.Add("10.0.0.3:8000")

	if ring.Size() != 3 {
		t.Errorf("expected 3 nodes, got %d", ring.Size())
	}

	// Adding same node again should be no-op
	ring.Add("10.0.0.1:8000")
	if ring.Size() != 3 {
		t.Errorf("expected 3 nodes after duplicate add, got %d", ring.Size())
	}

	// Remove a node
	ring.Remove("10.0.0.2:8000")
	if ring.Size() != 2 {
		t.Errorf("expected 2 nodes after remove, got %d", ring.Size())
	}

	// Removing non-existent node should be no-op
	ring.Remove("10.0.0.99:8000")
	if ring.Size() != 2 {
		t.Errorf("expected 2 nodes after removing non-existent, got %d", ring.Size())
	}
}

func TestHashRing_Get(t *testing.T) {
	ring := NewHashRing(100)

	// Empty ring returns empty string
	if node := ring.Get("test-key"); node != "" {
		t.Errorf("expected empty string from empty ring, got %s", node)
	}

	// Add nodes
	ring.Add("10.0.0.1:8000")
	ring.Add("10.0.0.2:8000")
	ring.Add("10.0.0.3:8000")

	// Same key should always return same node
	node1 := ring.Get("session-123")
	node2 := ring.Get("session-123")
	if node1 != node2 {
		t.Errorf("same key returned different nodes: %s vs %s", node1, node2)
	}

	// Different keys may return different nodes (probabilistic)
	// Just verify they return valid nodes
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key-%d", i)
		node := ring.Get(key)
		if node == "" {
			t.Errorf("key %s returned empty node", key)
		}
	}
}

func TestHashRing_Consistency(t *testing.T) {
	ring := NewHashRing(100)
	ring.Add("10.0.0.1:8000")
	ring.Add("10.0.0.2:8000")
	ring.Add("10.0.0.3:8000")

	// Record which nodes keys map to
	keyMappings := make(map[string]string)
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("session-%d", i)
		keyMappings[key] = ring.Get(key)
	}

	// Add a new node
	ring.Add("10.0.0.4:8000")

	// Most keys should still map to the same nodes
	unchanged := 0
	for key, originalNode := range keyMappings {
		if ring.Get(key) == originalNode {
			unchanged++
		}
	}

	// At least 60% should be unchanged (with 4 nodes, ~25% might move)
	if unchanged < 60 {
		t.Errorf("too many keys remapped: only %d/100 unchanged", unchanged)
	}
}

func TestHashRing_GetN(t *testing.T) {
	ring := NewHashRing(100)
	ring.Add("10.0.0.1:8000")
	ring.Add("10.0.0.2:8000")
	ring.Add("10.0.0.3:8000")

	// Get 2 nodes for failover
	nodes := ring.GetN("test-key", 2)
	if len(nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes))
	}

	// Nodes should be distinct
	if nodes[0] == nodes[1] {
		t.Errorf("GetN returned duplicate nodes: %v", nodes)
	}

	// Get more nodes than available
	nodes = ring.GetN("test-key", 10)
	if len(nodes) != 3 {
		t.Errorf("expected 3 nodes (all available), got %d", len(nodes))
	}
}

func TestHashRing_SetNodes(t *testing.T) {
	ring := NewHashRing(100)
	ring.Add("10.0.0.1:8000")
	ring.Add("10.0.0.2:8000")

	// Replace with new set
	ring.SetNodes([]string{"10.0.0.3:8000", "10.0.0.4:8000", "10.0.0.5:8000"})

	if ring.Size() != 3 {
		t.Errorf("expected 3 nodes after SetNodes, got %d", ring.Size())
	}

	nodes := ring.Nodes()
	nodeSet := make(map[string]bool)
	for _, n := range nodes {
		nodeSet[n] = true
	}

	if nodeSet["10.0.0.1:8000"] || nodeSet["10.0.0.2:8000"] {
		t.Error("old nodes should have been removed")
	}

	if !nodeSet["10.0.0.3:8000"] || !nodeSet["10.0.0.4:8000"] || !nodeSet["10.0.0.5:8000"] {
		t.Error("new nodes should have been added")
	}
}

func TestHashRing_Distribution(t *testing.T) {
	ring := NewHashRing(150)
	nodes := []string{
		"10.0.0.1:8000",
		"10.0.0.2:8000",
		"10.0.0.3:8000",
		"10.0.0.4:8000",
	}

	for _, node := range nodes {
		ring.Add(node)
	}

	// Count distribution across 10000 keys
	counts := make(map[string]int)
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		node := ring.Get(key)
		counts[node]++
	}

	// Each node should get roughly 25% (allow 25% variance for consistent hashing)
	// Consistent hashing trades perfect distribution for minimal disruption on node changes
	expectedPer := 10000 / len(nodes)
	tolerance := expectedPer * 25 / 100 // 25%

	for node, count := range counts {
		if count < expectedPer-tolerance || count > expectedPer+tolerance {
			t.Errorf("node %s has %d keys (expected ~%d +/- %d)", node, count, expectedPer, tolerance)
		}
	}
}

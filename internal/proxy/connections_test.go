package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Pod-level connection tracking tests

func TestPodConnectionTracking(t *testing.T) {
	p := setupTestProxy(t)

	// Initially zero
	assert.Equal(t, int64(0), p.getPodConnections("10.42.0.5:8000"))

	// Increment
	p.incrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(1), p.getPodConnections("10.42.0.5:8000"))

	p.incrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(2), p.getPodConnections("10.42.0.5:8000"))

	// Decrement
	p.decrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(1), p.getPodConnections("10.42.0.5:8000"))

	p.decrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(0), p.getPodConnections("10.42.0.5:8000"))
}

func TestPodConnectionTracking_MultiplePods(t *testing.T) {
	p := setupTestProxy(t)

	podA := "10.42.0.10:8000"
	podB := "10.42.0.11:8000"

	p.incrementPodConnections(podA)
	p.incrementPodConnections(podA)
	p.incrementPodConnections(podB)

	assert.Equal(t, int64(2), p.getPodConnections(podA))
	assert.Equal(t, int64(1), p.getPodConnections(podB))

	// Decrementing one pod does not affect the other
	p.decrementPodConnections(podA)
	assert.Equal(t, int64(1), p.getPodConnections(podA))
	assert.Equal(t, int64(1), p.getPodConnections(podB))
}

func TestPodConnections_DecrementNonExistent(t *testing.T) {
	p := setupTestProxy(t)

	// Decrement a pod that was never tracked -- must not panic
	p.decrementPodConnections("10.42.99.99:8000")

	// Count should still be zero (no entry created)
	assert.Equal(t, int64(0), p.getPodConnections("10.42.99.99:8000"))
}

func TestGetPodConnections_UnknownPod(t *testing.T) {
	p := setupTestProxy(t)

	// A pod address that was never touched should return 0
	assert.Equal(t, int64(0), p.getPodConnections("192.168.1.1:11434"))
}

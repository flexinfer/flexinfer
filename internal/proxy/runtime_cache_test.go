package proxy

import (
	"context"
	"testing"
	"time"

	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newTestRuntimeCache builds a RuntimeCache backed by a fake k8s client seeded
// with the supplied objects. Callers pass Pods, Nodes, or any client.Object.
func newTestRuntimeCache(t *testing.T, ttl time.Duration, objects ...client.Object) *RuntimeCache {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()

	return NewRuntimeCache(k8sClient, "default", ttl)
}

// makePod creates a pod with the runtime component label, the given phase, and
// optional PodReady condition.
func makePod(name, ip, nodeName string, phase corev1.PodPhase, ready *bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/component": runtimeComponentLabel,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
		Status: corev1.PodStatus{
			Phase: phase,
			PodIP: ip,
		},
	}
	if ready != nil {
		status := corev1.ConditionFalse
		if *ready {
			status = corev1.ConditionTrue
		}
		pod.Status.Conditions = []corev1.PodCondition{
			{Type: corev1.PodReady, Status: status},
		}
	}
	return pod
}

func makeNode(name string, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

func boolPtr(v bool) *bool { return &v }

// ---------------------------------------------------------------------------
// 1. TestNewRuntimeCache
// ---------------------------------------------------------------------------

func TestNewRuntimeCache(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	rc := NewRuntimeCache(k8sClient, "ai-system", 30*time.Second)

	assert.Equal(t, "ai-system", rc.namespace)
	assert.Equal(t, 30*time.Second, rc.ttl)
	assert.NotNil(t, rc.client)
	assert.Nil(t, rc.endpoints)
	assert.True(t, rc.lastFetch.IsZero(), "lastFetch should be zero-value on creation")
}

// ---------------------------------------------------------------------------
// 2. TestRuntimeCache_Refresh_DiscoversPods
// ---------------------------------------------------------------------------

func TestRuntimeCache_Refresh_DiscoversPods(t *testing.T) {
	readyTrue := boolPtr(true)
	pod1 := makePod("rt-pod-1", "10.0.0.1", "node-a", corev1.PodRunning, readyTrue)
	pod2 := makePod("rt-pod-2", "10.0.0.2", "node-b", corev1.PodRunning, readyTrue)

	rc := newTestRuntimeCache(t, time.Minute, pod1, pod2)
	require.NoError(t, rc.refresh(context.Background()))

	rc.mu.RLock()
	defer rc.mu.RUnlock()

	assert.Len(t, rc.endpoints, 2)

	names := map[string]bool{}
	for _, ep := range rc.endpoints {
		names[ep.PodName] = true
		assert.Equal(t, pkgrt.RuntimeAPIPort, ep.Port)
		assert.True(t, ep.Ready)
	}
	assert.True(t, names["rt-pod-1"])
	assert.True(t, names["rt-pod-2"])
}

// ---------------------------------------------------------------------------
// 3. TestRuntimeCache_Refresh_SkipsNonRunning
// ---------------------------------------------------------------------------

func TestRuntimeCache_Refresh_SkipsNonRunning(t *testing.T) {
	readyTrue := boolPtr(true)
	runningPod := makePod("running-pod", "10.0.0.1", "node-a", corev1.PodRunning, readyTrue)
	pendingPod := makePod("pending-pod", "10.0.0.2", "node-b", corev1.PodPending, readyTrue)
	failedPod := makePod("failed-pod", "10.0.0.3", "node-c", corev1.PodFailed, readyTrue)

	rc := newTestRuntimeCache(t, time.Minute, runningPod, pendingPod, failedPod)
	require.NoError(t, rc.refresh(context.Background()))

	rc.mu.RLock()
	defer rc.mu.RUnlock()

	require.Len(t, rc.endpoints, 1)
	assert.Equal(t, "running-pod", rc.endpoints[0].PodName)
}

// ---------------------------------------------------------------------------
// 4. TestRuntimeCache_Refresh_PodReadyCondition
// ---------------------------------------------------------------------------

func TestRuntimeCache_Refresh_PodReadyCondition(t *testing.T) {
	readyPod := makePod("ready-pod", "10.0.0.1", "node-a", corev1.PodRunning, boolPtr(true))
	notReadyPod := makePod("not-ready-pod", "10.0.0.2", "node-b", corev1.PodRunning, boolPtr(false))

	rc := newTestRuntimeCache(t, time.Minute, readyPod, notReadyPod)
	require.NoError(t, rc.refresh(context.Background()))

	rc.mu.RLock()
	defer rc.mu.RUnlock()

	require.Len(t, rc.endpoints, 2)

	epMap := map[string]*pkgrt.RuntimeEndpoint{}
	for _, ep := range rc.endpoints {
		epMap[ep.PodName] = ep
	}
	assert.True(t, epMap["ready-pod"].Ready)
	assert.False(t, epMap["not-ready-pod"].Ready)
}

// ---------------------------------------------------------------------------
// 5. TestRuntimeCache_ForModel_MatchingNodeSelector
// ---------------------------------------------------------------------------

func TestRuntimeCache_ForModel_MatchingNodeSelector(t *testing.T) {
	pod := makePod("gpu-pod", "10.0.0.1", "gpu-node", corev1.PodRunning, boolPtr(true))
	node := makeNode("gpu-node", map[string]string{
		"gpu.vendor": "amd",
		"gpu.arch":   "gfx1100",
	})

	rc := newTestRuntimeCache(t, time.Minute, pod, node)

	ep, err := rc.ForModel(context.Background(), map[string]string{
		"gpu.vendor": "amd",
		"gpu.arch":   "gfx1100",
	})
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, "gpu-pod", ep.PodName)
	assert.Equal(t, "10.0.0.1", ep.PodIP)
	assert.Equal(t, pkgrt.RuntimeAPIPort, ep.Port)
}

// ---------------------------------------------------------------------------
// 6. TestRuntimeCache_ForModel_NoMatch
// ---------------------------------------------------------------------------

func TestRuntimeCache_ForModel_NoMatch(t *testing.T) {
	pod := makePod("cpu-pod", "10.0.0.1", "cpu-node", corev1.PodRunning, boolPtr(true))
	node := makeNode("cpu-node", map[string]string{
		"gpu.vendor": "none",
	})

	rc := newTestRuntimeCache(t, time.Minute, pod, node)

	ep, err := rc.ForModel(context.Background(), map[string]string{
		"gpu.vendor": "amd",
	})
	require.NoError(t, err)
	assert.Nil(t, ep, "should return nil when node labels do not match selector")
}

// ---------------------------------------------------------------------------
// 7. TestRuntimeCache_ForModel_EmptySelector
// ---------------------------------------------------------------------------

func TestRuntimeCache_ForModel_EmptySelector(t *testing.T) {
	pod := makePod("any-pod", "10.0.0.1", "any-node", corev1.PodRunning, boolPtr(true))
	// Node not needed since empty selector matches any pod.

	rc := newTestRuntimeCache(t, time.Minute, pod)

	ep, err := rc.ForModel(context.Background(), map[string]string{})
	require.NoError(t, err)
	require.NotNil(t, ep)
	assert.Equal(t, "any-pod", ep.PodName)
}

// ---------------------------------------------------------------------------
// 8. TestRuntimeCache_ForModel_SkipsNotReady
// ---------------------------------------------------------------------------

func TestRuntimeCache_ForModel_SkipsNotReady(t *testing.T) {
	notReadyPod := makePod("not-ready", "10.0.0.1", "node-a", corev1.PodRunning, boolPtr(false))

	rc := newTestRuntimeCache(t, time.Minute, notReadyPod)

	ep, err := rc.ForModel(context.Background(), map[string]string{})
	require.NoError(t, err)
	assert.Nil(t, ep, "should skip pods that are not ready")
}

// ---------------------------------------------------------------------------
// 9. TestRuntimeCache_EnsureFresh_WithinTTL
// ---------------------------------------------------------------------------

func TestRuntimeCache_EnsureFresh_WithinTTL(t *testing.T) {
	pod := makePod("ttl-pod", "10.0.0.1", "node-a", corev1.PodRunning, boolPtr(true))
	rc := newTestRuntimeCache(t, 5*time.Minute, pod)

	// Manually seed the cache so ensureFresh considers it fresh.
	rc.mu.Lock()
	rc.lastFetch = time.Now()
	rc.endpoints = []*pkgrt.RuntimeEndpoint{
		{PodName: "stale-sentinel", PodIP: "1.2.3.4", Port: 9999, NodeName: "old", Ready: true},
	}
	rc.mu.Unlock()

	require.NoError(t, rc.ensureFresh(context.Background()))

	// The stale sentinel should still be there because no refresh occurred.
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	require.Len(t, rc.endpoints, 1)
	assert.Equal(t, "stale-sentinel", rc.endpoints[0].PodName)
}

// ---------------------------------------------------------------------------
// 10. TestRuntimeCache_EnsureFresh_Stale
// ---------------------------------------------------------------------------

func TestRuntimeCache_EnsureFresh_Stale(t *testing.T) {
	pod := makePod("fresh-pod", "10.0.0.5", "node-x", corev1.PodRunning, boolPtr(true))
	rc := newTestRuntimeCache(t, 1*time.Second, pod)

	// Seed stale data with lastFetch in the past.
	rc.mu.Lock()
	rc.lastFetch = time.Now().Add(-10 * time.Second) // well beyond 1s TTL
	rc.endpoints = []*pkgrt.RuntimeEndpoint{
		{PodName: "old-sentinel", PodIP: "0.0.0.0", Port: 1, NodeName: "gone", Ready: false},
	}
	rc.mu.Unlock()

	require.NoError(t, rc.ensureFresh(context.Background()))

	// Cache should now contain the fresh pod from the fake client.
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	require.Len(t, rc.endpoints, 1)
	assert.Equal(t, "fresh-pod", rc.endpoints[0].PodName)
	assert.Equal(t, "10.0.0.5", rc.endpoints[0].PodIP)
}

// ---------------------------------------------------------------------------
// 11. TestIsPodReadyFromConditions_Ready
// ---------------------------------------------------------------------------

func TestIsPodReadyFromConditions_Ready(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.True(t, isPodReadyFromConditions(pod))
}

// ---------------------------------------------------------------------------
// 12. TestIsPodReadyFromConditions_NotReady
// ---------------------------------------------------------------------------

func TestIsPodReadyFromConditions_NotReady(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionFalse},
			},
		},
	}
	assert.False(t, isPodReadyFromConditions(pod))
}

// ---------------------------------------------------------------------------
// 13. TestIsPodReadyFromConditions_NoPodReadyCond
// ---------------------------------------------------------------------------

func TestIsPodReadyFromConditions_NoPodReadyCond(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
				{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.False(t, isPodReadyFromConditions(pod))
}

// ---------------------------------------------------------------------------
// 14. TestNodeMatches_EmptySelector
// ---------------------------------------------------------------------------

func TestNodeMatches_EmptySelector(t *testing.T) {
	rc := newTestRuntimeCache(t, time.Minute)
	assert.True(t, rc.nodeMatches(context.Background(), "any-node", map[string]string{}))
}

// ---------------------------------------------------------------------------
// 15. TestNodeMatches_MismatchedLabels
// ---------------------------------------------------------------------------

func TestNodeMatches_MismatchedLabels(t *testing.T) {
	node := makeNode("label-node", map[string]string{
		"gpu.vendor": "nvidia",
	})
	rc := newTestRuntimeCache(t, time.Minute, node)

	result := rc.nodeMatches(context.Background(), "label-node", map[string]string{
		"gpu.vendor": "amd",
	})
	assert.False(t, result)
}

// ---------------------------------------------------------------------------
// 16. TestNodeMatches_AllLabelsMatch
// ---------------------------------------------------------------------------

func TestNodeMatches_AllLabelsMatch(t *testing.T) {
	node := makeNode("multi-label-node", map[string]string{
		"gpu.vendor": "amd",
		"gpu.arch":   "gfx1100",
		"zone":       "us-west",
	})
	rc := newTestRuntimeCache(t, time.Minute, node)

	result := rc.nodeMatches(context.Background(), "multi-label-node", map[string]string{
		"gpu.vendor": "amd",
		"gpu.arch":   "gfx1100",
	})
	assert.True(t, result)
}

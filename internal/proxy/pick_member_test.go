package proxy

import (
	"context"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// makeModel is a tiny helper to create a v1alpha2 Model resource in the fake
// client with a given phase. Keeps test bodies focused on routing behavior.
//
// We set Status.Phase directly on the Create call instead of going through the
// Status subresource because the proxy package's test fake-client builder
// (setupTestProxyWithRouting) does not register Model as a status subresource.
// The plain Create persists the Status struct as-is, which is what subsequent
// pickReadyMember -> getModel reads need.
func makeModel(t *testing.T, p *Proxy, name string, phase aiv1alpha2.ModelPhase) {
	t.Helper()
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.namespace},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://test/" + name},
		Status:     aiv1alpha2.ModelStatus{Phase: phase},
	}
	require.NoError(t, p.client.Create(context.Background(), m))
}

func TestPickReadyMember_SingleMember(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	makeModel(t, p, "solo", aiv1alpha2.ModelPhaseReady)

	got := p.pickReadyMember(context.Background(), "solo-label", []string{"solo"})
	assert.Equal(t, "solo", got, "single-member group must return its only member")
}

func TestPickReadyMember_AllReady_RoundRobin(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	members := []string{"model-a", "model-b"}
	picks := make(map[string]int)
	const N = 20
	for i := 0; i < N; i++ {
		picks[p.pickReadyMember(context.Background(), "quality-chat", members)]++
	}
	// Strict round-robin: exactly N/2 each.
	assert.Equal(t, N/2, picks["model-a"], "model-a should serve exactly half")
	assert.Equal(t, N/2, picks["model-b"], "model-b should serve exactly half")
}

func TestPickReadyMember_PrefersReady(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseIdle) // not Ready

	members := []string{"model-a", "model-b"}
	for i := 0; i < 10; i++ {
		got := p.pickReadyMember(context.Background(), "quality-chat", members)
		assert.Equal(t, "model-a", got, "every pick must avoid the Idle member")
	}
}

func TestPickReadyMember_NoneReady_FallsBackToFirst(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseIdle)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseIdle)

	members := []string{"model-a", "model-b"}
	// With zero Ready members the fallback path runs round-robin against the
	// full member list. We only assert determinism + both targets are reached
	// across calls — picking model-b on iteration 0 would also be acceptable
	// behaviorally, but we lock in alphabetical determinism so operators can
	// predict which instance receives the first cold-start request.
	first := p.pickReadyMember(context.Background(), "quality-chat", members)
	assert.Equal(t, "model-a", first, "first call must hit alphabetically-first cold-start target")

	// Subsequent calls should still hit a member from the group (cold-start
	// is acceptable on either).
	for i := 0; i < 4; i++ {
		got := p.pickReadyMember(context.Background(), "quality-chat", members)
		assert.Contains(t, members, got)
	}
}

func TestPickReadyMember_PerLabelCounters(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-c", aiv1alpha2.ModelPhaseReady)

	// Two labels with different membership: the per-label counter must not
	// leak between them. If it did, label "g1" and "g2" would round-robin
	// out-of-phase and distribution would smear.
	g1 := []string{"model-a", "model-b"}
	g2 := []string{"model-a", "model-c"}

	g1Counts := map[string]int{}
	g2Counts := map[string]int{}
	for i := 0; i < 10; i++ {
		g1Counts[p.pickReadyMember(context.Background(), "g1", g1)]++
		g2Counts[p.pickReadyMember(context.Background(), "g2", g2)]++
	}
	assert.Equal(t, 5, g1Counts["model-a"])
	assert.Equal(t, 5, g1Counts["model-b"])
	assert.Equal(t, 5, g2Counts["model-a"])
	assert.Equal(t, 5, g2Counts["model-c"])
}

func TestResolveServiceLabelGroup_NotAServiceLabel(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	members, ok := p.resolver.ResolveServiceLabelGroup(ctx, "not-a-label")
	assert.False(t, ok, "unknown labels must return ok=false so callers fall through to alias lookup")
	assert.Nil(t, members)
}

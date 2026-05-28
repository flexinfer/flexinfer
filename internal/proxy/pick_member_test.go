package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// chatBodyWithSystem builds a minimal /v1/chat/completions body with a system
// message of the given content. Used to drive routing.ExtractPrefixKey from
// tests without constructing a full OpenAI payload by hand.
func chatBodyWithSystem(model, system, user string) []byte {
	return []byte(fmt.Sprintf(
		`{"model":%q,"messages":[{"role":"system","content":%q},{"role":"user","content":%q}]}`,
		model, system, user,
	))
}

// newChatReq builds an *http.Request whose Body wraps the given JSON payload
// so routing.ExtractPrefixKey / ExtractSessionKey can read it.
func newChatReq(body []byte) *http.Request {
	r, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

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

// pickReadyMemberRouted tests (F4-proxy-prefix-pinning). The routed entry
// point honors the configured labelGroupRouting mode. Default (empty) must
// be byte-for-byte identical to pickReadyMember; non-default modes try
// consistent-hash pinning and fall back to RR when no key is extractable.

func TestPickReadyMemberRouted_DefaultRR_MatchesPickReadyMember(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	// labelGroupRouting left empty -> default round-robin path.
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	members := []string{"model-a", "model-b"}
	picks := make(map[string]int)
	const N = 20
	for i := 0; i < N; i++ {
		picks[p.pickReadyMemberRouted(context.Background(), "quality-chat", members, nil, nil)]++
	}
	assert.Equal(t, N/2, picks["model-a"], "default-RR mode should distribute exactly half to model-a")
	assert.Equal(t, N/2, picks["model-b"], "default-RR mode should distribute exactly half to model-b")
}

func TestPickReadyMemberRouted_PrefixOrRR_SamePrefixSameModel(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingPrefixOrRR
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	members := []string{"model-a", "model-b"}
	body := chatBodyWithSystem("quality-chat", "You are a senior K8s operator reviewing a long postmortem.", "Q1?")
	req := newChatReq(body)

	first := p.pickReadyMemberRouted(context.Background(), "quality-chat", members, req, body)
	for i := 0; i < 9; i++ {
		got := p.pickReadyMemberRouted(context.Background(), "quality-chat", members, req, body)
		assert.Equal(t, first, got, "same prefix key must pin to the same model on every call")
	}
}

func TestPickReadyMemberRouted_PrefixOrRR_DifferentPrefixesDistributed(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingPrefixOrRR
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	members := []string{"model-a", "model-b"}
	picks := make(map[string]int)
	const N = 50
	for i := 0; i < N; i++ {
		sys := fmt.Sprintf("system prompt variant %d — distinct seed material to vary the hash", i)
		body := chatBodyWithSystem("quality-chat", sys, "hi")
		req := newChatReq(body)
		picks[p.pickReadyMemberRouted(context.Background(), "quality-chat", members, req, body)]++
	}
	assert.Greater(t, picks["model-a"], 0, "50 distinct prefixes should hit model-a at least once")
	assert.Greater(t, picks["model-b"], 0, "50 distinct prefixes should hit model-b at least once")
}

func TestPickReadyMemberRouted_PrefixOrRR_NoKey_FallsBackToRR(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingPrefixOrRR
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	members := []string{"model-a", "model-b"}
	// No request body — ExtractPrefixKey returns "" so we fall through to RR.
	picks := make(map[string]int)
	const N = 20
	for i := 0; i < N; i++ {
		picks[p.pickReadyMemberRouted(context.Background(), "quality-chat", members, nil, nil)]++
	}
	assert.Equal(t, N/2, picks["model-a"], "no-key fallback must use RR — half to model-a")
	assert.Equal(t, N/2, picks["model-b"], "no-key fallback must use RR — half to model-b")
}

func TestPickReadyMemberRouted_PrefixOrRR_SingleMember_ShortCircuits(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingPrefixOrRR
	// No Model resources created intentionally — single-member groups must
	// short-circuit BEFORE the Phase lookup so they stay zero-overhead even
	// when the Model CR is not yet present.
	got := p.pickReadyMemberRouted(context.Background(), "solo-label", []string{"solo"}, nil, nil)
	assert.Equal(t, "solo", got)
}

func TestPickReadyMemberRouted_PrefixOrRR_NoneReady_FallsBackToFirst(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingPrefixOrRR
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseIdle)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseIdle)

	body := chatBodyWithSystem("quality-chat", "any system", "hi")
	req := newChatReq(body)
	// Even with a perfectly good prefix key, zero Ready candidates must take
	// the deterministic cold-start path (alphabetically first member).
	got := p.pickReadyMemberRouted(context.Background(), "quality-chat", []string{"model-a", "model-b"}, req, body)
	assert.Equal(t, "model-a", got, "fallback_no_ready must hit alphabetically-first cold-start target")
}

func TestPickReadyMemberRouted_PrefixOrRR_PrefersReady(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingPrefixOrRR
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseIdle) // not Ready

	members := []string{"model-a", "model-b"}
	body := chatBodyWithSystem("quality-chat", "system context for the hash", "hi")
	req := newChatReq(body)
	// Any prefix key must never route to the Idle member, even if the hash
	// would have selected it among all members. Only model-a is Ready, so
	// the routed path short-circuits with fallback_single -> model-a.
	for i := 0; i < 10; i++ {
		got := p.pickReadyMemberRouted(context.Background(), "quality-chat", members, req, body)
		assert.Equal(t, "model-a", got)
	}
}

func TestPickReadyMemberRouted_SessionOrRR_UsesSessionHeader(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingSessionOrRR
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	members := []string{"model-a", "model-b"}
	req := newChatReq([]byte(`{}`))
	req.Header.Set("X-Session-ID", "user-42-conversation-3")

	first := p.pickReadyMemberRouted(context.Background(), "quality-chat", members, req, nil)
	for i := 0; i < 9; i++ {
		got := p.pickReadyMemberRouted(context.Background(), "quality-chat", members, req, nil)
		assert.Equal(t, first, got, "same session header must pin to the same model")
	}
}

func TestConsistentHashPick_Stable(t *testing.T) {
	members := []string{"model-b", "model-a", "model-c"}
	first := consistentHashPick("session-key-xyz", members)
	for i := 0; i < 50; i++ {
		got := consistentHashPick("session-key-xyz", members)
		assert.Equal(t, first, got, "same key + same set must always return same target")
	}
}

func TestConsistentHashPick_DistributesKeys(t *testing.T) {
	members := []string{"model-a", "model-b"}
	hits := make(map[string]int)
	for i := 0; i < 200; i++ {
		hits[consistentHashPick(fmt.Sprintf("k%d", i), members)]++
	}
	assert.Greater(t, hits["model-a"], 0)
	assert.Greater(t, hits["model-b"], 0)
}

func TestConsistentHashPick_EdgeCases(t *testing.T) {
	assert.Equal(t, "", consistentHashPick("k", nil), "empty members returns empty")
	assert.Equal(t, "solo", consistentHashPick("k", []string{"solo"}), "single member short-circuits")
}

func TestIsValidLabelGroupRoutingMode(t *testing.T) {
	valid := []string{"", "round-robin", "prefix-or-rr", "session-or-rr", "prefix-session-or-rr"}
	for _, mode := range valid {
		assert.True(t, isValidLabelGroupRoutingMode(mode), "must accept %q", mode)
	}
	invalid := []string{"prefix", "session", "rr", "PREFIX-OR-RR", "true"}
	for _, mode := range invalid {
		assert.False(t, isValidLabelGroupRoutingMode(mode), "must reject %q", mode)
	}
}

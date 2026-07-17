package proxy

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// setReservationLedger installs a reservation ledger with an explicit TTL for
// the given proxy. A ttl of 0 disables reservations, reproducing the
// pre-reservation least-loaded behavior; a positive ttl enables burst-spread.
// Ledgers are lazily constructed on first use, so tests must call this before
// the first pick to keep the auto-constructed (env-driven) ledger from winning.
func setReservationLedger(t *testing.T, p *Proxy, ttl time.Duration) *reservationLedger {
	t.Helper()
	l := newReservationLedgerWithTTL(ttl)
	p.reservationLedger = l
	return l
}

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

func TestPickReadyMemberRouted_LeastLoaded_AvoidsBusyMember(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingLeastLoaded
	// Disable reservations: this test asserts the base active-connection
	// avoidance behavior. With reservations enabled the idle member's phantom
	// load would climb until it ties model-a's one real connection, so a strict
	// "every pick avoids the busy member" assertion only holds for the
	// pre-reservation algorithm.
	setReservationLedger(t, p, 0)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)
	p.incrementConnections("model-a")
	t.Cleanup(func() { p.decrementConnections("model-a") })

	for i := 0; i < 10; i++ {
		got := p.pickReadyMemberRouted(context.Background(), "quality-chat", []string{"model-a", "model-b"}, nil, nil)
		assert.Equal(t, "model-b", got)
	}
}

func TestPickReadyMemberRouted_LeastLoaded_RoundRobinsTies(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingLeastLoaded
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	picks := map[string]int{}
	for i := 0; i < 20; i++ {
		picks[p.pickReadyMemberRouted(context.Background(), "quality-chat", []string{"model-a", "model-b"}, nil, nil)]++
	}
	assert.Equal(t, 10, picks["model-a"])
	assert.Equal(t, 10, picks["model-b"])
}

func TestPickReadyMemberRouted_LeastLoaded_OnlyConsidersReadyMembers(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingLeastLoaded
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseIdle)
	p.incrementConnections("model-a")
	t.Cleanup(func() { p.decrementConnections("model-a") })

	got := p.pickReadyMemberRouted(context.Background(), "quality-chat", []string{"model-a", "model-b"}, nil, nil)
	assert.Equal(t, "model-a", got, "an idle Model is not a serving candidate even when its connection count is lower")
}

// TestPickLeastLoaded_BurstReservationsSplitEvenly is the core burst-safety
// case: two Ready members with equal (zero) active connections receive four
// sequential picks with NO connection registration in between. Reservations
// make each pick raise the picked member's observed load, so the burst splits
// 2/2 instead of piling onto whichever member the tie-break returned first.
func TestPickLeastLoaded_BurstReservationsSplitEvenly(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingLeastLoaded
	setReservationLedger(t, p, 10*time.Second)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	candidates := []string{"model-a", "model-b"}
	picks := map[string]int{}
	for i := 0; i < 4; i++ {
		// No incrementConnections between picks: the connection gauge stays 0
		// for both members, exactly the burst window the ledger guards.
		picks[p.pickLeastLoaded("quality-chat", candidates)]++
	}
	assert.Equal(t, 2, picks["model-a"], "burst must split evenly — model-a gets half")
	assert.Equal(t, 2, picks["model-b"], "burst must split evenly — model-b gets half")
}

// TestPickLeastLoaded_BurstSpreadsOffBusyMember is the discriminating case:
// model-a is idle, model-b already holds real connections. Without reservations
// every pick in the burst would read a<b and stampede model-a. Reservations let
// model-a's phantom load climb until it ties model-b, so the burst begins
// spilling onto model-b — model-b receives a non-zero share.
func TestPickLeastLoaded_BurstSpreadsOffBusyMember(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingLeastLoaded
	setReservationLedger(t, p, 10*time.Second)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	// model-b starts with 3 real in-flight connections.
	for i := 0; i < 3; i++ {
		p.incrementConnections("model-b")
	}
	t.Cleanup(func() {
		for i := 0; i < 3; i++ {
			p.decrementConnections("model-b")
		}
	})

	candidates := []string{"model-a", "model-b"}
	picks := map[string]int{}
	const N = 6
	for i := 0; i < N; i++ {
		picks[p.pickLeastLoaded("quality-chat", candidates)]++
	}
	assert.Greater(t, picks["model-b"], 0,
		"reservations must spill part of the burst onto the busy member once phantom load ties it")
	assert.Greater(t, picks["model-a"], picks["model-b"],
		"the idle member still receives the larger share")
	assert.Equal(t, N, picks["model-a"]+picks["model-b"], "all picks land on a candidate")
}

// TestPickLeastLoaded_DisabledReproducesLegacyBehavior confirms TTL 0 turns the
// ledger off: the burst-off-busy-member scenario stampedes the idle member
// exactly as the pre-reservation algorithm did.
func TestPickLeastLoaded_DisabledReproducesLegacyBehavior(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingLeastLoaded
	setReservationLedger(t, p, 0) // disabled
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	for i := 0; i < 3; i++ {
		p.incrementConnections("model-b")
	}
	t.Cleanup(func() {
		for i := 0; i < 3; i++ {
			p.decrementConnections("model-b")
		}
	})

	candidates := []string{"model-a", "model-b"}
	picks := map[string]int{}
	const N = 6
	for i := 0; i < N; i++ {
		picks[p.pickLeastLoaded("quality-chat", candidates)]++
	}
	assert.Equal(t, N, picks["model-a"], "disabled ledger: every burst pick goes to the least-loaded member")
	assert.Equal(t, 0, picks["model-b"], "disabled ledger: no reservation spill onto the busy member")
}

// TestPickLeastLoaded_IncrementConnectionsConsumesReservation verifies the
// hand-off from reservation to real connection: a pick reserves, the ensuing
// incrementConnections consumes it, and a redundant increment does not underflow.
func TestPickLeastLoaded_IncrementConnectionsConsumesReservation(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	p.labelGroupRouting = labelGroupRoutingLeastLoaded
	ledger := setReservationLedger(t, p, 10*time.Second)
	makeModel(t, p, "model-a", aiv1alpha2.ModelPhaseReady)
	makeModel(t, p, "model-b", aiv1alpha2.ModelPhaseReady)

	picked := p.pickLeastLoaded("quality-chat", []string{"model-a", "model-b"})
	require.Equal(t, 1, ledger.pending(picked), "a pick records exactly one reservation")

	p.incrementConnections(picked)
	t.Cleanup(func() { p.decrementConnections(picked) })
	assert.Equal(t, 0, ledger.pending(picked), "incrementConnections consumes the reservation")

	// A second connection for the same model finds no reservation to consume —
	// a cheap no-op, not an underflow.
	p.incrementConnections(picked)
	t.Cleanup(func() { p.decrementConnections(picked) })
	assert.Equal(t, 0, ledger.pending(picked), "double-consume is a no-op")
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
	valid := []string{"", "round-robin", "least-loaded", "prefix-or-rr", "session-or-rr", "prefix-session-or-rr"}
	for _, mode := range valid {
		assert.True(t, isValidLabelGroupRoutingMode(mode), "must accept %q", mode)
	}
	invalid := []string{"prefix", "session", "rr", "PREFIX-OR-RR", "true"}
	for _, mode := range invalid {
		assert.False(t, isValidLabelGroupRoutingMode(mode), "must reject %q", mode)
	}
}

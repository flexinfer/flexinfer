package weaver

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// stubRouter implements AutoComposeRouter for tests. It records the Query
// invocation so tests can assert on dispatched domains without reaching
// FlexInfer or the orchestrator.
type stubRouter struct {
	reg        *DomainRegistry
	lastReq    QueryRequest
	queryCalls int
	response   QueryResult
	err        error
}

func newStubRouter(domains []SubAgent) *stubRouter {
	reg := NewDomainRegistry()
	for _, d := range domains {
		reg.Register(d)
	}
	return &stubRouter{reg: reg}
}

func (s *stubRouter) Registry() *DomainRegistry { return s.reg }

func (s *stubRouter) Query(_ context.Context, req QueryRequest) (QueryResult, error) {
	s.queryCalls++
	s.lastReq = req
	if s.err != nil {
		return QueryResult{}, s.err
	}
	// Simulate a successful dispatch: echo back DomainsUsed so callers can
	// inspect which domains were picked.
	result := s.response
	if len(result.DomainsUsed) == 0 {
		result.DomainsUsed = append([]string(nil), req.Domains...)
	}
	return result, nil
}

// readDomains standardized for comparison regardless of order.
func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestAutoCompose_PicksByKeywordMatch(t *testing.T) {
	stub := newStubRouter([]SubAgent{
		{Name: "alpha", Description: "Handles alpha kubernetes cluster queries"},
		{Name: "beta", Description: "Handles beta logs and metrics"},
		{Name: "gamma", Description: "Completely unrelated topic"},
	})

	result, err := AutoCompose(context.Background(), stub, "how is the kubernetes cluster doing?", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.queryCalls != 1 {
		t.Fatalf("expected 1 query dispatch, got %d", stub.queryCalls)
	}
	got := sortedCopy(stub.lastReq.Domains)
	want := []string{"alpha"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected domains %v, got %v", want, got)
	}
	if !reflect.DeepEqual(sortedCopy(result.DomainsUsed), want) {
		t.Errorf("expected result.DomainsUsed %v, got %v", want, result.DomainsUsed)
	}
}

func TestAutoCompose_RefusesWriteDomains(t *testing.T) {
	stub := newStubRouter([]SubAgent{
		// A write domain with a description that would dominate scoring —
		// it must still be refused.
		{
			Name:        "danger",
			Description: "deploy kubernetes cluster kubernetes kubernetes",
			Write:       true,
		},
		{
			Name:        "safe",
			Description: "read-only kubernetes cluster status",
		},
	})

	_, err := AutoCompose(context.Background(), stub, "check the kubernetes cluster", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := stub.lastReq.Domains
	for _, name := range got {
		if name == "danger" {
			t.Fatalf("auto-compose must refuse write:true domains, got %v", got)
		}
	}
	if len(got) != 1 || got[0] != "safe" {
		t.Fatalf("expected [safe], got %v", got)
	}
}

func TestAutoCompose_CapsAtMaxDomains(t *testing.T) {
	stub := newStubRouter([]SubAgent{
		{Name: "d1", Description: "kubernetes cluster pods"},
		{Name: "d2", Description: "kubernetes cluster logs"},
		{Name: "d3", Description: "kubernetes cluster metrics"},
		{Name: "d4", Description: "kubernetes cluster alerts"},
		{Name: "d5", Description: "kubernetes cluster traces"},
	})

	_, err := AutoCompose(context.Background(), stub, "kubernetes cluster status", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.lastReq.Domains) != 2 {
		t.Fatalf("expected 2 domains (cap), got %d: %v", len(stub.lastReq.Domains), stub.lastReq.Domains)
	}
}

func TestAutoCompose_EmptyWhenNoDomainsMatch(t *testing.T) {
	stub := newStubRouter([]SubAgent{
		{Name: "d1", Description: "unrelated apple banana"},
		{Name: "d2", Description: "more unrelated content"},
	})

	result, err := AutoCompose(context.Background(), stub, "kubernetes cluster status", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.queryCalls != 0 {
		t.Errorf("expected no dispatch when no domains match, got %d calls", stub.queryCalls)
	}
	if len(result.DomainResults) != 0 || result.Answer != "" {
		t.Errorf("expected empty QueryResult, got %+v", result)
	}
}

func TestAutoCompose_NilRouterErrors(t *testing.T) {
	_, err := AutoCompose(context.Background(), nil, "query", 3)
	if err == nil {
		t.Fatal("expected error for nil router")
	}
}

func TestAutoCompose_DeterministicTieBreak(t *testing.T) {
	// Two domains tie on score; alphabetical order should win.
	stub := newStubRouter([]SubAgent{
		{Name: "zeta", Description: "kubernetes cluster"},
		{Name: "alpha", Description: "kubernetes cluster"},
	})

	_, err := AutoCompose(context.Background(), stub, "kubernetes cluster", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.lastReq.Domains) != 1 || stub.lastReq.Domains[0] != "alpha" {
		t.Fatalf("expected [alpha] via deterministic tie-break, got %v", stub.lastReq.Domains)
	}
}

func TestSelectDomains_TokenizationIgnoresShortTokens(t *testing.T) {
	// The query has short tokens like "of", "in", "on" that should be ignored.
	// Only "cluster" (>2 chars) should actually drive selection.
	domains := []SubAgent{
		{Name: "matching", Description: "cluster related topics"},
		{Name: "short", Description: "of in on to by"},
	}
	got := selectDomains(domains, "status of cluster in on", 3)
	want := []string{"matching"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestSelectDomains_EmptyInputs(t *testing.T) {
	// Empty query: no selection.
	got := selectDomains([]SubAgent{{Name: "x", Description: "anything"}}, "", 3)
	if len(got) != 0 {
		t.Errorf("expected no selection for empty query, got %v", got)
	}
	// Zero max: no selection.
	got = selectDomains([]SubAgent{{Name: "x", Description: "kubernetes"}}, "kubernetes", 0)
	if len(got) != 0 {
		t.Errorf("expected no selection with max=0, got %v", got)
	}
	// No domains: no selection.
	got = selectDomains(nil, "kubernetes", 3)
	if len(got) != 0 {
		t.Errorf("expected no selection with nil domains, got %v", got)
	}
}

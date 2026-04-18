package fleet

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestComputeEconomicsSnapshot_AllZero verifies that an EconomicsInputs with
// no data yields nil ratio values tagged "insufficient_data" so the UI
// renders "-" for every card.
func TestComputeEconomicsSnapshot_AllZero(t *testing.T) {
	now := time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	snap := ComputeEconomicsSnapshot(EconomicsInputs{}, "7d", now)

	if snap.Window != "7d" {
		t.Fatalf("expected window=7d, got %q", snap.Window)
	}
	if !snap.GeneratedAt.Equal(now) {
		t.Fatalf("expected generated_at=%v, got %v", now, snap.GeneratedAt)
	}
	if snap.Ratios == nil {
		t.Fatal("expected non-nil Ratios")
	}

	// Every ratio except tool_call_reduced (stubbed to 1.0) should be
	// insufficient_data with a nil Value.
	zeroChecks := []struct {
		name   string
		r      *Ratio
		status string
	}{
		{"token_savings", snap.Ratios.TokenSavings, "insufficient_data"},
		{"cost_ratio", snap.Ratios.CostRatio, "insufficient_data"},
		{"context_waste", snap.Ratios.ContextWaste, "insufficient_data"},
		{"compression", snap.Ratios.Compression, "insufficient_data"},
		// local_utilization should prefer the "weaver_metrics_unreachable"
		// status when the flag is false, which is its divide-by-zero guard
		// in the all-zero case.
		{"local_utilization", snap.Ratios.LocalUtilization, "weaver_metrics_unreachable"},
	}
	for _, c := range zeroChecks {
		if c.r == nil {
			t.Errorf("%s: ratio pointer is nil", c.name)
			continue
		}
		if c.r.Value != nil {
			t.Errorf("%s: expected nil Value, got %v", c.name, *c.r.Value)
		}
		if c.r.Status != c.status {
			t.Errorf("%s: expected status=%q, got %q", c.name, c.status, c.r.Status)
		}
	}

	// tool_call_reduced stubs to 1.0 when no weaver calls are observed.
	tcr := snap.Ratios.ToolCallReduced
	if tcr == nil || tcr.Value == nil {
		t.Fatal("tool_call_reduced: expected stubbed 1.0 value")
	}
	if *tcr.Value != 1.0 {
		t.Errorf("tool_call_reduced: expected 1.0, got %v", *tcr.Value)
	}
	if tcr.Status != "stub" {
		t.Errorf("tool_call_reduced: expected status=stub, got %q", tcr.Status)
	}

	// Token totals should be zero-valued (not nil) so the UI can still render
	// a 0/0 stacked bar without a branch on missing data.
	if snap.Tokens == nil {
		t.Fatal("expected non-nil Tokens")
	}
	if snap.Tokens.FrontierTokens != 0 || snap.Tokens.LocalTokens != 0 {
		t.Errorf("expected zero token totals, got %+v", snap.Tokens)
	}
}

// TestComputeEconomicsSnapshot_MixedInputs verifies the ratio math with a
// realistic fixture -- 1000 raw tool response tokens compressed to 200 etc.
func TestComputeEconomicsSnapshot_MixedInputs(t *testing.T) {
	in := EconomicsInputs{
		SpawnCount:             3,
		FrontierInputTokens:    10_000,
		FrontierOutputTokens:   2_000,
		FrontierCostUSD:        0.50,
		FrontierToolCalls:      40,
		WeaverToolCalls:        10,
		WeaverTokensTotal:      50_000,
		WeaverResponseTokens:   200,
		ToolResponseTokens:     1_000,
		WeaverMetricsReachable: true,
	}
	snap := ComputeEconomicsSnapshot(in, "7d", time.Now().UTC())

	const eps = 1e-9
	// token_savings = 1 - 200/1000 = 0.8
	assertRatio(t, "token_savings", snap.Ratios.TokenSavings, 0.8, eps, "ok")

	// tool_call_reduced = (40+10)/40 = 1.25
	assertRatio(t, "tool_call_reduced", snap.Ratios.ToolCallReduced, 1.25, eps, "ok")

	// cost_ratio: local_cost = 50000/1000 * 0.0001 = 0.005 USD
	// frontier_cost / (frontier_cost + local_cost) = 0.50 / 0.505
	assertRatio(t, "cost_ratio", snap.Ratios.CostRatio, 0.50/0.505, eps, "ok")

	// context_waste = 1000 / 10000 = 0.1
	assertRatio(t, "context_waste", snap.Ratios.ContextWaste, 0.1, eps, "ok")

	// compression = 1000 / 200 = 5.0
	assertRatio(t, "compression", snap.Ratios.Compression, 5.0, eps, "ok")

	// local_utilization = 50000 / (50000 + 10000 + 2000) = 50000/62000
	assertRatio(t, "local_utilization", snap.Ratios.LocalUtilization, 50000.0/62000.0, eps, "ok")

	// Tokens totals
	if snap.Tokens.FrontierTokens != 12_000 {
		t.Errorf("frontier_tokens: expected 12000, got %d", snap.Tokens.FrontierTokens)
	}
	if snap.Tokens.LocalTokens != 50_000 {
		t.Errorf("local_tokens: expected 50000, got %d", snap.Tokens.LocalTokens)
	}

	if snap.Inputs == nil {
		t.Fatal("expected non-nil Inputs")
	}
	if math.Abs(snap.Inputs.LocalCostUSD-0.005) > eps {
		t.Errorf("local_cost_usd: expected ~0.005, got %v", snap.Inputs.LocalCostUSD)
	}
}

// TestComputeEconomicsSnapshot_DivideByZeroGuards exercises every divide-by
// zero branch so a single missing counter cannot panic or return ±Inf.
func TestComputeEconomicsSnapshot_DivideByZeroGuards(t *testing.T) {
	cases := []struct {
		name string
		in   EconomicsInputs
		pick func(r *Ratios) *Ratio
	}{
		{
			name: "context_waste with zero frontier_input",
			in: EconomicsInputs{
				FrontierInputTokens:    0,
				ToolResponseTokens:     500,
				WeaverMetricsReachable: true,
			},
			pick: func(r *Ratios) *Ratio { return r.ContextWaste },
		},
		{
			name: "compression with zero weaver response",
			in: EconomicsInputs{
				ToolResponseTokens:     500,
				WeaverResponseTokens:   0,
				WeaverMetricsReachable: true,
			},
			pick: func(r *Ratios) *Ratio { return r.Compression },
		},
		{
			name: "cost_ratio with zero totals",
			in: EconomicsInputs{
				FrontierCostUSD:        0,
				WeaverTokensTotal:      0,
				WeaverMetricsReachable: true,
			},
			pick: func(r *Ratios) *Ratio { return r.CostRatio },
		},
		{
			name: "local_utilization with zero totals",
			in: EconomicsInputs{
				WeaverTokensTotal:      0,
				FrontierInputTokens:    0,
				FrontierOutputTokens:   0,
				WeaverMetricsReachable: true,
			},
			pick: func(r *Ratios) *Ratio { return r.LocalUtilization },
		},
	}

	for _, c := range cases {
		snap := ComputeEconomicsSnapshot(c.in, "7d", time.Now().UTC())
		r := c.pick(snap.Ratios)
		if r == nil {
			t.Errorf("%s: nil ratio pointer", c.name)
			continue
		}
		if r.Value != nil {
			t.Errorf("%s: expected nil value, got %v", c.name, *r.Value)
		}
		if r.Status != "insufficient_data" {
			t.Errorf("%s: expected status=insufficient_data, got %q", c.name, r.Status)
		}
	}
}

// TestEconomicsSnapshot_JSONShape ensures the serialised shape keeps the
// contract the Svelte panel depends on: window, tokens, ratios, generated_at.
// A null ratio value must round-trip as JSON null, not 0.
func TestEconomicsSnapshot_JSONShape(t *testing.T) {
	snap := ComputeEconomicsSnapshot(EconomicsInputs{}, "7d", time.Now().UTC())
	body, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"window", "tokens", "ratios", "generated_at", "inputs"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("missing top-level key %q; body=%s", key, body)
		}
	}

	ratios, ok := decoded["ratios"].(map[string]any)
	if !ok {
		t.Fatalf("ratios not an object; body=%s", body)
	}
	for _, key := range []string{
		"token_savings", "tool_call_reduced", "cost_ratio",
		"context_waste", "compression", "local_utilization",
	} {
		r, ok := ratios[key].(map[string]any)
		if !ok {
			t.Errorf("ratios.%s not an object; body=%s", key, body)
			continue
		}
		if _, ok := r["status"]; !ok {
			t.Errorf("ratios.%s missing status; body=%s", key, body)
		}
		if _, ok := r["value"]; !ok {
			t.Errorf("ratios.%s missing value key (should be null, not absent); body=%s", key, body)
		}
	}
}

// TestHandleEconomics_HTTPShape exercises the HTTP handler end-to-end with a
// stub Deps to confirm the wiring (auth, JSON serialisation, 200 OK). The
// actual ratio math is covered by the pure-function tests above.
func TestHandleEconomics_HTTPShape(t *testing.T) {
	d := New(&mockDeps{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/fleet/economics?window=7d", nil)
	d.handleEconomics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func assertRatio(t *testing.T, name string, r *Ratio, want, eps float64, status string) {
	t.Helper()
	if r == nil || r.Value == nil {
		t.Errorf("%s: nil value (status=%v)", name, r)
		return
	}
	if math.Abs(*r.Value-want) > eps {
		t.Errorf("%s: expected %v (±%v), got %v", name, want, eps, *r.Value)
	}
	if r.Status != status {
		t.Errorf("%s: expected status=%q, got %q", name, status, r.Status)
	}
}

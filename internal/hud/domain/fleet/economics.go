package fleet

import (
	"math"
	"net/http"
	"strings"
	"time"
)

// Slice D2 (F8) -- Token-economics dashboard.
//
// This handler derives six token-economics ratios from existing SpawnTelemetry
// counters and (stubbed) weaver metrics. No new telemetry is recorded here --
// the endpoint is pure aggregation + ratio computation.
//
// Source: .loom/87 §5.F8 and .loom/88 §5.D2. See also .loom/64 §5.1 for the
// canonical ratio definitions.

// EconomicsSnapshot is the shape returned by GET /api/fleet/economics.
// Exported for reuse by tests and downstream consumers.
type EconomicsSnapshot struct {
	Window      string        `json:"window"`
	Tokens      *TokenTotals  `json:"tokens,omitempty"`
	Ratios      *Ratios       `json:"ratios,omitempty"`
	GeneratedAt time.Time     `json:"generated_at"`
	Inputs      *InputSummary `json:"inputs,omitempty"`
}

// InputSummary records the raw counters that fed the ratio computation so the
// UI can render a stacked bar (frontier vs. local tokens) without recomputing.
type InputSummary struct {
	SpawnCount           int     `json:"spawn_count"`
	FrontierInputTokens  int     `json:"frontier_input_tokens"`
	FrontierOutputTokens int     `json:"frontier_output_tokens"`
	FrontierCostUSD      float64 `json:"frontier_cost_usd"`
	FrontierToolCalls    int     `json:"frontier_tool_calls"`
	WeaverToolCalls      int     `json:"weaver_tool_calls"`
	WeaverTokensTotal    int     `json:"weaver_tokens_total"`
	WeaverResponseTokens int     `json:"weaver_response_tokens"`
	ToolResponseTokens   int     `json:"tool_response_tokens"`
	LocalCostUSD         float64 `json:"local_cost_usd"`
}

// TokenTotals is the compact summary surfaced to the stacked-bar renderer.
type TokenTotals struct {
	FrontierTokens int `json:"frontier_tokens"` // input + output across spawns
	LocalTokens    int `json:"local_tokens"`    // weaver/on-prem
}

// Ratios carries the six product-spec F8 ratios. Any ratio whose inputs are
// missing or would divide by zero is serialised with "value": null and
// "status" set to a short reason code.
type Ratios struct {
	TokenSavings     *Ratio `json:"token_savings"`
	ToolCallReduced  *Ratio `json:"tool_call_reduced"`
	CostRatio        *Ratio `json:"cost_ratio"`
	ContextWaste     *Ratio `json:"context_waste"`
	Compression      *Ratio `json:"compression"`
	LocalUtilization *Ratio `json:"local_utilization"`
}

// Ratio is a single dimensionless ratio plus a status code. Value is a pointer
// so "insufficient_data" can be serialised as null rather than 0.
type Ratio struct {
	Value  *float64 `json:"value"`
	Status string   `json:"status"`
}

// Local-model cost-per-1K-tokens assumption. FlexInfer runs on-prem so
// marginal cost is power + amortised hardware. A flat 0.0001 USD/1K is the
// "cheap-model" rate cited in .loom/87 §5.F8 and .loom/88 §5.D2. If this
// needs to be configurable later, promote to a field on fleet.Deps.
const localCostPer1KTokens = 0.0001

// parseWindow converts a window query parameter ("7d", "24h", "30d") into a
// duration. Falls back to 7 days for unknown values so the UI always has a
// usable default. Returns the normalised label alongside the duration so the
// response echoes the value the handler actually applied.
func parseWindow(raw string) (time.Duration, string) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "24h", "1d":
		return 24 * time.Hour, raw
	case "30d":
		return 30 * 24 * time.Hour, raw
	case "", "7d":
		return 7 * 24 * time.Hour, "7d"
	}
	// Unknown window label -- default to 7 days but echo back what the caller
	// requested so the UI can surface the raw value.
	return 7 * 24 * time.Hour, "7d"
}

// handleEconomics serves GET /api/fleet/economics?window=7d.
//
// The handler is deliberately thin: it extracts the window, assembles a
// (currently zero-valued) EconomicsInputs from bridged telemetry if available,
// runs ComputeEconomicsSnapshot, and writes JSON. The pure-function split
// makes unit testing trivial -- see economics_test.go.
func (d *FleetDomain) handleEconomics(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	_, label := parseWindow(r.URL.Query().Get("window"))

	// NOTE: the fleet.Deps interface does not currently expose the spawn
	// orchestrator nor the weaver metrics client. Rather than expand Deps in
	// this slice (which would ripple through every adapter + mock), we accept
	// the degraded-but-honest behaviour: inputs are zero, ratios report
	// status="insufficient_data", and the UI renders "-" for every card.
	//
	// Follow-up D2.1 should thread a narrow SpawnSnapshotLister into Deps so
	// this endpoint can aggregate real frontier costs/tokens.
	inputs := EconomicsInputs{
		WeaverMetricsReachable: false,
	}

	snap := ComputeEconomicsSnapshot(inputs, label, time.Now().UTC())
	d.deps.WriteJSON(w, http.StatusOK, snap)
}

// EconomicsInputs is the pure-data bundle fed to ComputeEconomicsSnapshot.
// Keeping this separate from EconomicsSnapshot lets us unit-test the ratio
// math without wiring a real spawn orchestrator or weaver metrics client.
type EconomicsInputs struct {
	SpawnCount             int
	FrontierInputTokens    int
	FrontierOutputTokens   int
	FrontierCostUSD        float64
	FrontierToolCalls      int
	WeaverToolCalls        int
	WeaverTokensTotal      int
	WeaverResponseTokens   int
	ToolResponseTokens     int // raw tool response tokens before compression
	WeaverMetricsReachable bool
}

// ComputeEconomicsSnapshot is a pure function over EconomicsInputs. Exported
// for testing so we can assert the divide-by-zero guards and ratio math with
// fixture data.
func ComputeEconomicsSnapshot(in EconomicsInputs, windowLabel string, now time.Time) EconomicsSnapshot {
	localCost := (float64(in.WeaverTokensTotal) / 1000.0) * localCostPer1KTokens

	ratios := &Ratios{
		TokenSavings:     computeTokenSavings(in),
		ToolCallReduced:  computeToolCallReduction(in),
		CostRatio:        computeCostRatio(in, localCost),
		ContextWaste:     computeContextWaste(in),
		Compression:      computeCompression(in),
		LocalUtilization: computeLocalUtilization(in),
	}

	tokens := &TokenTotals{
		FrontierTokens: in.FrontierInputTokens + in.FrontierOutputTokens,
		LocalTokens:    in.WeaverTokensTotal,
	}

	return EconomicsSnapshot{
		Window:      windowLabel,
		Tokens:      tokens,
		Ratios:      ratios,
		GeneratedAt: now,
		Inputs: &InputSummary{
			SpawnCount:           in.SpawnCount,
			FrontierInputTokens:  in.FrontierInputTokens,
			FrontierOutputTokens: in.FrontierOutputTokens,
			FrontierCostUSD:      in.FrontierCostUSD,
			FrontierToolCalls:    in.FrontierToolCalls,
			WeaverToolCalls:      in.WeaverToolCalls,
			WeaverTokensTotal:    in.WeaverTokensTotal,
			WeaverResponseTokens: in.WeaverResponseTokens,
			ToolResponseTokens:   in.ToolResponseTokens,
			LocalCostUSD:         localCost,
		},
	}
}

// safeRatio returns a pointer to num/den, or nil with the provided status when
// den <= 0 or the result is not finite. Encapsulates the divide-by-zero guard
// so every ratio helper uses the same branch.
func safeRatio(num, den float64, zeroStatus string) *Ratio {
	if den <= 0 {
		return &Ratio{Value: nil, Status: zeroStatus}
	}
	v := num / den
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return &Ratio{Value: nil, Status: zeroStatus}
	}
	return &Ratio{Value: &v, Status: "ok"}
}

func computeTokenSavings(in EconomicsInputs) *Ratio {
	// 1 - (compressed_resp_tokens / raw_tool_response_tokens)
	if in.ToolResponseTokens <= 0 || in.WeaverResponseTokens <= 0 {
		return &Ratio{Value: nil, Status: "insufficient_data"}
	}
	v := 1.0 - float64(in.WeaverResponseTokens)/float64(in.ToolResponseTokens)
	return &Ratio{Value: &v, Status: "ok"}
}

func computeToolCallReduction(in EconomicsInputs) *Ratio {
	// frontier_tool_calls_before / frontier_tool_calls_after.
	// Without weaver adoption data we can only stub at 1.0 ("no reduction
	// observed") -- surface that as status=stub so the UI can grey out the
	// card without losing the placeholder.
	if in.WeaverToolCalls <= 0 {
		v := 1.0
		return &Ratio{Value: &v, Status: "stub"}
	}
	return safeRatio(
		float64(in.FrontierToolCalls+in.WeaverToolCalls),
		float64(in.FrontierToolCalls),
		"insufficient_data",
	)
}

func computeCostRatio(in EconomicsInputs, localCost float64) *Ratio {
	// frontier_cost / (frontier_cost + local_cost)
	total := in.FrontierCostUSD + localCost
	return safeRatio(in.FrontierCostUSD, total, "insufficient_data")
}

func computeContextWaste(in EconomicsInputs) *Ratio {
	// total_tool_response_tokens / total_frontier_input_tokens
	return safeRatio(
		float64(in.ToolResponseTokens),
		float64(in.FrontierInputTokens),
		"insufficient_data",
	)
}

func computeCompression(in EconomicsInputs) *Ratio {
	// raw_tool_response_tokens / weaver_compressed_response_tokens
	return safeRatio(
		float64(in.ToolResponseTokens),
		float64(in.WeaverResponseTokens),
		"insufficient_data",
	)
}

func computeLocalUtilization(in EconomicsInputs) *Ratio {
	// weaver_tokens / (weaver_tokens + frontier_input_tokens + frontier_output_tokens)
	if !in.WeaverMetricsReachable {
		return &Ratio{Value: nil, Status: "weaver_metrics_unreachable"}
	}
	total := float64(in.WeaverTokensTotal + in.FrontierInputTokens + in.FrontierOutputTokens)
	return safeRatio(float64(in.WeaverTokensTotal), total, "insufficient_data")
}

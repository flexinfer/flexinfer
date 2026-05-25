package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"time"
)

// ReportSchemaVersion identifies the on-disk format of the bench report.
// Bump this when you change the JSON shape in a backwards-incompatible way.
const ReportSchemaVersion = "flexinfer.spec_decode_bench.v1"

// SpeedupPassThreshold is the slice-1 kill-test gate: spec-decode must
// deliver at least this multiplier on p50 wall-clock tok/s vs baseline.
const SpeedupPassThreshold = 1.5

// ReportConfig echoes the CLI flags that produced a report so the artifact
// is self-describing. Keep keys aligned with the flag names.
type ReportConfig struct {
	CorpusPath         string  `json:"corpus_path,omitempty"`
	DraftN             int     `json:"draft_n"`
	MaxTokens          int     `json:"max_tokens"`
	MaxRounds          int     `json:"max_rounds"`
	Mode               string  `json:"mode"`
	Accept             string  `json:"accept"`
	Seed               int64   `json:"seed"`
	MockAcceptance     float64 `json:"mock_acceptance"`
	MockDecodeMsPerTok int     `json:"mock_decode_ms_per_token"`
	MockDraftMsPerTok  int     `json:"mock_draft_ms_per_token"`
	CorpusSize         int     `json:"corpus_size"`
}

// BaselineRunStats records a single baseline (no spec decode) run.
type BaselineRunStats struct {
	ElapsedSeconds  float64 `json:"elapsed_seconds"`
	TokensGenerated int     `json:"tokens_generated"`
	TokensPerSecond float64 `json:"tokens_per_second"`
}

// SpecDecodeRunStats records a single spec-decode run, including the
// Coordinate-internal time accounting from spec_decode.Result.
type SpecDecodeRunStats struct {
	ElapsedSeconds  float64 `json:"elapsed_seconds"`
	TokensGenerated int     `json:"tokens_generated"`
	TokensPerSecond float64 `json:"tokens_per_second"`
	Rounds          int     `json:"rounds"`
	AcceptanceRate  float64 `json:"acceptance_rate"`
	DraftSeconds    float64 `json:"draft_seconds"`
	VerifySeconds   float64 `json:"verify_seconds"`
	AcceptSeconds   float64 `json:"accept_seconds"`
}

// PerPromptResult holds both runs for one corpus prompt. Either nested run
// may be nil when --mode does not request that scenario.
type PerPromptResult struct {
	Name        string              `json:"name"`
	PromptChars int                 `json:"prompt_chars"`
	Baseline    *BaselineRunStats   `json:"baseline,omitempty"`
	SpecDecode  *SpecDecodeRunStats `json:"spec_decode,omitempty"`
}

// Summary aggregates per-prompt results into the headline numbers the
// kill-test gate cares about.
type Summary struct {
	BaselineP50TPS     float64 `json:"baseline_p50_tps"`
	BaselineP95TPS     float64 `json:"baseline_p95_tps"`
	SpecDecodeP50TPS   float64 `json:"spec_decode_p50_tps"`
	SpecDecodeP95TPS   float64 `json:"spec_decode_p95_tps"`
	SpeedupP50         float64 `json:"speedup_p50"`
	SpeedupP95         float64 `json:"speedup_p95"`
	MeanAcceptanceRate float64 `json:"mean_acceptance_rate"`
	VerdictPass        bool    `json:"verdict_pass"`
	VerdictReason      string  `json:"verdict_reason"`
}

// Report is the top-level JSON document written by the bench.
type Report struct {
	SchemaVersion string            `json:"schema_version"`
	CreatedAt     string            `json:"created_at"`
	Config        ReportConfig      `json:"config"`
	PerPrompt     []PerPromptResult `json:"per_prompt"`
	Summary       Summary           `json:"summary"`
}

// percentile returns the linear-interpolation percentile of xs (0 <= p <= 1).
// It treats an empty slice as zero so the caller doesn't have to special-case
// modes that skipped a scenario.
func percentile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// mean is a tiny helper to keep summary code readable.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// ComputeSummary derives the summary block from per-prompt rows. It is
// safe to call when one of the two scenarios was skipped — speedup falls
// back to 0 in that case and verdict_reason explains why.
func ComputeSummary(rows []PerPromptResult) Summary {
	var (
		baseTPS []float64
		specTPS []float64
		acceptR []float64
	)
	for _, r := range rows {
		if r.Baseline != nil {
			baseTPS = append(baseTPS, r.Baseline.TokensPerSecond)
		}
		if r.SpecDecode != nil {
			specTPS = append(specTPS, r.SpecDecode.TokensPerSecond)
			acceptR = append(acceptR, r.SpecDecode.AcceptanceRate)
		}
	}

	s := Summary{
		BaselineP50TPS:     percentile(baseTPS, 0.50),
		BaselineP95TPS:     percentile(baseTPS, 0.95),
		SpecDecodeP50TPS:   percentile(specTPS, 0.50),
		SpecDecodeP95TPS:   percentile(specTPS, 0.95),
		MeanAcceptanceRate: mean(acceptR),
	}
	if s.BaselineP50TPS > 0 {
		s.SpeedupP50 = s.SpecDecodeP50TPS / s.BaselineP50TPS
	}
	if s.BaselineP95TPS > 0 {
		s.SpeedupP95 = s.SpecDecodeP95TPS / s.BaselineP95TPS
	}

	switch {
	case len(baseTPS) == 0 || len(specTPS) == 0:
		s.VerdictPass = false
		s.VerdictReason = "incomplete: both baseline and spec-decode scenarios are required for a verdict"
	case s.SpeedupP50 >= SpeedupPassThreshold:
		s.VerdictPass = true
		s.VerdictReason = fmt.Sprintf("speedup_p50=%.3f meets >=%.2f gate", s.SpeedupP50, SpeedupPassThreshold)
	default:
		s.VerdictPass = false
		s.VerdictReason = fmt.Sprintf("speedup_p50=%.3f below %.2f gate (mean acceptance rate %.3f)",
			s.SpeedupP50, SpeedupPassThreshold, s.MeanAcceptanceRate)
	}
	return s
}

// NewReport stamps schema version + created_at and computes the summary.
func NewReport(cfg ReportConfig, rows []PerPromptResult) Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Config:        cfg,
		PerPrompt:     rows,
		Summary:       ComputeSummary(rows),
	}
}

// WriteReport serialises the report as indented JSON to w.
func WriteReport(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteReportFile writes the report to path (or stdout if path == "").
func WriteReportFile(path string, r Report) error {
	if path == "" {
		return WriteReport(os.Stdout, r)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create report file: %w", err)
	}
	defer f.Close()
	return WriteReport(f, r)
}

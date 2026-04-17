package quantization

import (
	"fmt"
	"math"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// QualityPolicy defines allowed quality deltas for a quantization format.
type QualityPolicy struct {
	MaxPerplexityRegressionPct float64
	MaxAcceptanceDropPct       float64
}

// QualityMetrics are the baseline and candidate metrics for policy evaluation.
type QualityMetrics struct {
	Perplexity     float64
	AcceptanceRate float64 // 0.0-1.0
}

// QualityEvaluation captures deterministic quality gate results.
type QualityEvaluation struct {
	Format             aiv1alpha2.QuantizationFormat
	Policy             QualityPolicy
	PerplexityDeltaPct float64
	AcceptanceDropPct  float64
	Pass               bool
	FailedChecks       []string
	Baseline           QualityMetrics
	Candidate          QualityMetrics
}

var qualityPolicies = map[aiv1alpha2.QuantizationFormat]QualityPolicy{
	aiv1alpha2.QuantizationFormatGGUF:              {MaxPerplexityRegressionPct: 10.0, MaxAcceptanceDropPct: 3.0},
	aiv1alpha2.QuantizationFormatAWQ:               {MaxPerplexityRegressionPct: 7.0, MaxAcceptanceDropPct: 2.0},
	aiv1alpha2.QuantizationFormatGPTQ:              {MaxPerplexityRegressionPct: 8.0, MaxAcceptanceDropPct: 2.5},
	aiv1alpha2.QuantizationFormatEXL2:              {MaxPerplexityRegressionPct: 6.0, MaxAcceptanceDropPct: 2.0},
	aiv1alpha2.QuantizationFormatFP8:               {MaxPerplexityRegressionPct: 5.0, MaxAcceptanceDropPct: 1.5},
	aiv1alpha2.QuantizationFormatCompressedTensors: {MaxPerplexityRegressionPct: 8.0, MaxAcceptanceDropPct: 2.5},
}

// QualityPolicyFor returns deterministic quality thresholds for a format.
func QualityPolicyFor(format aiv1alpha2.QuantizationFormat) (QualityPolicy, error) {
	policy, ok := qualityPolicies[normalizeFormat(format)]
	if !ok {
		return QualityPolicy{}, fmt.Errorf("quality policy is not defined for format %q", format)
	}
	return policy, nil
}

// NormalizeAcceptanceRate accepts either [0.0,1.0] or percent [0,100].
func NormalizeAcceptanceRate(v float64) (float64, bool, error) {
	switch {
	case v >= 0.0 && v <= 1.0:
		return v, false, nil
	case v > 1.0 && v <= 100.0:
		return v / 100.0, true, nil
	default:
		return 0, false, fmt.Errorf("acceptance rate %.4f out of range; expected 0..1 or 0..100", v)
	}
}

// EvaluateQuality evaluates candidate metrics against the policy for a format.
func EvaluateQuality(format aiv1alpha2.QuantizationFormat, baseline, candidate QualityMetrics) (QualityEvaluation, error) {
	policy, err := QualityPolicyFor(format)
	if err != nil {
		return QualityEvaluation{}, err
	}
	if baseline.Perplexity <= 0 {
		return QualityEvaluation{}, fmt.Errorf("baseline perplexity must be > 0")
	}
	if candidate.Perplexity <= 0 {
		return QualityEvaluation{}, fmt.Errorf("candidate perplexity must be > 0")
	}
	if baseline.AcceptanceRate < 0 || baseline.AcceptanceRate > 1 {
		return QualityEvaluation{}, fmt.Errorf("baseline acceptance rate must be in 0..1")
	}
	if candidate.AcceptanceRate < 0 || candidate.AcceptanceRate > 1 {
		return QualityEvaluation{}, fmt.Errorf("candidate acceptance rate must be in 0..1")
	}

	perplexityDeltaPct := ((candidate.Perplexity - baseline.Perplexity) / baseline.Perplexity) * 100
	acceptanceDropPct := (baseline.AcceptanceRate - candidate.AcceptanceRate) * 100
	perplexityDeltaPct = round2(perplexityDeltaPct)
	acceptanceDropPct = round2(acceptanceDropPct)

	failed := make([]string, 0, 2)
	if perplexityDeltaPct > policy.MaxPerplexityRegressionPct {
		failed = append(failed, fmt.Sprintf("perplexity regression %.2f%% exceeds %.2f%%", perplexityDeltaPct, policy.MaxPerplexityRegressionPct))
	}
	if acceptanceDropPct > policy.MaxAcceptanceDropPct {
		failed = append(failed, fmt.Sprintf("acceptance drop %.2fpp exceeds %.2fpp", acceptanceDropPct, policy.MaxAcceptanceDropPct))
	}

	return QualityEvaluation{
		Format:             normalizeFormat(format),
		Policy:             policy,
		PerplexityDeltaPct: perplexityDeltaPct,
		AcceptanceDropPct:  acceptanceDropPct,
		Pass:               len(failed) == 0,
		FailedChecks:       failed,
		Baseline:           baseline,
		Candidate:          candidate,
	}, nil
}

func normalizeFormat(format aiv1alpha2.QuantizationFormat) aiv1alpha2.QuantizationFormat {
	return aiv1alpha2.QuantizationFormat(strings.ToUpper(strings.TrimSpace(string(format))))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

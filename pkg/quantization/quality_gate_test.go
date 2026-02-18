package quantization

import (
	"strings"
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestQualityPolicyFor_AllFormats(t *testing.T) {
	formats := []aiv1alpha1.QuantizationFormat{
		aiv1alpha1.QuantizationFormatGGUF,
		aiv1alpha1.QuantizationFormatAWQ,
		aiv1alpha1.QuantizationFormatGPTQ,
		aiv1alpha1.QuantizationFormatEXL2,
		aiv1alpha1.QuantizationFormatFP8,
	}
	for _, format := range formats {
		policy, err := QualityPolicyFor(format)
		if err != nil {
			t.Fatalf("QualityPolicyFor(%s) error: %v", format, err)
		}
		if policy.MaxPerplexityRegressionPct <= 0 {
			t.Fatalf("QualityPolicyFor(%s) has invalid perplexity threshold: %+v", format, policy)
		}
		if policy.MaxAcceptanceDropPct <= 0 {
			t.Fatalf("QualityPolicyFor(%s) has invalid acceptance threshold: %+v", format, policy)
		}
	}
}

func TestEvaluateQuality(t *testing.T) {
	tests := []struct {
		name    string
		format  aiv1alpha1.QuantizationFormat
		base    QualityMetrics
		cand    QualityMetrics
		pass    bool
		wantErr string
	}{
		{
			name:   "gguf-pass",
			format: aiv1alpha1.QuantizationFormatGGUF,
			base:   QualityMetrics{Perplexity: 9.50, AcceptanceRate: 0.94},
			cand:   QualityMetrics{Perplexity: 10.10, AcceptanceRate: 0.92},
			pass:   true,
		},
		{
			name:   "awq-fail-perplexity",
			format: aiv1alpha1.QuantizationFormatAWQ,
			base:   QualityMetrics{Perplexity: 8.0, AcceptanceRate: 0.93},
			cand:   QualityMetrics{Perplexity: 9.0, AcceptanceRate: 0.92},
			pass:   false,
		},
		{
			name:   "fp8-fail-acceptance",
			format: aiv1alpha1.QuantizationFormatFP8,
			base:   QualityMetrics{Perplexity: 6.0, AcceptanceRate: 0.97},
			cand:   QualityMetrics{Perplexity: 6.1, AcceptanceRate: 0.94},
			pass:   false,
		},
		{
			name:    "invalid-baseline",
			format:  aiv1alpha1.QuantizationFormatGGUF,
			base:    QualityMetrics{Perplexity: 0, AcceptanceRate: 0.9},
			cand:    QualityMetrics{Perplexity: 1, AcceptanceRate: 0.9},
			wantErr: "baseline perplexity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := EvaluateQuality(tt.format, tt.base, tt.cand)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("EvaluateQuality() error: %v", err)
			}
			if out.Pass != tt.pass {
				t.Fatalf("EvaluateQuality() pass=%t want %t; failures=%v", out.Pass, tt.pass, out.FailedChecks)
			}
		})
	}
}

func TestNormalizeAcceptanceRate(t *testing.T) {
	tests := []struct {
		name      string
		in        float64
		want      float64
		wantPct   bool
		expectErr bool
	}{
		{name: "ratio", in: 0.91, want: 0.91},
		{name: "percent", in: 91, want: 0.91, wantPct: true},
		{name: "zero", in: 0, want: 0},
		{name: "hundred", in: 100, want: 1.0, wantPct: true},
		{name: "out-of-range", in: 120, expectErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotPct, err := NormalizeAcceptanceRate(tt.in)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error for %v", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeAcceptanceRate(%v) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeAcceptanceRate(%v)=%v want %v", tt.in, got, tt.want)
			}
			if gotPct != tt.wantPct {
				t.Fatalf("NormalizeAcceptanceRate(%v) pct=%v want %v", tt.in, gotPct, tt.wantPct)
			}
		})
	}
}

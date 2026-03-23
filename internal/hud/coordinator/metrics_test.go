package coordinator

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestRecordCompactionResult_ReducedPressure(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.RecordCompactionResult(&CompactionResult{
		Trigger:            "prompt_pressure",
		PromptTokensBefore: 15000,
		PromptTokensAfter:  11800,
		PromptTokensDelta:  3200,
	})

	if got := testutil.ToFloat64(metrics.CompactionOutcome.WithLabelValues("prompt_pressure", "reduced")); got != 1 {
		t.Fatalf("expected reduced outcome count=1, got %v", got)
	}

	family := gatherMetricFamily(t, metrics, "loom_coordinator_compaction_prompt_delta_tokens")
	metric := findMetricWithLabel(t, family, "trigger", "prompt_pressure")
	hist := metric.GetHistogram()
	if hist.GetSampleCount() != 1 {
		t.Fatalf("expected one prompt delta sample, got %d", hist.GetSampleCount())
	}
	if hist.GetSampleSum() != 3200 {
		t.Fatalf("expected prompt delta sum=3200, got %v", hist.GetSampleSum())
	}
}

func TestRecordCompactionResult_FlatOrUnmeasured(t *testing.T) {
	t.Parallel()

	metrics := NewMetrics()
	metrics.RecordCompactionResult(&CompactionResult{
		Trigger:            "prompt_pressure",
		PromptTokensBefore: 12000,
		PromptTokensAfter:  12200,
		PromptTokensDelta:  -200,
	})
	metrics.RecordCompactionResult(&CompactionResult{Trigger: "scheduled"})

	if got := testutil.ToFloat64(metrics.CompactionOutcome.WithLabelValues("prompt_pressure", "flat_or_increased")); got != 1 {
		t.Fatalf("expected flat_or_increased outcome count=1, got %v", got)
	}
	if got := testutil.ToFloat64(metrics.CompactionOutcome.WithLabelValues("scheduled", "unmeasured")); got != 1 {
		t.Fatalf("expected unmeasured outcome count=1, got %v", got)
	}

	if family := gatherMetricFamilyIfPresent(metrics, "loom_coordinator_compaction_prompt_delta_tokens"); family != nil {
		if metric := findMetricWithLabel(t, family, "trigger", "prompt_pressure"); metric.GetHistogram().GetSampleCount() != 0 {
			t.Fatalf("expected no positive prompt delta samples for flat_or_increased result, got %d", metric.GetHistogram().GetSampleCount())
		}
	}
}

func gatherMetricFamily(t *testing.T, metrics *Metrics, name string) *dto.MetricFamily {
	t.Helper()

	family := gatherMetricFamilyIfPresent(metrics, name)
	if family != nil {
		return family
	}
	t.Fatalf("metric family %s not found", name)
	return nil
}

func gatherMetricFamilyIfPresent(metrics *Metrics, name string) *dto.MetricFamily {
	families, err := metrics.registry.Gather()
	if err != nil {
		return nil
	}
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}

func findMetricWithLabel(t *testing.T, family *dto.MetricFamily, name, value string) *dto.Metric {
	t.Helper()

	for _, metric := range family.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == name && label.GetValue() == value {
				return metric
			}
		}
	}
	t.Fatalf("metric with %s=%s not found", name, value)
	return nil
}

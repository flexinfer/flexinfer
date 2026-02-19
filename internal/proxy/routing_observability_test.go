package proxy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/flexinfer/flexinfer/internal/routing"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordRoutingObservability_TracksCountersAndCardinality(t *testing.T) {
	p := setupTestProxy(t)
	model := "obs-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	strategy := routing.StrategyPrefix

	p.recordRoutingObservability(model, strategy, routing.RouteDecision{
		Key:       "tenant-a/doc-1",
		KeySource: routing.KeySourceExplicitHeader,
	}, "10.0.0.1:8000")
	p.recordRoutingObservability(model, strategy, routing.RouteDecision{
		Key:       "tenant-a/doc-2",
		KeySource: routing.KeySourceExplicitHeader,
	}, "10.0.0.1:8000")
	p.recordRoutingObservability(model, strategy, routing.RouteDecision{
		Key:       "tenant-a/doc-1",
		KeySource: routing.KeySourceExplicitHeader,
	}, "")

	if got := promtestutil.ToFloat64(routingDecisionsTotal.WithLabelValues(model, string(strategy), string(routing.KeySourceExplicitHeader), routingOutcomePod)); got != 2 {
		t.Fatalf("routing decisions pod=%v want 2", got)
	}
	if got := promtestutil.ToFloat64(routingDecisionsTotal.WithLabelValues(model, string(strategy), string(routing.KeySourceExplicitHeader), routingOutcomeServiceFallback)); got != 1 {
		t.Fatalf("routing decisions service-fallback=%v want 1", got)
	}
	if got := promtestutil.ToFloat64(routingTargetHitsTotal.WithLabelValues(model, string(strategy), "10.0.0.1:8000")); got != 2 {
		t.Fatalf("routing target pod hits=%v want 2", got)
	}
	if got := promtestutil.ToFloat64(routingTargetHitsTotal.WithLabelValues(model, string(strategy), routingTargetServiceDNS)); got != 1 {
		t.Fatalf("routing target service hits=%v want 1", got)
	}
	if got := promtestutil.ToFloat64(routingKeyCardinality.WithLabelValues(model, string(strategy), string(routing.KeySourceExplicitHeader))); got != 2 {
		t.Fatalf("routing key cardinality=%v want 2", got)
	}
}

func TestRecordRoutingObservability_CardinalityOverflow(t *testing.T) {
	p := setupTestProxy(t)
	model := "obs-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	strategy := routing.StrategyPrefix
	source := routing.KeySourceCanonical

	for i := 0; i < maxTrackedRoutingKeysPerSource+5; i++ {
		p.recordRoutingObservability(model, strategy, routing.RouteDecision{
			Key:       fmt.Sprintf("unique-key-%d", i),
			KeySource: source,
		}, "10.0.0.2:8000")
	}

	if got := promtestutil.ToFloat64(routingKeyCardinality.WithLabelValues(model, string(strategy), string(source))); got != float64(maxTrackedRoutingKeysPerSource) {
		t.Fatalf("routing key cardinality=%v want %d", got, maxTrackedRoutingKeysPerSource)
	}
	if got := promtestutil.ToFloat64(routingKeyCardinalityOverflowTotal.WithLabelValues(model, string(strategy), string(source))); got != 1 {
		t.Fatalf("overflow metric=%v want 1", got)
	}
}

package proxy

import (
	"strings"
	"testing"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
)

func TestInitModelMetrics_PreInitializesZeroValues(t *testing.T) {
	RegisterMetrics()

	// Use a unique model name to avoid collision with other tests.
	model := "init-test-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())

	InitModelMetrics(model)

	// Counters should exist with value 0
	if got := promtestutil.ToFloat64(requestsTotal.WithLabelValues(model, "success")); got != 0 {
		t.Errorf("requestsTotal[success] = %v, want 0", got)
	}
	if got := promtestutil.ToFloat64(requestsTotal.WithLabelValues(model, "error")); got != 0 {
		t.Errorf("requestsTotal[error] = %v, want 0", got)
	}
	if got := promtestutil.ToFloat64(scaleUpsTotal.WithLabelValues(model)); got != 0 {
		t.Errorf("scaleUpsTotal = %v, want 0", got)
	}
	if got := promtestutil.ToFloat64(queuedRequestsTotal.WithLabelValues(model)); got != 0 {
		t.Errorf("queuedRequestsTotal = %v, want 0", got)
	}
	if got := promtestutil.ToFloat64(queueRejectedTotal.WithLabelValues(model)); got != 0 {
		t.Errorf("queueRejectedTotal = %v, want 0", got)
	}
	if got := promtestutil.ToFloat64(activationRetriesTotal.WithLabelValues(model)); got != 0 {
		t.Errorf("activationRetriesTotal = %v, want 0", got)
	}

	// Gauges should exist with value 0
	if got := promtestutil.ToFloat64(activeConnections.WithLabelValues(model)); got != 0 {
		t.Errorf("activeConnections = %v, want 0", got)
	}
	if got := promtestutil.ToFloat64(queueDepth.WithLabelValues(model)); got != 0 {
		t.Errorf("queueDepth = %v, want 0", got)
	}
	if got := promtestutil.ToFloat64(endpointCount.WithLabelValues(model)); got != 0 {
		t.Errorf("endpointCount = %v, want 0", got)
	}
}

func TestInitModelMetrics_IdempotentAfterIncrement(t *testing.T) {
	RegisterMetrics()

	model := "idempotent-test-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())

	// Initialize, then increment, then re-initialize — value should not reset.
	InitModelMetrics(model)
	requestsTotal.WithLabelValues(model, "success").Inc()
	InitModelMetrics(model) // should not reset

	if got := promtestutil.ToFloat64(requestsTotal.WithLabelValues(model, "success")); got != 1 {
		t.Errorf("requestsTotal[success] after re-init = %v, want 1", got)
	}
}

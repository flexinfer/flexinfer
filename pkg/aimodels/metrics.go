package aimodels

import "github.com/prometheus/client_golang/prometheus"

// Metrics holds the Prometheus surface for the aimodels resolver. Keep
// the surface minimal — this package is a lookup table, not a hot path.
type Metrics struct {
	// ResolutionTotal counts each Resolve call with the role,
	// resolved model name, and whether a fallback was used. Lets
	// operators spot defaults that drift from cluster reality.
	ResolutionTotal *prometheus.CounterVec
}

// NewMetrics constructs and registers the resolver metrics. Pass the
// shared Prometheus registerer (e.g., prometheus.DefaultRegisterer or a
// custom one for tests).
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		ResolutionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_aimodel_resolution_total",
			Help: "Total Resolve calls into pkg/aimodels by role, resolved model, and whether a fallback was used.",
		}, []string{"role", "resolved_model", "fallback_used"}),
	}
	if reg != nil {
		reg.MustRegister(m.ResolutionTotal)
	}
	return m
}

func (m *Metrics) recordResolution(role Role, model string, fallbackUsed bool) {
	if m == nil || m.ResolutionTotal == nil {
		return
	}
	used := "false"
	if fallbackUsed {
		used = "true"
	}
	m.ResolutionTotal.WithLabelValues(string(role), model, used).Inc()
}

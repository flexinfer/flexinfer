package main

import (
	"bytes"
	"context"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

// metrics holds all Prometheus metrics for mcp-devbox.
type metrics struct {
	sandboxesActive *prometheus.GaugeVec
	execDuration    *prometheus.HistogramVec
	execTotal       *prometheus.CounterVec
	builds          *prometheus.CounterVec
	buildDuration   *prometheus.HistogramVec
	idleReaps       *prometheus.CounterVec
	errors          *prometheus.CounterVec

	registry *prometheus.Registry
}

func newMetrics() *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
	}

	m.sandboxesActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "loom",
			Subsystem: "devbox",
			Name:      "sandboxes_active",
			Help:      "Currently running or paused sandboxes",
		},
		[]string{"project", "backend"},
	)

	m.execDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "loom",
			Subsystem: "devbox",
			Name:      "exec_duration_seconds",
			Help:      "Exec command duration in seconds",
			Buckets:   prometheus.ExponentialBuckets(0.01, 2, 14), // 10ms to ~80s
		},
		[]string{"project", "exit_code"},
	)

	m.execTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "devbox",
			Name:      "exec_total",
			Help:      "Total exec calls",
		},
		[]string{"project", "exit_code"},
	)

	m.builds = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "devbox",
			Name:      "builds_total",
			Help:      "Total builds by status (cached/built/failed)",
		},
		[]string{"project", "status"},
	)

	m.buildDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "loom",
			Subsystem: "devbox",
			Name:      "build_duration_seconds",
			Help:      "Build duration in seconds",
			Buckets:   prometheus.ExponentialBuckets(0.5, 2, 10), // 0.5s to ~256s
		},
		[]string{"project"},
	)

	m.idleReaps = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "devbox",
			Name:      "idle_reaps_total",
			Help:      "Containers reaped (paused or stopped) for idleness",
		},
		[]string{"project"},
	)

	m.errors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "loom",
			Subsystem: "devbox",
			Name:      "errors_total",
			Help:      "Error count by operation",
		},
		[]string{"operation"},
	)

	m.registry.MustRegister(
		m.sandboxesActive,
		m.execDuration,
		m.execTotal,
		m.builds,
		m.buildDuration,
		m.idleReaps,
		m.errors,
	)

	return m
}

// gather returns all metrics in Prometheus text exposition format.
func (m *metrics) gather() (string, error) {
	families, err := m.registry.Gather()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range families {
		if err := enc.Encode(mf); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

// handleMetrics returns Prometheus metrics as text.
func (m *manager) handleMetrics(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	if m.metrics == nil {
		return mcp.ErrorResult(fmt.Errorf("metrics not initialized")), nil
	}
	text, err := m.metrics.gather()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("gather metrics: %w", err)), nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: text}},
	}, nil
}

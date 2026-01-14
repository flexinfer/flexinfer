// Package metrics provides a shared Prometheus metrics exporter.
package metrics

import (
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// TokensPerSecond is a gauge for the tokens per second.
	TokensPerSecond = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_tokens_per_second",
			Help: "Rolling 1-minute average tokens per second.",
		},
		[]string{"model", "backend", "node"},
	)

	// ModelLoadSeconds is a gauge for the model load time.
	ModelLoadSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_model_load_seconds",
			Help: "Time to pull model from cache/registry.",
		},
		[]string{"model", "node"},
	)

	// GPUTemperature is a gauge for the GPU temperature.
	GPUTemperature = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_gpu_temperature_celsius",
			Help: "GPU core temperature in Celsius.",
		},
		[]string{"gpu", "node"},
	)
)

func init() {
	// Register the metrics with the default registry.
	prometheus.MustRegister(TokensPerSecond)
	prometheus.MustRegister(ModelLoadSeconds)
	prometheus.MustRegister(GPUTemperature)
}

// Exporter handles serving the Prometheus metrics.
type Exporter struct {
	// In the future, this could hold configuration for the exporter.
}

// NewExporter creates a new Exporter.
func NewExporter() *Exporter {
	return &Exporter{}
}

// Run starts an HTTP server to expose the metrics.
// The server runs in a goroutine and logs errors instead of panicking.
func (e *Exporter) Run(addr string) {
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil && err != http.ErrServerClosed {
			// Log error but don't panic - this is a background goroutine
			// The main application should continue even if metrics fail
			// This allows debugging without crashing the entire process
			log.Printf("metrics server error on %s: %v", addr, err)
		}
	}()
}

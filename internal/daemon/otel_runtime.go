package daemon

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/crb2nu/loom/pkg/mcpotel"
	"go.opentelemetry.io/otel/trace"
)

// daemonOTelState captures the runtime OTel wiring resolved for the daemon.
// It is used to report what the daemon is actually doing, not just what the
// config file requested.
type daemonOTelState struct {
	Configured  bool
	Enabled     bool
	Endpoint    string
	Protocol    string
	ServiceName string
	SampleRate  float64
	InitError   string
}

func initDaemonOTel(ctx context.Context, cfg OTelConfig, logger *slog.Logger) (trace.TracerProvider, mcpotel.ShutdownFunc, daemonOTelState, error) {
	state := daemonOTelState{
		Endpoint:    resolveOTelEndpoint(cfg),
		Protocol:    resolveOTelProtocol(cfg),
		ServiceName: resolveOTelServiceName(cfg),
		SampleRate:  resolveOTelSampleRate(cfg),
	}
	state.Configured = state.Endpoint != ""

	opts := mcpotel.Options{
		Endpoint:   cfg.Endpoint,
		Protocol:   cfg.Protocol,
		Headers:    cfg.Headers,
		SampleRate: state.SampleRate,
	}

	tp, shutdown, err := mcpotel.InitTracerWithOptions(ctx, state.ServiceName, logger, opts)
	state.Enabled = state.Configured && err == nil
	if err != nil {
		state.InitError = err.Error()
	}
	return tp, shutdown, state, err
}

func resolveOTelEndpoint(cfg OTelConfig) string {
	if endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); endpoint != "" {
		return endpoint
	}
	return strings.TrimSpace(cfg.Endpoint)
}

func resolveOTelProtocol(cfg OTelConfig) string {
	if protocol := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")); protocol != "" {
		return strings.ToLower(protocol)
	}
	if protocol := strings.TrimSpace(cfg.Protocol); protocol != "" {
		return strings.ToLower(protocol)
	}
	return "http"
}

func resolveOTelServiceName(cfg OTelConfig) string {
	if serviceName := strings.TrimSpace(cfg.ServiceName); serviceName != "" {
		return serviceName
	}
	return "loomd"
}

func resolveOTelSampleRate(cfg OTelConfig) float64 {
	if cfg.SampleRate == nil {
		return 1.0
	}
	if rate := *cfg.SampleRate; rate > 0 && rate < 1 {
		return rate
	}
	return 1.0
}

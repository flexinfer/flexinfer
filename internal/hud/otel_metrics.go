package hud

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// HUDMetrics holds OTel metric instruments for the HUD server.
type HUDMetrics struct {
	AgentSpawnTotal       metric.Int64Counter
	PushNotificationTotal metric.Int64Counter
	SpawnedAgentActive    metric.Int64UpDownCounter
	PushDeliveryLatency   metric.Float64Histogram
}

// NewHUDMetrics registers OTel metrics via the global meter provider.
func NewHUDMetrics() *HUDMetrics {
	meter := otel.Meter("loom-hud")

	spawnTotal, _ := meter.Int64Counter("agent_spawn_total",
		metric.WithDescription("Total agent spawn attempts"),
		metric.WithUnit("{spawn}"),
	)
	pushTotal, _ := meter.Int64Counter("push_notification_sent_total",
		metric.WithDescription("Total push notifications sent"),
		metric.WithUnit("{notification}"),
	)
	spawnActive, _ := meter.Int64UpDownCounter("spawned_agent_active",
		metric.WithDescription("Currently active spawned agents"),
		metric.WithUnit("{agent}"),
	)
	pushLatency, _ := meter.Float64Histogram("push_delivery_latency_seconds",
		metric.WithDescription("APNs push delivery latency"),
		metric.WithUnit("s"),
	)

	return &HUDMetrics{
		AgentSpawnTotal:       spawnTotal,
		PushNotificationTotal: pushTotal,
		SpawnedAgentActive:    spawnActive,
		PushDeliveryLatency:   pushLatency,
	}
}

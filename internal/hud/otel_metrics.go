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

	// Spawn telemetry metrics.
	SpawnTokensTotal      metric.Int64Counter
	SpawnCostTotal        metric.Float64Counter
	SpawnTurnsTotal       metric.Int64Counter
	SpawnToolCallsTotal   metric.Int64Counter
	SpawnFileChangesTotal metric.Int64Counter
	SpawnErrorsTotal      metric.Int64Counter
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

	spawnTokens, _ := meter.Int64Counter("spawn_tokens_total",
		metric.WithDescription("Total tokens consumed by spawned agents"),
		metric.WithUnit("{token}"),
	)
	spawnCost, _ := meter.Float64Counter("spawn_cost_usd_total",
		metric.WithDescription("Total cost in USD of spawned agents"),
		metric.WithUnit("USD"),
	)
	spawnTurns, _ := meter.Int64Counter("spawn_turns_total",
		metric.WithDescription("Total turns executed by spawned agents"),
		metric.WithUnit("{turn}"),
	)
	spawnToolCalls, _ := meter.Int64Counter("spawn_tool_calls_total",
		metric.WithDescription("Total tool calls by spawned agents"),
		metric.WithUnit("{call}"),
	)
	spawnFileChanges, _ := meter.Int64Counter("spawn_file_changes_total",
		metric.WithDescription("Total file changes by spawned agents"),
		metric.WithUnit("{change}"),
	)
	spawnErrors, _ := meter.Int64Counter("spawn_errors_total",
		metric.WithDescription("Total errors encountered by spawned agents"),
		metric.WithUnit("{error}"),
	)

	return &HUDMetrics{
		AgentSpawnTotal:       spawnTotal,
		PushNotificationTotal: pushTotal,
		SpawnedAgentActive:    spawnActive,
		PushDeliveryLatency:   pushLatency,

		SpawnTokensTotal:      spawnTokens,
		SpawnCostTotal:        spawnCost,
		SpawnTurnsTotal:       spawnTurns,
		SpawnToolCallsTotal:   spawnToolCalls,
		SpawnFileChangesTotal: spawnFileChanges,
		SpawnErrorsTotal:      spawnErrors,
	}
}

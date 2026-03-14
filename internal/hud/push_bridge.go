package hud

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// PushInterruptionLevel mirrors iOS interruption levels.
type PushInterruptionLevel string

const (
	PushLevelActive        PushInterruptionLevel = "active"
	PushLevelTimeSensitive PushInterruptionLevel = "time_sensitive"
)

// PushClassification describes whether an SSE event should trigger a push.
type PushClassification struct {
	Worthy    bool
	EventType string
	Level     PushInterruptionLevel
	Title     string
	Body      string
	Category  string
	DeepLink  string
}

// PushEventBridge subscribes to SSE hub events and sends push notifications
// for classified events. Includes per-event-type rate limiting.
type PushEventBridge struct {
	sender     *APNsSender
	tokenStore *DeviceTokenStore
	tracer     trace.Tracer
	metrics    *HUDMetrics
	logger     *slog.Logger

	// Rate limiting: max 1 push per event type per interval.
	rateLimitInterval time.Duration
	mu                sync.Mutex
	lastPush          map[string]time.Time
}

// NewPushEventBridge creates a new push event bridge.
func NewPushEventBridge(
	sender *APNsSender,
	tokenStore *DeviceTokenStore,
	tracer trace.Tracer,
	metrics *HUDMetrics,
	logger *slog.Logger,
) *PushEventBridge {
	return &PushEventBridge{
		sender:            sender,
		tokenStore:        tokenStore,
		tracer:            tracer,
		metrics:           metrics,
		logger:            logger.With("component", "push-bridge"),
		rateLimitInterval: 60 * time.Second,
		lastPush:          make(map[string]time.Time),
	}
}

// HandleEvent is called for each SSE hub broadcast. It classifies the event,
// checks rate limits, and sends push notifications to all registered devices.
func (b *PushEventBridge) HandleEvent(event bridge.SSEEvent) {
	classification := classifyPushEvent(event)
	if !classification.Worthy {
		return
	}

	if !b.allowPush(classification.EventType) {
		b.logger.Debug("push rate limited",
			"event_type", classification.EventType,
			"interval", b.rateLimitInterval,
		)
		return
	}

	ctx := context.Background()
	ctx, span := b.tracer.Start(ctx, "push.send",
		trace.WithAttributes(
			attribute.String("push.event_type", classification.EventType),
			attribute.String("push.level", string(classification.Level)),
		),
	)
	defer span.End()

	payload := PushPayload{
		Title:    classification.Title,
		Body:     classification.Body,
		Category: classification.Category,
		Data: map[string]string{
			"event_type": classification.EventType,
			"deep_link":  classification.DeepLink,
		},
	}

	tokens := b.tokenStore.List()
	span.SetAttributes(attribute.Int("push.target_count", len(tokens)))

	var sent, failed int
	for _, tok := range tokens {
		if tok.Platform != "apns" {
			continue
		}
		if err := b.sender.Send(ctx, tok.Token, payload); err != nil {
			b.logger.Warn("push delivery failed",
				"token_prefix", safeTokenPrefix(tok.Token),
				"error", err,
			)
			failed++
		} else {
			sent++
		}
	}

	if b.metrics != nil {
		b.metrics.PushNotificationTotal.Add(ctx, int64(sent),
			metric.WithAttributes(
				attribute.String("event_type", classification.EventType),
				attribute.String("outcome", "sent"),
			),
		)
		if failed > 0 {
			b.metrics.PushNotificationTotal.Add(ctx, int64(failed),
				metric.WithAttributes(
					attribute.String("event_type", classification.EventType),
					attribute.String("outcome", "failed"),
				),
			)
		}
	}

	b.logger.Info("push batch complete",
		"event_type", classification.EventType,
		"sent", sent,
		"failed", failed,
	)
}

// allowPush checks and updates the per-event-type rate limit.
func (b *PushEventBridge) allowPush(eventType string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if last, ok := b.lastPush[eventType]; ok {
		if now.Sub(last) < b.rateLimitInterval {
			return false
		}
	}
	b.lastPush[eventType] = now
	return true
}

// classifyPushEvent determines if an SSE event should trigger a push notification.
func classifyPushEvent(event bridge.SSEEvent) PushClassification {
	switch event.Type {
	case "hud.health":
		return classifyHealthEvent(event)
	case "agent.session.reaped":
		return PushClassification{
			Worthy:    true,
			EventType: event.Type,
			Level:     PushLevelActive,
			Title:     "Agent Session Reaped",
			Body:      "An agent session was automatically ended due to inactivity.",
			Category:  "agent_session",
			DeepLink:  "loom://sessions",
		}
	case "hud.workflow.reject":
		return PushClassification{
			Worthy:    true,
			EventType: event.Type,
			Level:     PushLevelActive,
			Title:     "Workflow Rejected",
			Body:      "A workflow approval request was rejected.",
			Category:  "workflow",
			DeepLink:  "loom://dashboard",
		}
	case "hud.workflow.waiting_approval":
		return classifyWorkflowApproval(event)
	default:
		return PushClassification{Worthy: false}
	}
}

// classifyHealthEvent checks if a health event contains down servers.
func classifyHealthEvent(event bridge.SSEEvent) PushClassification {
	var payload struct {
		Servers []struct {
			Status string `json:"status"`
			Name   string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return PushClassification{Worthy: false}
	}

	var downCount int
	var downNames []string
	for _, s := range payload.Servers {
		if s.Status == "down" || s.Status == "error" {
			downCount++
			downNames = append(downNames, s.Name)
		}
	}
	if downCount == 0 {
		return PushClassification{Worthy: false}
	}

	var body string
	if len(downNames) <= 3 {
		body = "Down: " + joinStrings(downNames, ", ")
	} else {
		body = fmt.Sprintf("%d servers down", downCount)
	}

	return PushClassification{
		Worthy:    true,
		EventType: event.Type,
		Level:     PushLevelTimeSensitive,
		Title:     "Infrastructure Alert",
		Body:      body,
		Category:  "health",
		DeepLink:  "loom://dashboard",
	}
}

// classifyWorkflowApproval extracts workflow details for approval push.
func classifyWorkflowApproval(event bridge.SSEEvent) PushClassification {
	var payload struct {
		WorkflowID string `json:"workflow_id"`
		Name       string `json:"name"`
	}
	_ = json.Unmarshal(event.Data, &payload)

	name := payload.Name
	if name == "" {
		name = payload.WorkflowID
	}

	return PushClassification{
		Worthy:    true,
		EventType: event.Type,
		Level:     PushLevelTimeSensitive,
		Title:     "Approval Required",
		Body:      fmt.Sprintf("Workflow %q is waiting for your approval.", name),
		Category:  "workflow_approval",
		DeepLink:  fmt.Sprintf("loom://workflow/%s/approve", payload.WorkflowID),
	}
}

// joinStrings joins a string slice with a separator.
func joinStrings(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	result := items[0]
	for _, item := range items[1:] {
		result += sep + item
	}
	return result
}

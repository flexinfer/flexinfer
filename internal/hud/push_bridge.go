package hud

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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
			"interval", pushRateInterval(classification.EventType),
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
		ThreadID: threadIDForCategory(classification.Category),
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

// pushRateInterval returns a custom rate limit interval per event type.
// Time-sensitive events get shorter cooldowns; less urgent events get longer ones.
func pushRateInterval(eventType string) time.Duration {
	switch eventType {
	case "hud.pipeline.failed", "hud.workflow.waiting_approval":
		return 30 * time.Second // Time-sensitive events get shorter cooldown.
	case "agent.session.start", "agent.session.end":
		return 120 * time.Second // Session events are less urgent.
	default:
		return 60 * time.Second
	}
}

// allowPush checks and updates the per-event-type rate limit.
func (b *PushEventBridge) allowPush(eventType string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	interval := pushRateInterval(eventType)
	now := time.Now()
	if last, ok := b.lastPush[eventType]; ok {
		if now.Sub(last) < interval {
			return false
		}
	}
	b.lastPush[eventType] = now
	return true
}

// threadIDForCategory returns an iOS thread-id for notification grouping
// based on the push category.
func threadIDForCategory(category string) string {
	switch category {
	case "pipeline":
		return "loom-pipeline"
	case "agent_session":
		return "loom-sessions"
	case "workflow", "workflow_approval":
		return "loom-workflows"
	case "health":
		return "loom-health"
	case "handoff":
		return "loom-handoffs"
	default:
		return ""
	}
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
	case "hud.pipeline":
		return classifyPipelineEvent(event)
	case "agent.session.start":
		return classifySessionStartEvent(event)
	case "agent.session.end":
		return classifySessionEndEvent(event)
	case "hud.handoff.created":
		return classifyHandoffEvent(event)
	case "coordinator.plan.complete":
		return classifyPlanCompleteEvent(event)
	default:
		return PushClassification{Worthy: false}
	}
}

// classifyPipelineEvent checks pipeline status for push-worthy events.
// When multiple pipelines have the same status, it aggregates them into
// a summary notification instead of returning only the first match.
func classifyPipelineEvent(event bridge.SSEEvent) PushClassification {
	var payload struct {
		Pipelines []struct {
			ID      int    `json:"id"`
			Project string `json:"project"`
			Ref     string `json:"ref"`
			Status  string `json:"status"`
		} `json:"pipelines"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return PushClassification{Worthy: false}
	}

	var (
		failedProjects  []string
		successProjects []string
		runningCount    int
		pendingCount    int
		manualCount     int
		firstRunning    string
		firstPending    string
		firstManual     string
	)

	for _, p := range payload.Pipelines {
		label := pipelineDisplayLabel(p.Project, p.Ref)
		switch normalizePipelinePushStatus(p.Status) {
		case "failed":
			failedProjects = append(failedProjects, label)
		case "running":
			runningCount++
			if firstRunning == "" {
				firstRunning = label
			}
		case "pending":
			pendingCount++
			if firstPending == "" {
				firstPending = label
			}
		case "manual":
			manualCount++
			if firstManual == "" {
				firstManual = label
			}
		case "success":
			successProjects = append(successProjects, label)
		}
	}

	// Failures take priority over successes.
	if len(failedProjects) > 0 {
		var body, deepLink string
		if len(failedProjects) == 1 {
			// Find the matching pipeline for ref info.
			for _, p := range payload.Pipelines {
				if normalizePipelinePushStatus(p.Status) == "failed" {
					body = fmt.Sprintf("%s (%s) failed", p.Project, p.Ref)
					deepLink = fmt.Sprintf("loom://pipeline/%d", p.ID)
					break
				}
			}
		} else {
			body = fmt.Sprintf("%d pipelines failed: %s", len(failedProjects), joinStrings(failedProjects, ", "))
			deepLink = "loom://dashboard"
		}
		return PushClassification{
			Worthy:    true,
			EventType: "hud.pipeline.failed",
			Level:     PushLevelTimeSensitive,
			Title:     "Pipeline Failed",
			Body:      body,
			Category:  "pipeline",
			DeepLink:  deepLink,
		}
	}

	activeCount := runningCount + pendingCount + manualCount
	if activeCount > 0 {
		body := buildPipelineActiveBody(runningCount, pendingCount, manualCount, len(successProjects), firstRunning, firstPending, firstManual)
		title := "Pipeline Still Running"
		if runningCount == 0 {
			title = "Pipeline Waiting"
		}
		return PushClassification{
			Worthy:    true,
			EventType: "hud.pipeline.active",
			Level:     PushLevelActive,
			Title:     title,
			Body:      body,
			Category:  "pipeline",
			DeepLink:  "loom://dashboard",
		}
	}

	if len(successProjects) > 0 {
		var body, deepLink string
		if len(successProjects) == 1 {
			for _, p := range payload.Pipelines {
				if normalizePipelinePushStatus(p.Status) == "success" {
					body = fmt.Sprintf("%s (%s) passed", p.Project, p.Ref)
					deepLink = fmt.Sprintf("loom://pipeline/%d", p.ID)
					break
				}
			}
		} else {
			body = fmt.Sprintf("%d pipelines passed: %s", len(successProjects), joinStrings(successProjects, ", "))
			deepLink = "loom://dashboard"
		}
		return PushClassification{
			Worthy:    true,
			EventType: "hud.pipeline.success",
			Level:     PushLevelActive,
			Title:     "Pipeline Succeeded",
			Body:      body,
			Category:  "pipeline",
			DeepLink:  deepLink,
		}
	}

	return PushClassification{Worthy: false}
}

func normalizePipelinePushStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "running"
	case "pending", "created", "scheduled":
		return "pending"
	case "manual":
		return "manual"
	case "success", "passed":
		return "success"
	case "failed", "canceled", "cancelled", "skipped":
		return "failed"
	default:
		return ""
	}
}

func pipelineDisplayLabel(project, ref string) string {
	project = strings.TrimSpace(project)
	ref = strings.TrimSpace(ref)
	if project == "" {
		project = "pipeline"
	}
	if ref == "" {
		return project
	}
	return fmt.Sprintf("%s (%s)", project, ref)
}

func buildPipelineActiveBody(runningCount, pendingCount, manualCount, successCount int, firstRunning, firstPending, firstManual string) string {
	if runningCount == 1 && pendingCount == 0 && manualCount == 0 && successCount == 0 && firstRunning != "" {
		return fmt.Sprintf("%s is still running", firstRunning)
	}
	if runningCount == 0 && manualCount == 1 && pendingCount == 0 && successCount == 0 && firstManual != "" {
		return fmt.Sprintf("%s is waiting on manual jobs", firstManual)
	}
	if runningCount == 0 && manualCount == 0 && pendingCount == 1 && successCount == 0 && firstPending != "" {
		return fmt.Sprintf("%s is pending", firstPending)
	}

	parts := make([]string, 0, 4)
	if runningCount > 0 {
		parts = append(parts, countPhrase(runningCount, "running pipeline", "running pipelines"))
	}
	if manualCount > 0 {
		parts = append(parts, countPhrase(manualCount, "pipeline waiting on manual jobs", "pipelines waiting on manual jobs"))
	}
	if pendingCount > 0 {
		parts = append(parts, countPhrase(pendingCount, "pending pipeline", "pending pipelines"))
	}
	if successCount > 0 {
		parts = append(parts, countPhrase(successCount, "passed pipeline", "passed pipelines"))
	}
	if len(parts) == 0 {
		return "Pipelines are still in progress."
	}
	return joinStrings(parts, ", ")
}

func countPhrase(count int, singular, plural string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%d %s", count, plural)
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

// classifySessionStartEvent extracts agent session start details.
func classifySessionStartEvent(event bridge.SSEEvent) PushClassification {
	var payload struct {
		AgentID   string `json:"agent_id"`
		AgentType string `json:"agent_type"`
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return PushClassification{Worthy: false}
	}

	agentType := payload.AgentType
	if agentType == "" {
		agentType = payload.AgentID
	}
	ns := payload.Namespace
	if ns == "" {
		ns = "unknown"
	}

	return PushClassification{
		Worthy:    true,
		EventType: event.Type,
		Level:     PushLevelActive,
		Title:     "Agent Session Started",
		Body:      fmt.Sprintf("%s started on %s", agentType, ns),
		Category:  "agent_session",
		DeepLink:  "loom://sessions",
	}
}

// classifySessionEndEvent extracts agent session end details.
func classifySessionEndEvent(event bridge.SSEEvent) PushClassification {
	var payload struct {
		AgentID   string `json:"agent_id"`
		SessionID string `json:"session_id"`
		Summary   string `json:"summary"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return PushClassification{Worthy: false}
	}

	body := payload.Summary
	if body == "" {
		agentID := payload.AgentID
		if agentID == "" {
			agentID = "agent"
		}
		body = fmt.Sprintf("%s session completed", agentID)
	}

	deepLink := "loom://sessions"
	if payload.SessionID != "" {
		deepLink = fmt.Sprintf("loom://sessions/%s", payload.SessionID)
	}

	return PushClassification{
		Worthy:    true,
		EventType: event.Type,
		Level:     PushLevelActive,
		Title:     "Agent Session Ended",
		Body:      body,
		Category:  "agent_session",
		DeepLink:  deepLink,
	}
}

// classifyHandoffEvent extracts handoff details for push notification.
func classifyHandoffEvent(event bridge.SSEEvent) PushClassification {
	var payload struct {
		FromAgent string `json:"from_agent"`
		ToAgent   string `json:"to_agent"`
		Title     string `json:"title"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return PushClassification{Worthy: false}
	}

	return PushClassification{
		Worthy:    true,
		EventType: event.Type,
		Level:     PushLevelTimeSensitive,
		Title:     "Handoff Ready",
		Body:      fmt.Sprintf("%s → %s: %s", payload.FromAgent, payload.ToAgent, payload.Title),
		Category:  "handoff",
		DeepLink:  "loom://handoffs",
	}
}

// classifyPlanCompleteEvent extracts plan completion details.
func classifyPlanCompleteEvent(event bridge.SSEEvent) PushClassification {
	var payload struct {
		WorkflowID string `json:"workflow_id"`
		Name       string `json:"name"`
		Result     string `json:"result"`
	}
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return PushClassification{Worthy: false}
	}

	name := payload.Name
	if name == "" {
		name = payload.WorkflowID
	}

	return PushClassification{
		Worthy:    true,
		EventType: event.Type,
		Level:     PushLevelActive,
		Title:     "Plan Complete",
		Body:      fmt.Sprintf("%s: %s", name, payload.Result),
		Category:  "workflow",
		DeepLink:  fmt.Sprintf("loom://workflow/%s", payload.WorkflowID),
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

package hud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

// FleetWebhook pushes agent presence and session snapshots to a remote
// endpoint (e.g., flexdeck running in a K8s cluster). It is called from
// the fleet monitor's OnRefresh callback.
type FleetWebhook struct {
	url        string
	token      string
	httpClient *http.Client
	logger     *slog.Logger

	// Backoff state: skip pushes after consecutive errors.
	consecutiveErrors int
	lastError         time.Time
}

// webhookPayload is the JSON body sent to the push endpoint.
type webhookPayload struct {
	Agents   []webhookAgent   `json:"agents"`
	Sessions []webhookSession `json:"sessions"`
}

// webhookAgent matches the flexdeck PresenceInfo JSON contract.
type webhookAgent struct {
	AgentID       string   `json:"agent_id"`
	AgentType     string   `json:"agent_type"`
	Status        string   `json:"status"`
	CurrentTask   string   `json:"current_task,omitempty"`
	ActiveFiles   []string `json:"active_files,omitempty"`
	Branch        string   `json:"branch,omitempty"`
	PRURL         string   `json:"pr_url,omitempty"`
	LastHeartbeat string   `json:"last_heartbeat,omitempty"`
	SessionID     string   `json:"session_id,omitempty"`
	Namespace     string   `json:"namespace,omitempty"`
}

// webhookSession matches the flexdeck SessionInfo JSON contract.
type webhookSession struct {
	ID          string `json:"id"`
	AgentID     string `json:"agent_id"`
	Namespace   string `json:"namespace,omitempty"`
	StartedAt   string `json:"started_at"`
	EndedAt     string `json:"ended_at,omitempty"`
	Status      string `json:"status"`
	Description string `json:"description,omitempty"`
	EntryCount  int    `json:"entry_count,omitempty"`
	TotalTokens int64  `json:"total_tokens,omitempty"`
}

// NewFleetWebhook creates a webhook pusher targeting the given URL.
func NewFleetWebhook(url, token string, logger *slog.Logger) *FleetWebhook {
	return &FleetWebhook{
		url:   url,
		token: token,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		logger: logger.With("component", "fleet-webhook"),
	}
}

// Push extracts presence and session data from the fleet snapshot and
// POSTs it to the configured URL. It applies exponential backoff on
// consecutive errors (up to 5 skips ≈ 75s gap at 15s intervals).
func (w *FleetWebhook) Push(snap monitor.FleetSnapshot) {
	// Backoff: skip push if recent consecutive errors.
	if w.consecutiveErrors > 0 {
		skipCount := min(w.consecutiveErrors, 5)
		backoff := time.Duration(skipCount) * 15 * time.Second
		if time.Since(w.lastError) < backoff {
			return
		}
	}

	payload := w.buildPayload(snap)

	body, err := json.Marshal(payload)
	if err != nil {
		w.logger.Error("failed to marshal webhook payload", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		w.logger.Error("failed to create webhook request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if w.token != "" {
		req.Header.Set("X-Push-Token", w.token)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		w.recordError(err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.recordError(fmt.Errorf("push returned status %d", resp.StatusCode))
		return
	}

	// Success — reset backoff.
	if w.consecutiveErrors > 0 {
		w.logger.Info("webhook push recovered", "after_errors", w.consecutiveErrors)
	}
	w.consecutiveErrors = 0

	w.logger.Debug("webhook push ok", "agents", len(payload.Agents), "sessions", len(payload.Sessions))
}

func (w *FleetWebhook) buildPayload(snap monitor.FleetSnapshot) webhookPayload {
	agents := make([]webhookAgent, len(snap.Agents))
	for i, a := range snap.Agents {
		agents[i] = webhookAgent{
			AgentID:       a.AgentID,
			AgentType:     a.AgentType,
			Status:        a.Status,
			CurrentTask:   a.CurrentTask,
			ActiveFiles:   a.ActiveFiles,
			Branch:        a.Branch,
			PRURL:         a.PRUrl,
			LastHeartbeat: a.LastHeartbeat,
			SessionID:     a.SessionID,
		}
	}

	sessions := make([]webhookSession, len(snap.Sessions))
	for i, s := range snap.Sessions {
		sessions[i] = webhookSession{
			ID:          s.ID,
			AgentID:     s.AgentID,
			Namespace:   s.Namespace,
			StartedAt:   s.StartedAt,
			EndedAt:     s.EndedAt,
			Status:      s.Status,
			Description: s.Description,
			EntryCount:  s.EntryCount,
			TotalTokens: int64(s.TotalTokens),
		}
	}

	return webhookPayload{
		Agents:   agents,
		Sessions: sessions,
	}
}

func (w *FleetWebhook) recordError(err error) {
	w.consecutiveErrors++
	w.lastError = time.Now()
	if w.consecutiveErrors <= 3 {
		w.logger.Warn("webhook push failed", "error", err, "consecutive", w.consecutiveErrors)
	}
}

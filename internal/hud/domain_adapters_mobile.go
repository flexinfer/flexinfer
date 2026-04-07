// domain_adapters_mobile.go provides mobile and spawn domain adapters.
package hud

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/domain/mobile"
	domainspawn "github.com/crb2nu/loom/internal/hud/domain/spawn"
	domainwebhook "github.com/crb2nu/loom/internal/hud/domain/webhook"
	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// --- Mobile adapter helpers ---

// mobileEventLogAdapter wraps *EventLog to satisfy mobile.EventLogOps,
// converting hud.TimelineEntry to mobile.TimelineEntry.
type mobileEventLogAdapter struct {
	log *EventLog
}

func (e *mobileEventLogAdapter) All(limit int) []mobile.TimelineEntry {
	entries := e.log.All(limit)
	result := make([]mobile.TimelineEntry, len(entries))
	for i, entry := range entries {
		result[i] = mobile.TimelineEntry{
			Timestamp: entry.Timestamp,
			EventType: entry.EventType,
			AgentID:   entry.AgentID,
			AgentType: entry.AgentType,
			Data:      entry.Data,
		}
	}
	return result
}

func (e *mobileEventLogAdapter) AllExcluding(limit int, excludeTypes ...string) []mobile.TimelineEntry {
	entries := e.log.AllExcluding(limit, excludeTypes...)
	result := make([]mobile.TimelineEntry, len(entries))
	for i, entry := range entries {
		result[i] = mobile.TimelineEntry{
			Timestamp: entry.Timestamp,
			EventType: entry.EventType,
			AgentID:   entry.AgentID,
			AgentType: entry.AgentType,
			Data:      entry.Data,
		}
	}
	return result
}

// mobileSpawnerAdapter wraps *SpawnOrchestrator to satisfy mobile.SpawnerOps.
type mobileSpawnerAdapter struct {
	s *SpawnOrchestrator
}

func (sa *mobileSpawnerAdapter) Spawn(ctx context.Context, req pkgspawn.Request) (string, error) {
	return sa.s.Spawn(ctx, req)
}

func (sa *mobileSpawnerAdapter) GetSpawn(spawnID string) (*pkgspawn.State, bool) {
	return sa.s.GetSpawn(spawnID)
}

func (sa *mobileSpawnerAdapter) ListSpawns() []*pkgspawn.State {
	return sa.s.ListSpawns()
}

func (sa *mobileSpawnerAdapter) StopSpawn(ctx context.Context, spawnID string) error {
	return sa.s.StopSpawn(ctx, spawnID)
}

func (sa *mobileSpawnerAdapter) Projects() []string {
	return sa.s.Projects()
}

func (sa *mobileSpawnerAdapter) GetSpawnTelemetry(spawnID string) (*bridge.SpawnTelemetry, bool) {
	return sa.s.GetSpawnTelemetry(spawnID)
}

func (sa *mobileSpawnerAdapter) SendControlMessage(ctx context.Context, spawnID string, cmd pkgspawn.ControlCommand) error {
	return sa.s.SendControlMessage(ctx, spawnID, cmd)
}

// --- Spawn domain Deps adapter ---

// spawnDepsAdapter wraps *App to satisfy domainspawn.Deps. A separate adapter
// is needed because *App.Spawner() returns mobile.SpawnerOps (for the mobile
// domain), while spawn.Deps requires spawn.SpawnerOps. Both interfaces have
// identical method sets but are distinct Go types.
type spawnDepsAdapter struct {
	app *App
}

func (s *spawnDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	s.app.WriteJSON(w, status, v)
}

func (s *spawnDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	s.app.WriteError(w, status, msg, err)
}

func (s *spawnDepsAdapter) RequireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	return s.app.RequireAdminToken(w, r)
}

func (s *spawnDepsAdapter) Spawner() domainspawn.SpawnerOps {
	if s.app.spawner == nil {
		return nil
	}
	return s.app.spawner
}

// --- Webhook domain Deps adapter ---

type webhookDepsAdapter struct {
	app *App
}

func (w *webhookDepsAdapter) WriteJSON(wr http.ResponseWriter, status int, v any) {
	w.app.WriteJSON(wr, status, v)
}

func (w *webhookDepsAdapter) WriteError(wr http.ResponseWriter, status int, msg string, err error) {
	w.app.WriteError(wr, status, msg, err)
}

func (w *webhookDepsAdapter) RequireAdminToken(wr http.ResponseWriter, r *http.Request) bool {
	return w.app.RequireAdminToken(wr, r)
}

func (w *webhookDepsAdapter) Logger() *slog.Logger {
	return w.app.logger
}

func (w *webhookDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	w.app.BroadcastAgentEvent(eventType, payload)
}

func (w *webhookDepsAdapter) Spawner() domainwebhook.SpawnerOps {
	if w.app.spawner == nil {
		return nil
	}
	return w.app.spawner
}

func (w *webhookDepsAdapter) WebhookConfig() domainwebhook.WebhookCfg {
	return domainwebhook.WebhookCfg{
		InboundEnabled: w.app.config.WebhookInboundEnabled,
		GitLabSecret:   w.app.config.WebhookGitLabSecret,
		GitHubSecret:   w.app.config.WebhookGitHubSecret,
	}
}

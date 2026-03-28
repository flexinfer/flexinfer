// domain_adapters_ops.go provides smaller operational domain adapters (graph, handoff, merge, orchestration, context, codebase, alerting, memory).
package hud

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordination"
	domainalerting "github.com/crb2nu/loom/internal/hud/domain/alerting"
	domainctx "github.com/crb2nu/loom/internal/hud/domain/context"
	"github.com/crb2nu/loom/internal/hud/domain/memory"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/hud/orchestration"
)

// --- Graph domain Deps adapter ---

// graphDepsAdapter wraps *App to satisfy graph.Deps.
type graphDepsAdapter struct {
	app *App
}

func (g *graphDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	g.app.WriteJSON(w, status, v)
}

func (g *graphDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	g.app.WriteError(w, status, msg, err)
}

func (g *graphDepsAdapter) Logger() *slog.Logger { return g.app.Logger() }

func (g *graphDepsAdapter) Agent() *bridge.AgentBridge { return g.app.Agent() }

func (g *graphDepsAdapter) CacheGet(key string) (any, bool) { return g.app.CacheGet(key) }

func (g *graphDepsAdapter) CacheSet(key string, value any, ttl time.Duration) {
	g.app.CacheSet(key, value, ttl)
}

// --- Memory domain Deps adapter ---

type memoryDepsAdapter struct {
	app *App
}

func (m *memoryDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	m.app.WriteJSON(w, status, v)
}

func (m *memoryDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	m.app.WriteError(w, status, msg, err)
}

func (m *memoryDepsAdapter) Logger() *slog.Logger { return m.app.Logger() }

func (m *memoryDepsAdapter) Agent() *bridge.AgentBridge { return m.app.Agent() }

func (m *memoryDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	m.app.BroadcastAgentEvent(eventType, payload)
}

func (m *memoryDepsAdapter) MemoryMonitor() memory.MemoryMonitorOps {
	return m.app.memoryMonitor
}

// --- Handoff domain Deps adapter ---

type handoffDepsAdapter struct {
	app *App
}

func (h *handoffDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	h.app.WriteJSON(w, status, v)
}

func (h *handoffDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	h.app.WriteError(w, status, msg, err)
}

func (h *handoffDepsAdapter) Logger() *slog.Logger { return h.app.Logger() }

func (h *handoffDepsAdapter) Agent() *bridge.AgentBridge { return h.app.Agent() }

func (h *handoffDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	h.app.BroadcastAgentEvent(eventType, payload)
}

// --- Merge domain Deps adapter ---

type mergeDepsAdapter struct {
	app *App
}

func (m *mergeDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	m.app.WriteJSON(w, status, v)
}

func (m *mergeDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	m.app.WriteError(w, status, msg, err)
}

func (m *mergeDepsAdapter) Logger() *slog.Logger { return m.app.Logger() }

func (m *mergeDepsAdapter) CoordinationSnapshot() coordination.Snapshot {
	return m.app.fleetMonitor.Snapshot().Coordination
}

// --- Orchestration domain Deps adapter ---

type orchDepsAdapter struct {
	app *App
}

func (o *orchDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	o.app.WriteJSON(w, status, v)
}

func (o *orchDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	o.app.WriteError(w, status, msg, err)
}

func (o *orchDepsAdapter) Logger() *slog.Logger { return o.app.Logger() }

func (o *orchDepsAdapter) OrchestrationEngine() *orchestration.Engine {
	return o.app.orchEngine
}

func (o *orchDepsAdapter) OrchestrationMonitor() *orchestration.OrchestrationMonitor {
	return o.app.orchMonitor
}

func (o *orchDepsAdapter) OrchestrationBridge() orchestration.Bridge {
	return o.app.agent
}

// --- Context health domain Deps adapter ---

type ctxDepsAdapter struct {
	app *App
}

func (c *ctxDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	c.app.WriteJSON(w, status, v)
}

func (c *ctxDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	c.app.WriteError(w, status, msg, err)
}

func (c *ctxDepsAdapter) Logger() *slog.Logger { return c.app.Logger() }

func (c *ctxDepsAdapter) ContextHealthMonitor() domainctx.ContextHealthMonitorOps {
	if c.app.contextHealthMonitor == nil {
		return nil
	}
	return c.app.contextHealthMonitor
}

// --- Codebase domain Deps adapter ---

type codebaseDepsAdapter struct {
	app *App
}

func (cb *codebaseDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	cb.app.WriteJSON(w, status, v)
}

func (cb *codebaseDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	cb.app.WriteError(w, status, msg, err)
}

func (cb *codebaseDepsAdapter) Logger() *slog.Logger { return cb.app.Logger() }

func (cb *codebaseDepsAdapter) Agent() *bridge.AgentBridge { return cb.app.Agent() }

func (cb *codebaseDepsAdapter) CodebaseMonitor() *monitor.CodebaseMonitor {
	return cb.app.codebaseMonitor
}

// --- Alerting domain Deps adapter ---

type alertingDepsAdapter struct {
	app *App
}

func (al *alertingDepsAdapter) WriteJSON(w http.ResponseWriter, status int, v any) {
	al.app.WriteJSON(w, status, v)
}

func (al *alertingDepsAdapter) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	al.app.WriteError(w, status, msg, err)
}

func (al *alertingDepsAdapter) RequireAdminToken(w http.ResponseWriter, r *http.Request) bool {
	return al.app.RequireAdminToken(w, r)
}

func (al *alertingDepsAdapter) AlertEngine() domainalerting.AlertEngineOps {
	if al.app.alertEngine == nil {
		return nil
	}
	return al.app.alertEngine
}

func (al *alertingDepsAdapter) AutoFixEngine() domainalerting.AutoFixEngineOps {
	if al.app.autofixEngine == nil {
		return nil
	}
	return al.app.autofixEngine
}

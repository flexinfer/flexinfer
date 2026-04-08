package mobile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func (d *MobileDomain) handleMobileSessions(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	sessions, err := d.deps.Agent().Sessions()
	if err != nil {
		d.deps.Logger().Warn("mobile: sessions upstream error, falling back to cache", "error", err)
		snap := d.deps.Monitors().Fleet.Snapshot()
		sessions = snap.Sessions
	}
	if sessions == nil {
		sessions = []bridge.SessionInfo{}
	}

	// Default filter: only surface live ("active") sessions. Clients can
	// override with ?status=active,ended,summarized or ?status=all to inspect
	// the full history. This prevents stale ended/summarized sessions from
	// leaking into list views that the user expects to reflect "what's
	// currently running". See internal/hud/domain/mobile/handler_sessions_test.go.
	statusFilter := parseMobileSessionStatusFilter(r.URL.Query().Get("status"))
	if statusFilter != nil {
		filtered := sessions[:0:0]
		for _, s := range sessions {
			if statusFilter[strings.ToLower(strings.TrimSpace(s.Status))] {
				filtered = append(filtered, s)
			}
		}
		sessions = filtered
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

// parseMobileSessionStatusFilter returns a set of allowed session statuses, or
// nil if no filtering should be applied. An empty/omitted value defaults to
// {"active"}; "all" returns nil (no filtering). Unknown values are dropped.
func parseMobileSessionStatusFilter(raw string) map[string]bool {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return map[string]bool{"active": true}
	}
	if trimmed == "all" || trimmed == "*" {
		return nil
	}
	allowed := map[string]bool{}
	for _, part := range strings.Split(trimmed, ",") {
		if p := strings.TrimSpace(part); p != "" {
			allowed[p] = true
		}
	}
	if len(allowed) == 0 {
		return map[string]bool{"active": true}
	}
	return allowed
}

func (d *MobileDomain) handleMobileSessionDetail(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	agentID := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if sessionID == "" && agentID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "session_id or agent_id is required")
		return
	}

	sessions, err := d.deps.Agent().Sessions()
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list sessions")
		return
	}

	var found *bridge.SessionInfo
	for i := range sessions {
		s := &sessions[i]
		if sessionID != "" && strings.TrimSpace(s.ID) == sessionID {
			found = s
			break
		}
		if agentID != "" && strings.TrimSpace(s.AgentID) == agentID && s.Status == "active" {
			found = s
			break
		}
	}
	if found == nil {
		d.writeMobileError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}

	result := map[string]any{"session": found}

	type inspectResult struct {
		data *bridge.ContextInspectResult
		err  error
	}
	ch := make(chan inspectResult, 1)
	go func() {
		data, e := d.deps.Agent().ContextInspect(found.AgentID, found.ID, true, 200)
		ch <- inspectResult{data, e}
	}()
	var inspect *bridge.ContextInspectResult
	select {
	case res := <-ch:
		inspect, err = res.data, res.err
	case <-time.After(8 * time.Second):
		err = fmt.Errorf("context inspect timed out")
	}
	if err == nil && inspect != nil {
		result["entry_breakdown"] = inspect.ByEntryType
		result["top_entries"] = inspect.TopEntries
		result["tasks"] = inspect.Tasks

		var decisions, errors []bridge.ContextInspectTopEntry
		for _, e := range inspect.TopEntries {
			switch e.EntryType {
			case "decision":
				decisions = append(decisions, e)
			case "error":
				errors = append(errors, e)
			}
		}
		result["decisions"] = decisions
		result["errors"] = errors

		fileHits := make(map[string]int)
		for _, e := range inspect.TopEntries {
			if e.EntryType == "file_read" || e.EntryType == "code_context" {
				if e.Title != "" {
					fileHits[e.Title]++
				}
			}
		}
		type touchedFile struct {
			FilePath   string `json:"file_path"`
			TouchCount int    `json:"touch_count"`
		}
		topFiles := make([]touchedFile, 0, len(fileHits))
		for fp, count := range fileHits {
			topFiles = append(topFiles, touchedFile{FilePath: fp, TouchCount: count})
		}
		sortSliceStable(topFiles, func(i, j int) bool {
			return topFiles[i].TouchCount > topFiles[j].TouchCount
		})
		if len(topFiles) > 10 {
			topFiles = topFiles[:10]
		}
		result["top_files"] = topFiles
	}

	d.writeMobileJSON(w, http.StatusOK, result)
}

func (d *MobileDomain) handleMobileSessionEvents(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "session_id is required")
		return
	}

	limit := 100
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}

	events := make([]TimelineEntry, 0, limit)
	if el := d.deps.EventLog(); el != nil {
		for _, evt := range el.All(1000) {
			if eventHasSessionID(evt.Data, sessionID) {
				events = append(events, evt)
				if len(events) >= limit {
					break
				}
			}
		}
	}

	if len(events) == 0 && d.deps.Agent() != nil {
		if entries, err := d.deps.Agent().SessionEntries(sessionID, limit); err == nil {
			for _, e := range entries {
				ts, _ := time.Parse(time.RFC3339, e.Entry.Timestamp)
				if ts.IsZero() {
					ts = time.Now().UTC()
				}
				events = append(events, TimelineEntry{
					Timestamp: ts,
					EventType: e.Entry.EntryType,
					Data:      json.RawMessage(fmt.Sprintf(`{"title":%q,"content":%q,"entry_type":%q}`, e.Entry.Title, e.Entry.Content, e.Entry.EntryType)),
				})
				if len(events) >= limit {
					break
				}
			}
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"events":     events,
	})
}

func (d *MobileDomain) handleMobileSessionCreate(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeSessionCreate) {
		return
	}

	var body bridge.SessionStartParams
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if body.AgentID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "agent_id is required")
		return
	}
	if strings.TrimSpace(body.AgentType) == "" {
		body.AgentType = "mobile"
	}
	if strings.TrimSpace(body.Description) == "" {
		body.Description = "Mobile session"
	}

	d.logMobileAudit(r, "session_create", map[string]string{
		"agent_id":  body.AgentID,
		"namespace": body.Namespace,
	}, "initiated", nil)

	result, err := d.deps.Agent().StartSession(body)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to start session")
		return
	}

	d.deps.BroadcastAgentEvent("agent.session.start", map[string]any{
		"session_id": result.SessionID,
		"agent_id":   body.AgentID,
		"agent_type": body.AgentType,
		"namespace":  body.Namespace,
	})
	if !result.AlreadyExisted {
		d.deps.FleetIncrementKPI("sessions", 1)
	}
	go d.deps.FleetRefresh()
	go d.deps.MaybeAutoProvisionSandbox(body.Namespace)

	d.writeMobileJSON(w, http.StatusOK, result)
}

func (d *MobileDomain) handleMobileSessionEnd(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeSessionEnd) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "session_id is required")
		return
	}

	var body struct {
		Summarize *bool `json:"summarize,omitempty"`
	}
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			d.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				d.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
				return
			}
		}
	}

	summarize := true
	if body.Summarize != nil {
		summarize = *body.Summarize
	}

	d.logMobileAudit(r, "session_end", map[string]string{
		"session_id": sessionID,
		"summarize":  strconv.FormatBool(summarize),
	}, "initiated", nil)

	endParams := bridge.SessionEndParams{
		SessionID: sessionID,
		Summarize: body.Summarize,
	}
	endParams, _ = d.deps.PlanSessionEndSummary(endParams)
	ended, err := d.deps.Agent().EndSession(endParams)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to end session")
		return
	}

	if ended {
		d.deps.BroadcastAgentEvent("agent.session.end", map[string]any{
			"session_id": sessionID,
		})
		go d.deps.FleetRefresh()
		d.deps.OnSessionEnd(sessionID, "")
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"ended":      ended,
		"session_id": sessionID,
	})
}

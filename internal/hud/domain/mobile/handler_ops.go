package mobile

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

type mobilePipelineAgentRef struct {
	ID   string
	Type string
}

type mobilePipelineJobDTO struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Stage     string `json:"stage"`
	Duration  int    `json:"duration,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	WebURL    string `json:"web_url,omitempty"`
}

type mobilePipelineStageDTO struct {
	Name   string                 `json:"name"`
	Status string                 `json:"status"`
	Jobs   []mobilePipelineJobDTO `json:"jobs,omitempty"`
}

type mobilePipelineDTO struct {
	ID              int                      `json:"id"`
	Project         string                   `json:"project"`
	Ref             string                   `json:"ref"`
	Status          string                   `json:"status"`
	Source          string                   `json:"source,omitempty"`
	CreatedAt       string                   `json:"created_at"`
	WebURL          string                   `json:"web_url,omitempty"`
	CurrentStage    string                   `json:"current_stage,omitempty"`
	CompletedStages int                      `json:"completed_stages"`
	TotalStages     int                      `json:"total_stages"`
	FailedJobCount  int                      `json:"failed_job_count"`
	Stages          []mobilePipelineStageDTO `json:"stages,omitempty"`
	AgentID         string                   `json:"agent_id,omitempty"`
	AgentType       string                   `json:"agent_type,omitempty"`
	Correlation     string                   `json:"correlation,omitempty"`
}

func buildMobilePipelineResponse(p bridge.PipelineInfo, detail *bridge.PipelineDetail) mobilePipelineDTO {
	resp := mobilePipelineDTO{
		ID:        p.ID,
		Project:   p.Project,
		Ref:       p.Ref,
		Status:    p.Status,
		Source:    p.Source,
		CreatedAt: p.CreatedAt,
		WebURL:    p.WebURL,
	}
	if detail == nil {
		return resp
	}

	resp.CurrentStage = detail.CurrentStage
	resp.CompletedStages = detail.CompletedStages
	resp.TotalStages = detail.TotalStages
	resp.FailedJobCount = detail.FailedJobCount
	if len(detail.Stages) > 0 {
		resp.Stages = make([]mobilePipelineStageDTO, 0, len(detail.Stages))
		for _, stage := range detail.Stages {
			stageDTO := mobilePipelineStageDTO{
				Name:   stage.Name,
				Status: stage.Status,
			}
			if len(stage.Jobs) > 0 {
				stageDTO.Jobs = make([]mobilePipelineJobDTO, 0, len(stage.Jobs))
				for _, job := range stage.Jobs {
					stageDTO.Jobs = append(stageDTO.Jobs, mobilePipelineJobDTO{
						ID:        job.ID,
						Name:      job.Name,
						Status:    job.Status,
						Stage:     job.Stage,
						Duration:  int(job.Duration),
						StartedAt: job.StartedAt,
						WebURL:    job.WebURL,
					})
				}
			}
			resp.Stages = append(resp.Stages, stageDTO)
		}
	}
	return resp
}

func buildMobilePipelineResponses(pipelines []bridge.PipelineInfo, branchAgents map[string]mobilePipelineAgentRef, detailFn func(project string, pipelineID int) (*bridge.PipelineDetail, error)) []mobilePipelineDTO {
	results := make([]mobilePipelineDTO, len(pipelines))
	if len(pipelines) == 0 {
		return results
	}
	if detailFn == nil {
		detailFn = func(string, int) (*bridge.PipelineDetail, error) { return nil, nil }
	}

	workerCount := len(pipelines)
	if workerCount > 2 {
		workerCount = 2
	}

	var wg sync.WaitGroup
	jobs := make(chan int)
	wg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				p := pipelines[idx]
				resp := buildMobilePipelineResponse(p, nil)
				if detail, err := detailFn(p.Project, p.ID); err == nil && detail != nil {
					resp = buildMobilePipelineResponse(p, detail)
				}
				if ar, ok := branchAgents[p.Ref]; ok {
					resp.AgentID = ar.ID
					resp.AgentType = ar.Type
					resp.Correlation = "branch_match"
				}
				results[idx] = resp
			}
		}()
	}

	for idx := range pipelines {
		jobs <- idx
	}
	close(jobs)
	wg.Wait()

	return results
}

func (d *MobileDomain) handleMobileReasoningChains(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))

	chains, err := d.deps.Agent().ReasoningChainList()
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list reasoning chains")
		return
	}
	if chains == nil {
		chains = []bridge.ReasoningChainInfo{}
	}

	filtered := make([]bridge.ReasoningChainInfo, 0, len(chains))
	for _, chain := range chains {
		status := normalizeMobileReasoningStatus(chain.Status)
		if statusFilter != "" && status != statusFilter {
			continue
		}
		chain.Status = status
		filtered = append(filtered, chain)
	}

	sortSliceStable(filtered, func(i, j int) bool {
		ti := parseMobileTime(filtered[i].CreatedAt)
		tj := parseMobileTime(filtered[j].CreatedAt)
		if ti.Equal(tj) {
			return filtered[i].ID < filtered[j].ID
		}
		return ti.After(tj)
	})
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	result := make([]reasoningChainDTO, len(filtered))
	for i, chain := range filtered {
		result[i] = reasoningChainDTO{
			ID:          chain.ID,
			Title:       chain.Title,
			Status:      chain.Status,
			StepCount:   chain.StepCount,
			Confidence:  chain.Confidence,
			CreatedAt:   chain.CreatedAt,
			CompletedAt: chain.CompletedAt,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{"chains": result})
}

func (d *MobileDomain) handleMobileReasoningChainDetail(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	chainID := strings.TrimSpace(r.PathValue("chain_id"))
	if chainID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "chain_id is required")
		return
	}

	detail, err := d.deps.Agent().ReasoningChainGet(chainID)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load reasoning chain")
		return
	}

	steps := make([]reasoningStepDTO, len(detail.Steps))
	for i, step := range detail.Steps {
		steps[i] = reasoningStepDTO{
			ID:          step.ID,
			Description: step.Description,
			Confidence:  step.Confidence,
			Evidence:    step.Evidence,
			CreatedAt:   step.CreatedAt,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"chain": reasoningChainDTO{
			ID:          detail.ID,
			Title:       detail.Title,
			Status:      normalizeMobileReasoningStatus(detail.Status),
			StepCount:   detail.StepCount,
			Confidence:  detail.Confidence,
			CreatedAt:   detail.CreatedAt,
			CompletedAt: detail.CompletedAt,
			Steps:       steps,
		},
	})
}

func (d *MobileDomain) handleMobileEventsStream(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}
	d.deps.HandleSSE(w, r)
}

func (d *MobileDomain) handleMobileAudit(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	sourceFilter := strings.TrimSpace(r.URL.Query().Get("source"))

	var entries []TimelineEntry
	if el := d.deps.EventLog(); el != nil {
		for _, evt := range el.All(500) {
			if sourceFilter != "" && !eventHasField(evt.Data, "source", sourceFilter) {
				continue
			}
			entries = append(entries, evt)
			if len(entries) >= limit {
				break
			}
		}
	}
	if entries == nil {
		entries = []TimelineEntry{}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"source":  sourceFilter,
		"count":   len(entries),
	})
}

func (d *MobileDomain) handleMobileAlertsPolicy(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}
	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"policy":  AlertPolicyMatrix(),
		"version": "v1",
	})
}

func (d *MobileDomain) handleMobileHandoffs(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	_ = limit

	handoffs, err := d.deps.Agent().HandoffList()
	if err != nil {
		d.writeMobileError(w, http.StatusInternalServerError, "HANDOFF_LIST_FAILED", err.Error())
		return
	}

	if handoffs == nil {
		handoffs = []bridge.HandoffInfo{}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"handoffs": handoffs,
		"total":    len(handoffs),
	})
}

// handleMobileHandoffAccept accepts a handoff via the agent bridge.
// Body shape mirrors the existing /api/handoffs/{id}/accept endpoint:
// either session_id or target_agent_id must be provided (the latter
// resolves to the agent's active session). import_entries is optional.
//
// Slice 2-β of the cross-agent GUI integration plan: lets the loom
// fleet widget surface an Accept button on each pending handoff card
// without leaving the host (Claude/ChatGPT) UI.
func (d *MobileDomain) handleMobileHandoffAccept(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	handoffID := strings.TrimSpace(r.PathValue("handoff_id"))
	if handoffID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "MISSING_HANDOFF_ID", "handoff_id is required")
		return
	}

	var body struct {
		SessionID     string `json:"session_id"`
		TargetAgentID string `json:"target_agent_id"`
		ImportEntries bool   `json:"import_entries"`
	}
	if r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			d.writeMobileError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
			return
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				d.writeMobileError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
				return
			}
		}
	}
	body.SessionID = strings.TrimSpace(body.SessionID)
	body.TargetAgentID = strings.TrimSpace(body.TargetAgentID)

	if body.SessionID == "" {
		if body.TargetAgentID == "" {
			d.writeMobileError(w, http.StatusBadRequest, "MISSING_SESSION", "session_id or target_agent_id is required")
			return
		}
		active, err := d.deps.Agent().GetActiveSession(body.TargetAgentID)
		if err != nil {
			d.writeMobileError(w, http.StatusBadGateway, "RESOLVE_SESSION_FAILED", err.Error())
			return
		}
		if active == nil || strings.TrimSpace(active.ID) == "" {
			d.writeMobileError(w, http.StatusBadRequest, "NO_ACTIVE_SESSION", "target agent has no active session")
			return
		}
		body.SessionID = strings.TrimSpace(active.ID)
	}

	result, err := d.deps.Agent().HandoffAccept(bridge.HandoffAcceptParams{
		HandoffID:     handoffID,
		SessionID:     body.SessionID,
		ImportEntries: body.ImportEntries,
	})
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "HANDOFF_ACCEPT_FAILED", err.Error())
		return
	}

	d.deps.Logger().Info("handoff accepted via mobile", "handoff_id", handoffID, "session_id", body.SessionID)
	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"status":     "accepted",
		"handoff_id": handoffID,
		"session_id": body.SessionID,
		"result":     result,
	})
}

// handleMobileHandoffReject marks a handoff rejected. Reason is
// optional; when present it is forwarded to the source agent via
// agent_handoff_reject so they understand why.
func (d *MobileDomain) handleMobileHandoffReject(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	handoffID := strings.TrimSpace(r.PathValue("handoff_id"))
	if handoffID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "MISSING_HANDOFF_ID", "handoff_id is required")
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		data, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			d.writeMobileError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
			return
		}
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				d.writeMobileError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
				return
			}
		}
	}

	result, err := d.deps.Agent().HandoffReject(bridge.HandoffRejectParams{
		HandoffID: handoffID,
		Reason:    strings.TrimSpace(body.Reason),
	})
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "HANDOFF_REJECT_FAILED", err.Error())
		return
	}

	d.deps.Logger().Info("handoff rejected via mobile", "handoff_id", handoffID, "reason_len", len(body.Reason))
	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"status":     "rejected",
		"handoff_id": handoffID,
		"result":     result,
	})
}

func (d *MobileDomain) handleMobilePipelines(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	mon := d.deps.Monitors()
	if mon.Pipeline == nil {
		d.writeMobileJSON(w, http.StatusOK, map[string]any{
			"pipelines":        []any{},
			"recent_pipelines": []any{},
			"summary": map[string]any{
				"running":       0,
				"passed":        0,
				"failed":        0,
				"pending":       0,
				"last_activity": "",
			},
			"available": false,
		})
		return
	}

	// Build branch-to-agent lookup for pipeline-agent correlation.
	branchAgents := map[string]mobilePipelineAgentRef{}
	if fleet := mon.Fleet; fleet != nil {
		snap := fleet.Snapshot()
		for _, pa := range snap.Agents {
			if pa.Branch != "" {
				agentType := pa.AgentType
				if agentType == "" || agentType == "unknown" {
					agentType = inferAgentType(pa.AgentID)
				}
				branchAgents[pa.Branch] = mobilePipelineAgentRef{ID: pa.AgentID, Type: agentType}
			}
		}
	}

	active := mon.Pipeline.Pipelines()
	recent := mon.Pipeline.RecentPipelines()
	var pipelineErr error

	if len(active) == 0 && mon.Pipeline.Ready() {
		if err := mon.Pipeline.Refresh(); err == nil {
			active = mon.Pipeline.Pipelines()
		} else {
			pipelineErr = err
		}
	}

	if len(recent) == 0 && mon.Pipeline.Ready() {
		if err := mon.Pipeline.RefreshRecent(); err == nil {
			recent = mon.Pipeline.RecentPipelines()
		} else if pipelineErr == nil {
			pipelineErr = err
		}
	}

	if len(recent) == 0 && len(mon.Pipeline.Projects()) > 0 {
		if direct, err := d.deps.Agent().ListRecentPipelines(mon.Pipeline.Projects(), 10); err == nil {
			recent = direct
		} else if pipelineErr == nil {
			pipelineErr = err
		}
	}

	if len(active) == 0 && len(recent) > 0 {
		active = filterActivePipelineInfos(recent)
	}

	if len(active) == 0 && len(recent) == 0 && pipelineErr != nil {
		d.writeMobileJSON(w, http.StatusOK, map[string]any{
			"pipelines":        []any{},
			"recent_pipelines": []any{},
			"summary": map[string]any{
				"running":       0,
				"passed":        0,
				"failed":        0,
				"pending":       0,
				"last_activity": "",
			},
			"available": false,
		})
		return
	}

	if len(active) == 0 && len(recent) > 0 {
		active = filterActivePipelineInfos(recent)
	}
	if len(recent) > 0 && len(active) == 0 {
		active = filterActivePipelineInfos(recent)
	}

	activeResults := buildMobilePipelineResponses(active, branchAgents, mon.Pipeline.Detail)
	recentResults := buildMobilePipelineResponses(limitMobilePipelineInfos(recent, 10), branchAgents, nil)

	summary := summarizePipelineInfos(active, recent)

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"pipelines":        activeResults,
		"recent_pipelines": recentResults,
		"summary":          summary,
		"available":        true,
	})
}

func filterActivePipelineInfos(pipelines []bridge.PipelineInfo) []bridge.PipelineInfo {
	filtered := make([]bridge.PipelineInfo, 0, len(pipelines))
	for _, pipeline := range pipelines {
		if isActivePipelineStatus(pipeline.Status) {
			filtered = append(filtered, pipeline)
		}
	}
	return filtered
}

func limitMobilePipelineInfos(pipelines []bridge.PipelineInfo, limit int) []bridge.PipelineInfo {
	if limit <= 0 || len(pipelines) <= limit {
		out := make([]bridge.PipelineInfo, len(pipelines))
		copy(out, pipelines)
		return out
	}
	out := make([]bridge.PipelineInfo, limit)
	copy(out, pipelines[:limit])
	return out
}

func isActivePipelineStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "pending":
		return true
	default:
		return false
	}
}

type mobilePipelineSummaryDTO struct {
	Running      int    `json:"running"`
	Passed       int    `json:"passed"`
	Failed       int    `json:"failed"`
	Pending      int    `json:"pending"`
	LastActivity string `json:"last_activity,omitempty"`
}

func summarizePipelineInfos(active, recent []bridge.PipelineInfo) mobilePipelineSummaryDTO {
	seen := map[int]struct{}{}
	combined := make([]bridge.PipelineInfo, 0, len(active)+len(recent))
	for _, pipeline := range active {
		if _, ok := seen[pipeline.ID]; ok {
			continue
		}
		seen[pipeline.ID] = struct{}{}
		combined = append(combined, pipeline)
	}
	for _, pipeline := range recent {
		if _, ok := seen[pipeline.ID]; ok {
			continue
		}
		seen[pipeline.ID] = struct{}{}
		combined = append(combined, pipeline)
	}

	summary := mobilePipelineSummaryDTO{}
	var newest time.Time
	for _, pipeline := range combined {
		switch normalizeMobilePipelineStatus(pipeline.Status) {
		case "running":
			summary.Running++
		case "success":
			summary.Passed++
		case "pending":
			summary.Pending++
		default:
			summary.Failed++
		}
		if ts := parseMobilePipelineTime(pipeline.CreatedAt); ts.After(newest) {
			newest = ts
		}
		if ts := parseMobilePipelineTime(pipeline.UpdatedAt); ts.After(newest) {
			newest = ts
		}
	}
	if !newest.IsZero() {
		summary.LastActivity = relativeMobilePipelineTime(newest)
	}
	return summary
}

func normalizeMobilePipelineStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "running"
	case "success", "passed":
		return "success"
	case "pending", "created", "scheduled", "manual":
		return "pending"
	case "failed", "canceled", "cancelled", "skipped":
		return "failed"
	default:
		return "failed"
	}
}

func parseMobilePipelineTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts
	}
	return time.Time{}
}

func relativeMobilePipelineTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	diff := time.Since(ts)
	if diff < 0 {
		diff = 0
	}
	switch {
	case diff < 5*time.Second:
		return "just now"
	case diff < time.Minute:
		return strconv.Itoa(int(diff.Seconds())) + "s ago"
	case diff < time.Hour:
		return strconv.Itoa(int(diff.Minutes())) + "m ago"
	case diff < 24*time.Hour:
		return strconv.Itoa(int(diff.Hours())) + "h ago"
	default:
		return strconv.Itoa(int(diff.Hours()/24)) + "d ago"
	}
}

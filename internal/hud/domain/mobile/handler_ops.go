package mobile

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

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

func (d *MobileDomain) handleMobilePipelines(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	mon := d.deps.Monitors()
	if mon.Pipeline == nil {
		d.writeMobileJSON(w, http.StatusOK, map[string]any{
			"pipelines": []any{},
			"available": false,
		})
		return
	}

	pipelines := mon.Pipeline.Pipelines()

	// Build branch-to-agent lookup for pipeline-agent correlation.
	type agentRef struct {
		ID   string
		Type string
	}
	branchAgents := map[string]agentRef{}
	if fleet := mon.Fleet; fleet != nil {
		snap := fleet.Snapshot()
		for _, pa := range snap.Agents {
			if pa.Branch != "" {
				agentType := pa.AgentType
				if agentType == "" || agentType == "unknown" {
					agentType = inferAgentType(pa.AgentID)
				}
				branchAgents[pa.Branch] = agentRef{ID: pa.AgentID, Type: agentType}
			}
		}
	}

	type pipelineResponse struct {
		ID              int    `json:"id"`
		Project         string `json:"project"`
		Ref             string `json:"ref"`
		Status          string `json:"status"`
		Source          string `json:"source,omitempty"`
		CreatedAt       string `json:"created_at"`
		WebURL          string `json:"web_url,omitempty"`
		CurrentStage    string `json:"current_stage,omitempty"`
		CompletedStages int    `json:"completed_stages"`
		TotalStages     int    `json:"total_stages"`
		FailedJobCount  int    `json:"failed_job_count"`
		AgentID         string `json:"agent_id,omitempty"`
		AgentType       string `json:"agent_type,omitempty"`
	}

	results := make([]pipelineResponse, 0, len(pipelines))
	for _, p := range pipelines {
		resp := pipelineResponse{
			ID:        p.ID,
			Project:   p.Project,
			Ref:       p.Ref,
			Status:    p.Status,
			Source:    p.Source,
			CreatedAt: p.CreatedAt,
			WebURL:    p.WebURL,
		}
		if detail, err := mon.Pipeline.Detail(p.Project, p.ID); err == nil {
			resp.CurrentStage = detail.CurrentStage
			resp.CompletedStages = detail.CompletedStages
			resp.TotalStages = detail.TotalStages
			resp.FailedJobCount = detail.FailedJobCount
		}
		if ar, ok := branchAgents[p.Ref]; ok {
			resp.AgentID = ar.ID
			resp.AgentType = ar.Type
		}
		results = append(results, resp)
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"pipelines": results,
		"available": true,
	})
}

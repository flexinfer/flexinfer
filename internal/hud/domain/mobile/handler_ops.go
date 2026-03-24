package mobile

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

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
	var pipelineErr error
	includeDetail := true
	if len(pipelines) == 0 {
		if mon.Pipeline.Ready() {
			if err := mon.Pipeline.Refresh(); err == nil {
				pipelines = mon.Pipeline.Pipelines()
			} else {
				pipelineErr = err
			}
		}
		if len(pipelines) == 0 {
			if projects := mon.Pipeline.Projects(); len(projects) > 0 {
				if direct, err := d.deps.Agent().ListActivePipelines(projects); err == nil {
					pipelines = direct
				} else if pipelineErr == nil {
					pipelineErr = err
				}
			}
		}
	}
	if len(pipelines) == 0 && pipelineErr != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_unavailable", "failed to load pipelines")
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
	if len(pipelines) == 0 {
		if recent, err := d.deps.Agent().ListRecentPipelines(mon.Pipeline.Projects(), 10); err == nil {
			pipelines = selectMobileRelevantRecentPipelines(recent, branchAgents, 5)
			if len(pipelines) > 0 {
				includeDetail = false
			}
		} else if pipelineErr == nil {
			pipelineErr = err
		}
	}
	if len(pipelines) == 0 && pipelineErr != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_unavailable", "failed to load pipelines")
		return
	}
	var detailFn func(project string, pipelineID int) (*bridge.PipelineDetail, error)
	if includeDetail {
		detailFn = mon.Pipeline.Detail
	}
	results := buildMobilePipelineResponses(pipelines, branchAgents, detailFn)

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"pipelines": results,
		"available": true,
	})
}

func selectMobileRelevantRecentPipelines(pipelines []bridge.PipelineInfo, branchAgents map[string]mobilePipelineAgentRef, limit int) []bridge.PipelineInfo {
	if limit <= 0 {
		limit = 5
	}

	preferred := make([]bridge.PipelineInfo, 0, len(pipelines))
	fallback := make([]bridge.PipelineInfo, 0, len(pipelines))
	for _, pipeline := range pipelines {
		ref := strings.TrimSpace(pipeline.Ref)
		if ref == "" {
			fallback = append(fallback, pipeline)
			continue
		}
		if _, ok := branchAgents[ref]; ok || isAgentLikePipelineRef(ref) {
			preferred = append(preferred, pipeline)
			continue
		}
		fallback = append(fallback, pipeline)
	}
	slices.SortStableFunc(preferred, func(a, b bridge.PipelineInfo) int {
		return mobilePipelinePreferenceRank(strings.TrimSpace(b.Ref), branchAgents) -
			mobilePipelinePreferenceRank(strings.TrimSpace(a.Ref), branchAgents)
	})

	selected := preferred
	if len(selected) == 0 {
		selected = fallback
	}
	if len(selected) > limit {
		selected = selected[:limit]
	}
	return selected
}

func mobilePipelinePreferenceRank(ref string, branchAgents map[string]mobilePipelineAgentRef) int {
	if _, ok := branchAgents[ref]; ok {
		return 3
	}
	if isPlatformPipelineRef(ref) {
		return 2
	}
	if ref == "main" {
		return 1
	}
	return 0
}

func isAgentLikePipelineRef(ref string) bool {
	if ref == "main" {
		return true
	}
	return isPlatformPipelineRef(ref)
}

func isPlatformPipelineRef(ref string) bool {
	for _, prefix := range []string{"codex/", "claude/", "gemini/", "kilocode/", "antigravity/"} {
		if strings.HasPrefix(ref, prefix) {
			return true
		}
	}
	return false
}

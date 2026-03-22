package mobile

import (
	"net/http"
	"sort"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func (d *MobileDomain) handleMobilePresence(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	snap := d.deps.Monitors().Fleet.Snapshot()
	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))

	agents := make([]bridge.PresenceInfo, 0, len(snap.Agents))
	for _, agent := range snap.Agents {
		status := normalizeMobilePresenceStatus(agent.Status)
		if statusFilter != "" && status != statusFilter {
			continue
		}
		if agentFilter != "" && !strings.EqualFold(agent.AgentID, agentFilter) {
			continue
		}
		agent.Status = status
		agents = append(agents, agent)
	}
	sortSliceStable(agents, func(i, j int) bool {
		ti := parseMobileTime(agents[i].LastHeartbeat)
		tj := parseMobileTime(agents[j].LastHeartbeat)
		if ti.Equal(tj) {
			return agents[i].AgentID < agents[j].AgentID
		}
		return ti.After(tj)
	})
	if len(agents) > limit {
		agents = agents[:limit]
	}

	claims := make([]bridge.FileClaimInfo, 0, len(snap.FileClaims))
	for _, claim := range snap.FileClaims {
		if agentFilter != "" && !strings.EqualFold(claim.AgentID, agentFilter) {
			continue
		}
		claims = append(claims, claim)
	}
	sortSliceStable(claims, func(i, j int) bool {
		ti := parseMobileTime(claims[i].CreatedAt)
		tj := parseMobileTime(claims[j].CreatedAt)
		if ti.Equal(tj) {
			return claims[i].ID < claims[j].ID
		}
		return ti.After(tj)
	})
	if len(claims) > limit {
		claims = claims[:limit]
	}

	worktrees := make([]bridge.WorktreeInfo, 0, len(snap.Worktrees))
	for _, wt := range snap.Worktrees {
		if agentFilter != "" && !strings.EqualFold(wt.AgentID, agentFilter) {
			continue
		}
		worktrees = append(worktrees, wt)
	}
	sortSliceStable(worktrees, func(i, j int) bool {
		ti := parseMobileTime(worktrees[i].CreatedAt)
		tj := parseMobileTime(worktrees[j].CreatedAt)
		if ti.Equal(tj) {
			return worktrees[i].AssignmentID < worktrees[j].AssignmentID
		}
		return ti.After(tj)
	})
	if len(worktrees) > limit {
		worktrees = worktrees[:limit]
	}

	summary := presenceSummary{
		TotalAgents:   len(agents),
		ClaimCount:    len(claims),
		WorktreeCount: len(worktrees),
	}
	for _, agent := range agents {
		switch agent.Status {
		case "active":
			summary.ActiveAgents++
		case "idle":
			summary.IdleAgents++
		case "offline":
			summary.OfflineAgents++
		}
	}

	var spawns any = snap.Spawns
	if snap.Spawns == nil {
		spawns = []struct{}{}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"agents":    agents,
		"claims":    claims,
		"worktrees": worktrees,
		"spawns":    spawns,
		"summary":   summary,
		"coordination": map[string]any{
			"summary":          snap.Coordination.Summary,
			"attention_agents": limitMobileSlice(filterMobileCoordinationAgents(snap.Coordination.Agents, agentFilter, statusFilter), 8),
			"relations":        limitMobileSlice(filterMobileRelations(snap.Coordination.Relations, agentFilter), 10),
		},
	})
}

func (d *MobileDomain) handleMobileNamespaces(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	snap := d.deps.Monitors().Fleet.Snapshot()

	// Build agent status lookup from presence data.
	agentActive := map[string]bool{}
	for _, pa := range snap.Agents {
		agentActive[pa.AgentID] = normalizeMobilePresenceStatus(pa.Status) == "active"
	}

	type nsData struct {
		sessions int
		agents   map[string]bool // agentID -> isActive
	}
	nsMap := map[string]*nsData{}

	for _, sess := range snap.Sessions {
		if sess.Namespace == "" {
			continue
		}
		data, ok := nsMap[sess.Namespace]
		if !ok {
			data = &nsData{agents: map[string]bool{}}
			nsMap[sess.Namespace] = data
		}
		data.sessions++
		isActive := agentActive[sess.AgentID]
		if prev, seen := data.agents[sess.AgentID]; !seen || (!prev && isActive) {
			data.agents[sess.AgentID] = isActive
		}
	}

	results := make([]namespaceSummary, 0, len(nsMap))
	for ns, data := range nsMap {
		active := 0
		for _, a := range data.agents {
			if a {
				active++
			}
		}
		results = append(results, namespaceSummary{
			Namespace:    ns,
			SessionCount: data.sessions,
			AgentCount:   len(data.agents),
			ActiveAgents: active,
		})
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].ActiveAgents != results[j].ActiveAgents {
			return results[i].ActiveAgents > results[j].ActiveAgents
		}
		if results[i].SessionCount != results[j].SessionCount {
			return results[i].SessionCount > results[j].SessionCount
		}
		return results[i].Namespace < results[j].Namespace
	})

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"namespaces": results,
	})
}

func (d *MobileDomain) handleMobileAgents(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	snap := d.deps.Monitors().Fleet.Snapshot()
	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	typeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))

	agentMap := make(map[string]*unifiedAgent)

	for _, pa := range snap.Agents {
		status := normalizeMobilePresenceStatus(pa.Status)
		agentType := pa.AgentType
		if agentType == "" || agentType == "unknown" {
			agentType = inferAgentType(pa.AgentID)
		}
		ua := &unifiedAgent{
			AgentID:         pa.AgentID,
			AgentType:       agentType,
			Status:          status,
			Source:          "presence",
			Description:     pa.Description,
			CurrentTask:     pa.CurrentTask,
			Branch:          pa.Branch,
			LastHeartbeat:   pa.LastHeartbeat,
			SessionID:       pa.SessionID,
			ActiveFileCount: len(pa.ActiveFiles),
		}
		agentMap[pa.AgentID] = ua
	}

	for _, sess := range snap.Sessions {
		if ua, ok := agentMap[sess.AgentID]; ok {
			if ua.SessionID != "" && ua.SessionStatus == "active" && sess.Status != "active" {
				continue
			}
			ua.SessionID = sess.ID
			ua.Namespace = sess.Namespace
			ua.SessionStatus = sess.Status
			ua.SessionStarted = sess.StartedAt
			ua.EntryCount = sess.EntryCount
			ua.TotalTokens = sess.TotalTokens
			if ua.Description == "" {
				ua.Description = sess.Description
			}
		} else {
			status := "offline"
			if sess.Status == "active" {
				status = "active"
			}
			ua := &unifiedAgent{
				AgentID:        sess.AgentID,
				AgentType:      inferAgentType(sess.AgentID),
				Status:         status,
				Source:         "session_only",
				Description:    sess.Description,
				SessionID:      sess.ID,
				Namespace:      sess.Namespace,
				SessionStatus:  sess.Status,
				SessionStarted: sess.StartedAt,
				EntryCount:     sess.EntryCount,
				TotalTokens:    sess.TotalTokens,
			}
			agentMap[sess.AgentID] = ua
		}
	}

	for _, sp := range snap.Spawns {
		if ua, ok := agentMap[sp.AgentID]; ok {
			ua.SpawnID = sp.SpawnID
			ua.SpawnStatus = sp.Status
			ua.Project = sp.Project
			if ua.Branch == "" {
				ua.Branch = sp.Branch
			}
			if ua.AgentType == "unknown" || ua.AgentType == "" {
				ua.AgentType = sp.AgentType
			}
		} else {
			ua := &unifiedAgent{
				AgentID:     sp.AgentID,
				AgentType:   sp.AgentType,
				Status:      "active",
				Source:      "spawn",
				Description: sp.Task,
				Branch:      sp.Branch,
				SpawnID:     sp.SpawnID,
				SpawnStatus: sp.Status,
				Project:     sp.Project,
			}
			agentMap[sp.AgentID] = ua
		}
	}

	for _, ca := range snap.Coordination.Agents {
		if ua, ok := agentMap[ca.AgentID]; ok {
			ua.NeedsAttention = ca.NeedsAttention
			ua.AttentionReasons = ca.AttentionReasons
			ua.TaskCount = ca.TaskCount
			ua.BlockedTasks = ca.BlockedTasks
			ua.ClaimCount = ca.ClaimCount
		}
	}

	// Correlate pipelines by branch.
	if mon := d.deps.Monitors(); mon.Pipeline != nil {
		branchPipelines := map[string]struct {
			count  int
			status string
		}{}
		for _, p := range mon.Pipeline.Pipelines() {
			if p.Ref == "" {
				continue
			}
			bp := branchPipelines[p.Ref]
			bp.count++
			// Keep the most relevant status (running > pending > failed > success).
			if bp.status == "" || p.Status == "running" || (bp.status != "running" && p.Status == "failed") {
				bp.status = p.Status
			}
			branchPipelines[p.Ref] = bp
		}
		for _, ua := range agentMap {
			if ua.Branch == "" {
				continue
			}
			if bp, ok := branchPipelines[ua.Branch]; ok {
				ua.PipelineCount = bp.count
				ua.PipelineStatus = bp.status
			}
		}
	}

	agents := make([]unifiedAgent, 0, len(agentMap))
	for _, ua := range agentMap {
		if statusFilter != "" && ua.Status != statusFilter {
			continue
		}
		if typeFilter != "" && !strings.EqualFold(ua.AgentType, typeFilter) {
			continue
		}
		agents = append(agents, *ua)
	}

	statusOrder := map[string]int{"active": 0, "idle": 1, "offline": 2, "unknown": 3}
	sort.SliceStable(agents, func(i, j int) bool {
		oi, oj := statusOrder[agents[i].Status], statusOrder[agents[j].Status]
		if oi != oj {
			return oi < oj
		}
		ti := agentSortTime(agents[i])
		tj := agentSortTime(agents[j])
		if ti.Equal(tj) {
			return agents[i].AgentID < agents[j].AgentID
		}
		return ti.After(tj)
	})

	if len(agents) > limit {
		agents = agents[:limit]
	}

	summary := unifiedAgentsSummary{TotalAgents: len(agents)}
	for _, ua := range agents {
		switch ua.Status {
		case "active":
			summary.ActiveAgents++
		case "idle":
			summary.IdleAgents++
		case "offline":
			summary.OfflineAgents++
		}
		if ua.SpawnID != "" {
			summary.SpawnedAgents++
		}
		if ua.SessionID != "" {
			summary.WithSessions++
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"agents":  agents,
		"summary": summary,
	})
}

// handleMobileSessionActivity returns a unified view of session tasks, pipeline, and recent context.
func (d *MobileDomain) handleMobileSessionActivity(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	sessionID := strings.TrimSpace(r.PathValue("session_id"))
	if sessionID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "session_id is required")
		return
	}

	agent := d.deps.Agent()

	// Fetch tasks for this session.
	tasks, err := agent.Tasks(sessionID)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list session tasks")
		return
	}
	if tasks == nil {
		tasks = []bridge.TaskInfo{}
	}

	// Build task DTOs with pipeline/workflow info.
	type activityTaskDTO struct {
		ID         string   `json:"id"`
		Title      string   `json:"title"`
		Status     string   `json:"status"`
		Priority   string   `json:"priority"`
		Tags       []string `json:"tags,omitempty"`
		WorkflowID string   `json:"workflow_id,omitempty"`
		PipelineID int      `json:"pipeline_id,omitempty"`
		CreatedAt  string   `json:"created_at"`
		UpdatedAt  string   `json:"updated_at"`
	}

	taskDTOs := make([]activityTaskDTO, 0, len(tasks))
	for _, t := range tasks {
		dto := activityTaskDTO{
			ID:        t.ID,
			Title:     t.Title,
			Status:    normalizeMobileTaskStatus(t.Status),
			Priority:  normalizeMobilePriority(t.Priority),
			Tags:      t.Tags,
			CreatedAt: t.CreatedAt,
			UpdatedAt: t.UpdatedAt,
		}
		taskDTOs = append(taskDTOs, dto)
	}

	// Check if any active pipelines match this session's agent by branch.
	type pipelineSummaryDTO struct {
		ID             int    `json:"id"`
		Project        string `json:"project"`
		Ref            string `json:"ref"`
		Status         string `json:"status"`
		CurrentStage   string `json:"current_stage,omitempty"`
		FailedJobCount int    `json:"failed_job_count"`
		WebURL         string `json:"web_url,omitempty"`
	}

	var pipelines []pipelineSummaryDTO
	if mon := d.deps.Monitors(); mon.Pipeline != nil {
		for _, p := range mon.Pipeline.Pipelines() {
			detail, err := mon.Pipeline.Detail(p.Project, p.ID)
			if err != nil {
				continue
			}
			pipelines = append(pipelines, pipelineSummaryDTO{
				ID:             p.ID,
				Project:        p.Project,
				Ref:            p.Ref,
				Status:         p.Status,
				CurrentStage:   detail.CurrentStage,
				FailedJobCount: detail.FailedJobCount,
				WebURL:         p.WebURL,
			})
		}
	}
	if pipelines == nil {
		pipelines = []pipelineSummaryDTO{}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"session_id":     sessionID,
		"tasks":          taskDTOs,
		"pipelines":      pipelines,
		"task_count":     len(taskDTOs),
		"pipeline_count": len(pipelines),
	})
}

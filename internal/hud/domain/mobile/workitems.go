package mobile

import (
	"crypto/sha1"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

func buildMobileTaskFeed(explicit []bridge.TaskInfo, snap monitor.FleetSnapshot) []taskDTO {
	tasks := make([]taskDTO, 0, len(explicit)+len(snap.Agents))
	explicitKeys := make(map[string]struct{}, len(explicit))

	for _, task := range explicit {
		dto := MapMobileTask(task)
		tasks = append(tasks, dto)
		explicitKeys[mobileTaskIdentity(dto.AgentID, dto.SessionID, dto.Title)] = struct{}{}
	}

	tasks = append(tasks, projectMobileTasks(snap, explicitKeys)...)
	sortMobileTasks(tasks)
	return tasks
}

func summarizeMobileTaskCounts(tasks []taskDTO) taskCounts {
	counts := taskCounts{}
	for _, task := range tasks {
		switch normalizeMobileTaskStatus(task.Status) {
		case "pending":
			counts.Pending++
		case "in_progress":
			counts.InProgress++
		case "blocked":
			counts.Blocked++
		case "completed":
			counts.Completed++
		}
	}
	return counts
}

func summarizeMobileTaskCountsByAgent(tasks []taskDTO) map[string]taskCounts {
	counts := make(map[string]taskCounts)
	for _, task := range tasks {
		agentID := strings.TrimSpace(task.AgentID)
		if agentID == "" {
			continue
		}
		next := counts[agentID]
		switch normalizeMobileTaskStatus(task.Status) {
		case "pending":
			next.Pending++
		case "in_progress":
			next.InProgress++
		case "blocked":
			next.Blocked++
		case "completed":
			next.Completed++
		}
		counts[agentID] = next
	}
	return counts
}

func filterMobileTasks(tasks []taskDTO, statusFilter, agentFilter, sessionFilter, searchFilter string) []taskDTO {
	filtered := make([]taskDTO, 0, len(tasks))
	for _, task := range tasks {
		if !mobileTaskMatchesFilters(task, statusFilter, agentFilter, sessionFilter, searchFilter) {
			continue
		}
		filtered = append(filtered, task)
	}
	return filtered
}

func mobileTaskMatchesFilters(task taskDTO, statusFilter, agentFilter, sessionFilter, searchFilter string) bool {
	if statusFilter != "" && normalizeMobileTaskStatus(task.Status) != statusFilter {
		return false
	}
	if agentFilter != "" && !strings.EqualFold(strings.TrimSpace(task.AgentID), agentFilter) {
		return false
	}
	if sessionFilter != "" && !strings.EqualFold(strings.TrimSpace(task.SessionID), sessionFilter) {
		return false
	}
	if searchFilter == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		task.ID,
		task.SessionID,
		task.AgentID,
		task.Namespace,
		task.Project,
		task.Title,
		task.Context,
		task.TaskKind,
		task.SourcePlatform,
		task.SourceID,
		task.WorkflowID,
		task.PipelineRefString(),
	}, " "))
	return strings.Contains(haystack, searchFilter)
}

func projectMobileTasks(snap monitor.FleetSnapshot, explicitKeys map[string]struct{}) []taskDTO {
	sessionByID := make(map[string]bridge.SessionInfo, len(snap.Sessions))
	sessionByAgent := make(map[string]bridge.SessionInfo, len(snap.Sessions))
	for _, sess := range snap.Sessions {
		if sess.ID != "" {
			sessionByID[sess.ID] = sess
		}
		if sess.AgentID == "" {
			continue
		}
		current, ok := sessionByAgent[sess.AgentID]
		if !ok || sessionMoreRelevant(sess, current) {
			sessionByAgent[sess.AgentID] = sess
		}
	}

	seen := make(map[string]struct{}, len(snap.Agents))
	projected := make([]taskDTO, 0, len(snap.Agents))
	for _, agent := range snap.Agents {
		status := normalizeMobilePresenceStatus(agent.Status)
		if status == "offline" {
			continue
		}

		title := strings.TrimSpace(agent.CurrentTask)
		if title == "" {
			continue
		}

		session := sessionByID[strings.TrimSpace(agent.SessionID)]
		if session.ID == "" {
			session = sessionByAgent[agent.AgentID]
		}
		sessionID := strings.TrimSpace(session.ID)
		identity := mobileTaskIdentity(agent.AgentID, sessionID, title)
		if _, ok := explicitKeys[identity]; ok {
			continue
		}
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}

		agentType := strings.TrimSpace(agent.AgentType)
		if agentType == "" || agentType == "unknown" {
			agentType = inferAgentType(agent.AgentID)
		}
		projected = append(projected, taskDTO{
			ID:             projectedTaskID(agent.AgentID, sessionID, title),
			SessionID:      sessionID,
			AgentID:        agent.AgentID,
			Namespace:      session.Namespace,
			Project:        bridge.CanonicalProject(session.Project, session.Namespace, session.PipelineRef),
			Title:          title,
			Context:        strings.TrimSpace(agent.Description),
			Priority:       "medium",
			Status:         "in_progress",
			TaskKind:       "projected",
			SourcePlatform: normalizeMobileSourcePlatform(agentType),
			SourceID:       identity,
			SourceKind:     "projected",
			NativeKey:      identity,
			IsProjected:    true,
			Tags:           []string{},
			BlockedBy:      []string{},
			CreatedAt:      chooseFirstNonEmpty(agent.LastHeartbeat, ""),
			UpdatedAt:      chooseFirstNonEmpty(agent.LastHeartbeat, ""),
		})
	}

	return projected
}

func sortMobileTasks(tasks []taskDTO) {
	sort.SliceStable(tasks, func(i, j int) bool {
		ti := parseMobileTime(tasks[i].UpdatedAt)
		tj := parseMobileTime(tasks[j].UpdatedAt)
		if ti.Equal(tj) {
			ki := mobileTaskKindOrder(tasks[i].TaskKind)
			kj := mobileTaskKindOrder(tasks[j].TaskKind)
			if ki != kj {
				return ki < kj
			}
			if tasks[i].Title != tasks[j].Title {
				return tasks[i].Title < tasks[j].Title
			}
			return tasks[i].ID < tasks[j].ID
		}
		return ti.After(tj)
	})
}

func mobileTaskKindOrder(kind string) int {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "explicit":
		return 0
	case "projected":
		return 1
	default:
		return 2
	}
}

func mobileTaskIdentity(agentID, sessionID, title string) string {
	return strings.ToLower(strings.TrimSpace(agentID)) + "\x00" +
		strings.ToLower(strings.TrimSpace(sessionID)) + "\x00" +
		normalizeMobileTaskTitle(title)
}

func normalizeMobileTaskTitle(title string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
}

func projectedTaskID(agentID, sessionID, title string) string {
	sum := sha1.Sum([]byte(mobileTaskIdentity(agentID, sessionID, title)))
	return "proj_" + hex.EncodeToString(sum[:8])
}

func normalizeMobileSourcePlatform(agentType string) string {
	platform := strings.ToLower(strings.TrimSpace(agentType))
	platform = strings.ReplaceAll(platform, "_", "-")
	switch {
	case platform == "":
		return "unknown"
	case strings.Contains(platform, "claude"):
		return "claude"
	case strings.Contains(platform, "codex"):
		return "codex"
	case strings.Contains(platform, "gemini"):
		return "gemini"
	case strings.Contains(platform, "proxy"):
		return "proxy"
	case strings.Contains(platform, "agent-context"):
		return "agent_context"
	default:
		return platform
	}
}

func sessionMoreRelevant(candidate, current bridge.SessionInfo) bool {
	if candidate.Status != current.Status {
		return candidate.Status == "active"
	}
	candidateStarted := parseMobileTime(candidate.StartedAt)
	currentStarted := parseMobileTime(current.StartedAt)
	if !candidateStarted.Equal(currentStarted) {
		return candidateStarted.After(currentStarted)
	}
	return candidate.ID < current.ID
}

func (t taskDTO) PipelineRefString() string {
	if t.PipelineRef == nil {
		return ""
	}
	return strings.Join([]string{
		strings.TrimSpace(t.PipelineRef.Project),
		strconv.Itoa(t.PipelineRef.ID),
		strings.TrimSpace(t.PipelineRef.Ref),
		strings.TrimSpace(t.PipelineRef.WebURL),
	}, " ")
}

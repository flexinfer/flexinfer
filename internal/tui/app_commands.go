package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/crb2nu/loom/internal/tui/panels"
)

// batchDataMsg carries all panel data in a single message.
// The Update loop unpacks it and routes to individual panels.
type batchDataMsg struct {
	overview panels.MsgOverviewData
	fleet    panels.MsgFleetData
	health   panels.MsgHealthData
	tasks    panels.MsgTasksData
	memory   panels.MsgMemoryData
	stream   panels.MsgStreamData
	presence panels.MsgPresenceData
}

// tickCmd returns a command that sends a tick after the refresh interval.
func (m Model) tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return msgTick(t)
	})
}

// fetchAll creates a command that fetches data from all monitors and
// dispatches the results as panel data messages.
func (m Model) fetchAll() tea.Cmd {
	return func() tea.Msg {
		// Snapshot all monitors (thread-safe reads).
		snap := m.client.FleetSnapshot()
		servers := m.client.Servers()
		memStats := m.client.MemoryStats()
		entries := m.client.StreamEntries()

		// Build fleet data.
		fleetSessions := make([]panels.SessionData, len(snap.Sessions))
		for i, s := range snap.Sessions {
			fleetSessions[i] = panels.SessionData{
				ID:          s.ID,
				AgentID:     s.AgentID,
				Namespace:   s.Namespace,
				Status:      s.Status,
				Description: s.Description,
				StartedAt:   s.StartedAt,
				TokenCount:  s.TotalTokens,
				EntryCount:  s.EntryCount,
			}
		}
		fleetAgents := make([]panels.AgentData, len(snap.Agents))
		for i, a := range snap.Agents {
			fleetAgents[i] = panels.AgentData{
				AgentID:       a.AgentID,
				SessionID:     a.SessionID,
				Status:        a.Status,
				AgentType:     a.AgentType,
				Description:   a.Description,
				CurrentTask:   a.CurrentTask,
				Branch:        a.Branch,
				LastHeartbeat: a.LastHeartbeat,
			}
		}
		fleetAgentCoordination := make([]panels.FleetAgentCoordination, len(snap.Coordination.Agents))
		presenceAgentCoordination := make([]panels.PresenceAgentCoordination, len(snap.Coordination.Agents))
		for i, coord := range snap.Coordination.Agents {
			fleetAgentCoordination[i] = panels.FleetAgentCoordination{
				AgentID:          coord.AgentID,
				ClaimCount:       coord.ClaimCount,
				ConflictFiles:    coord.ConflictFiles,
				BlockingOthers:   coord.BlockingOthers,
				BlockedByOthers:  coord.BlockedByOthers,
				AttentionReasons: coord.AttentionReasons,
				NeedsAttention:   coord.NeedsAttention,
			}
			presenceAgentCoordination[i] = panels.PresenceAgentCoordination{
				AgentID:          coord.AgentID,
				TaskCount:        coord.TaskCount,
				BlockedTasks:     coord.BlockedTasks,
				ClaimCount:       coord.ClaimCount,
				ConflictFiles:    coord.ConflictFiles,
				BlockingOthers:   coord.BlockingOthers,
				BlockedByOthers:  coord.BlockedByOthers,
				AttentionReasons: coord.AttentionReasons,
				NeedsAttention:   coord.NeedsAttention,
			}
		}
		namespaceCoordination := make([]panels.FleetNamespaceCoordination, len(snap.Coordination.Namespaces))
		for i, coord := range snap.Coordination.Namespaces {
			namespaceCoordination[i] = panels.FleetNamespaceCoordination{
				Namespace:          coord.Namespace,
				BlockedTasks:       coord.BlockedTasks,
				OrphanTasks:        coord.OrphanTasks,
				ConflictFiles:      coord.ConflictFiles,
				SharedBranches:     coord.SharedBranches,
				CrossAgentBlockers: coord.CrossAgentBlockers,
				AttentionScore:     coord.AttentionScore,
				AttentionReasons:   coord.AttentionReasons,
				NeedsAttention:     coord.NeedsAttention,
			}
		}

		// Build health data.
		healthServers := make([]panels.ServerData, len(servers))
		for i, s := range servers {
			healthServers[i] = panels.ServerData{
				Name:           s.Name,
				Running:        s.Running,
				Healthy:        s.Healthy,
				Latency:        s.AvgLatencyMs,
				LatencyHistory: s.LatencyHistory,
				ConsecFails:    s.ConsecFails,
				Error:          s.ErrorMessage,
			}
		}

		// Build memory data.
		var memData panels.MsgMemoryData
		if memStats != nil {
			memData = panels.MsgMemoryData{
				WorkingItems:       memStats.WorkingMemory.Items,
				WorkingTokens:      memStats.WorkingMemory.Tokens,
				ShortItems:         memStats.ShortTermMemory.Items,
				ShortTokens:        memStats.ShortTermMemory.Tokens,
				LongItems:          memStats.LongTermMemory.Items,
				LongTokens:         memStats.LongTermMemory.Tokens,
				TotalItems:         memStats.TotalItems,
				TotalTokens:        memStats.TotalTokens,
				History:            m.client.MemoryTokenHistory(),
				CompressionRatio:   memStats.CompressionRatio,
				ItemsAdded24h:      memStats.ItemsAddedLast24h,
				ItemsCompressed24h: memStats.ItemsCompressedLast24h,
			}
		}

		// Build tasks data.
		tasksList := make([]panels.TaskData, len(snap.Tasks))
		for i, t := range snap.Tasks {
			tasksList[i] = panels.TaskData{
				ID:        t.ID,
				Title:     t.Title,
				Status:    t.Status,
				Priority:  t.Priority,
				AgentID:   t.AgentID,
				Namespace: t.Namespace,
				BlockedBy: t.BlockedBy,
			}
		}
		taskBlockers := make([]panels.TaskBlockerData, len(snap.Coordination.Blockers))
		for i, blocker := range snap.Coordination.Blockers {
			taskBlockers[i] = panels.TaskBlockerData{
				TaskID:             blocker.TaskID,
				TaskTitle:          blocker.TaskTitle,
				TaskAgentID:        blocker.TaskAgentID,
				TaskNamespace:      blocker.TaskNamespace,
				BlockedByTaskID:    blocker.BlockedByTaskID,
				BlockedByTaskTitle: blocker.BlockedByTaskTitle,
				BlockedByStatus:    blocker.BlockedByStatus,
				BlockedByAgentID:   blocker.BlockedByAgentID,
				BlockedByNamespace: blocker.BlockedByNamespace,
				CrossAgent:         blocker.CrossAgent,
				Resolved:           blocker.Resolved,
			}
		}

		// Build stream data.
		streamEntries := make([]panels.StreamEntryData, len(entries))
		for i, e := range entries {
			streamEntries[i] = panels.StreamEntryData{
				ID:        e.ID,
				EntryType: e.EntryType,
				AgentID:   e.AgentID,
				Namespace: e.Namespace,
				Title:     e.Title,
				Timestamp: e.Timestamp,
			}
		}

		// Build presence data.
		presenceAgents := make([]panels.PresenceAgentData, len(snap.Agents))
		for i, a := range snap.Agents {
			presenceAgents[i] = panels.PresenceAgentData{
				AgentID:       a.AgentID,
				Status:        a.Status,
				AgentType:     a.AgentType,
				Description:   a.Description,
				CurrentTask:   a.CurrentTask,
				Branch:        a.Branch,
				LastHeartbeat: a.LastHeartbeat,
			}
		}
		presenceClaims := make([]panels.ClaimData, len(snap.FileClaims))
		for i, c := range snap.FileClaims {
			presenceClaims[i] = panels.ClaimData{
				FilePath:  c.FilePath,
				AgentID:   c.AgentID,
				ClaimType: c.ClaimType,
				Reason:    c.Reason,
				CreatedAt: c.CreatedAt,
			}
		}
		presenceWorktrees := make([]panels.WorktreeData, len(snap.Worktrees))
		for i, w := range snap.Worktrees {
			presenceWorktrees[i] = panels.WorktreeData{
				Branch:    w.Branch,
				AgentID:   w.AgentID,
				Status:    w.Status,
				Purpose:   w.Purpose,
				CreatedAt: w.CreatedAt,
			}
		}

		// Build overview data by aggregating across monitors.
		healthyCount := 0
		downCount := 0
		for _, s := range servers {
			if s.Healthy {
				healthyCount++
			} else if s.Running && !s.Healthy {
				downCount++
			}
		}
		conflictCount := snap.Coordination.Summary.ConflictFiles
		overviewData := panels.MsgOverviewData{
			DaemonRunning:  snap.DaemonRunning,
			ServerCount:    snap.ServerCount,
			HealthyServers: healthyCount,
			DownServers:    downCount,
			ActiveSessions: snap.ActiveSessions,
			ActiveAgents:   snap.ActiveAgents,
			IdleAgents:     snap.IdleAgents,
			TotalTokens:    snap.TotalTokens,
			PendingTasks:   snap.PendingTasks,
			ActiveTasks:    snap.ActiveTasks,
			BlockedTasks:   snap.BlockedTasks,
			StreamEntries:  len(entries),
			Conflicts:      conflictCount,
			Worktrees:      len(snap.Worktrees),
			MemoryHistory:  m.client.MemoryTokenHistory(),
		}
		if memStats != nil {
			overviewData.MemoryItems = memStats.TotalItems
			overviewData.MemoryTokens = memStats.TotalTokens
		}

		// Return a batch message. We use a wrapper to send multiple messages.
		return batchDataMsg{
			overview: overviewData,
			fleet: panels.MsgFleetData{
				DaemonRunning:          snap.DaemonRunning,
				ServerCount:            snap.ServerCount,
				Sessions:               fleetSessions,
				ActiveSessions:         snap.ActiveSessions,
				Agents:                 fleetAgents,
				AgentCoordination:      fleetAgentCoordination,
				NamespaceCoordination:  namespaceCoordination,
				TotalTokens:            snap.TotalTokens,
				NamespacesAtRisk:       snap.Coordination.Summary.NamespacesAtRisk,
				AgentsNeedingAttention: snap.Coordination.Summary.AgentsNeedingAttention,
				SharedBranches:         snap.Coordination.Summary.SharedBranches,
				ConflictFiles:          snap.Coordination.Summary.ConflictFiles,
				OrphanTasks:            snap.Coordination.Summary.OrphanTasks,
				UpdatedAt:              snap.UpdatedAt,
			},
			health: panels.MsgHealthData{Servers: healthServers},
			tasks: panels.MsgTasksData{
				Tasks:              tasksList,
				PendingCount:       snap.PendingTasks,
				ActiveCount:        snap.ActiveTasks,
				BlockedCount:       snap.BlockedTasks,
				CrossAgentBlockers: snap.Coordination.Summary.CrossAgentBlockers,
				OrphanTasks:        snap.Coordination.Summary.OrphanTasks,
				Blockers:           taskBlockers,
			},
			memory: memData,
			stream: panels.MsgStreamData{Entries: streamEntries},
			presence: panels.MsgPresenceData{
				Agents:            presenceAgents,
				AgentCoordination: presenceAgentCoordination,
				Claims:            presenceClaims,
				Worktrees:         presenceWorktrees,
				ActiveAgents:      snap.ActiveAgents,
				IdleAgents:        snap.IdleAgents,
				TotalClaims:       len(snap.FileClaims),
				SharedBranches:    snap.Coordination.Summary.SharedBranches,
				IdleClaimHolders:  snap.Coordination.Summary.IdleClaimHolders,
			},
		}
	}
}

// fetchMemoryItems fetches items for a memory tier and dispatches them as a panel message.
func (m Model) fetchMemoryItems(tier string) tea.Cmd {
	return func() tea.Msg {
		items := m.client.MemoryItems(tier)
		result := make([]panels.MemoryItemData, len(items))
		for i, item := range items {
			result[i] = panels.MemoryItemData{
				ID:         item.ID,
				Title:      item.Title,
				Tier:       item.Tier,
				Importance: item.Importance,
				Tokens:     item.Tokens,
			}
		}
		return panels.MsgMemoryItems{
			Tier:  tier,
			Items: result,
		}
	}
}

// updateTaskStatus sends a status update to the daemon and triggers a refresh.
func (m Model) updateTaskStatus(taskID, status string) tea.Cmd {
	return func() tea.Msg {
		if err := m.client.UpdateTaskStatus(taskID, status); err != nil {
			// Best effort — refresh anyway.
			_ = err
		}
		return msgRefreshDone{}
	}
}

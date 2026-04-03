package hud

import (
	"time"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

// spawnAdapter adapts SpawnOrchestrator to the monitor.SpawnLister interface.
type spawnAdapter struct {
	orch *SpawnOrchestrator
}

func (a spawnAdapter) ListSpawnInfos() []monitor.SpawnInfo {
	states := a.orch.ListSpawns()
	infos := make([]monitor.SpawnInfo, 0, len(states))
	for _, state := range states {
		info := monitor.SpawnInfo{
			SpawnID:   state.SpawnID,
			AgentID:   state.AgentID,
			PodName:   state.PodName,
			Status:    string(state.Status),
			Project:   state.Request.Project,
			Branch:    state.Request.Branch,
			Task:      state.Request.TaskDescription,
			AgentType: state.Request.AgentType,
			StartedAt: state.StartedAt.Format(time.RFC3339),
		}
		if state.EndedAt != nil {
			info.EndedAt = state.EndedAt.Format(time.RFC3339)
		}
		info.Error = state.Error
		infos = append(infos, info)
	}
	return infos
}

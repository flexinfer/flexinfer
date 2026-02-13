package hud

import (
	"net/http"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

// TopologyNode represents an agent in the topology graph.
type TopologyNode struct {
	AgentID     string `json:"agent_id"`
	Status      string `json:"status"`
	AgentType   string `json:"agent_type"`
	CurrentTask string `json:"current_task,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRUrl       string `json:"pr_url,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
}

// TopologyEdge represents a relationship between two agents.
type TopologyEdge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	EdgeType string `json:"edge_type"` // handoff, shared_file, shared_branch
	Weight   int    `json:"weight"`
	Label    string `json:"label,omitempty"`
	Status   string `json:"status,omitempty"`
}

// TopologyCluster groups agents by namespace project.
type TopologyCluster struct {
	Project  string   `json:"project"`
	AgentIDs []string `json:"agent_ids"`
}

// TopologyGraph is the complete topology response.
type TopologyGraph struct {
	Nodes    []TopologyNode    `json:"nodes"`
	Edges    []TopologyEdge    `json:"edges"`
	Clusters []TopologyCluster `json:"clusters"`
}

// topologyCache caches the computed topology for 5 seconds.
type topologyCache struct {
	mu       sync.Mutex
	graph    TopologyGraph
	snapTime time.Time
	validAt  time.Time
}

var topoCache topologyCache

// handleTopology returns the agent topology graph.
// GET /api/topology
func (a *App) handleTopology(w http.ResponseWriter, _ *http.Request) {
	snap := a.fleetMonitor.Snapshot()

	topoCache.mu.Lock()
	defer topoCache.mu.Unlock()

	// Reuse cached result if snapshot hasn't changed and cache is <5s old.
	if !topoCache.validAt.IsZero() &&
		time.Since(topoCache.validAt) < 5*time.Second &&
		!snap.UpdatedAt.After(topoCache.snapTime) {
		a.writeJSON(w, http.StatusOK, topoCache.graph)
		return
	}

	graph := computeTopology(snap, a)
	topoCache.graph = graph
	topoCache.snapTime = snap.UpdatedAt
	topoCache.validAt = time.Now()

	a.writeJSON(w, http.StatusOK, graph)
}

// computeTopology builds the topology graph from fleet snapshot data.
func computeTopology(snap monitor.FleetSnapshot, a *App) TopologyGraph {
	graph := TopologyGraph{}

	// Build session lookup for namespace resolution.
	sessionNS := make(map[string]string) // agent_id -> namespace
	for _, s := range snap.Sessions {
		if s.Status == "active" {
			sessionNS[s.AgentID] = s.Namespace
		}
	}

	// Nodes: one per agent.
	for _, ag := range snap.Agents {
		ns := sessionNS[ag.AgentID]
		graph.Nodes = append(graph.Nodes, TopologyNode{
			AgentID:     ag.AgentID,
			Status:      ag.Status,
			AgentType:   ag.AgentType,
			CurrentTask: ag.CurrentTask,
			Branch:      ag.Branch,
			PRUrl:       ag.PRUrl,
			Namespace:   ns,
		})
	}

	if len(graph.Nodes) == 0 {
		return graph
	}

	// Edge dedup key.
	type edgeKey struct{ a, b, t string }
	edgeWeights := make(map[edgeKey]int)
	edgeLabels := make(map[edgeKey]string)
	edgeStatuses := make(map[edgeKey]string)

	// Handoff edges.
	if a != nil {
		handoffs, err := a.agent.HandoffList()
		if err == nil {
			for _, h := range handoffs {
				if h.FromAgent != "" && h.ToAgent != "" {
					k := edgeKey{h.FromAgent, h.ToAgent, "handoff"}
					edgeWeights[k]++
					edgeLabels[k] = h.Summary
					edgeStatuses[k] = h.Status
				}
			}
		}
	}

	// Shared file edges: agents claiming the same file path.
	fileAgents := make(map[string][]string)
	for _, c := range snap.FileClaims {
		fileAgents[c.FilePath] = appendUnique(fileAgents[c.FilePath], c.AgentID)
	}
	for _, agents := range fileAgents {
		if len(agents) < 2 {
			continue
		}
		for i := 0; i < len(agents); i++ {
			for j := i + 1; j < len(agents); j++ {
				a, b := agents[i], agents[j]
				if a > b {
					a, b = b, a
				}
				k := edgeKey{a, b, "shared_file"}
				edgeWeights[k]++
			}
		}
	}

	// Shared branch edges: agents on the same branch.
	branchAgents := make(map[string][]string)
	for _, ag := range snap.Agents {
		if ag.Branch != "" && ag.Status != "offline" {
			branchAgents[ag.Branch] = appendUnique(branchAgents[ag.Branch], ag.AgentID)
		}
	}
	for _, agents := range branchAgents {
		if len(agents) < 2 {
			continue
		}
		for i := 0; i < len(agents); i++ {
			for j := i + 1; j < len(agents); j++ {
				a, b := agents[i], agents[j]
				if a > b {
					a, b = b, a
				}
				k := edgeKey{a, b, "shared_branch"}
				edgeWeights[k]++
			}
		}
	}

	// Flatten edges.
	for k, w := range edgeWeights {
		graph.Edges = append(graph.Edges, TopologyEdge{
			Source:   k.a,
			Target:   k.b,
			EdgeType: k.t,
			Weight:   w,
			Label:    edgeLabels[k],
			Status:   edgeStatuses[k],
		})
	}

	// Clusters: group agents by namespace project.
	projectAgents := make(map[string][]string)
	for _, n := range graph.Nodes {
		proj := extractProjectFromNS(n.Namespace)
		projectAgents[proj] = append(projectAgents[proj], n.AgentID)
	}
	for proj, ids := range projectAgents {
		graph.Clusters = append(graph.Clusters, TopologyCluster{
			Project:  proj,
			AgentIDs: ids,
		})
	}

	return graph
}

// extractProjectFromNS extracts the project name from a namespace string.
func extractProjectFromNS(ns string) string {
	if ns == "" {
		return "(ungrouped)"
	}
	for i, c := range ns {
		if c == '/' {
			if i > 0 {
				return ns[:i]
			}
			return "(ungrouped)"
		}
	}
	return ns
}

// appendUnique appends s to the slice only if not already present.
func appendUnique(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

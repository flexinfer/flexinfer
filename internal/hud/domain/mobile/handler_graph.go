package mobile

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func (d *MobileDomain) handleMobileTopology(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	graph := d.deps.ComputeTopology(d.deps.Monitors().Fleet.Snapshot())

	nodes := make([]topologyNodeDTO, len(graph.Nodes))
	for i, node := range graph.Nodes {
		nodes[i] = topologyNodeDTO{
			AgentID:     node.AgentID,
			Status:      normalizeMobilePresenceStatus(node.Status),
			AgentType:   node.AgentType,
			CurrentTask: node.CurrentTask,
			Branch:      node.Branch,
			PRURL:       node.PRUrl,
			Namespace:   node.Namespace,
		}
	}
	edges := make([]topologyEdgeDTO, len(graph.Edges))
	for i, edge := range graph.Edges {
		edges[i] = topologyEdgeDTO(edge)
	}
	clusters := make([]topologyClusterDTO, len(graph.Clusters))
	for i, cluster := range graph.Clusters {
		agentIDs := cluster.AgentIDs
		if agentIDs == nil {
			agentIDs = []string{}
		}
		clusters[i] = topologyClusterDTO{
			Project:  cluster.Project,
			AgentIDs: agentIDs,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"nodes":      nodes,
		"edges":      edges,
		"clusters":   clusters,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func (d *MobileDomain) handleMobileGraphStats(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}
	stats, err := d.deps.Agent().GraphStats()
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to load graph stats")
		return
	}
	if stats.EntityTypes == nil {
		stats.EntityTypes = map[string]int{}
	}
	if stats.RelationTypes == nil {
		stats.RelationTypes = map[string]int{}
	}
	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"stats": map[string]any{
			"total_entities":  stats.EntityCount,
			"total_relations": stats.RelationCount,
			"entity_types":    stats.EntityTypes,
			"relation_types":  stats.RelationTypes,
		},
	})
}

func (d *MobileDomain) handleMobileGraphEntities(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	limit := parseMobileLimit(r, DefaultLimit, MaxLimit)
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	entityType := strings.TrimSpace(r.URL.Query().Get("type"))

	entities, err := d.deps.Agent().EntityFind(query, entityType, limit)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to list entities")
		return
	}
	if entities == nil {
		entities = []bridge.EntityInfo{}
	}

	sortSliceStable(entities, func(i, j int) bool {
		ni := strings.ToLower(strings.TrimSpace(entities[i].Name))
		nj := strings.ToLower(strings.TrimSpace(entities[j].Name))
		if ni == nj {
			return entities[i].ID < entities[j].ID
		}
		return ni < nj
	})

	result := make([]graphEntityDTO, len(entities))
	for i, entity := range entities {
		entityKind := strings.TrimSpace(chooseFirstNonEmpty(entity.EntityType, entity.Type))
		if entityKind == "" {
			entityKind = "unknown"
		}
		props := entity.Properties
		if props == nil {
			props = map[string]any{}
		}
		result[i] = graphEntityDTO{
			ID:          entity.ID,
			Name:        entity.Name,
			EntityType:  entityKind,
			Description: entity.Description,
			Namespace:   entity.Namespace,
			Properties:  props,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{"entities": result})
}

func (d *MobileDomain) handleMobileGraphPath(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}
	sourceID := strings.TrimSpace(r.URL.Query().Get("source_id"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	if sourceID == "" || targetID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "source_id and target_id are required")
		return
	}

	maxDepth := 5
	if raw := strings.TrimSpace(r.URL.Query().Get("max_depth")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			d.writeMobileError(w, http.StatusBadRequest, "bad_request", "max_depth must be a positive integer")
			return
		}
		if parsed > 20 {
			parsed = 20
		}
		maxDepth = parsed
	}

	path, err := d.deps.Agent().GraphFindPath(sourceID, targetID, maxDepth)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "upstream_error", "failed to compute graph path")
		return
	}
	if path == nil {
		path = []bridge.EntityInfo{}
	}

	nodes := make([]graphEntityDTO, len(path))
	for i, entity := range path {
		entityKind := strings.TrimSpace(chooseFirstNonEmpty(entity.EntityType, entity.Type))
		if entityKind == "" {
			entityKind = "unknown"
		}
		props := entity.Properties
		if props == nil {
			props = map[string]any{}
		}
		nodes[i] = graphEntityDTO{
			ID:          entity.ID,
			Name:        entity.Name,
			EntityType:  entityKind,
			Description: entity.Description,
			Namespace:   entity.Namespace,
			Properties:  props,
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"path": map[string]any{
			"nodes":  nodes,
			"length": len(nodes),
		},
	})
}

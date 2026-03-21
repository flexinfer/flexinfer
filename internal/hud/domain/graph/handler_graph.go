package graph

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

func (d *GraphDomain) handleGraphStats(w http.ResponseWriter, _ *http.Request) {
	if cached, ok := d.deps.CacheGet("graph_stats"); ok {
		d.deps.WriteJSON(w, http.StatusOK, cached)
		return
	}
	stats, err := d.deps.Agent().GraphStats()
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to get graph stats", err)
		return
	}
	d.deps.CacheSet("graph_stats", stats, 10*time.Second)
	d.deps.WriteJSON(w, http.StatusOK, stats)
}

func (d *GraphDomain) handleGraphEntities(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	entityType := r.URL.Query().Get("type")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	entities, err := d.deps.Agent().EntityFind(query, entityType, limit)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to find entities", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"entities": entities})
}

func (d *GraphDomain) handleContextStream(w http.ResponseWriter, r *http.Request) {
	var since time.Time
	if s := r.URL.Query().Get("since"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			since = parsed
		}
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	entries, err := d.deps.Agent().ContextStream(since, limit)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to get context stream", err)
		return
	}

	// Flatten ContextEntryInfo (score + nested entry) into the flat shape
	// the frontend expects: {id, entry_type, agent_id, agent, namespace, title, timestamp, score}.
	flat := make([]map[string]any, len(entries))
	for i, e := range entries {
		flat[i] = map[string]any{
			"id":         e.Entry.ID,
			"entry_type": e.Entry.EntryType,
			"agent_id":   e.Entry.AgentID,
			"agent":      e.Entry.AgentID,
			"namespace":  e.Entry.Namespace,
			"title":      e.Entry.Title,
			"content":    e.Entry.Content,
			"timestamp":  e.Entry.Timestamp,
			"score":      e.Score,
		}
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"entries": flat})
}

func (d *GraphDomain) handleGraphEntityDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing entity id", nil)
		return
	}
	detail, err := d.deps.Agent().EntityGet(id)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to get entity", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, detail)
}

func (d *GraphDomain) handleGraphEntityCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string         `json:"name"`
		EntityType string         `json:"entity_type"`
		Namespace  string         `json:"namespace"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Name == "" || body.EntityType == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "name and entity_type are required", nil)
		return
	}
	if err := d.deps.Agent().EntityAdd(body.Name, body.EntityType, body.Namespace, body.Properties); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to create entity", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (d *GraphDomain) handleGraphEntityDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing entity id", nil)
		return
	}
	if err := d.deps.Agent().EntityDelete(id); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to delete entity", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (d *GraphDomain) handleGraphRelationCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SourceID     string `json:"source_id"`
		TargetID     string `json:"target_id"`
		RelationType string `json:"relation_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.SourceID == "" || body.TargetID == "" || body.RelationType == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "source_id, target_id, and relation_type are required", nil)
		return
	}
	if err := d.deps.Agent().RelationAdd(body.SourceID, body.TargetID, body.RelationType); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to create relation", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (d *GraphDomain) handleGraphRelationDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing relation id", nil)
		return
	}
	if err := d.deps.Agent().RelationDelete(id); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to delete relation", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (d *GraphDomain) handleGraphFindPath(w http.ResponseWriter, r *http.Request) {
	fromID := r.URL.Query().Get("from")
	toID := r.URL.Query().Get("to")
	if fromID == "" || toID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "from and to query params are required", nil)
		return
	}
	maxDepth := 5
	depthArg := r.URL.Query().Get("max_depth")
	if depthArg == "" {
		depthArg = r.URL.Query().Get("depth")
	}
	if d := depthArg; d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			maxDepth = parsed
		}
	}
	path, err := d.deps.Agent().GraphFindPath(fromID, toID, maxDepth)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to find path", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"path": path})
}

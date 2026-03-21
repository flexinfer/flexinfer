package graph

import (
	"encoding/json"
	"net/http"
)

func (d *GraphDomain) handleReasoningChainList(w http.ResponseWriter, _ *http.Request) {
	chains, err := d.deps.Agent().ReasoningChainList()
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to list reasoning chains", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"chains": chains})
}

func (d *GraphDomain) handleReasoningChainDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing chain id", nil)
		return
	}
	detail, err := d.deps.Agent().ReasoningChainGet(id)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to get reasoning chain", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, detail)
}

func (d *GraphDomain) handleReasoningChainCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	if body.Title == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "title is required", nil)
		return
	}
	if err := d.deps.Agent().ReasoningChainAdd(body.Title, body.Description); err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to create reasoning chain", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

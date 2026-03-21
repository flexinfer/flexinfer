// Package graph implements the graph domain -- knowledge graph entity/relation
// CRUD, path finding, context streaming, and reasoning chain management.
package graph

import (
	"net/http"
)

// GraphDomain registers knowledge graph, context stream, and reasoning chain routes.
type GraphDomain struct {
	deps Deps
}

// New creates a new GraphDomain backed by the given Deps interface.
func New(deps Deps) *GraphDomain {
	return &GraphDomain{deps: deps}
}

// Name returns "graph".
func (d *GraphDomain) Name() string { return "graph" }

// RegisterRoutes wires the graph, stream, and reasoning endpoints to the ServeMux.
func (d *GraphDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	// Knowledge graph entity/relation CRUD.
	mux.HandleFunc("GET /api/graph/stats", mw(d.handleGraphStats))
	mux.HandleFunc("GET /api/graph/entities", mw(d.handleGraphEntities))
	mux.HandleFunc("GET /api/graph/entities/{id}", mw(d.handleGraphEntityDetail))
	mux.HandleFunc("POST /api/graph/entities", mw(d.handleGraphEntityCreate))
	mux.HandleFunc("DELETE /api/graph/entities/{id}", mw(d.handleGraphEntityDelete))
	mux.HandleFunc("POST /api/graph/relations", mw(d.handleGraphRelationCreate))
	mux.HandleFunc("DELETE /api/graph/relations/{id}", mw(d.handleGraphRelationDelete))
	mux.HandleFunc("GET /api/graph/path", mw(d.handleGraphFindPath))

	// Context stream.
	mux.HandleFunc("GET /api/stream", mw(d.handleContextStream))

	// Reasoning chains.
	mux.HandleFunc("GET /api/reasoning/chains", mw(d.handleReasoningChainList))
	mux.HandleFunc("GET /api/reasoning/chains/{id}", mw(d.handleReasoningChainDetail))
	mux.HandleFunc("POST /api/reasoning/chains", mw(d.handleReasoningChainCreate))
}

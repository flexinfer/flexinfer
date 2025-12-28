// mcp-memory is a knowledge graph memory MCP server written in Go.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

// Entity represents a node in the knowledge graph
type Entity struct {
	Name         string   `json:"name"`
	EntityType   string   `json:"entityType"`
	Observations []string `json:"observations"`
}

// Relation represents an edge between entities
type Relation struct {
	From         string `json:"from"`
	To           string `json:"to"`
	RelationType string `json:"relationType"`
}

// KnowledgeGraph stores the entire graph
type KnowledgeGraph struct {
	Entities  map[string]*Entity `json:"entities"`
	Relations []*Relation        `json:"relations"`
}

type memoryServer struct {
	graph    *KnowledgeGraph
	filePath string
	mu       sync.RWMutex
	autoSave bool
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Get persist path from env or default
	persistPath := os.Getenv("MEMORY_PERSIST_PATH")
	if persistPath == "" {
		home, _ := os.UserHomeDir()
		persistPath = filepath.Join(home, ".config", "loom", "memory_graph.json")
	}

	autoSave := os.Getenv("MEMORY_AUTO_SAVE") != "false"

	mem := &memoryServer{
		graph: &KnowledgeGraph{
			Entities:  make(map[string]*Entity),
			Relations: make([]*Relation, 0),
		},
		filePath: persistPath,
		autoSave: autoSave,
	}

	// Load existing graph if available
	if err := mem.load(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load existing graph: %v\n", err)
	}

	server := mcp.NewServer("mcp-memory", version)
	server.SetInstructions("Knowledge graph memory for persistent context. Store entities, relations, and observations.")

	// create_entities
	server.AddTool(mcp.Tool{
		Name:        "create_entities",
		Description: "Create new entities in the knowledge graph",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"entities": map[string]any{
					"type":        "array",
					"description": "Array of entities to create",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":         map[string]any{"type": "string", "description": "Entity name (unique identifier)"},
							"entityType":   map[string]any{"type": "string", "description": "Type of entity (e.g., person, concept, file)"},
							"observations": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Initial observations"},
						},
						"required": []string{"name", "entityType"},
					},
				},
			},
			Required: []string{"entities"},
		},
	}, mem.handleCreateEntities)

	// create_relations
	server.AddTool(mcp.Tool{
		Name:        "create_relations",
		Description: "Create relations between entities",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"relations": map[string]any{
					"type":        "array",
					"description": "Array of relations to create",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"from":         map[string]any{"type": "string", "description": "Source entity name"},
							"to":           map[string]any{"type": "string", "description": "Target entity name"},
							"relationType": map[string]any{"type": "string", "description": "Type of relation"},
						},
						"required": []string{"from", "to", "relationType"},
					},
				},
			},
			Required: []string{"relations"},
		},
	}, mem.handleCreateRelations)

	// add_observations
	server.AddTool(mcp.Tool{
		Name:        "add_observations",
		Description: "Add observations to existing entities",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"observations": map[string]any{
					"type":        "array",
					"description": "Array of observations to add",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"entityName":  map[string]any{"type": "string", "description": "Entity to add observation to"},
							"observation": map[string]any{"type": "string", "description": "Observation text"},
						},
						"required": []string{"entityName", "observation"},
					},
				},
			},
			Required: []string{"observations"},
		},
	}, mem.handleAddObservations)

	// delete_entities
	server.AddTool(mcp.Tool{
		Name:        "delete_entities",
		Description: "Delete entities from the graph",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"names": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Names of entities to delete",
				},
			},
			Required: []string{"names"},
		},
	}, mem.handleDeleteEntities)

	// delete_relations
	server.AddTool(mcp.Tool{
		Name:        "delete_relations",
		Description: "Delete relations from the graph",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"relations": map[string]any{
					"type":        "array",
					"description": "Array of relations to delete",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"from":         map[string]any{"type": "string"},
							"to":           map[string]any{"type": "string"},
							"relationType": map[string]any{"type": "string"},
						},
						"required": []string{"from", "to", "relationType"},
					},
				},
			},
			Required: []string{"relations"},
		},
	}, mem.handleDeleteRelations)

	// delete_observations
	server.AddTool(mcp.Tool{
		Name:        "delete_observations",
		Description: "Delete specific observations from entities",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"deletions": map[string]any{
					"type":        "array",
					"description": "Array of observation deletions",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"entityName":  map[string]any{"type": "string"},
							"observation": map[string]any{"type": "string", "description": "Exact observation text to delete"},
						},
						"required": []string{"entityName", "observation"},
					},
				},
			},
			Required: []string{"deletions"},
		},
	}, mem.handleDeleteObservations)

	// read_graph
	server.AddTool(mcp.Tool{
		Name:        "read_graph",
		Description: "Read the entire knowledge graph",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mem.handleReadGraph)

	// search_nodes
	server.AddTool(mcp.Tool{
		Name:        "search_nodes",
		Description: "Search for entities by name or observation content",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (matches entity names and observations)",
				},
			},
			Required: []string{"query"},
		},
	}, mem.handleSearchNodes)

	// open_nodes
	server.AddTool(mcp.Tool{
		Name:        "open_nodes",
		Description: "Get details of specific entities by name",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"names": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Names of entities to retrieve",
				},
			},
			Required: []string{"names"},
		},
	}, mem.handleOpenNodes)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (m *memoryServer) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No existing file is OK
		}
		return err
	}

	return json.Unmarshal(data, m.graph)
}

func (m *memoryServer) save() error {
	if !m.autoSave {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Ensure directory exists
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m.graph, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.filePath, data, 0644)
}

func (m *memoryServer) handleCreateEntities(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	entities, ok := args["entities"].([]any)
	if !ok {
		return nil, fmt.Errorf("entities must be an array")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	created := make([]string, 0)
	for _, e := range entities {
		entityMap, ok := e.(map[string]any)
		if !ok {
			continue
		}

		name, _ := entityMap["name"].(string)
		entityType, _ := entityMap["entityType"].(string)
		if name == "" || entityType == "" {
			continue
		}

		// Don't overwrite existing entities
		if _, exists := m.graph.Entities[name]; exists {
			continue
		}

		entity := &Entity{
			Name:         name,
			EntityType:   entityType,
			Observations: make([]string, 0),
		}

		if obs, ok := entityMap["observations"].([]any); ok {
			for _, o := range obs {
				if s, ok := o.(string); ok {
					entity.Observations = append(entity.Observations, s)
				}
			}
		}

		m.graph.Entities[name] = entity
		created = append(created, name)
	}

	if err := m.save(); err != nil {
		return nil, fmt.Errorf("save graph: %w", err)
	}

	return mcp.JSONResult(map[string]any{"created": created, "count": len(created)})
}

func (m *memoryServer) handleCreateRelations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	relations, ok := args["relations"].([]any)
	if !ok {
		return nil, fmt.Errorf("relations must be an array")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	created := 0
	for _, r := range relations {
		relMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		from, _ := relMap["from"].(string)
		to, _ := relMap["to"].(string)
		relType, _ := relMap["relationType"].(string)

		if from == "" || to == "" || relType == "" {
			continue
		}

		// Check if relation already exists
		exists := false
		for _, existing := range m.graph.Relations {
			if existing.From == from && existing.To == to && existing.RelationType == relType {
				exists = true
				break
			}
		}

		if !exists {
			m.graph.Relations = append(m.graph.Relations, &Relation{
				From:         from,
				To:           to,
				RelationType: relType,
			})
			created++
		}
	}

	if err := m.save(); err != nil {
		return nil, fmt.Errorf("save graph: %w", err)
	}

	return mcp.JSONResult(map[string]any{"created": created})
}

func (m *memoryServer) handleAddObservations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	observations, ok := args["observations"].([]any)
	if !ok {
		return nil, fmt.Errorf("observations must be an array")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	added := 0
	for _, o := range observations {
		obsMap, ok := o.(map[string]any)
		if !ok {
			continue
		}

		entityName, _ := obsMap["entityName"].(string)
		observation, _ := obsMap["observation"].(string)

		if entityName == "" || observation == "" {
			continue
		}

		entity, exists := m.graph.Entities[entityName]
		if !exists {
			continue
		}

		entity.Observations = append(entity.Observations, observation)
		added++
	}

	if err := m.save(); err != nil {
		return nil, fmt.Errorf("save graph: %w", err)
	}

	return mcp.JSONResult(map[string]any{"added": added})
}

func (m *memoryServer) handleDeleteEntities(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	names, ok := args["names"].([]any)
	if !ok {
		return nil, fmt.Errorf("names must be an array")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	deleted := 0
	for _, n := range names {
		name, ok := n.(string)
		if !ok || name == "" {
			continue
		}

		if _, exists := m.graph.Entities[name]; exists {
			delete(m.graph.Entities, name)
			deleted++

			// Also remove any relations involving this entity
			newRelations := make([]*Relation, 0)
			for _, r := range m.graph.Relations {
				if r.From != name && r.To != name {
					newRelations = append(newRelations, r)
				}
			}
			m.graph.Relations = newRelations
		}
	}

	if err := m.save(); err != nil {
		return nil, fmt.Errorf("save graph: %w", err)
	}

	return mcp.JSONResult(map[string]any{"deleted": deleted})
}

func (m *memoryServer) handleDeleteRelations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	relations, ok := args["relations"].([]any)
	if !ok {
		return nil, fmt.Errorf("relations must be an array")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	deleted := 0
	for _, r := range relations {
		relMap, ok := r.(map[string]any)
		if !ok {
			continue
		}

		from, _ := relMap["from"].(string)
		to, _ := relMap["to"].(string)
		relType, _ := relMap["relationType"].(string)

		newRelations := make([]*Relation, 0)
		for _, existing := range m.graph.Relations {
			if existing.From == from && existing.To == to && existing.RelationType == relType {
				deleted++
			} else {
				newRelations = append(newRelations, existing)
			}
		}
		m.graph.Relations = newRelations
	}

	if err := m.save(); err != nil {
		return nil, fmt.Errorf("save graph: %w", err)
	}

	return mcp.JSONResult(map[string]any{"deleted": deleted})
}

func (m *memoryServer) handleDeleteObservations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	deletions, ok := args["deletions"].([]any)
	if !ok {
		return nil, fmt.Errorf("deletions must be an array")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	deleted := 0
	for _, d := range deletions {
		delMap, ok := d.(map[string]any)
		if !ok {
			continue
		}

		entityName, _ := delMap["entityName"].(string)
		observation, _ := delMap["observation"].(string)

		if entityName == "" || observation == "" {
			continue
		}

		entity, exists := m.graph.Entities[entityName]
		if !exists {
			continue
		}

		newObs := make([]string, 0)
		for _, o := range entity.Observations {
			if o == observation {
				deleted++
			} else {
				newObs = append(newObs, o)
			}
		}
		entity.Observations = newObs
	}

	if err := m.save(); err != nil {
		return nil, fmt.Errorf("save graph: %w", err)
	}

	return mcp.JSONResult(map[string]any{"deleted": deleted})
}

func (m *memoryServer) handleReadGraph(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Convert to array format for easier consumption
	entities := make([]map[string]any, 0, len(m.graph.Entities))
	for _, e := range m.graph.Entities {
		entities = append(entities, map[string]any{
			"name":         e.Name,
			"entityType":   e.EntityType,
			"observations": e.Observations,
		})
	}

	relations := make([]map[string]any, 0, len(m.graph.Relations))
	for _, r := range m.graph.Relations {
		relations = append(relations, map[string]any{
			"from":         r.From,
			"to":           r.To,
			"relationType": r.RelationType,
		})
	}

	return mcp.JSONResult(map[string]any{
		"entities":      entities,
		"relations":     relations,
		"entityCount":   len(entities),
		"relationCount": len(relations),
	})
}

func (m *memoryServer) handleSearchNodes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	queryLower := strings.ToLower(query)
	matches := make([]map[string]any, 0)

	for _, e := range m.graph.Entities {
		// Search in name
		if strings.Contains(strings.ToLower(e.Name), queryLower) {
			matches = append(matches, map[string]any{
				"name":         e.Name,
				"entityType":   e.EntityType,
				"observations": e.Observations,
			})
			continue
		}

		// Search in entity type
		if strings.Contains(strings.ToLower(e.EntityType), queryLower) {
			matches = append(matches, map[string]any{
				"name":         e.Name,
				"entityType":   e.EntityType,
				"observations": e.Observations,
			})
			continue
		}

		// Search in observations
		for _, obs := range e.Observations {
			if strings.Contains(strings.ToLower(obs), queryLower) {
				matches = append(matches, map[string]any{
					"name":         e.Name,
					"entityType":   e.EntityType,
					"observations": e.Observations,
				})
				break
			}
		}
	}

	return mcp.JSONResult(map[string]any{"results": matches, "count": len(matches)})
}

func (m *memoryServer) handleOpenNodes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	names, ok := args["names"].([]any)
	if !ok {
		return nil, fmt.Errorf("names must be an array")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]map[string]any, 0)
	for _, n := range names {
		name, ok := n.(string)
		if !ok || name == "" {
			continue
		}

		entity, exists := m.graph.Entities[name]
		if !exists {
			continue
		}

		// Find relations involving this entity
		relatedRelations := make([]map[string]any, 0)
		for _, r := range m.graph.Relations {
			if r.From == name || r.To == name {
				relatedRelations = append(relatedRelations, map[string]any{
					"from":         r.From,
					"to":           r.To,
					"relationType": r.RelationType,
				})
			}
		}

		results = append(results, map[string]any{
			"name":         entity.Name,
			"entityType":   entity.EntityType,
			"observations": entity.Observations,
			"relations":    relatedRelations,
		})
	}

	return mcp.JSONResult(map[string]any{"entities": results, "count": len(results)})
}

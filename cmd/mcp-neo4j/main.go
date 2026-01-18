// mcp-neo4j is an MCP server for Neo4j graph database queries and schema inspection.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/crb2nu/loom/pkg/validate"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

type neo4jServer struct {
	driver neo4j.DriverWithContext
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

	uri := os.Getenv("NEO4J_URI")
	if uri == "" {
		uri = "bolt://localhost:7687"
	}

	username := os.Getenv("NEO4J_USERNAME")
	if username == "" {
		username = "neo4j"
	}

	password := os.Getenv("NEO4J_PASSWORD")
	if password == "" {
		password = "password"
	}

	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(username, password, ""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Neo4j driver: %v\n", err)
		os.Exit(1)
	}
	defer driver.Close(ctx)

	// Verify connectivity
	if err := driver.VerifyConnectivity(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to Neo4j: %v\n", err)
		os.Exit(1)
	}

	ns := &neo4jServer{driver: driver}

	server := mcp.NewServer("mcp-neo4j", version)
	server.SetInstructions("Neo4j graph database MCP server. Execute Cypher queries, inspect schema, and explore graph data.")

	// neo4j_query
	server.AddTool(mcp.Tool{
		Name:        "neo4j_query",
		Description: "Execute a Cypher query (read-only by default)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Cypher query to execute",
				},
				"params": map[string]any{
					"type":        "object",
					"description": "Query parameters as key-value pairs",
				},
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Max records to return (default: 100, max: 1000)",
				},
				"write": map[string]any{
					"type":        "boolean",
					"description": "Allow write operations (default: false)",
				},
			},
			Required: []string{"query"},
		},
	}, ns.handleQuery)

	// neo4j_schema
	server.AddTool(mcp.Tool{
		Name:        "neo4j_schema",
		Description: "Get database schema (labels, relationship types, property keys)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
		},
	}, ns.handleSchema)

	// neo4j_labels
	server.AddTool(mcp.Tool{
		Name:        "neo4j_labels",
		Description: "List all node labels in the database",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
		},
	}, ns.handleLabels)

	// neo4j_relationship_types
	server.AddTool(mcp.Tool{
		Name:        "neo4j_relationship_types",
		Description: "List all relationship types in the database",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
		},
	}, ns.handleRelationshipTypes)

	// neo4j_indexes
	server.AddTool(mcp.Tool{
		Name:        "neo4j_indexes",
		Description: "List all indexes in the database",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
		},
	}, ns.handleIndexes)

	// neo4j_constraints
	server.AddTool(mcp.Tool{
		Name:        "neo4j_constraints",
		Description: "List all constraints in the database",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
		},
	}, ns.handleConstraints)

	// neo4j_count_nodes
	server.AddTool(mcp.Tool{
		Name:        "neo4j_count_nodes",
		Description: "Count nodes, optionally filtered by label",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"label": map[string]any{
					"type":        "string",
					"description": "Node label to filter by (optional)",
				},
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
		},
	}, ns.handleCountNodes)

	// neo4j_count_relationships
	server.AddTool(mcp.Tool{
		Name:        "neo4j_count_relationships",
		Description: "Count relationships, optionally filtered by type",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"type": map[string]any{
					"type":        "string",
					"description": "Relationship type to filter by (optional)",
				},
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
		},
	}, ns.handleCountRelationships)

	// neo4j_node_properties
	server.AddTool(mcp.Tool{
		Name:        "neo4j_node_properties",
		Description: "Get property keys for nodes with a specific label",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"label": map[string]any{
					"type":        "string",
					"description": "Node label",
				},
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (default: neo4j)",
				},
			},
			Required: []string{"label"},
		},
	}, ns.handleNodeProperties)

	// neo4j_databases
	server.AddTool(mcp.Tool{
		Name:        "neo4j_databases",
		Description: "List all databases (Enterprise Edition)",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, ns.handleDatabases)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (s *neo4jServer) getSession(ctx context.Context, database string, accessMode neo4j.AccessMode) neo4j.SessionWithContext {
	if database == "" {
		database = "neo4j"
	}
	return s.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: database,
		AccessMode:   accessMode,
	})
}

func (s *neo4jServer) handleQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	database := v.String("database", "neo4j")
	limit := v.IntRange("limit", 100, 1, 1000)
	write := v.Bool("write", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse params
	params := make(map[string]any)
	if p, ok := args["params"].(map[string]any); ok {
		params = p
	}

	// Check for write operations if not explicitly allowed
	if !write && isWriteQuery(query) {
		return mcp.ErrorResult(fmt.Errorf("write operations not allowed; set write=true to enable")), nil
	}

	accessMode := neo4j.AccessModeRead
	if write {
		accessMode = neo4j.AccessModeWrite
	}

	session := s.getSession(ctx, database, accessMode)
	defer session.Close(ctx)

	result, err := session.Run(ctx, query, params)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	records := make([]map[string]any, 0)
	count := 0

	for result.Next(ctx) && count < limit {
		record := result.Record()
		row := make(map[string]any)

		for _, key := range record.Keys {
			val, _ := record.Get(key)
			row[key] = convertNeo4jValue(val)
		}

		records = append(records, row)
		count++
	}

	if err := result.Err(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	summary, _ := result.Consume(ctx)

	response := map[string]any{
		"ok":      true,
		"records": records,
		"count":   len(records),
	}

	if summary != nil {
		counters := summary.Counters()
		if counters.ContainsUpdates() {
			response["updates"] = map[string]any{
				"nodes_created":         counters.NodesCreated(),
				"nodes_deleted":         counters.NodesDeleted(),
				"relationships_created": counters.RelationshipsCreated(),
				"relationships_deleted": counters.RelationshipsDeleted(),
				"properties_set":        counters.PropertiesSet(),
				"labels_added":          counters.LabelsAdded(),
				"labels_removed":        counters.LabelsRemoved(),
			}
		}
	}

	return mcp.JSONResult(response)
}

func isWriteQuery(query string) bool {
	q := strings.ToUpper(strings.TrimSpace(query))
	writeKeywords := []string{"CREATE", "MERGE", "DELETE", "DETACH", "SET", "REMOVE", "DROP", "CALL"}
	for _, kw := range writeKeywords {
		if strings.HasPrefix(q, kw) || strings.Contains(q, " "+kw+" ") {
			return true
		}
	}
	return false
}

func convertNeo4jValue(val any) any {
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case neo4j.Node:
		return map[string]any{
			"_type":      "node",
			"id":         v.GetElementId(),
			"labels":     v.Labels,
			"properties": v.Props,
		}
	case neo4j.Relationship:
		return map[string]any{
			"_type":      "relationship",
			"id":         v.GetElementId(),
			"type":       v.Type,
			"start":      v.StartElementId,
			"end":        v.EndElementId,
			"properties": v.Props,
		}
	case neo4j.Path:
		nodes := make([]any, len(v.Nodes))
		for i, n := range v.Nodes {
			nodes[i] = convertNeo4jValue(n)
		}
		rels := make([]any, len(v.Relationships))
		for i, r := range v.Relationships {
			rels[i] = convertNeo4jValue(r)
		}
		return map[string]any{
			"_type":         "path",
			"nodes":         nodes,
			"relationships": rels,
		}
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = convertNeo4jValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any)
		for k, item := range v {
			result[k] = convertNeo4jValue(item)
		}
		return result
	default:
		return v
	}
}

func (s *neo4jServer) handleSchema(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	database := v.String("database", "neo4j")

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	// Get labels
	labels, err := s.queryStringList(ctx, session, "CALL db.labels()")
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to get labels: %w", err)), nil
	}

	// Get relationship types
	relTypes, err := s.queryStringList(ctx, session, "CALL db.relationshipTypes()")
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to get relationship types: %w", err)), nil
	}

	// Get property keys
	propKeys, err := s.queryStringList(ctx, session, "CALL db.propertyKeys()")
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to get property keys: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":                 true,
		"database":           database,
		"labels":             labels,
		"relationship_types": relTypes,
		"property_keys":      propKeys,
	})
}

func (s *neo4jServer) queryStringList(ctx context.Context, session neo4j.SessionWithContext, query string) ([]string, error) {
	result, err := session.Run(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	var items []string
	for result.Next(ctx) {
		record := result.Record()
		if len(record.Values) > 0 {
			if str, ok := record.Values[0].(string); ok {
				items = append(items, str)
			}
		}
	}

	return items, result.Err()
}

func (s *neo4jServer) handleLabels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	database := v.String("database", "neo4j")

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	labels, err := s.queryStringList(ctx, session, "CALL db.labels()")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"labels": labels,
		"count":  len(labels),
	})
}

func (s *neo4jServer) handleRelationshipTypes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	database := v.String("database", "neo4j")

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	relTypes, err := s.queryStringList(ctx, session, "CALL db.relationshipTypes()")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":                 true,
		"relationship_types": relTypes,
		"count":              len(relTypes),
	})
}

func (s *neo4jServer) handleIndexes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	database := v.String("database", "neo4j")

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	result, err := session.Run(ctx, "SHOW INDEXES", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	indexes := make([]map[string]any, 0)
	for result.Next(ctx) {
		record := result.Record()
		idx := make(map[string]any)
		for _, key := range record.Keys {
			val, _ := record.Get(key)
			idx[key] = val
		}
		indexes = append(indexes, idx)
	}

	if err := result.Err(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"indexes": indexes,
		"count":   len(indexes),
	})
}

func (s *neo4jServer) handleConstraints(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	database := v.String("database", "neo4j")

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	result, err := session.Run(ctx, "SHOW CONSTRAINTS", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	constraints := make([]map[string]any, 0)
	for result.Next(ctx) {
		record := result.Record()
		constraint := make(map[string]any)
		for _, key := range record.Keys {
			val, _ := record.Get(key)
			constraint[key] = val
		}
		constraints = append(constraints, constraint)
	}

	if err := result.Err(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"constraints": constraints,
		"count":       len(constraints),
	})
}

func (s *neo4jServer) handleCountNodes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	label := v.String("label", "")
	database := v.String("database", "neo4j")

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	var query string
	if label != "" {
		query = fmt.Sprintf("MATCH (n:`%s`) RETURN count(n) as count", label)
	} else {
		query = "MATCH (n) RETURN count(n) as count"
	}

	result, err := session.Run(ctx, query, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var count int64
	if result.Next(ctx) {
		if c, ok := result.Record().Get("count"); ok {
			count = c.(int64)
		}
	}

	if err := result.Err(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	response := map[string]any{
		"ok":    true,
		"count": count,
	}
	if label != "" {
		response["label"] = label
	}

	return mcp.JSONResult(response)
}

func (s *neo4jServer) handleCountRelationships(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	relType := v.String("type", "")
	database := v.String("database", "neo4j")

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	var query string
	if relType != "" {
		query = fmt.Sprintf("MATCH ()-[r:`%s`]->() RETURN count(r) as count", relType)
	} else {
		query = "MATCH ()-[r]->() RETURN count(r) as count"
	}

	result, err := session.Run(ctx, query, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var count int64
	if result.Next(ctx) {
		if c, ok := result.Record().Get("count"); ok {
			count = c.(int64)
		}
	}

	if err := result.Err(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	response := map[string]any{
		"ok":    true,
		"count": count,
	}
	if relType != "" {
		response["type"] = relType
	}

	return mcp.JSONResult(response)
}

func (s *neo4jServer) handleNodeProperties(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	label := v.Required("label")
	database := v.String("database", "neo4j")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	session := s.getSession(ctx, database, neo4j.AccessModeRead)
	defer session.Close(ctx)

	// Sample some nodes to discover properties
	query := fmt.Sprintf("MATCH (n:`%s`) RETURN keys(n) as props LIMIT 100", label)
	result, err := session.Run(ctx, query, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	propSet := make(map[string]bool)
	for result.Next(ctx) {
		if props, ok := result.Record().Get("props"); ok {
			if propList, ok := props.([]any); ok {
				for _, p := range propList {
					if prop, ok := p.(string); ok {
						propSet[prop] = true
					}
				}
			}
		}
	}

	if err := result.Err(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	properties := make([]string, 0, len(propSet))
	for prop := range propSet {
		properties = append(properties, prop)
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"label":      label,
		"properties": properties,
		"count":      len(properties),
	})
}

func (s *neo4jServer) handleDatabases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	session := s.driver.NewSession(ctx, neo4j.SessionConfig{
		DatabaseName: "system",
		AccessMode:   neo4j.AccessModeRead,
	})
	defer session.Close(ctx)

	result, err := session.Run(ctx, "SHOW DATABASES", nil)
	if err != nil {
		// Community edition doesn't support SHOW DATABASES
		return mcp.JSONResult(map[string]any{
			"ok":        true,
			"databases": []string{"neo4j"},
			"note":      "SHOW DATABASES requires Enterprise Edition",
		})
	}

	databases := make([]map[string]any, 0)
	for result.Next(ctx) {
		record := result.Record()
		db := make(map[string]any)
		for _, key := range record.Keys {
			val, _ := record.Get(key)
			db[key] = val
		}
		databases = append(databases, db)
	}

	if err := result.Err(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"databases": databases,
		"count":     len(databases),
	})
}

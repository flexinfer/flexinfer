// mcp-mongodb provides MCP tools for MongoDB database operations.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	version = "0.1.0"

	mongoURI = getEnv("MONGODB_URI", "mongodb://localhost:27017")
	mongoDB  = os.Getenv("MONGODB_DATABASE")

	client *mongo.Client
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func initMongo(ctx context.Context) error {
	opts := options.Client().ApplyURI(mongoURI)
	opts.SetConnectTimeout(10 * time.Second)
	opts.SetServerSelectionTimeout(5 * time.Second)

	var err error
	client, err = mongo.Connect(ctx, opts)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	// Verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	if err := initMongo(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "MongoDB init error: %v\n", err)
		os.Exit(1)
	}
	defer client.Disconnect(context.Background())

	server := mcp.NewServer("mcp-mongodb", version)
	server.SetInstructions("MongoDB database tools. Configure with MONGODB_URI and optionally MONGODB_DATABASE for default database.")

	// Database operations
	server.AddTool(mcp.Tool{
		Name:        "mongo_list_databases",
		Description: "List all databases",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListDatabases)

	server.AddTool(mcp.Tool{
		Name:        "mongo_list_collections",
		Description: "List collections in a database",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name (uses MONGODB_DATABASE env if not specified)",
				},
			},
		},
	}, handleListCollections)

	server.AddTool(mcp.Tool{
		Name:        "mongo_collection_stats",
		Description: "Get collection statistics",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name",
				},
				"collection": map[string]any{
					"type":        "string",
					"description": "Collection name",
				},
			},
			Required: []string{"collection"},
		},
	}, handleCollectionStats)

	// Query operations
	server.AddTool(mcp.Tool{
		Name:        "mongo_find",
		Description: "Find documents in a collection",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name",
				},
				"collection": map[string]any{
					"type":        "string",
					"description": "Collection name",
				},
				"filter": map[string]any{
					"type":        "object",
					"description": "Query filter (MongoDB query syntax)",
				},
				"projection": map[string]any{
					"type":        "object",
					"description": "Fields to include/exclude",
				},
				"sort": map[string]any{
					"type":        "object",
					"description": "Sort order (e.g., {\"createdAt\": -1})",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum documents to return (default: 20, max: 1000)",
				},
				"skip": map[string]any{
					"type":        "integer",
					"description": "Number of documents to skip",
				},
			},
			Required: []string{"collection"},
		},
	}, handleFind)

	server.AddTool(mcp.Tool{
		Name:        "mongo_find_one",
		Description: "Find a single document by ID or filter",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name",
				},
				"collection": map[string]any{
					"type":        "string",
					"description": "Collection name",
				},
				"id": map[string]any{
					"type":        "string",
					"description": "Document ObjectId (alternative to filter)",
				},
				"filter": map[string]any{
					"type":        "object",
					"description": "Query filter",
				},
			},
			Required: []string{"collection"},
		},
	}, handleFindOne)

	server.AddTool(mcp.Tool{
		Name:        "mongo_count",
		Description: "Count documents matching a filter",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name",
				},
				"collection": map[string]any{
					"type":        "string",
					"description": "Collection name",
				},
				"filter": map[string]any{
					"type":        "object",
					"description": "Query filter (empty for total count)",
				},
			},
			Required: []string{"collection"},
		},
	}, handleCount)

	server.AddTool(mcp.Tool{
		Name:        "mongo_aggregate",
		Description: "Run an aggregation pipeline",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name",
				},
				"collection": map[string]any{
					"type":        "string",
					"description": "Collection name",
				},
				"pipeline": map[string]any{
					"type":        "array",
					"description": "Aggregation pipeline stages",
				},
			},
			Required: []string{"collection", "pipeline"},
		},
	}, handleAggregate)

	server.AddTool(mcp.Tool{
		Name:        "mongo_distinct",
		Description: "Get distinct values for a field",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name",
				},
				"collection": map[string]any{
					"type":        "string",
					"description": "Collection name",
				},
				"field": map[string]any{
					"type":        "string",
					"description": "Field name to get distinct values for",
				},
				"filter": map[string]any{
					"type":        "object",
					"description": "Optional filter",
				},
			},
			Required: []string{"collection", "field"},
		},
	}, handleDistinct)

	server.AddTool(mcp.Tool{
		Name:        "mongo_indexes",
		Description: "List indexes on a collection",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"database": map[string]any{
					"type":        "string",
					"description": "Database name",
				},
				"collection": map[string]any{
					"type":        "string",
					"description": "Collection name",
				},
			},
			Required: []string{"collection"},
		},
	}, handleIndexes)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func getDatabase(args map[string]any) string {
	if db, ok := args["database"].(string); ok && db != "" {
		return db
	}
	return mongoDB
}

func handleListDatabases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := client.ListDatabases(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("list databases: %w", err)
	}

	databases := make([]map[string]any, 0, len(result.Databases))
	for _, db := range result.Databases {
		databases = append(databases, map[string]any{
			"name":       db.Name,
			"size_bytes": db.SizeOnDisk,
			"empty":      db.Empty,
		})
	}

	return mcp.JSONResult(map[string]any{
		"databases":   databases,
		"count":       len(databases),
		"total_bytes": result.TotalSize,
	})
}

func handleListCollections(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	db := client.Database(dbName)
	collections, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"database":    dbName,
		"collections": collections,
		"count":       len(collections),
	})
}

func handleCollectionStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	collName, ok := args["collection"].(string)
	if !ok || collName == "" {
		return mcp.ErrorResult(fmt.Errorf("collection is required")), nil
	}

	db := client.Database(dbName)
	var result bson.M
	err := db.RunCommand(ctx, bson.D{{Key: "collStats", Value: collName}}).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("collection stats: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"database":     dbName,
		"collection":   collName,
		"count":        result["count"],
		"size":         result["size"],
		"avg_obj_size": result["avgObjSize"],
		"storage_size": result["storageSize"],
		"indexes":      result["nindexes"],
		"index_size":   result["totalIndexSize"],
	})
}

func handleFind(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	collName, ok := args["collection"].(string)
	if !ok || collName == "" {
		return mcp.ErrorResult(fmt.Errorf("collection is required")), nil
	}

	filter := bson.M{}
	if f, ok := args["filter"].(map[string]any); ok {
		filter = toBsonM(f)
	}

	opts := options.Find()

	if projection, ok := args["projection"].(map[string]any); ok {
		opts.SetProjection(toBsonM(projection))
	}

	if sort, ok := args["sort"].(map[string]any); ok {
		opts.SetSort(toBsonM(sort))
	}

	limit := int64(20)
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int64(l)
		if limit > 1000 {
			limit = 1000
		}
	}
	opts.SetLimit(limit)

	if skip, ok := args["skip"].(float64); ok && skip > 0 {
		opts.SetSkip(int64(skip))
	}

	coll := client.Database(dbName).Collection(collName)
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}
	defer cursor.Close(ctx)

	var documents []bson.M
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	// Convert ObjectIds to strings for JSON
	docs := make([]map[string]any, 0, len(documents))
	for _, doc := range documents {
		docs = append(docs, convertBsonM(doc))
	}

	return mcp.JSONResult(map[string]any{
		"database":   dbName,
		"collection": collName,
		"documents":  docs,
		"count":      len(docs),
	})
}

func handleFindOne(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	collName, ok := args["collection"].(string)
	if !ok || collName == "" {
		return mcp.ErrorResult(fmt.Errorf("collection is required")), nil
	}

	filter := bson.M{}
	if id, ok := args["id"].(string); ok && id != "" {
		oid, err := primitive.ObjectIDFromHex(id)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("invalid ObjectId: %w", err)), nil
		}
		filter["_id"] = oid
	} else if f, ok := args["filter"].(map[string]any); ok {
		filter = toBsonM(f)
	}

	coll := client.Database(dbName).Collection(collName)
	var result bson.M
	err := coll.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return mcp.JSONResult(map[string]any{
				"database":   dbName,
				"collection": collName,
				"found":      false,
			})
		}
		return nil, fmt.Errorf("find one: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"database":   dbName,
		"collection": collName,
		"found":      true,
		"document":   convertBsonM(result),
	})
}

func handleCount(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	collName, ok := args["collection"].(string)
	if !ok || collName == "" {
		return mcp.ErrorResult(fmt.Errorf("collection is required")), nil
	}

	filter := bson.M{}
	if f, ok := args["filter"].(map[string]any); ok {
		filter = toBsonM(f)
	}

	coll := client.Database(dbName).Collection(collName)
	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"database":   dbName,
		"collection": collName,
		"count":      count,
	})
}

func handleAggregate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	collName, ok := args["collection"].(string)
	if !ok || collName == "" {
		return mcp.ErrorResult(fmt.Errorf("collection is required")), nil
	}

	pipelineArg, ok := args["pipeline"].([]any)
	if !ok || len(pipelineArg) == 0 {
		return mcp.ErrorResult(fmt.Errorf("pipeline is required")), nil
	}

	pipeline := make([]bson.M, 0, len(pipelineArg))
	for _, stage := range pipelineArg {
		if stageMap, ok := stage.(map[string]any); ok {
			pipeline = append(pipeline, toBsonM(stageMap))
		}
	}

	coll := client.Database(dbName).Collection(collName)
	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("aggregate: %w", err)
	}
	defer cursor.Close(ctx)

	var results []bson.M
	if err := cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	docs := make([]map[string]any, 0, len(results))
	for _, doc := range results {
		docs = append(docs, convertBsonM(doc))
	}

	return mcp.JSONResult(map[string]any{
		"database":   dbName,
		"collection": collName,
		"results":    docs,
		"count":      len(docs),
	})
}

func handleDistinct(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	collName, ok := args["collection"].(string)
	if !ok || collName == "" {
		return mcp.ErrorResult(fmt.Errorf("collection is required")), nil
	}

	field, ok := args["field"].(string)
	if !ok || field == "" {
		return mcp.ErrorResult(fmt.Errorf("field is required")), nil
	}

	filter := bson.M{}
	if f, ok := args["filter"].(map[string]any); ok {
		filter = toBsonM(f)
	}

	coll := client.Database(dbName).Collection(collName)
	values, err := coll.Distinct(ctx, field, filter)
	if err != nil {
		return nil, fmt.Errorf("distinct: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"database":   dbName,
		"collection": collName,
		"field":      field,
		"values":     values,
		"count":      len(values),
	})
}

func handleIndexes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	dbName := getDatabase(args)
	if dbName == "" {
		return mcp.ErrorResult(fmt.Errorf("database is required")), nil
	}

	collName, ok := args["collection"].(string)
	if !ok || collName == "" {
		return mcp.ErrorResult(fmt.Errorf("collection is required")), nil
	}

	coll := client.Database(dbName).Collection(collName)
	cursor, err := coll.Indexes().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list indexes: %w", err)
	}
	defer cursor.Close(ctx)

	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	idxList := make([]map[string]any, 0, len(indexes))
	for _, idx := range indexes {
		idxList = append(idxList, convertBsonM(idx))
	}

	return mcp.JSONResult(map[string]any{
		"database":   dbName,
		"collection": collName,
		"indexes":    idxList,
		"count":      len(idxList),
	})
}

// toBsonM converts a map[string]any to bson.M
func toBsonM(m map[string]any) bson.M {
	result := bson.M{}
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			result[k] = toBsonM(val)
		case []any:
			arr := make([]any, len(val))
			for i, item := range val {
				if itemMap, ok := item.(map[string]any); ok {
					arr[i] = toBsonM(itemMap)
				} else {
					arr[i] = item
				}
			}
			result[k] = arr
		default:
			result[k] = v
		}
	}
	return result
}

// convertBsonM converts bson.M to map[string]any with ObjectId -> string conversion
func convertBsonM(m bson.M) map[string]any {
	result := make(map[string]any)
	for k, v := range m {
		switch val := v.(type) {
		case primitive.ObjectID:
			result[k] = val.Hex()
		case primitive.DateTime:
			result[k] = val.Time().Format(time.RFC3339)
		case bson.M:
			result[k] = convertBsonM(val)
		case []bson.M:
			arr := make([]map[string]any, len(val))
			for i, item := range val {
				arr[i] = convertBsonM(item)
			}
			result[k] = arr
		case primitive.A:
			arr := make([]any, len(val))
			for i, item := range val {
				if itemMap, ok := item.(bson.M); ok {
					arr[i] = convertBsonM(itemMap)
				} else if oid, ok := item.(primitive.ObjectID); ok {
					arr[i] = oid.Hex()
				} else {
					arr[i] = item
				}
			}
			result[k] = arr
		default:
			result[k] = v
		}
	}
	return result
}

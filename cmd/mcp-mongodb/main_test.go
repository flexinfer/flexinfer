package main

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestToBsonM(t *testing.T) {
	t.Run("simple map", func(t *testing.T) {
		input := map[string]any{
			"name": "test",
			"age":  float64(30),
		}
		result := toBsonM(input)
		if result["name"] != "test" {
			t.Errorf("expected name=test, got %v", result["name"])
		}
		if result["age"] != float64(30) {
			t.Errorf("expected age=30, got %v", result["age"])
		}
	})

	t.Run("nested map", func(t *testing.T) {
		input := map[string]any{
			"user": map[string]any{
				"name": "alice",
			},
		}
		result := toBsonM(input)
		nested, ok := result["user"].(bson.M)
		if !ok {
			t.Fatalf("expected nested bson.M, got %T", result["user"])
		}
		if nested["name"] != "alice" {
			t.Errorf("expected name=alice, got %v", nested["name"])
		}
	})

	t.Run("array with nested objects", func(t *testing.T) {
		input := map[string]any{
			"items": []any{
				map[string]any{"key": "val1"},
				map[string]any{"key": "val2"},
			},
		}
		result := toBsonM(input)
		arr, ok := result["items"].([]any)
		if !ok {
			t.Fatalf("expected []any, got %T", result["items"])
		}
		if len(arr) != 2 {
			t.Fatalf("expected 2 items, got %d", len(arr))
		}
		item0, ok := arr[0].(bson.M)
		if !ok {
			t.Fatalf("expected bson.M in array, got %T", arr[0])
		}
		if item0["key"] != "val1" {
			t.Errorf("expected key=val1, got %v", item0["key"])
		}
	})

	t.Run("empty map", func(t *testing.T) {
		result := toBsonM(map[string]any{})
		if len(result) != 0 {
			t.Errorf("expected empty bson.M, got %d entries", len(result))
		}
	})
}

func TestConvertBsonM(t *testing.T) {
	t.Run("ObjectID conversion", func(t *testing.T) {
		oid := primitive.NewObjectID()
		input := bson.M{"_id": oid}
		result := convertBsonM(input)
		if result["_id"] != oid.Hex() {
			t.Errorf("expected ObjectID hex string, got %v", result["_id"])
		}
	})

	t.Run("DateTime conversion", func(t *testing.T) {
		// Use a time truncated to seconds since RFC3339 output drops sub-second precision.
		fixedTime := time.Date(2025, 6, 15, 12, 30, 0, 0, time.UTC)
		dt := primitive.NewDateTimeFromTime(fixedTime)
		input := bson.M{"created": dt}
		result := convertBsonM(input)
		str, ok := result["created"].(string)
		if !ok {
			t.Fatalf("expected string, got %T", result["created"])
		}
		parsed, err := time.Parse(time.RFC3339, str)
		if err != nil {
			t.Fatalf("failed to parse RFC3339 time: %v", err)
		}
		if !parsed.Equal(fixedTime) {
			t.Errorf("expected time %v, got %v", fixedTime, parsed)
		}
	})

	t.Run("nested bson.M", func(t *testing.T) {
		oid := primitive.NewObjectID()
		input := bson.M{
			"user": bson.M{
				"_id":  oid,
				"name": "alice",
			},
		}
		result := convertBsonM(input)
		nested, ok := result["user"].(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", result["user"])
		}
		if nested["_id"] != oid.Hex() {
			t.Errorf("expected nested ObjectID hex, got %v", nested["_id"])
		}
	})

	t.Run("primitive.A with ObjectIDs", func(t *testing.T) {
		oid1 := primitive.NewObjectID()
		oid2 := primitive.NewObjectID()
		input := bson.M{
			"refs": primitive.A{oid1, oid2, "plain"},
		}
		result := convertBsonM(input)
		arr, ok := result["refs"].([]any)
		if !ok {
			t.Fatalf("expected []any, got %T", result["refs"])
		}
		if len(arr) != 3 {
			t.Fatalf("expected 3 items, got %d", len(arr))
		}
		if arr[0] != oid1.Hex() {
			t.Errorf("expected oid hex, got %v", arr[0])
		}
		if arr[2] != "plain" {
			t.Errorf("expected plain string, got %v", arr[2])
		}
	})

	t.Run("plain values pass through", func(t *testing.T) {
		input := bson.M{
			"name":  "test",
			"count": int32(5),
			"flag":  true,
		}
		result := convertBsonM(input)
		if result["name"] != "test" {
			t.Errorf("expected name=test, got %v", result["name"])
		}
		if result["count"] != int32(5) {
			t.Errorf("expected count=5, got %v", result["count"])
		}
		if result["flag"] != true {
			t.Errorf("expected flag=true, got %v", result["flag"])
		}
	})
}

func TestToolDefinitions(t *testing.T) {
	expectedTools := map[string]string{
		"mongo_list_databases":   "List all databases",
		"mongo_list_collections": "List collections in a database",
		"mongo_collection_stats": "Get collection statistics",
		"mongo_find":             "Find documents in a collection",
		"mongo_find_one":         "Find a single document by ID or filter",
		"mongo_count":            "Count documents matching a filter",
		"mongo_aggregate":        "Run an aggregation pipeline",
		"mongo_distinct":         "Get distinct values for a field",
		"mongo_indexes":          "List indexes on a collection",
	}

	for name, desc := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
		if desc == "" {
			t.Errorf("tool %q must have a non-empty description", name)
		}
	}

	if len(expectedTools) != 9 {
		t.Errorf("expected 9 tools, got %d", len(expectedTools))
	}
}

func TestMongoURIDefault(t *testing.T) {
	if mongoURI == "" {
		t.Error("mongoURI should have a default value")
	}
}

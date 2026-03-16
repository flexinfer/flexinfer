package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestVisibleToolsAddsBulkForEligibleServers(t *testing.T) {
	base := []mcp.Tool{
		{Name: "gitlab__create_issue"},
		{Name: "gitlab__list_issues"},
		{Name: "github__create_issue"},
		{Name: "prometheus__query"},
		{Name: "time__add_duration"},
	}

	visible := visibleTools(base)
	names := make(map[string]struct{}, len(visible))
	for _, tool := range visible {
		names[tool.Name] = struct{}{}
	}

	if _, ok := names["gitlab__bulk"]; !ok {
		t.Fatalf("expected gitlab__bulk to be synthesized")
	}
	if _, ok := names["github__bulk"]; !ok {
		t.Fatalf("expected github__bulk to be synthesized")
	}
	if _, ok := names["prometheus__bulk"]; ok {
		t.Fatalf("did not expect prometheus__bulk to be synthesized")
	}
	if _, ok := names["time__bulk"]; ok {
		t.Fatalf("did not expect time__bulk to be synthesized")
	}
}

func TestLoadBulkManifestSupportsJSONAndYAML(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "bulk.json")
	yamlPath := filepath.Join(dir, "bulk.yaml")

	if err := os.WriteFile(jsonPath, []byte(`{"default_tool":"create_issue","operations":[{"id":"a","arguments":{"title":"one"}}]}`), 0o644); err != nil {
		t.Fatalf("write json manifest: %v", err)
	}
	if err := os.WriteFile(yamlPath, []byte("default_tool: create_issue\noperations:\n  - id: b\n    arguments:\n      title: two\n"), 0o644); err != nil {
		t.Fatalf("write yaml manifest: %v", err)
	}

	jsonManifest, err := loadBulkManifest(jsonPath)
	if err != nil {
		t.Fatalf("load json manifest: %v", err)
	}
	if got := jsonManifest.Operations[0].Arguments["title"]; got != "one" {
		t.Fatalf("unexpected json title: %v", got)
	}

	yamlManifest, err := loadBulkManifest(yamlPath)
	if err != nil {
		t.Fatalf("load yaml manifest: %v", err)
	}
	if got := yamlManifest.Operations[0].Arguments["title"]; got != "two" {
		t.Fatalf("unexpected yaml title: %v", got)
	}
}

func TestExecuteBulkManifestContinuesOnError(t *testing.T) {
	manifest := bulkManifest{
		DefaultTool: "create_issue",
		Operations: []bulkManifestOperation{
			{ID: "one", Arguments: map[string]any{"title": "first"}},
			{ID: "two", Arguments: map[string]any{"title": "second"}},
		},
	}

	calls := 0
	result, err := executeBulkManifest(context.Background(), bulkExecutionOptions{
		Server: "gitlab",
		ValidateTool: func(tool string) error {
			if tool != "create_issue" {
				return errors.New("unexpected tool")
			}
			return nil
		},
		Invoke: func(_ context.Context, tool string, args map[string]any) (any, error) {
			calls++
			if args["title"] == "first" {
				return nil, errors.New("boom")
			}
			return map[string]any{"iid": 42, "title": args["title"]}, nil
		},
	}, manifest)
	if err != nil {
		t.Fatalf("executeBulkManifest returned unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
	if result.Succeeded != 1 || result.Failed != 1 {
		t.Fatalf("unexpected result counts: %+v", result)
	}
	if result.StoppedAt != "" {
		t.Fatalf("did not expect stop marker: %+v", result)
	}
}

func TestExecuteBulkManifestStopsOnError(t *testing.T) {
	manifest := bulkManifest{
		DefaultTool: "create_issue",
		Operations: []bulkManifestOperation{
			{ID: "one", Arguments: map[string]any{"title": "first"}},
			{ID: "two", Arguments: map[string]any{"title": "second"}},
		},
	}

	calls := 0
	result, err := executeBulkManifest(context.Background(), bulkExecutionOptions{
		Server:      "gitlab",
		StopOnError: true,
		ValidateTool: func(tool string) error {
			if tool != "create_issue" {
				return errors.New("unexpected tool")
			}
			return nil
		},
		Invoke: func(_ context.Context, tool string, args map[string]any) (any, error) {
			calls++
			if args["title"] == "first" {
				return nil, errors.New("boom")
			}
			return map[string]any{"iid": 42, "title": args["title"]}, nil
		},
	}, manifest)
	if err == nil {
		t.Fatalf("expected stop-on-error execution to return an error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call before stop, got %d", calls)
	}
	if result.Failed != 1 || result.Executed != 1 {
		t.Fatalf("unexpected result counts: %+v", result)
	}
	if result.StoppedAt == "" {
		t.Fatalf("expected stopped marker: %+v", result)
	}
}

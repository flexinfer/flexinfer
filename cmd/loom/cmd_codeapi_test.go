package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestRenderArgsTypeScript_ObjectShape(t *testing.T) {
	schema := mcp.InputSchema{
		Type: "object",
		Properties: map[string]any{
			"query": map[string]any{"type": "string"},
			"days":  map[string]any{"type": "integer"},
			"mode":  map[string]any{"enum": []any{"basic", "advanced"}},
		},
		Required: []string{"query"},
	}

	rendered := renderArgsTypeScript("SearchArgs", schema)
	if !strings.Contains(rendered, "interface SearchArgs") {
		t.Fatalf("missing interface declaration: %s", rendered)
	}
	if !strings.Contains(rendered, "query: string;") {
		t.Fatalf("missing required query field: %s", rendered)
	}
	if !strings.Contains(rendered, "days?: number;") {
		t.Fatalf("missing optional numeric field: %s", rendered)
	}
	if !strings.Contains(rendered, "mode?: \"basic\" | \"advanced\";") {
		t.Fatalf("missing enum union field: %s", rendered)
	}
}

func TestSchemaNodeToTS_ArrayAndUnion(t *testing.T) {
	arrayNode := map[string]any{
		"type":  "array",
		"items": map[string]any{"type": "string"},
	}
	if got := schemaNodeToTS(arrayNode); got != "string[]" {
		t.Fatalf("schemaNodeToTS(array) = %q, want string[]", got)
	}

	unionNode := map[string]any{
		"type": []any{"string", "null"},
	}
	if got := schemaNodeToTS(unionNode); got != "string | null" {
		t.Fatalf("schemaNodeToTS(union) = %q, want \"string | null\"", got)
	}
}

func TestSanitizeHelpers(t *testing.T) {
	if got := sanitizePathPart("GitHub Search/Reops"); got != "github-search-reops" {
		t.Fatalf("sanitizePathPart() = %q", got)
	}
	if got := sanitizeTSIdentifier("3rd-party tool"); got != "_3rd_party_tool" {
		t.Fatalf("sanitizeTSIdentifier() = %q", got)
	}
	if got := toPascalCase("search_news"); got != "SearchNews" {
		t.Fatalf("toPascalCase() = %q", got)
	}
	if got := toCamelCase("search_news"); got != "searchNews" {
		t.Fatalf("toCamelCase() = %q", got)
	}
}

func TestEmitCodeAPI_WritesServerAndRootIndexes(t *testing.T) {
	tmp := t.TempDir()
	tools := []codeAPIToolGetResponse{
		{
			Name:     "tavily__search",
			Server:   "tavily",
			ToolName: "search",
			Tool: mcp.Tool{
				Name:        "tavily__search",
				Description: "Search the web",
				InputSchema: mcp.InputSchema{Type: "object"},
			},
		},
		{
			Name:     "gitlab__list_projects",
			Server:   "gitlab",
			ToolName: "list_projects",
			Tool: mcp.Tool{
				Name:        "gitlab__list_projects",
				Description: "List projects",
				InputSchema: mcp.InputSchema{Type: "object"},
			},
		},
	}

	if err := emitCodeAPI(tmp, tools); err != nil {
		t.Fatalf("emitCodeAPI() error = %v", err)
	}

	rootIndex := filepath.Join(tmp, "index.ts")
	if _, err := os.Stat(rootIndex); err != nil {
		t.Fatalf("missing root index: %v", err)
	}
	rootBytes, err := os.ReadFile(rootIndex)
	if err != nil {
		t.Fatalf("read root index: %v", err)
	}
	rootContent := string(rootBytes)
	if !strings.Contains(rootContent, "export * as tavily") {
		t.Fatalf("root index missing tavily export: %s", rootContent)
	}
	if !strings.Contains(rootContent, "export * as gitlab") {
		t.Fatalf("root index missing gitlab export: %s", rootContent)
	}

	wrapperPath := filepath.Join(tmp, "servers", "tavily", "search.ts")
	if _, err := os.Stat(wrapperPath); err != nil {
		t.Fatalf("missing wrapper file: %v", err)
	}
	wrapperBytes, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	if !strings.Contains(string(wrapperBytes), "export const toolName = \"tavily__search\";") {
		t.Fatalf("wrapper missing toolName constant: %s", string(wrapperBytes))
	}
}

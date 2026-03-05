package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/spf13/cobra"
)

type codeAPISearchResponse struct {
	NextCursor string `json:"nextCursor,omitempty"`
	Count      int    `json:"count"`
	Results    []struct {
		Name   string `json:"name"`
		Server string `json:"server,omitempty"`
	} `json:"results"`
}

type codeAPIToolGetResponse struct {
	Name     string   `json:"name"`
	Server   string   `json:"server,omitempty"`
	ToolName string   `json:"toolName,omitempty"`
	Tool     mcp.Tool `json:"tool"`
}

func newCodeAPICmd(socketPath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codeapi",
		Short: "Generate code-first wrappers for Loom tools",
	}
	cmd.AddCommand(newCodeAPIGenerateCmd(socketPath))
	return cmd
}

func newCodeAPIGenerateCmd(socketPath string) *cobra.Command {
	var outputDir string
	var query string
	var servers []string
	var pageLimit int
	var maxTools int

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate TypeScript wrappers from loom/tools/search + loom/tools/get",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			out := outputDir
			if !filepath.IsAbs(out) {
				out = filepath.Join(cwd, out)
			}

			tools, err := discoverToolsForCodeAPI(socketPath, strings.TrimSpace(query), servers, pageLimit, maxTools)
			if err != nil {
				return err
			}
			if len(tools) == 0 {
				return fmt.Errorf("no tools matched filters")
			}

			if err := emitCodeAPI(out, tools); err != nil {
				return err
			}

			fmt.Printf("Generated %d tool wrapper(s) in %s\n", len(tools), out)
			return nil
		},
	}

	cmd.Flags().StringVar(&outputDir, "output-dir", ".loom/codeapi", "Output directory for generated wrappers")
	cmd.Flags().StringVar(&query, "query", "", "Optional search query")
	cmd.Flags().StringSliceVar(&servers, "server", nil, "Restrict generation to one or more servers")
	cmd.Flags().IntVar(&pageLimit, "limit", 100, "Search page size per request")
	cmd.Flags().IntVar(&maxTools, "max-tools", 0, "Maximum number of tools to generate (0 = all)")
	return cmd
}

func discoverToolsForCodeAPI(socketPath, query string, servers []string, pageLimit, maxTools int) ([]codeAPIToolGetResponse, error) {
	if pageLimit <= 0 {
		pageLimit = 100
	}
	if pageLimit > 500 {
		pageLimit = 500
	}

	type discoveredTool struct {
		Name string
	}
	discovered := make([]discoveredTool, 0, pageLimit)
	cursor := ""
	for {
		raw, err := call(socketPath, "loom/tools/search", map[string]any{
			"query":   query,
			"servers": servers,
			"limit":   pageLimit,
			"cursor":  cursor,
			"detail":  "name",
		})
		if err != nil {
			return nil, fmt.Errorf("search tools: %w", err)
		}

		var page codeAPISearchResponse
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("parse search response: %w", err)
		}

		for _, result := range page.Results {
			discovered = append(discovered, discoveredTool{Name: result.Name})
			if maxTools > 0 && len(discovered) >= maxTools {
				break
			}
		}
		if maxTools > 0 && len(discovered) >= maxTools {
			break
		}
		if page.NextCursor == "" || page.Count == 0 {
			break
		}
		cursor = page.NextCursor
	}

	out := make([]codeAPIToolGetResponse, 0, len(discovered))
	for _, t := range discovered {
		raw, err := call(socketPath, "loom/tools/get", map[string]any{
			"name": t.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("get tool %s: %w", t.Name, err)
		}
		var detail codeAPIToolGetResponse
		if err := json.Unmarshal(raw, &detail); err != nil {
			return nil, fmt.Errorf("parse tool %s: %w", t.Name, err)
		}
		out = append(out, detail)
	}

	return out, nil
}

func emitCodeAPI(outputDir string, tools []codeAPIToolGetResponse) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	serversDir := filepath.Join(outputDir, "servers")
	if err := os.MkdirAll(serversDir, 0755); err != nil {
		return fmt.Errorf("create servers dir: %w", err)
	}

	grouped := make(map[string][]codeAPIToolGetResponse)
	for _, t := range tools {
		server := strings.TrimSpace(t.Server)
		if server == "" {
			server, _ = splitToolInventoryName(t.Name)
		}
		if server == "" {
			server = "unknown"
		}
		grouped[server] = append(grouped[server], t)
	}

	serverNames := make([]string, 0, len(grouped))
	for server := range grouped {
		serverNames = append(serverNames, server)
	}
	sort.Strings(serverNames)

	rootExports := make([]string, 0, len(serverNames))
	for _, server := range serverNames {
		serverTools := grouped[server]
		sort.Slice(serverTools, func(i, j int) bool { return serverTools[i].Name < serverTools[j].Name })

		serverDir := filepath.Join(serversDir, sanitizePathPart(server))
		if err := os.MkdirAll(serverDir, 0755); err != nil {
			return fmt.Errorf("create server dir %s: %w", server, err)
		}

		serverExports := make([]string, 0, len(serverTools))
		for _, tool := range serverTools {
			_, short := splitToolInventoryName(tool.Name)
			if short == "" {
				short = tool.Name
			}
			baseName := sanitizePathPart(short)
			filePath := filepath.Join(serverDir, baseName+".ts")

			rendered, err := renderToolWrapperTS(tool)
			if err != nil {
				return fmt.Errorf("render %s: %w", tool.Name, err)
			}
			if err := os.WriteFile(filePath, []byte(rendered), 0644); err != nil {
				return fmt.Errorf("write %s: %w", filePath, err)
			}
			serverExports = append(serverExports, fmt.Sprintf("export * from \"./%s\";", baseName))
		}

		indexPath := filepath.Join(serverDir, "index.ts")
		if err := os.WriteFile(indexPath, []byte(strings.Join(serverExports, "\n")+"\n"), 0644); err != nil {
			return fmt.Errorf("write %s: %w", indexPath, err)
		}
		rootExports = append(rootExports, fmt.Sprintf("export * as %s from \"./servers/%s\";", sanitizeTSIdentifier(server), sanitizePathPart(server)))
	}

	rootIndex := "/* Code generated by `loom codeapi generate`; DO NOT EDIT. */\n\n" + strings.Join(rootExports, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(outputDir, "index.ts"), []byte(rootIndex), 0644); err != nil {
		return fmt.Errorf("write root index: %w", err)
	}

	return nil
}

func renderToolWrapperTS(tool codeAPIToolGetResponse) (string, error) {
	_, short := splitToolInventoryName(tool.Name)
	if short == "" {
		short = tool.Name
	}

	typeName := toPascalCase(short) + "Args"
	funcName := toCamelCase(short)
	argsDef := renderArgsTypeScript(typeName, tool.Tool.InputSchema)

	schemaJSON, err := json.MarshalIndent(tool.Tool.InputSchema, "", "  ")
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("/* Code generated by `loom codeapi generate`; DO NOT EDIT. */\n\n")
	b.WriteString("export type ToolCaller = (name: string, args: Record<string, unknown>) => Promise<unknown>;\n\n")
	b.WriteString(argsDef)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("export const toolName = %q;\n", tool.Name))
	if strings.TrimSpace(tool.Tool.Description) != "" {
		b.WriteString(fmt.Sprintf("export const description = %q;\n", tool.Tool.Description))
	}
	b.WriteString("export const inputSchema = ")
	b.WriteString(string(schemaJSON))
	b.WriteString(" as const;\n\n")
	b.WriteString(fmt.Sprintf("export async function %s(callTool: ToolCaller, args: %s = {} as %s): Promise<unknown> {\n", funcName, typeName, typeName))
	b.WriteString("  return callTool(toolName, args as Record<string, unknown>);\n")
	b.WriteString("}\n")

	return b.String(), nil
}

func renderArgsTypeScript(typeName string, schema mcp.InputSchema) string {
	if strings.ToLower(strings.TrimSpace(schema.Type)) != "object" || len(schema.Properties) == 0 {
		return fmt.Sprintf("export type %s = Record<string, unknown>;", typeName)
	}

	required := make(map[string]struct{}, len(schema.Required))
	for _, key := range schema.Required {
		required[key] = struct{}{}
	}

	keys := make([]string, 0, len(schema.Properties))
	for key := range schema.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var lines []string
	lines = append(lines, fmt.Sprintf("export interface %s {", typeName))
	for _, key := range keys {
		propType := schemaNodeToTS(schema.Properties[key])
		optional := "?"
		if _, ok := required[key]; ok {
			optional = ""
		}
		lines = append(lines, fmt.Sprintf("  %s%s: %s;", sanitizeTSIdentifier(key), optional, propType))
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}

func schemaNodeToTS(node any) string {
	m, ok := node.(map[string]any)
	if !ok {
		return "unknown"
	}

	if enumRaw, ok := m["enum"].([]any); ok && len(enumRaw) > 0 {
		enumParts := make([]string, 0, len(enumRaw))
		for _, v := range enumRaw {
			switch vv := v.(type) {
			case string:
				enumParts = append(enumParts, fmt.Sprintf("%q", vv))
			case float64:
				enumParts = append(enumParts, fmt.Sprintf("%v", vv))
			case bool:
				if vv {
					enumParts = append(enumParts, "true")
				} else {
					enumParts = append(enumParts, "false")
				}
			}
		}
		if len(enumParts) > 0 {
			return strings.Join(enumParts, " | ")
		}
	}

	switch t := m["type"].(type) {
	case string:
		return primitiveSchemaTypeToTS(t, m)
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				continue
			}
			parts = append(parts, primitiveSchemaTypeToTS(s, m))
		}
		if len(parts) > 0 {
			return strings.Join(parts, " | ")
		}
	}
	return "unknown"
}

func primitiveSchemaTypeToTS(schemaType string, node map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(schemaType)) {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "array":
		if items, ok := node["items"]; ok {
			return schemaNodeToTS(items) + "[]"
		}
		return "unknown[]"
	case "object":
		return "Record<string, unknown>"
	case "null":
		return "null"
	default:
		return "unknown"
	}
}

func splitToolInventoryName(name string) (server, short string) {
	parts := strings.SplitN(strings.TrimSpace(name), "__", 2)
	if len(parts) != 2 {
		return "", strings.TrimSpace(name)
	}
	return parts[0], parts[1]
}

func sanitizePathPart(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "tool"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteRune('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "tool"
	}
	return out
}

func sanitizeTSIdentifier(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "tool"
	}
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := b.String()
	out = strings.Trim(out, "_")
	if out == "" {
		out = "tool"
	}
	if out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	return out
}

func toPascalCase(raw string) string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(parts) == 0 {
		return "Tool"
	}
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		runes := []rune(strings.ToLower(p))
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	out := b.String()
	if out == "" {
		return "Tool"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "Tool" + out
	}
	return out
}

func toCamelCase(raw string) string {
	p := toPascalCase(raw)
	if p == "" {
		return "tool"
	}
	runes := []rune(p)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

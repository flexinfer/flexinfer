package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/pkg/strutil"
)

const (
	syntheticBulkToolName     = "bulk"
	defaultBulkResultLimit    = 25
	defaultBulkOperationLimit = 100
	maxBulkResultLimit        = 200
	maxBulkOperationLimit     = 500
	maxBulkSummaryLength      = 160
)

var bulkServerExclusions = map[string]struct{}{
	"agent_context":       {},
	"asus_router":         {},
	"browserkit":          {},
	"codebase_memory":     {},
	"context7":            {},
	"devbox":              {},
	"docker":              {},
	"git":                 {},
	"git_worktree":        {},
	"godot_debug":         {},
	"grafana":             {},
	"helm":                {},
	"k8s_apps_k3s":        {},
	"k8s_harvester_infra": {},
	"loki":                {},
	"longhorn_k3s":        {},
	"memory":              {},
	"minio":               {},
	"morph_embeddings":    {},
	"morph_fast_apply":    {},
	"neo4j":               {},
	"ops_mcp":             {},
	"prometheus":          {},
	"qdrant":              {},
	"quality":             {},
	"redis":               {},
	"release":             {},
	"sequentialthinking":  {},
	"server_mgmt":         {},
	"tavily":              {},
	"time":                {},
	"youtube":             {},
	"zep":                 {},
}

var bulkMutatingTokens = []string{
	"activate",
	"add",
	"approve",
	"cancel",
	"close",
	"compose",
	"create",
	"delete",
	"deregister",
	"demote",
	"dispatch",
	"end",
	"import",
	"ingest",
	"merge",
	"play",
	"promote",
	"publish",
	"push",
	"purge",
	"reboot",
	"recover",
	"refresh",
	"register",
	"reject",
	"release",
	"restore",
	"retry",
	"save",
	"scale",
	"send",
	"silence",
	"start",
	"stop",
	"suspend",
	"sync",
	"transition",
	"undo",
	"update",
	"upload",
	"upsert",
	"write",
}

type bulkToolArgs struct {
	File           string `json:"file"`
	DryRun         bool   `json:"dry_run,omitempty"`
	StopOnError    bool   `json:"stop_on_error,omitempty"`
	ResultLimit    int    `json:"result_limit,omitempty"`
	OperationLimit int    `json:"operation_limit,omitempty"`
}

type bulkManifest struct {
	Version         int                     `json:"version,omitempty" yaml:"version,omitempty"`
	DefaultTool     string                  `json:"default_tool,omitempty" yaml:"default_tool,omitempty"`
	ContinueOnError *bool                   `json:"continue_on_error,omitempty" yaml:"continue_on_error,omitempty"`
	Operations      []bulkManifestOperation `json:"operations" yaml:"operations"`
}

type bulkManifestOperation struct {
	ID        string         `json:"id,omitempty" yaml:"id,omitempty"`
	Tool      string         `json:"tool,omitempty" yaml:"tool,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty" yaml:"arguments,omitempty"`
}

type bulkExecutionOptions struct {
	Server         string
	File           string
	DryRun         bool
	StopOnError    bool
	ResultLimit    int
	OperationLimit int
	ValidateTool   func(string) error
	Invoke         func(context.Context, string, map[string]any) (any, error)
}

type bulkExecutionResult struct {
	Server           string                `json:"server"`
	File             string                `json:"file"`
	DryRun           bool                  `json:"dryRun"`
	DefaultTool      string                `json:"defaultTool,omitempty"`
	StopOnError      bool                  `json:"stopOnError"`
	OperationLimit   int                   `json:"operationLimit"`
	ResultLimit      int                   `json:"resultLimit"`
	TotalOperations  int                   `json:"totalOperations"`
	Executed         int                   `json:"executed"`
	Succeeded        int                   `json:"succeeded"`
	Failed           int                   `json:"failed"`
	ResultsTruncated bool                  `json:"resultsTruncated"`
	StoppedAt        string                `json:"stoppedAt,omitempty"`
	Results          []bulkOperationResult `json:"results,omitempty"`
}

type bulkOperationResult struct {
	ID      string         `json:"id,omitempty"`
	Tool    string         `json:"tool"`
	Index   int            `json:"index"`
	OK      bool           `json:"ok"`
	Summary string         `json:"summary,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   string         `json:"error,omitempty"`
}

func bulkSyntheticTools(base []mcp.Tool) []mcp.Tool {
	if len(base) == 0 {
		return nil
	}

	byServer := make(map[string][]string)
	for _, tool := range base {
		server, shortName := splitNamespacedToolName(tool.Name)
		if server == "" || shortName == "" {
			continue
		}
		if shortName == syntheticBulkToolName {
			continue
		}
		byServer[server] = append(byServer[server], shortName)
	}

	servers := make([]string, 0, len(byServer))
	for server, toolNames := range byServer {
		if !serverSupportsBulk(server, toolNames) {
			continue
		}
		servers = append(servers, server)
	}
	slices.Sort(servers)

	tools := make([]mcp.Tool, 0, len(servers))
	for _, server := range servers {
		tools = append(tools, bulkSyntheticTool(server))
	}
	return tools
}

func visibleTools(base []mcp.Tool) []mcp.Tool {
	out := append([]mcp.Tool(nil), base...)
	out = append(out, bulkSyntheticTools(base)...)
	return out
}

func bulkSyntheticTool(server string) mcp.Tool {
	return mcp.Tool{
		Name:        server + "__" + syntheticBulkToolName,
		Description: fmt.Sprintf("Execute a compact batch of %s tool calls from a JSON or YAML manifest file.", server),
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file": map[string]any{
					"type":        "string",
					"description": "Absolute path to a JSON or YAML manifest file containing operations for this server.",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "Validate the manifest and tool names without executing any operations.",
				},
				"stop_on_error": map[string]any{
					"type":        "boolean",
					"description": "Stop at the first failed operation instead of continuing through the manifest.",
				},
				"result_limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of per-operation summaries to include in the response. Defaults to 25.",
				},
				"operation_limit": map[string]any{
					"type":        "integer",
					"description": "Safety cap: reject manifests larger than this many operations. Defaults to 100.",
				},
			},
			Required: []string{"file"},
		},
	}
}

func serverSupportsBulk(server string, toolNames []string) bool {
	if _, excluded := bulkServerExclusions[server]; excluded {
		return false
	}
	for _, toolName := range toolNames {
		if toolLooksMutating(toolName) {
			return true
		}
	}
	return false
}

func toolLooksMutating(toolName string) bool {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(toolName)), func(r rune) bool {
		return r == '_'
	})
	for _, part := range parts {
		if slices.Contains(bulkMutatingTokens, part) {
			return true
		}
	}
	return false
}

func loadBulkManifest(path string) (bulkManifest, error) {
	var manifest bulkManifest

	if !filepath.IsAbs(path) {
		return manifest, fmt.Errorf("file must be an absolute path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return manifest, fmt.Errorf("read manifest: %w", err)
	}

	if err := json.Unmarshal(data, &manifest); err != nil {
		if yamlErr := yaml.Unmarshal(data, &manifest); yamlErr != nil {
			return manifest, fmt.Errorf("parse manifest as JSON or YAML: %v / %v", err, yamlErr)
		}
	}

	return manifest, nil
}

func executeBulkManifest(ctx context.Context, opts bulkExecutionOptions, manifest bulkManifest) (bulkExecutionResult, error) {
	resultLimit := opts.ResultLimit
	if resultLimit <= 0 {
		resultLimit = defaultBulkResultLimit
	}
	if resultLimit > maxBulkResultLimit {
		resultLimit = maxBulkResultLimit
	}

	operationLimit := opts.OperationLimit
	if operationLimit <= 0 {
		operationLimit = defaultBulkOperationLimit
	}
	if operationLimit > maxBulkOperationLimit {
		operationLimit = maxBulkOperationLimit
	}

	if len(manifest.Operations) == 0 {
		return bulkExecutionResult{}, fmt.Errorf("manifest must contain at least one operation")
	}
	if len(manifest.Operations) > operationLimit {
		return bulkExecutionResult{}, fmt.Errorf("manifest has %d operations, exceeding the limit of %d", len(manifest.Operations), operationLimit)
	}

	stopOnError := opts.StopOnError
	if manifest.ContinueOnError != nil {
		stopOnError = !*manifest.ContinueOnError
	}

	result := bulkExecutionResult{
		Server:          opts.Server,
		File:            opts.File,
		DryRun:          opts.DryRun,
		DefaultTool:     strings.TrimSpace(manifest.DefaultTool),
		StopOnError:     stopOnError,
		OperationLimit:  operationLimit,
		ResultLimit:     resultLimit,
		TotalOperations: len(manifest.Operations),
	}

	if !opts.DryRun && opts.Invoke == nil {
		return bulkExecutionResult{}, fmt.Errorf("bulk executor is not configured")
	}

	for idx, op := range manifest.Operations {
		toolName, err := resolveBulkOperationTool(opts.Server, manifest.DefaultTool, op.Tool)
		if err != nil {
			result.Failed++
			result.StoppedAt = fmt.Sprintf("operation %d", idx+1)
			appendBulkResult(&result, resultLimit, bulkOperationResult{
				ID:    strings.TrimSpace(op.ID),
				Tool:  strings.TrimSpace(op.Tool),
				Index: idx + 1,
				OK:    false,
				Error: truncateBulkText(err.Error()),
			})
			return result, err
		}

		if opts.ValidateTool != nil {
			if err := opts.ValidateTool(toolName); err != nil {
				result.Failed++
				opResult := bulkOperationResult{
					ID:    strings.TrimSpace(op.ID),
					Tool:  toolName,
					Index: idx + 1,
					OK:    false,
					Error: truncateBulkText(err.Error()),
				}
				appendBulkResult(&result, resultLimit, opResult)
				if stopOnError {
					result.StoppedAt = fmt.Sprintf("operation %d", idx+1)
					return result, err
				}
				continue
			}
		}

		args := op.Arguments
		if args == nil {
			args = map[string]any{}
		}

		if opts.DryRun {
			result.Executed++
			result.Succeeded++
			appendBulkResult(&result, resultLimit, bulkOperationResult{
				ID:      strings.TrimSpace(op.ID),
				Tool:    toolName,
				Index:   idx + 1,
				OK:      true,
				Summary: "validated",
			})
			continue
		}

		payload, err := opts.Invoke(ctx, toolName, args)
		if err != nil {
			result.Executed++
			result.Failed++
			appendBulkResult(&result, resultLimit, bulkOperationResult{
				ID:    strings.TrimSpace(op.ID),
				Tool:  toolName,
				Index: idx + 1,
				OK:    false,
				Error: truncateBulkText(err.Error()),
			})
			if stopOnError {
				result.StoppedAt = fmt.Sprintf("operation %d", idx+1)
				return result, err
			}
			continue
		}

		result.Executed++
		result.Succeeded++
		appendBulkResult(&result, resultLimit, summarizeBulkPayload(idx+1, strings.TrimSpace(op.ID), toolName, payload))
	}

	return result, nil
}

func resolveBulkOperationTool(server, defaultTool, rawTool string) (string, error) {
	toolName := strings.TrimSpace(rawTool)
	if toolName == "" {
		toolName = strings.TrimSpace(defaultTool)
	}
	if toolName == "" {
		return "", errors.New("operation is missing a tool and the manifest has no default_tool")
	}

	namespacedServer, shortName := splitNamespacedToolName(toolName)
	if namespacedServer != "" && namespacedServer != server {
		return "", fmt.Errorf("operation targets %q but this bulk tool is scoped to %q", namespacedServer, server)
	}
	if shortName == syntheticBulkToolName {
		return "", errors.New("nested bulk operations are not supported")
	}
	return shortName, nil
}

func appendBulkResult(result *bulkExecutionResult, limit int, op bulkOperationResult) {
	if len(result.Results) < limit {
		result.Results = append(result.Results, op)
		return
	}
	result.ResultsTruncated = true
}

func summarizeBulkPayload(index int, id, toolName string, payload any) bulkOperationResult {
	summary := bulkOperationResult{
		ID:    id,
		Tool:  toolName,
		Index: index,
		OK:    true,
	}

	switch typed := payload.(type) {
	case map[string]any:
		compact := compactBulkMap(typed)
		summary.Result = compact
		summary.Summary = truncateBulkText(renderBulkSummary(compact))
	case []any:
		summary.Summary = fmt.Sprintf("returned %d item(s)", len(typed))
	default:
		summary.Summary = truncateBulkText(fmt.Sprint(typed))
	}

	if summary.Summary == "" {
		summary.Summary = "ok"
	}
	return summary
}

func compactBulkMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}

	keys := []string{
		"id",
		"iid",
		"number",
		"name",
		"title",
		"status",
		"state",
		"phase",
		"url",
		"web_url",
		"html_url",
		"result",
		"ok",
	}

	out := make(map[string]any)
	for _, key := range keys {
		if value, ok := in[key]; ok {
			out[key] = value
		}
	}
	if len(out) > 0 {
		return out
	}

	countKeys := []string{"count", "items", "jobs", "issues", "results"}
	for _, key := range countKeys {
		if value, ok := in[key]; ok {
			switch typed := value.(type) {
			case []any:
				out[key+"_count"] = len(typed)
			default:
				out[key] = typed
			}
		}
	}
	if len(out) > 0 {
		return out
	}

	extracted := 0
	for key, value := range in {
		out[key] = value
		extracted++
		if extracted >= 4 {
			break
		}
	}
	return out
}

func renderBulkSummary(values map[string]any) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]string, 0, len(values))
	for _, key := range []string{"id", "iid", "number", "name", "title", "status", "state", "phase", "url", "web_url", "html_url", "result", "ok"} {
		value, ok := values[key]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}
	if len(parts) == 0 {
		for key, value := range values {
			parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		}
		slices.Sort(parts)
	}
	return strings.Join(parts, " ")
}

func truncateBulkText(text string) string {
	text = strings.TrimSpace(text)
	if len(text) <= maxBulkSummaryLength {
		return text
	}
	return text[:maxBulkSummaryLength-3] + "..."
}

func parseEmbeddedStructuredText(text string) (any, bool) {
	raw, err := strutil.ExtractEmbeddedJSON([]byte(text))
	if err != nil {
		return nil, false
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	return value, true
}

func unwrapToolCallPayload(resp *mcp.Message) (any, error) {
	if resp == nil {
		return nil, errors.New("tool call returned no response")
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}

	var envelope mcp.CallToolResult
	if err := json.Unmarshal(resp.Result, &envelope); err != nil {
		return nil, fmt.Errorf("parse tool result envelope: %w", err)
	}
	if envelope.IsError {
		if len(envelope.Content) == 0 {
			return nil, errors.New("tool returned an empty error result")
		}
		return nil, errors.New(strings.TrimSpace(envelope.Content[0].Text))
	}
	if len(envelope.Content) == 0 {
		return map[string]any{}, nil
	}

	text := envelope.Content[0].Text
	if text == "" {
		return map[string]any{}, nil
	}
	if parsed, ok := parseEmbeddedStructuredText(text); ok {
		return parsed, nil
	}
	return text, nil
}

func (d *Daemon) serverEligibleForBulk(server string) bool {
	if _, excluded := bulkServerExclusions[server]; excluded {
		return false
	}

	d.toolCache.mu.RLock()
	tools := append([]mcp.Tool(nil), d.toolCache.tools...)
	d.toolCache.mu.RUnlock()

	if len(tools) == 0 {
		tools = d.getStaticToolsFromRegistry()
	}
	if len(tools) == 0 {
		return true
	}

	serverTools := make([]string, 0)
	for _, tool := range tools {
		toolServer, shortName := splitNamespacedToolName(tool.Name)
		if toolServer != server || shortName == "" || shortName == syntheticBulkToolName {
			continue
		}
		serverTools = append(serverTools, shortName)
	}
	return serverSupportsBulk(server, serverTools)
}

func (d *Daemon) hasVisibleTool(server, tool string) bool {
	compound := server + "__" + tool
	d.toolCache.mu.RLock()
	tools := append([]mcp.Tool(nil), d.toolCache.tools...)
	d.toolCache.mu.RUnlock()
	if len(tools) == 0 {
		tools = d.getStaticToolsFromRegistry()
	}

	for _, visible := range visibleTools(tools) {
		if visible.Name == compound {
			return true
		}
	}
	return false
}

func (p *callPipeline) isSyntheticBulkTool() bool {
	return p.toolName == syntheticBulkToolName
}

func (p *callPipeline) executeSyntheticBulk() *mcp.Message {
	p.stage = stageExecute

	if !p.daemon.serverEligibleForBulk(p.serverName) {
		return p.invalidParamsError(fmt.Sprintf("bulk is not enabled for server: %s", p.serverName))
	}

	rawArgs := p.params.Arguments
	if len(rawArgs) == 0 {
		rawArgs = p.params.Params
	}

	var args bulkToolArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return p.invalidParamsError("invalid bulk arguments: " + err.Error())
	}

	args.File = strings.TrimSpace(args.File)
	if args.File == "" {
		return p.invalidParamsError("file is required")
	}
	if !filepath.IsAbs(args.File) {
		return p.invalidParamsError("file must be an absolute path")
	}

	manifest, err := loadBulkManifest(args.File)
	if err != nil {
		return p.invalidParamsError(err.Error())
	}

	result, execErr := executeBulkManifest(p.ctx, bulkExecutionOptions{
		Server:         p.serverName,
		File:           args.File,
		DryRun:         args.DryRun,
		StopOnError:    args.StopOnError,
		ResultLimit:    args.ResultLimit,
		OperationLimit: args.OperationLimit,
		ValidateTool: func(tool string) error {
			if tool == syntheticBulkToolName {
				return errors.New("nested bulk operations are not supported")
			}
			if !p.daemon.hasVisibleTool(p.serverName, tool) {
				return fmt.Errorf("unknown tool for %s: %s", p.serverName, tool)
			}
			return nil
		},
		Invoke: func(ctx context.Context, tool string, args map[string]any) (any, error) {
			nextID := time.Now().UnixNano()
			callID := fmt.Sprintf("bulk-%s-%d", p.serverName, nextID)
			req, err := mcp.NewRequest(callID, "tools/call", callParams{
				Server:    p.serverName,
				Tool:      tool,
				Method:    "tools/call",
				AgentID:   p.params.AgentID,
				AgentType: p.params.AgentType,
				SessionID: p.params.SessionID,
				Arguments: mustMarshalJSON(args),
			})
			if err != nil {
				return nil, err
			}
			resp, err := p.daemon.handleCallWithOptions(ctx, req, true)
			if err != nil {
				return nil, err
			}
			return unwrapToolCallPayload(resp)
		},
	}, manifest)
	_ = execErr

	payload, err := mcp.JSONResult(result)
	if err != nil {
		return p.internalError(err)
	}
	resp, err := mcp.NewResponse(p.msg.ID, payload)
	if err != nil {
		return p.internalError(err)
	}

	duration := time.Since(p.auditStart)
	p.daemon.metrics.RecordRequest(p.serverName, p.method, "success", "synthetic", duration)
	p.daemon.emitAudit(p.params, p.serverName, p.toolName, "synthetic", p.auditStart, "success", "", false, nil, p.stage)
	p.emitDecompHintIfLarge(resp)
	return resp
}

func mustMarshalJSON(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage(`{}`)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(data)
}

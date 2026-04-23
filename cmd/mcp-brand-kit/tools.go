package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
	"github.com/crb2nu/loom/pkg/validate"
)

func registerTools(srv *mcpscaffold.Server) {
	srv.AddTracedTool(mcp.Tool{
		Name:        "brand_list_repos",
		Description: "List repositories that can be inspected or processed by banner-kit.",
		InputSchema: listSchema(),
	}, handleListRepos)

	srv.AddTracedTool(mcp.Tool{
		Name:        "brand_inspect",
		Description: "Inspect one repository's local branding assets and planned banner-kit command.",
		InputSchema: inspectSchema(),
	}, handleInspect)

	srv.AddTracedTool(mcp.Tool{
		Name:        "brand_lint",
		Description: "Run banner-kit lint with JSON output when supported by the CLI.",
		InputSchema: runSchema(false),
	}, handleLint)

	srv.AddTracedTool(mcp.Tool{
		Name:        "brand_preview",
		Description: "Preview banner-kit fixes using fix --dry-run.",
		InputSchema: runSchema(false),
	}, handlePreview)

	srv.AddTracedTool(mcp.Tool{
		Name:        "brand_render",
		Description: "Generate selected branding assets for explicit repositories or an explicit all=true selection.",
		InputSchema: renderSchema(),
	}, handleRender)

	srv.AddTracedTool(mcp.Tool{
		Name:        "brand_fix",
		Description: "Apply banner-kit fixes for explicit repositories or an explicit all=true selection.",
		InputSchema: runSchema(true),
	}, handleFix)
}

func handleListRepos(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p, errResult := parseParams(args, false)
	if errResult != nil {
		return errResult, nil
	}
	result, err := listRepos(p)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(result)
}

func handleInspect(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	repo := v.Required("repo")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	p, errResult := parseParams(args, false)
	if errResult != nil {
		return errResult, nil
	}
	result, err := inspectRepo(p, repo)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(result)
}

func handleLint(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p, errResult := parseParams(args, false)
	if errResult != nil {
		return errResult, nil
	}
	extra := []string{}
	if p.Verify {
		extra = append(extra, "--verify")
	}
	result, err := runBrandKit(ctx, p, "lint", extra...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(result)
}

func handlePreview(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p, errResult := parseParams(args, false)
	if errResult != nil {
		return errResult, nil
	}
	extra := []string{"--dry-run"}
	if p.Verify {
		extra = append(extra, "--verify")
	}
	result, err := runBrandKit(ctx, p, "fix", extra...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(result)
}

func handleRender(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p, errResult := parseParams(args, true)
	if errResult != nil {
		return errResult, nil
	}
	action := p.Asset
	if action == "" {
		action = "generate"
	}
	result, err := runBrandKit(ctx, p, action)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(result)
}

func handleFix(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p, errResult := parseParams(args, true)
	if errResult != nil {
		return errResult, nil
	}
	extra := []string{}
	if p.Verify {
		extra = append(extra, "--verify")
	}
	result, err := runBrandKit(ctx, p, "fix", extra...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(result)
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcp.TextResult(string(data)), nil
}

func parseParams(args map[string]any, mutating bool) (brandParams, *mcp.CallToolResult) {
	v := validate.NewArgs(args)
	p := brandParams{
		Root:   v.String("root", ""),
		Kind:   v.Enum("kind", "", "library", "service"),
		Config: v.String("config", ""),
		Repos:  v.StringSlice("repos"),
		Verify: v.Bool("verify", false),
		All:    v.Bool("all", false),
		Limit:  v.IntRange("limit", maxRepos, 1, maxRepos),
		Asset:  v.Enum("asset", "generate", "generate", "header", "icon"),
	}
	if err := v.Validate(); err != nil {
		return brandParams{}, mcp.ErrorResult(err)
	}
	if mutating && len(p.Repos) == 0 && !p.All {
		return brandParams{}, mcp.ErrorResult(fmt.Errorf("mutating brand operations require repos or explicit all=true"))
	}
	if err := validateRepos(p.Repos); err != nil {
		return brandParams{}, mcp.ErrorResult(err)
	}
	return p, nil
}

func commonProperties() map[string]any {
	return map[string]any{
		"root": map[string]any{
			"type":        "string",
			"description": "Workspace or bucket root. Defaults to BRAND_KIT_DEFAULT_ROOT.",
		},
		"kind": map[string]any{
			"type":        "string",
			"description": "Repository kind. When root is a workspace, library maps to libs and service maps to services.",
			"enum":        []string{"library", "service"},
		},
		"config": map[string]any{
			"type":        "string",
			"description": "Optional banner-kit config path.",
		},
	}
}

func listSchema() mcp.InputSchema {
	props := commonProperties()
	props["limit"] = map[string]any{
		"type":        "integer",
		"description": "Maximum repositories to return. Defaults to 200.",
	}
	return mcp.InputSchema{Type: "object", Properties: props}
}

func inspectSchema() mcp.InputSchema {
	props := commonProperties()
	props["repo"] = map[string]any{
		"type":        "string",
		"description": "Repository directory name to inspect.",
	}
	return mcp.InputSchema{Type: "object", Properties: props, Required: []string{"repo"}}
}

func runSchema(mutating bool) mcp.InputSchema {
	props := commonProperties()
	props["repos"] = map[string]any{
		"type":        "array",
		"description": "Repository names to process.",
		"items":       map[string]any{"type": "string"},
	}
	props["verify"] = map[string]any{
		"type":        "boolean",
		"description": "Pass --verify to banner-kit when supported.",
	}
	if mutating {
		props["all"] = map[string]any{
			"type":        "boolean",
			"description": "Explicitly allow a mutating operation to run without repo selectors.",
		}
	}
	return mcp.InputSchema{Type: "object", Properties: props}
}

func renderSchema() mcp.InputSchema {
	schema := runSchema(true)
	schema.Properties["asset"] = map[string]any{
		"type":        "string",
		"description": "Asset command to run.",
		"enum":        []string{"generate", "header", "icon"},
	}
	return schema
}

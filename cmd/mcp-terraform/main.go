// mcp-terraform provides MCP tools for Terraform state and plan management.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version = "0.1.0"

	// Terraform Cloud/Enterprise settings
	tfcHost  = env.String("TFC_HOST", "https://app.terraform.io")
	tfcToken = os.Getenv("TFC_TOKEN")
	tfcOrg   = os.Getenv("TFC_ORGANIZATION")

	httpClient *httpclient.Client
)

func init() {
	cfg := httpclient.DefaultConfig()
	cfg.Timeout = 60 * time.Second
	// Support TFC_SKIP_VERIFY in addition to the standard TLS_SKIP_VERIFY
	if skipVerify := os.Getenv("TFC_SKIP_VERIFY"); strings.ToLower(skipVerify) == "true" || skipVerify == "1" {
		cfg.TLSSkipVerify = true
	}
	httpClient = httpclient.New(cfg)
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-terraform",
		logger,
	)
	if err != nil {
		logger.Warn("OTel tracer init failed",

			"error",

			err)
	}
	defer func() {
		_ = shutdownTracer(ctx)
	}()
	tracer := mcpotel.
		Tracer(tp, "mcp-terraform")

	logger.Info("starting server", "name", "mcp-terraform", "version", version, "host", tfcHost)

	server := mcp.NewServer("mcp-terraform", version)
	server.SetInstructions("Terraform Cloud/Enterprise state and plan management tools. Configure with TFC_TOKEN and TFC_ORGANIZATION. Optionally set TFC_HOST for Enterprise.")

	// Workspaces
	server.AddTool(mcp.Tool{
		Name:        "tf_list_workspaces",
		Description: "List workspaces in the organization",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"search": map[string]any{
					"type":        "string",
					"description": "Search by workspace name",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of results per page (default: 20)",
				},
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default: 1)",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "tf_list_workspaces", handleListWorkspaces))

	server.AddTool(mcp.Tool{
		Name:        "tf_get_workspace",
		Description: "Get details of a specific workspace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Workspace name",
				},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(

		// State
		tracer, "tf_get_workspace", handleGetWorkspace))

	server.AddTool(mcp.Tool{
		Name:        "tf_current_state",
		Description: "Get current state version for a workspace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "Workspace name",
				},
			},
			Required: []string{"workspace"},
		},
	}, mcpotel.TracedToolHandler(tracer, "tf_current_state", handleCurrentState))

	server.AddTool(mcp.Tool{
		Name:        "tf_state_resources",
		Description: "List resources in the current state",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "Workspace name",
				},
				"filter_type": map[string]any{
					"type":        "string",
					"description": "Filter by resource type (e.g., 'aws_instance')",
				},
			},
			Required: []string{"workspace"},
		},
	}, mcpotel.TracedToolHandler(tracer, "tf_state_resources", handleStateResources))

	server.AddTool(mcp.Tool{
		Name:        "tf_state_outputs",
		Description: "Get outputs from the current state",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "Workspace name",
				},
			},
			Required: []string{"workspace"},
		},
	}, mcpotel.TracedToolHandler(

		// Runs
		tracer, "tf_state_outputs", handleStateOutputs))

	server.AddTool(mcp.Tool{
		Name:        "tf_list_runs",
		Description: "List runs for a workspace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "Workspace name",
				},
				"status": map[string]any{
					"type":        "string",
					"description": "Filter by status (pending, planning, planned, applying, applied, errored, etc.)",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of results (default: 20)",
				},
			},
			Required: []string{"workspace"},
		},
	}, mcpotel.TracedToolHandler(tracer, "tf_list_runs", handleListRuns))

	server.AddTool(mcp.Tool{
		Name:        "tf_get_run",
		Description: "Get details of a specific run",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id": map[string]any{
					"type":        "string",
					"description": "Run ID",
				},
			},
			Required: []string{"run_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "tf_get_run", handleGetRun))

	server.AddTool(mcp.Tool{
		Name:        "tf_run_plan",
		Description: "Get the plan output for a run",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"run_id": map[string]any{
					"type":        "string",
					"description": "Run ID",
				},
			},
			Required: []string{"run_id"},
		},
	}, mcpotel.TracedToolHandler(

		// Variables
		tracer, "tf_run_plan", handleRunPlan))

	server.AddTool(mcp.Tool{
		Name:        "tf_list_variables",
		Description: "List variables for a workspace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "Workspace name",
				},
			},
			Required: []string{"workspace"},
		},
	}, mcpotel.TracedToolHandler(

		// Variable Sets
		tracer, "tf_list_variables", handleListVariables))

	server.AddTool(mcp.Tool{
		Name:        "tf_list_varsets",
		Description: "List variable sets in the organization",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "tf_list_varsets", handleListVarsets))

	server.AddTool(mcp.Tool{
		Name:        "tf_get_varset",
		Description: "Get details of a variable set",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"varset_id": map[string]any{
					"type":        "string",
					"description": "Variable set ID",
				},
			},
			Required: []string{"varset_id"},
		},
	}, mcpotel.TracedToolHandler(

		// Organizations
		tracer, "tf_get_varset", handleGetVarset))

	server.AddTool(mcp.Tool{
		Name:        "tf_get_organization",
		Description: "Get organization details",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer,

		// Policies
		"tf_get_organization", handleGetOrganization))

	server.AddTool(mcp.Tool{
		Name:        "tf_list_policies",
		Description: "List Sentinel policies in the organization",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"page_size": map[string]any{
					"type":        "integer",
					"description": "Number of results (default: 20)",
				},
			},
		},
	}, mcpotel.TracedToolHandler(

		// Registry Modules
		tracer, "tf_list_policies", handleListPolicies))

	server.AddTool(mcp.Tool{
		Name:        "tf_list_modules",
		Description: "List private registry modules",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"provider": map[string]any{
					"type":        "string",
					"description": "Filter by provider (e.g., 'aws')",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "tf_list_modules", handleListModules))

	return server.Run(ctx)
}

// tfcRequest makes an authenticated request to Terraform Cloud API
func tfcRequest(ctx context.Context, method, path string) (map[string]any, error) {
	apiURL := strings.TrimSuffix(tfcHost, "/") + "/api/v2" + path

	req, err := http.NewRequestWithContext(ctx, method, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tfcToken)
	req.Header.Set("Content-Type", "application/vnd.api+json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]any
		if json.Unmarshal(body, &errResp) == nil {
			if errors, ok := errResp["errors"].([]any); ok && len(errors) > 0 {
				if errObj, ok := errors[0].(map[string]any); ok {
					return nil, mcperror.APIError("Terraform Cloud", resp.StatusCode, fmt.Sprintf("%v", errObj["detail"]))
				}
			}
		}
		return nil, mcperror.APIError("Terraform Cloud", resp.StatusCode, string(body))
	}

	var result map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
	}

	return result, nil
}

func handleListWorkspaces(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	search := v.String("search", "")
	pageSize := v.Int("page_size", 20)
	page := v.Int("page", 1)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "/organizations/" + tfcOrg + "/workspaces"
	params := []string{}

	if search != "" {
		params = append(params, "search[name]="+search)
	}
	if pageSize != 20 {
		params = append(params, "page[size]="+strconv.Itoa(pageSize))
	}
	if page != 1 {
		params = append(params, "page[number]="+strconv.Itoa(page))
	}

	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}

	result, err := tfcRequest(ctx, "GET", path)
	if err != nil {
		return nil, err
	}

	workspaces := []map[string]any{}
	if data, ok := result["data"].([]any); ok {
		for _, item := range data {
			if ws, ok := item.(map[string]any); ok {
				workspaces = append(workspaces, formatWorkspace(ws))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"workspaces": workspaces,
		"count":      len(workspaces),
	})
}

func handleGetWorkspace(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg+"/workspaces/"+name)
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].(map[string]any); ok {
		return mcp.JSONResult(formatWorkspaceDetailed(data))
	}

	return mcp.ErrorResult(fmt.Errorf("workspace not found")), nil
}

func handleCurrentState(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workspace := v.Required("workspace")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get workspace ID first
	wsResult, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg+"/workspaces/"+workspace)
	if err != nil {
		return nil, err
	}

	wsData, ok := wsResult["data"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("workspace not found")), nil
	}
	wsID := wsData["id"].(string)

	// Get current state version
	result, err := tfcRequest(ctx, "GET", "/workspaces/"+wsID+"/current-state-version")
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].(map[string]any); ok {
		return mcp.JSONResult(formatStateVersion(data))
	}

	return mcp.JSONResult(map[string]any{
		"message": "No state version found",
	})
}

func handleStateResources(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workspace := v.Required("workspace")
	filterType := v.String("filter_type", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get workspace ID
	wsResult, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg+"/workspaces/"+workspace)
	if err != nil {
		return nil, err
	}

	wsData, ok := wsResult["data"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("workspace not found")), nil
	}
	wsID := wsData["id"].(string)

	// Get resources
	result, err := tfcRequest(ctx, "GET", "/workspaces/"+wsID+"/resources")
	if err != nil {
		return nil, err
	}

	resources := []map[string]any{}
	if data, ok := result["data"].([]any); ok {
		for _, item := range data {
			if res, ok := item.(map[string]any); ok {
				attrs, _ := res["attributes"].(map[string]any)
				resType, _ := attrs["address"].(string)

				if filterType != "" && !strings.Contains(resType, filterType) {
					continue
				}

				resources = append(resources, map[string]any{
					"id":       res["id"],
					"address":  attrs["address"],
					"name":     attrs["name"],
					"provider": attrs["provider-name"],
					"module":   attrs["module"],
				})
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"resources": resources,
		"count":     len(resources),
	})
}

func handleStateOutputs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workspace := v.Required("workspace")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get workspace ID
	wsResult, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg+"/workspaces/"+workspace)
	if err != nil {
		return nil, err
	}

	wsData, ok := wsResult["data"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("workspace not found")), nil
	}
	wsID := wsData["id"].(string)

	// Get current state version with outputs
	result, err := tfcRequest(ctx, "GET", "/workspaces/"+wsID+"/current-state-version?include=outputs")
	if err != nil {
		return nil, err
	}

	outputs := []map[string]any{}
	if included, ok := result["included"].([]any); ok {
		for _, item := range included {
			if output, ok := item.(map[string]any); ok {
				if output["type"] == "state-version-outputs" {
					attrs, _ := output["attributes"].(map[string]any)
					outputs = append(outputs, map[string]any{
						"name":      attrs["name"],
						"value":     attrs["value"],
						"sensitive": attrs["sensitive"],
						"type":      attrs["type"],
					})
				}
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"outputs": outputs,
		"count":   len(outputs),
	})
}

func handleListRuns(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workspace := v.Required("workspace")
	status := v.String("status", "")
	pageSize := v.Int("page_size", 20)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get workspace ID
	wsResult, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg+"/workspaces/"+workspace)
	if err != nil {
		return nil, err
	}

	wsData, ok := wsResult["data"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("workspace not found")), nil
	}
	wsID := wsData["id"].(string)

	path := "/workspaces/" + wsID + "/runs"
	params := []string{}

	if status != "" {
		params = append(params, "filter[status]="+status)
	}
	if pageSize != 20 {
		params = append(params, "page[size]="+strconv.Itoa(pageSize))
	}

	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}

	result, err := tfcRequest(ctx, "GET", path)
	if err != nil {
		return nil, err
	}

	runs := []map[string]any{}
	if data, ok := result["data"].([]any); ok {
		for _, item := range data {
			if run, ok := item.(map[string]any); ok {
				runs = append(runs, formatRun(run))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"runs":  runs,
		"count": len(runs),
	})
}

func handleGetRun(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := v.Required("run_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := tfcRequest(ctx, "GET", "/runs/"+runID)
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].(map[string]any); ok {
		return mcp.JSONResult(formatRunDetailed(data))
	}

	return mcp.ErrorResult(fmt.Errorf("run not found")), nil
}

func handleRunPlan(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	runID := v.Required("run_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get run to find plan ID
	runResult, err := tfcRequest(ctx, "GET", "/runs/"+runID)
	if err != nil {
		return nil, err
	}

	runData, ok := runResult["data"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("run not found")), nil
	}

	relationships, _ := runData["relationships"].(map[string]any)
	planRel, _ := relationships["plan"].(map[string]any)
	planData, _ := planRel["data"].(map[string]any)
	planID, _ := planData["id"].(string)

	if planID == "" {
		return mcp.ErrorResult(fmt.Errorf("no plan found for run")), nil
	}

	// Get plan
	result, err := tfcRequest(ctx, "GET", "/plans/"+planID)
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].(map[string]any); ok {
		attrs, _ := data["attributes"].(map[string]any)
		return mcp.JSONResult(map[string]any{
			"id":                    data["id"],
			"status":                attrs["status"],
			"has_changes":           attrs["has-changes"],
			"resource_additions":    attrs["resource-additions"],
			"resource_changes":      attrs["resource-changes"],
			"resource_destructions": attrs["resource-destructions"],
			"log_read_url":          attrs["log-read-url"],
		})
	}

	return mcp.ErrorResult(fmt.Errorf("plan not found")), nil
}

func handleListVariables(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workspace := v.Required("workspace")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get workspace ID
	wsResult, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg+"/workspaces/"+workspace)
	if err != nil {
		return nil, err
	}

	wsData, ok := wsResult["data"].(map[string]any)
	if !ok {
		return mcp.ErrorResult(fmt.Errorf("workspace not found")), nil
	}
	wsID := wsData["id"].(string)

	result, err := tfcRequest(ctx, "GET", "/workspaces/"+wsID+"/vars")
	if err != nil {
		return nil, err
	}

	variables := []map[string]any{}
	if data, ok := result["data"].([]any); ok {
		for _, item := range data {
			if v, ok := item.(map[string]any); ok {
				attrs, _ := v["attributes"].(map[string]any)
				variable := map[string]any{
					"id":          v["id"],
					"key":         attrs["key"],
					"category":    attrs["category"],
					"sensitive":   attrs["sensitive"],
					"hcl":         attrs["hcl"],
					"description": attrs["description"],
				}
				// Don't include value for sensitive vars
				if sensitive, ok := attrs["sensitive"].(bool); !ok || !sensitive {
					variable["value"] = attrs["value"]
				}
				variables = append(variables, variable)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"variables": variables,
		"count":     len(variables),
	})
}

func handleListVarsets(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg+"/varsets")
	if err != nil {
		return nil, err
	}

	varsets := []map[string]any{}
	if data, ok := result["data"].([]any); ok {
		for _, item := range data {
			if vs, ok := item.(map[string]any); ok {
				attrs, _ := vs["attributes"].(map[string]any)
				varsets = append(varsets, map[string]any{
					"id":          vs["id"],
					"name":        attrs["name"],
					"description": attrs["description"],
					"global":      attrs["global"],
					"var_count":   attrs["var-count"],
				})
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"variable_sets": varsets,
		"count":         len(varsets),
	})
}

func handleGetVarset(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	varsetID := v.Required("varset_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := tfcRequest(ctx, "GET", "/varsets/"+varsetID+"?include=vars")
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].(map[string]any); ok {
		attrs, _ := data["attributes"].(map[string]any)
		response := map[string]any{
			"id":          data["id"],
			"name":        attrs["name"],
			"description": attrs["description"],
			"global":      attrs["global"],
		}

		// Include variables
		variables := []map[string]any{}
		if included, ok := result["included"].([]any); ok {
			for _, item := range included {
				if v, ok := item.(map[string]any); ok {
					if v["type"] == "vars" {
						vAttrs, _ := v["attributes"].(map[string]any)
						variable := map[string]any{
							"id":        v["id"],
							"key":       vAttrs["key"],
							"category":  vAttrs["category"],
							"sensitive": vAttrs["sensitive"],
						}
						if sensitive, ok := vAttrs["sensitive"].(bool); !ok || !sensitive {
							variable["value"] = vAttrs["value"]
						}
						variables = append(variables, variable)
					}
				}
			}
		}
		response["variables"] = variables

		return mcp.JSONResult(response)
	}

	return mcp.ErrorResult(fmt.Errorf("variable set not found")), nil
}

func handleGetOrganization(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := tfcRequest(ctx, "GET", "/organizations/"+tfcOrg)
	if err != nil {
		return nil, err
	}

	if data, ok := result["data"].(map[string]any); ok {
		attrs, _ := data["attributes"].(map[string]any)
		return mcp.JSONResult(map[string]any{
			"id":                    data["id"],
			"name":                  attrs["name"],
			"email":                 attrs["email"],
			"external_id":           attrs["external-id"],
			"created_at":            attrs["created-at"],
			"trial_expires_at":      attrs["trial-expires-at"],
			"cost_estimation":       attrs["cost-estimation-enabled"],
			"sentinel":              attrs["sentinel-enabled"],
			"run_task":              attrs["run-task-enabled"],
			"two_factor_conformant": attrs["two-factor-conformant"],
		})
	}

	return mcp.ErrorResult(fmt.Errorf("organization not found")), nil
}

func handleListPolicies(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	pageSize := v.Int("page_size", 20)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "/organizations/" + tfcOrg + "/policies"
	if pageSize != 20 {
		path += "?page[size]=" + strconv.Itoa(pageSize)
	}

	result, err := tfcRequest(ctx, "GET", path)
	if err != nil {
		return nil, err
	}

	policies := []map[string]any{}
	if data, ok := result["data"].([]any); ok {
		for _, item := range data {
			if p, ok := item.(map[string]any); ok {
				attrs, _ := p["attributes"].(map[string]any)
				policies = append(policies, map[string]any{
					"id":                p["id"],
					"name":              attrs["name"],
					"description":       attrs["description"],
					"enforcement_level": attrs["enforcement-level"],
					"kind":              attrs["kind"],
				})
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"policies": policies,
		"count":    len(policies),
	})
}

func handleListModules(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	provider := v.String("provider", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := "/organizations/" + tfcOrg + "/registry-modules"
	if provider != "" {
		path += "?filter[provider]=" + provider
	}

	result, err := tfcRequest(ctx, "GET", path)
	if err != nil {
		return nil, err
	}

	modules := []map[string]any{}
	if data, ok := result["data"].([]any); ok {
		for _, item := range data {
			if m, ok := item.(map[string]any); ok {
				attrs, _ := m["attributes"].(map[string]any)
				modules = append(modules, map[string]any{
					"id":        m["id"],
					"name":      attrs["name"],
					"namespace": attrs["namespace"],
					"provider":  attrs["provider"],
					"status":    attrs["status"],
					"version":   attrs["version-statuses"],
				})
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"modules": modules,
		"count":   len(modules),
	})
}

// Format helpers

func formatWorkspace(ws map[string]any) map[string]any {
	attrs, _ := ws["attributes"].(map[string]any)
	return map[string]any{
		"id":                ws["id"],
		"name":              attrs["name"],
		"description":       attrs["description"],
		"terraform_version": attrs["terraform-version"],
		"auto_apply":        attrs["auto-apply"],
		"execution_mode":    attrs["execution-mode"],
		"working_directory": attrs["working-directory"],
		"locked":            attrs["locked"],
		"resource_count":    attrs["resource-count"],
		"updated_at":        attrs["updated-at"],
	}
}

func formatWorkspaceDetailed(ws map[string]any) map[string]any {
	formatted := formatWorkspace(ws)
	attrs, _ := ws["attributes"].(map[string]any)

	formatted["created_at"] = attrs["created-at"]
	formatted["environment"] = attrs["environment"]
	formatted["file_triggers_enabled"] = attrs["file-triggers-enabled"]
	formatted["trigger_prefixes"] = attrs["trigger-prefixes"]
	formatted["queue_all_runs"] = attrs["queue-all-runs"]
	formatted["speculative_enabled"] = attrs["speculative-enabled"]
	formatted["vcs_repo"] = attrs["vcs-repo"]

	return formatted
}

func formatStateVersion(sv map[string]any) map[string]any {
	attrs, _ := sv["attributes"].(map[string]any)
	return map[string]any{
		"id":                sv["id"],
		"created_at":        attrs["created-at"],
		"serial":            attrs["serial"],
		"state_version":     attrs["state-version"],
		"terraform_version": attrs["terraform-version"],
		"resource_count":    attrs["resources-processed"],
	}
}

func formatRun(run map[string]any) map[string]any {
	attrs, _ := run["attributes"].(map[string]any)
	return map[string]any{
		"id":          run["id"],
		"status":      attrs["status"],
		"message":     attrs["message"],
		"source":      attrs["source"],
		"created_at":  attrs["created-at"],
		"has_changes": attrs["has-changes"],
		"is_destroy":  attrs["is-destroy"],
		"auto_apply":  attrs["auto-apply"],
	}
}

func formatRunDetailed(run map[string]any) map[string]any {
	formatted := formatRun(run)
	attrs, _ := run["attributes"].(map[string]any)

	formatted["trigger_reason"] = attrs["trigger-reason"]
	formatted["target_addrs"] = attrs["target-addrs"]
	formatted["refresh"] = attrs["refresh"]
	formatted["refresh_only"] = attrs["refresh-only"]
	formatted["plan_only"] = attrs["plan-only"]

	if statusTimestamps, ok := attrs["status-timestamps"].(map[string]any); ok {
		formatted["timestamps"] = statusTimestamps
	}

	return formatted
}

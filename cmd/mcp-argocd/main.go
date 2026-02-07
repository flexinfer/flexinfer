// mcp-argocd provides MCP tools for ArgoCD GitOps application management.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version = "0.1.0"

	argocdURL   = env.String("ARGOCD_SERVER", "https://argocd.local")
	argocdToken = os.Getenv("ARGOCD_AUTH_TOKEN")

	httpClient *httpclient.Client
)

func init() {
	cfg := httpclient.DefaultConfig()
	// Check ARGOCD_INSECURE env var (in addition to TLS_SKIP_VERIFY)
	if skipVerify := os.Getenv("ARGOCD_INSECURE"); strings.ToLower(skipVerify) == "true" || skipVerify == "1" {
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
	logger.Info("starting server", "name", "mcp-argocd", "version", version, "url", argocdURL)

	server := mcp.NewServer("mcp-argocd", version)
	server.SetInstructions("ArgoCD GitOps application management tools. Configure with ARGOCD_SERVER and ARGOCD_AUTH_TOKEN. Set ARGOCD_INSECURE=true to skip TLS verification.")

	// Applications
	server.AddTool(mcp.Tool{
		Name:        "argocd_list_apps",
		Description: "List all ArgoCD applications",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Filter by project name",
				},
				"selector": map[string]any{
					"type":        "string",
					"description": "Label selector (e.g., 'team=backend')",
				},
			},
		},
	}, handleListApps)

	server.AddTool(mcp.Tool{
		Name:        "argocd_get_app",
		Description: "Get details of a specific application",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Application name",
				},
			},
			Required: []string{"name"},
		},
	}, handleGetApp)

	server.AddTool(mcp.Tool{
		Name:        "argocd_app_resources",
		Description: "List resources managed by an application",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Application name",
				},
			},
			Required: []string{"name"},
		},
	}, handleAppResources)

	server.AddTool(mcp.Tool{
		Name:        "argocd_app_manifests",
		Description: "Get rendered manifests for an application",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Application name",
				},
				"revision": map[string]any{
					"type":        "string",
					"description": "Git revision (default: current)",
				},
			},
			Required: []string{"name"},
		},
	}, handleAppManifests)

	server.AddTool(mcp.Tool{
		Name:        "argocd_app_diff",
		Description: "Get diff between live and desired state",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Application name",
				},
			},
			Required: []string{"name"},
		},
	}, handleAppDiff)

	server.AddTool(mcp.Tool{
		Name:        "argocd_sync_app",
		Description: "Sync an application to its target state",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Application name",
				},
				"revision": map[string]any{
					"type":        "string",
					"description": "Git revision to sync to",
				},
				"prune": map[string]any{
					"type":        "boolean",
					"description": "Prune resources not in Git",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "Perform dry run only",
				},
			},
			Required: []string{"name"},
		},
	}, handleSyncApp)

	server.AddTool(mcp.Tool{
		Name:        "argocd_refresh_app",
		Description: "Refresh application state from Git",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Application name",
				},
				"hard": map[string]any{
					"type":        "boolean",
					"description": "Force cache invalidation",
				},
			},
			Required: []string{"name"},
		},
	}, handleRefreshApp)

	server.AddTool(mcp.Tool{
		Name:        "argocd_app_history",
		Description: "Get sync history for an application",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Application name",
				},
			},
			Required: []string{"name"},
		},
	}, handleAppHistory)

	// Projects
	server.AddTool(mcp.Tool{
		Name:        "argocd_list_projects",
		Description: "List all ArgoCD projects",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListProjects)

	server.AddTool(mcp.Tool{
		Name:        "argocd_get_project",
		Description: "Get details of a specific project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Project name",
				},
			},
			Required: []string{"name"},
		},
	}, handleGetProject)

	// Repositories
	server.AddTool(mcp.Tool{
		Name:        "argocd_list_repos",
		Description: "List configured Git repositories",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListRepos)

	server.AddTool(mcp.Tool{
		Name:        "argocd_get_repo",
		Description: "Get details of a specific repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository URL",
				},
			},
			Required: []string{"repo"},
		},
	}, handleGetRepo)

	// Clusters
	server.AddTool(mcp.Tool{
		Name:        "argocd_list_clusters",
		Description: "List configured Kubernetes clusters",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListClusters)

	server.AddTool(mcp.Tool{
		Name:        "argocd_get_cluster",
		Description: "Get details of a specific cluster",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"server": map[string]any{
					"type":        "string",
					"description": "Cluster server URL",
				},
			},
			Required: []string{"server"},
		},
	}, handleGetCluster)

	// Settings
	server.AddTool(mcp.Tool{
		Name:        "argocd_settings",
		Description: "Get ArgoCD server settings",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleSettings)

	server.AddTool(mcp.Tool{
		Name:        "argocd_version",
		Description: "Get ArgoCD server version",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleVersion)

	return server.Run(ctx)
}

// argocdRequest makes an authenticated request to ArgoCD API
func argocdRequest(ctx context.Context, method, path string, query url.Values) (map[string]any, error) {
	apiURL := strings.TrimSuffix(argocdURL, "/") + "/api/v1" + path
	if len(query) > 0 {
		apiURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+argocdToken)
	req.Header.Set("Content-Type", "application/json")

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
			if msg, ok := errResp["message"].(string); ok {
				return nil, mcperror.APIError("ArgoCD", resp.StatusCode, msg)
			}
		}
		return nil, mcperror.APIError("ArgoCD", resp.StatusCode, string(body))
	}

	var result map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
	}

	return result, nil
}

func handleListApps(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.String("project", "")
	selector := v.String("selector", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	if project != "" {
		query.Set("projects", project)
	}
	if selector != "" {
		query.Set("selector", selector)
	}

	result, err := argocdRequest(ctx, "GET", "/applications", query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	apps := []map[string]any{}
	if items, ok := result["items"].([]any); ok {
		for _, item := range items {
			if app, ok := item.(map[string]any); ok {
				apps = append(apps, formatApp(app))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"applications": apps,
		"count":        len(apps),
	})
}

func handleGetApp(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := argocdRequest(ctx, "GET", "/applications/"+url.PathEscape(name), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(formatAppDetailed(result))
}

func handleAppResources(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := argocdRequest(ctx, "GET", "/applications/"+url.PathEscape(name)+"/resource-tree", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	resources := []map[string]any{}
	if nodes, ok := result["nodes"].([]any); ok {
		for _, node := range nodes {
			if res, ok := node.(map[string]any); ok {
				resources = append(resources, formatResource(res))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"resources": resources,
		"count":     len(resources),
	})
}

func handleAppManifests(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	revision := v.String("revision", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	if revision != "" {
		query.Set("revision", revision)
	}

	result, err := argocdRequest(ctx, "GET", "/applications/"+url.PathEscape(name)+"/manifests", query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func handleAppDiff(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Get application to check status
	app, err := argocdRequest(ctx, "GET", "/applications/"+url.PathEscape(name), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	status, _ := app["status"].(map[string]any)
	sync, _ := status["sync"].(map[string]any)
	resources, _ := status["resources"].([]any)

	diffs := []map[string]any{}
	for _, res := range resources {
		if resource, ok := res.(map[string]any); ok {
			if status, ok := resource["status"].(string); ok && status != "Synced" {
				diffs = append(diffs, map[string]any{
					"kind":      resource["kind"],
					"name":      resource["name"],
					"namespace": resource["namespace"],
					"status":    status,
				})
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"sync_status": sync["status"],
		"revision":    sync["revision"],
		"diffs":       diffs,
		"diff_count":  len(diffs),
	})
}

func handleSyncApp(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	revision := v.String("revision", "")
	prune := v.Bool("prune", false)
	dryRun := v.Bool("dry_run", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	if revision != "" {
		query.Set("revision", revision)
	}
	if prune {
		query.Set("prune", "true")
	}
	if dryRun {
		query.Set("dryRun", "true")
	}

	result, err := argocdRequest(ctx, "POST", "/applications/"+url.PathEscape(name)+"/sync", query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"message":  "Sync initiated",
		"revision": result["revision"],
	})
}

func handleRefreshApp(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	hard := v.Bool("hard", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	if hard {
		query.Set("refresh", "hard")
	} else {
		query.Set("refresh", "normal")
	}

	result, err := argocdRequest(ctx, "GET", "/applications/"+url.PathEscape(name), query)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"message": "Application refreshed",
		"app":     formatApp(result),
	})
}

func handleAppHistory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	app, err := argocdRequest(ctx, "GET", "/applications/"+url.PathEscape(name), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	history := []map[string]any{}
	if status, ok := app["status"].(map[string]any); ok {
		if hist, ok := status["history"].([]any); ok {
			for _, h := range hist {
				if entry, ok := h.(map[string]any); ok {
					history = append(history, map[string]any{
						"id":          entry["id"],
						"revision":    entry["revision"],
						"deployed_at": entry["deployedAt"],
						"source":      entry["source"],
					})
				}
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"history": history,
		"count":   len(history),
	})
}

func handleListProjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := argocdRequest(ctx, "GET", "/projects", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	projects := []map[string]any{}
	if items, ok := result["items"].([]any); ok {
		for _, item := range items {
			if proj, ok := item.(map[string]any); ok {
				projects = append(projects, formatProject(proj))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"projects": projects,
		"count":    len(projects),
	})
}

func handleGetProject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := argocdRequest(ctx, "GET", "/projects/"+url.PathEscape(name), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(formatProjectDetailed(result))
}

func handleListRepos(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := argocdRequest(ctx, "GET", "/repositories", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	repos := []map[string]any{}
	if items, ok := result["items"].([]any); ok {
		for _, item := range items {
			if repo, ok := item.(map[string]any); ok {
				repos = append(repos, formatRepo(repo))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"repositories": repos,
		"count":        len(repos),
	})
}

func handleGetRepo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	repo := v.Required("repo")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := argocdRequest(ctx, "GET", "/repositories/"+url.PathEscape(repo), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(formatRepo(result))
}

func handleListClusters(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := argocdRequest(ctx, "GET", "/clusters", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	clusters := []map[string]any{}
	if items, ok := result["items"].([]any); ok {
		for _, item := range items {
			if cluster, ok := item.(map[string]any); ok {
				clusters = append(clusters, formatCluster(cluster))
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"clusters": clusters,
		"count":    len(clusters),
	})
}

func handleGetCluster(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	server := v.Required("server")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := argocdRequest(ctx, "GET", "/clusters/"+url.PathEscape(server), nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(formatClusterDetailed(result))
}

func handleSettings(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := argocdRequest(ctx, "GET", "/settings", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

func handleVersion(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := argocdRequest(ctx, "GET", "/version", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(result)
}

// Format helpers

func formatApp(app map[string]any) map[string]any {
	metadata, _ := app["metadata"].(map[string]any)
	spec, _ := app["spec"].(map[string]any)
	status, _ := app["status"].(map[string]any)

	formatted := map[string]any{
		"name":      metadata["name"],
		"namespace": metadata["namespace"],
		"project":   spec["project"],
	}

	if source, ok := spec["source"].(map[string]any); ok {
		formatted["repo_url"] = source["repoURL"]
		formatted["path"] = source["path"]
		formatted["target_revision"] = source["targetRevision"]
	}

	if dest, ok := spec["destination"].(map[string]any); ok {
		formatted["dest_server"] = dest["server"]
		formatted["dest_namespace"] = dest["namespace"]
	}

	if sync, ok := status["sync"].(map[string]any); ok {
		formatted["sync_status"] = sync["status"]
	}

	if health, ok := status["health"].(map[string]any); ok {
		formatted["health_status"] = health["status"]
	}

	return formatted
}

func formatAppDetailed(app map[string]any) map[string]any {
	formatted := formatApp(app)

	status, _ := app["status"].(map[string]any)

	if resources, ok := status["resources"].([]any); ok {
		formatted["resource_count"] = len(resources)

		statusCounts := map[string]int{}
		for _, res := range resources {
			if r, ok := res.(map[string]any); ok {
				if s, ok := r["status"].(string); ok {
					statusCounts[s]++
				}
			}
		}
		formatted["resource_status"] = statusCounts
	}

	if conditions, ok := status["conditions"].([]any); ok {
		condList := []map[string]any{}
		for _, cond := range conditions {
			if c, ok := cond.(map[string]any); ok {
				condList = append(condList, map[string]any{
					"type":    c["type"],
					"message": c["message"],
				})
			}
		}
		formatted["conditions"] = condList
	}

	if operationState, ok := status["operationState"].(map[string]any); ok {
		formatted["last_sync"] = map[string]any{
			"phase":       operationState["phase"],
			"message":     operationState["message"],
			"started_at":  operationState["startedAt"],
			"finished_at": operationState["finishedAt"],
		}
	}

	return formatted
}

func formatResource(res map[string]any) map[string]any {
	return map[string]any{
		"group":      res["group"],
		"kind":       res["kind"],
		"name":       res["name"],
		"namespace":  res["namespace"],
		"version":    res["version"],
		"health":     res["health"],
		"created_at": res["createdAt"],
	}
}

func formatProject(proj map[string]any) map[string]any {
	metadata, _ := proj["metadata"].(map[string]any)
	spec, _ := proj["spec"].(map[string]any)

	formatted := map[string]any{
		"name":        metadata["name"],
		"description": spec["description"],
	}

	if dests, ok := spec["destinations"].([]any); ok {
		formatted["destinations"] = len(dests)
	}

	if sources, ok := spec["sourceRepos"].([]any); ok {
		formatted["source_repos"] = len(sources)
	}

	return formatted
}

func formatProjectDetailed(proj map[string]any) map[string]any {
	formatted := formatProject(proj)

	spec, _ := proj["spec"].(map[string]any)

	formatted["destinations"] = spec["destinations"]
	formatted["source_repos"] = spec["sourceRepos"]
	formatted["cluster_resource_whitelist"] = spec["clusterResourceWhitelist"]
	formatted["namespace_resource_blacklist"] = spec["namespaceResourceBlacklist"]

	return formatted
}

func formatRepo(repo map[string]any) map[string]any {
	return map[string]any{
		"repo":             repo["repo"],
		"type":             repo["type"],
		"name":             repo["name"],
		"connection_state": repo["connectionState"],
		"inherited_creds":  repo["inheritedCreds"],
		"insecure":         repo["insecure"],
		"enable_lfs":       repo["enableLfs"],
	}
}

func formatCluster(cluster map[string]any) map[string]any {
	return map[string]any{
		"server":         cluster["server"],
		"name":           cluster["name"],
		"namespaces":     cluster["namespaces"],
		"server_version": getNestedString(cluster, "serverVersion"),
	}
}

func formatClusterDetailed(cluster map[string]any) map[string]any {
	formatted := formatCluster(cluster)

	if info, ok := cluster["info"].(map[string]any); ok {
		formatted["server_version"] = info["serverVersion"]
		formatted["connection_state"] = info["connectionState"]
		formatted["cache_info"] = info["cacheInfo"]
	}

	if config, ok := cluster["config"].(map[string]any); ok {
		formatted["tls_client_config"] = map[string]any{
			"insecure": config["tlsClientConfig"].(map[string]any)["insecure"],
		}
	}

	return formatted
}

func getNestedString(m map[string]any, keys ...string) string {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			if v, ok := current[key].(string); ok {
				return v
			}
			return ""
		}
		if next, ok := current[key].(map[string]any); ok {
			current = next
		} else {
			return ""
		}
	}
	return ""
}

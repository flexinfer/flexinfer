// mcp-helm is an MCP server for Helm chart management.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type helmServer struct {
	kubeconfig string
	namespace  string
	timeout    time.Duration
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	kubeconfig := os.Getenv("HELM_KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	namespace := os.Getenv("HELM_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	timeout := 60 * time.Second
	if t := os.Getenv("HELM_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}

	h := &helmServer{
		kubeconfig: kubeconfig,
		namespace:  namespace,
		timeout:    timeout,
	}

	logger.Info("starting server", "name", "mcp-helm", "version", version, "namespace", namespace)

	server := mcp.NewServer("mcp-helm", version)
	server.SetInstructions("Helm MCP server. Manage Helm releases and charts.")

	// helm_list
	server.AddTool(mcp.Tool{
		Name:        "helm_list",
		Description: "List Helm releases",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace to list releases from (use 'all' for all namespaces)",
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Filter releases by name (regex)",
				},
				"deployed": map[string]any{
					"type":        "boolean",
					"description": "Show only deployed releases",
				},
				"failed": map[string]any{
					"type":        "boolean",
					"description": "Show only failed releases",
				},
				"pending": map[string]any{
					"type":        "boolean",
					"description": "Show only pending releases",
				},
			},
		},
	}, h.handleList)

	// helm_status
	server.AddTool(mcp.Tool{
		Name:        "helm_status",
		Description: "Get status of a Helm release including history",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"release": map[string]any{
					"type":        "string",
					"description": "Release name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Release namespace",
				},
				"revision": map[string]any{
					"type":        "integer",
					"description": "Specific revision to show status for",
				},
			},
			Required: []string{"release"},
		},
	}, h.handleStatus)

	// helm_values
	server.AddTool(mcp.Tool{
		Name:        "helm_values",
		Description: "Get values for a Helm release",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"release": map[string]any{
					"type":        "string",
					"description": "Release name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Release namespace",
				},
				"all": map[string]any{
					"type":        "boolean",
					"description": "Show all values including defaults",
				},
				"revision": map[string]any{
					"type":        "integer",
					"description": "Specific revision to get values for",
				},
			},
			Required: []string{"release"},
		},
	}, h.handleValues)

	// helm_history
	server.AddTool(mcp.Tool{
		Name:        "helm_history",
		Description: "Get release history",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"release": map[string]any{
					"type":        "string",
					"description": "Release name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Release namespace",
				},
				"max": map[string]any{
					"type":        "integer",
					"description": "Maximum number of revisions to show",
				},
			},
			Required: []string{"release"},
		},
	}, h.handleHistory)

	// helm_search
	server.AddTool(mcp.Tool{
		Name:        "helm_search",
		Description: "Search for Helm charts in configured repositories",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"keyword": map[string]any{
					"type":        "string",
					"description": "Search keyword",
				},
				"repo": map[string]any{
					"type":        "boolean",
					"description": "Search in helm repos (true) or Artifact Hub (false)",
				},
				"versions": map[string]any{
					"type":        "boolean",
					"description": "Show all versions",
				},
			},
			Required: []string{"keyword"},
		},
	}, h.handleSearch)

	// helm_show
	server.AddTool(mcp.Tool{
		Name:        "helm_show",
		Description: "Show information about a chart",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chart": map[string]any{
					"type":        "string",
					"description": "Chart reference (repo/chart or URL)",
				},
				"info": map[string]any{
					"type":        "string",
					"description": "What to show: chart, values, readme, all",
				},
				"version": map[string]any{
					"type":        "string",
					"description": "Chart version",
				},
			},
			Required: []string{"chart"},
		},
	}, h.handleShow)

	// helm_template
	server.AddTool(mcp.Tool{
		Name:        "helm_template",
		Description: "Render chart templates locally",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"chart": map[string]any{
					"type":        "string",
					"description": "Chart reference (repo/chart, URL, or local path)",
				},
				"release": map[string]any{
					"type":        "string",
					"description": "Release name for template",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace for template",
				},
				"values": map[string]any{
					"type":        "string",
					"description": "Inline YAML values to use",
				},
				"version": map[string]any{
					"type":        "string",
					"description": "Chart version",
				},
			},
			Required: []string{"chart", "release"},
		},
	}, h.handleTemplate)

	// helm_repo_list
	server.AddTool(mcp.Tool{
		Name:        "helm_repo_list",
		Description: "List configured Helm repositories",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, h.handleRepoList)

	return server.Run(ctx)
}

func (h *helmServer) runHelm(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	// Add kubeconfig if set
	if h.kubeconfig != "" {
		args = append([]string{"--kubeconfig", h.kubeconfig}, args...)
	}

	cmd := exec.CommandContext(ctx, "helm", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
		}
		return nil, err
	}

	return stdout.Bytes(), nil
}

func (h *helmServer) handleList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", h.namespace)
	filter := v.String("filter", "")
	deployed := v.Bool("deployed", false)
	failed := v.Bool("failed", false)
	pending := v.Bool("pending", false)

	cmdArgs := []string{"list", "-o", "json"}

	if namespace == "all" {
		cmdArgs = append(cmdArgs, "-A")
	} else {
		cmdArgs = append(cmdArgs, "-n", namespace)
	}

	if filter != "" {
		cmdArgs = append(cmdArgs, "--filter", filter)
	}
	if deployed {
		cmdArgs = append(cmdArgs, "--deployed")
	}
	if failed {
		cmdArgs = append(cmdArgs, "--failed")
	}
	if pending {
		cmdArgs = append(cmdArgs, "--pending")
	}

	output, err := h.runHelm(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var releases []any
	if err := json.Unmarshal(output, &releases); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse output: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(releases),
		"releases": releases,
	})
}

func (h *helmServer) handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	release := v.Required("release")
	namespace := v.String("namespace", h.namespace)
	revision := v.Int("revision", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cmdArgs := []string{"status", release, "-n", namespace, "-o", "json"}
	if revision > 0 {
		cmdArgs = append(cmdArgs, "--revision", fmt.Sprintf("%d", revision))
	}

	output, err := h.runHelm(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var status map[string]any
	if err := json.Unmarshal(output, &status); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse output: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"status": status,
	})
}

func (h *helmServer) handleValues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	release := v.Required("release")
	namespace := v.String("namespace", h.namespace)
	all := v.Bool("all", false)
	revision := v.Int("revision", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cmdArgs := []string{"get", "values", release, "-n", namespace, "-o", "json"}
	if all {
		cmdArgs = append(cmdArgs, "-a")
	}
	if revision > 0 {
		cmdArgs = append(cmdArgs, "--revision", fmt.Sprintf("%d", revision))
	}

	output, err := h.runHelm(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var values map[string]any
	if err := json.Unmarshal(output, &values); err != nil {
		// If JSON parse fails, return as raw string (might be null)
		return mcp.JSONResult(map[string]any{
			"ok":     true,
			"values": string(output),
		})
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"values": values,
	})
}

func (h *helmServer) handleHistory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	release := v.Required("release")
	namespace := v.String("namespace", h.namespace)
	max := v.Int("max", 10)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cmdArgs := []string{"history", release, "-n", namespace, "-o", "json", "--max", fmt.Sprintf("%d", max)}

	output, err := h.runHelm(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var history []any
	if err := json.Unmarshal(output, &history); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse output: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(history),
		"history": history,
	})
}

func (h *helmServer) handleSearch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	keyword := v.Required("keyword")
	searchRepo := v.Bool("repo", true)
	versions := v.Bool("versions", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var cmdArgs []string
	if searchRepo {
		cmdArgs = []string{"search", "repo", keyword, "-o", "json"}
	} else {
		cmdArgs = []string{"search", "hub", keyword, "-o", "json"}
	}

	if versions {
		cmdArgs = append(cmdArgs, "--versions")
	}

	output, err := h.runHelm(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var charts []any
	if err := json.Unmarshal(output, &charts); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse output: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(charts),
		"charts": charts,
	})
}

func (h *helmServer) handleShow(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chart := v.Required("chart")
	info := v.Enum("info", "chart", "chart", "values", "readme", "all")
	version := v.String("version", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cmdArgs := []string{"show", info, chart}
	if version != "" {
		cmdArgs = append(cmdArgs, "--version", version)
	}

	output, err := h.runHelm(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"chart":   chart,
		"info":    info,
		"content": string(output),
	})
}

func (h *helmServer) handleTemplate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	chart := v.Required("chart")
	release := v.Required("release")
	namespace := v.String("namespace", h.namespace)
	values := v.String("values", "")
	version := v.String("version", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cmdArgs := []string{"template", release, chart, "-n", namespace}
	if version != "" {
		cmdArgs = append(cmdArgs, "--version", version)
	}

	// If inline values provided, write to temp file
	if values != "" {
		tmpfile, err := os.CreateTemp("", "helm-values-*.yaml")
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("create temp file: %w", err)), nil
		}
		defer os.Remove(tmpfile.Name())

		if _, err := tmpfile.WriteString(values); err != nil {
			return mcp.ErrorResult(fmt.Errorf("write values: %w", err)), nil
		}
		tmpfile.Close()
		cmdArgs = append(cmdArgs, "-f", tmpfile.Name())
	}

	output, err := h.runHelm(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"release":  release,
		"chart":    chart,
		"manifest": string(output),
	})
}

func (h *helmServer) handleRepoList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	output, err := h.runHelm(ctx, "repo", "list", "-o", "json")
	if err != nil {
		// No repos configured is not an error
		if strings.Contains(err.Error(), "no repositories") {
			return mcp.JSONResult(map[string]any{
				"ok":           true,
				"count":        0,
				"repositories": []any{},
			})
		}
		return mcp.ErrorResult(err), nil
	}

	var repos []any
	if err := json.Unmarshal(output, &repos); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse output: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"count":        len(repos),
		"repositories": repos,
	})
}

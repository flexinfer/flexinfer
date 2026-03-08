// mcp-k8s is a fast Kubernetes MCP server written in Go using client-go.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "1.0.0"

type k8sServer struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	kubeconfig    string
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-k8s", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-k8s")

	// Get kubeconfig from env or default
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Allow override via MCP_K8S_KUBECONFIG
	if kc := os.Getenv("MCP_K8S_KUBECONFIG"); kc != "" {
		kubeconfig = kc
	}

	k8s := &k8sServer{kubeconfig: kubeconfig}
	if err := k8s.connect(); err != nil {
		logger.Warn("failed to connect to cluster", "error", err)
	}

	logger.Info("starting server", "name", "mcp-k8s", "version", version, "kubeconfig", kubeconfig)

	server := mcp.NewServer("mcp-k8s", version)
	server.SetInstructions("Fast Go-native Kubernetes MCP server. Supports pods, deployments, services, logs, and more.")

	// list_pods
	server.AddTool(mcp.Tool{
		Name:        "list_pods",
		Description: "List pods in a namespace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Kubernetes namespace. Use 'all' for all namespaces. Defaults to 'default'.",
				},
				"label_selector": map[string]any{
					"type":        "string",
					"description": "Label selector (e.g., 'app=nginx')",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_pods", k8s.handleListPods))

	// get_pod
	server.AddTool(mcp.Tool{
		Name:        "get_pod",
		Description: "Get detailed information about a specific pod",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Pod name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "get_pod", k8s.handleGetPod))

	// get_logs
	server.AddTool(mcp.Tool{
		Name:        "get_logs",
		Description: "Get logs from a pod",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Pod name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
				"container": map[string]any{
					"type":        "string",
					"description": "Container name (required if pod has multiple containers)",
				},
				"tail": map[string]any{
					"type":        "integer",
					"description": "Number of lines to return from the end. Defaults to 100.",
				},
				"previous": map[string]any{
					"type":        "boolean",
					"description": "Return logs from previous container instance",
				},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "get_logs", k8s.handleGetLogs))

	// list_deployments
	server.AddTool(mcp.Tool{
		Name:        "list_deployments",
		Description: "List deployments in a namespace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Use 'all' for all namespaces.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_deployments", k8s.handleListDeployments))

	// list_services
	server.AddTool(mcp.Tool{
		Name:        "list_services",
		Description: "List services in a namespace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Use 'all' for all namespaces.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_services", k8s.handleListServices))

	// get_resource
	server.AddTool(mcp.Tool{
		Name:        "get_resource",
		Description: "Get any Kubernetes resource by kind",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "Resource kind (e.g., pod, deployment, service, configmap, secret, ingress)",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Resource name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
			},
			Required: []string{"kind", "name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "get_resource", k8s.handleGetResource))

	// list_namespaces
	server.AddTool(mcp.Tool{
		Name:        "list_namespaces",
		Description: "List all namespaces in the cluster",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_namespaces", k8s.handleListNamespaces))

	// scale_deployment
	server.AddTool(mcp.Tool{
		Name:        "scale_deployment",
		Description: "Scale a deployment to a specific number of replicas",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Deployment name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
				"replicas": map[string]any{
					"type":        "integer",
					"description": "Desired number of replicas",
				},
			},
			Required: []string{"name", "replicas"},
		},
	}, mcpotel.TracedToolHandler(tracer, "scale_deployment", k8s.handleScaleDeployment))

	// restart_deployment
	server.AddTool(mcp.Tool{
		Name:        "restart_deployment",
		Description: "Restart a deployment by triggering a rollout",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Deployment name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "restart_deployment", k8s.handleRestartDeployment))

	// list_events
	server.AddTool(mcp.Tool{
		Name:        "list_events",
		Description: "List Kubernetes events in a namespace (critical for debugging)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Use 'all' for all namespaces. Defaults to 'default'.",
				},
				"field_selector": map[string]any{
					"type":        "string",
					"description": "Field selector (e.g., 'involvedObject.name=my-pod')",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of events to return. Defaults to 50.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_events", k8s.handleListEvents))

	// get_configmap
	server.AddTool(mcp.Tool{
		Name:        "get_configmap",
		Description: "Get ConfigMap contents",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "ConfigMap name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "get_configmap", k8s.handleGetConfigMap))

	// get_secret
	server.AddTool(mcp.Tool{
		Name:        "get_secret",
		Description: "Get Secret contents (values are base64 decoded)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Secret name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
				"decode": map[string]any{
					"type":        "boolean",
					"description": "Decode base64 values. Defaults to true.",
				},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "get_secret", k8s.handleGetSecret))

	// list_ingresses
	server.AddTool(mcp.Tool{
		Name:        "list_ingresses",
		Description: "List Ingress resources",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Use 'all' for all namespaces. Defaults to 'default'.",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_ingresses", k8s.handleListIngresses))

	// describe_resource
	server.AddTool(mcp.Tool{
		Name:        "describe_resource",
		Description: "Get detailed description of a resource including events",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "Resource kind (pod, deployment, service, etc.)",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Resource name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace. Defaults to 'default'.",
				},
			},
			Required: []string{"kind", "name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "describe_resource", k8s.handleDescribeResource))

	return server.Run(ctx)
}

func (k *k8sServer) connect() error {
	config, err := clientcmd.BuildConfigFromFlags("", k.kubeconfig)
	if err != nil {
		return err
	}

	// Ensure requests have an upper bound, since many MCP clients also enforce tool-call deadlines.
	timeoutSeconds := env.Int("MCP_K8S_TIMEOUT_SECONDS", 55)
	if timeoutSeconds > 0 {
		config.Timeout = time.Duration(timeoutSeconds) * time.Second
	}

	k.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	k.dynamicClient, err = dynamic.NewForConfig(config)
	return err
}

func (k *k8sServer) ensureConnected() error {
	if k.clientset == nil {
		return k.connect()
	}
	return nil
}

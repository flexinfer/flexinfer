// mcp-k8s is a fast Kubernetes MCP server written in Go using client-go.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var version = "1.0.0"

type k8sServer struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	kubeconfig    string
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

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
		fmt.Fprintf(os.Stderr, "Warning: failed to connect to cluster: %v\n", err)
	}

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
	}, k8s.handleListPods)

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
	}, k8s.handleGetPod)

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
	}, k8s.handleGetLogs)

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
	}, k8s.handleListDeployments)

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
	}, k8s.handleListServices)

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
	}, k8s.handleGetResource)

	// list_namespaces
	server.AddTool(mcp.Tool{
		Name:        "list_namespaces",
		Description: "List all namespaces in the cluster",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, k8s.handleListNamespaces)

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
	}, k8s.handleScaleDeployment)

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
	}, k8s.handleRestartDeployment)

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
	}, k8s.handleListEvents)

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
	}, k8s.handleGetConfigMap)

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
	}, k8s.handleGetSecret)

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
	}, k8s.handleListIngresses)

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
	}, k8s.handleDescribeResource)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func (k *k8sServer) connect() error {
	config, err := clientcmd.BuildConfigFromFlags("", k.kubeconfig)
	if err != nil {
		return err
	}

	// Ensure requests have an upper bound, since many MCP clients also enforce tool-call deadlines.
	timeoutSeconds := getEnvInt("MCP_K8S_TIMEOUT_SECONDS", 55)
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

func (k *k8sServer) handleListPods(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	ns := getStringArg(args, "namespace", "default")
	selector := getStringArg(args, "label_selector", "")

	opts := metav1.ListOptions{LabelSelector: selector}

	var pods *corev1.PodList
	var err error

	if ns == "all" {
		pods, err = k.clientset.CoreV1().Pods("").List(ctx, opts)
	} else {
		pods, err = k.clientset.CoreV1().Pods(ns).List(ctx, opts)
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, pod := range pods.Items {
		ready := 0
		total := len(pod.Status.ContainerStatuses)
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
		}

		result = append(result, map[string]any{
			"name":      pod.Name,
			"namespace": pod.Namespace,
			"status":    string(pod.Status.Phase),
			"ready":     fmt.Sprintf("%d/%d", ready, total),
			"restarts":  getRestarts(pod.Status.ContainerStatuses),
			"age":       formatAge(pod.CreationTimestamp.Time),
			"node":      pod.Spec.NodeName,
		})
	}

	return mcp.JSONResult(map[string]any{"pods": result, "count": len(result)})
}

func (k *k8sServer) handleGetPod(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	pod, err := k.clientset.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(pod)
}

func (k *k8sServer) handleGetLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")
	container := getStringArg(args, "container", "")
	tail := getIntArg(args, "tail", 100)
	previous := getBoolArg(args, "previous", false)

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	tailLines := int64(tail)
	opts := &corev1.PodLogOptions{
		Container: container,
		TailLines: &tailLines,
		Previous:  previous,
	}

	req := k.clientset.CoreV1().Pods(ns).GetLogs(name, opts)
	logs, err := req.DoRaw(ctx)
	if err != nil {
		return nil, err
	}

	return mcp.TextResult(string(logs)), nil
}

func (k *k8sServer) handleListDeployments(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	ns := getStringArg(args, "namespace", "default")

	var deployments *unstructured.UnstructuredList
	var err error

	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	if ns == "all" {
		deployments, err = k.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	} else {
		deployments, err = k.dynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, d := range deployments.Items {
		spec, _, _ := unstructured.NestedMap(d.Object, "spec")
		status, _, _ := unstructured.NestedMap(d.Object, "status")
		replicas, _, _ := unstructured.NestedInt64(spec, "replicas")
		ready, _, _ := unstructured.NestedInt64(status, "readyReplicas")
		available, _, _ := unstructured.NestedInt64(status, "availableReplicas")

		result = append(result, map[string]any{
			"name":      d.GetName(),
			"namespace": d.GetNamespace(),
			"replicas":  replicas,
			"ready":     ready,
			"available": available,
			"age":       formatAge(d.GetCreationTimestamp().Time),
		})
	}

	return mcp.JSONResult(map[string]any{"deployments": result, "count": len(result)})
}

func (k *k8sServer) handleListServices(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	ns := getStringArg(args, "namespace", "default")

	var services *corev1.ServiceList
	var err error

	if ns == "all" {
		services, err = k.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	} else {
		services, err = k.clientset.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, svc := range services.Items {
		var ports []string
		for _, p := range svc.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}

		result = append(result, map[string]any{
			"name":       svc.Name,
			"namespace":  svc.Namespace,
			"type":       string(svc.Spec.Type),
			"clusterIP":  svc.Spec.ClusterIP,
			"externalIP": strings.Join(svc.Spec.ExternalIPs, ","),
			"ports":      strings.Join(ports, ","),
			"age":        formatAge(svc.CreationTimestamp.Time),
		})
	}

	return mcp.JSONResult(map[string]any{"services": result, "count": len(result)})
}

func (k *k8sServer) handleGetResource(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	kind := strings.ToLower(getStringArg(args, "kind", ""))
	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")

	if kind == "" || name == "" {
		return nil, fmt.Errorf("kind and name are required")
	}

	gvr := kindToGVR(kind)
	if gvr.Resource == "" {
		return nil, fmt.Errorf("unknown kind: %s", kind)
	}

	var obj *unstructured.Unstructured
	var err error

	if isNamespaced(kind) {
		obj, err = k.dynamicClient.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	} else {
		obj, err = k.dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(obj.Object)
}

func (k *k8sServer) handleListNamespaces(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	nsList, err := k.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, ns := range nsList.Items {
		result = append(result, map[string]any{
			"name":   ns.Name,
			"status": string(ns.Status.Phase),
			"age":    formatAge(ns.CreationTimestamp.Time),
		})
	}

	return mcp.JSONResult(map[string]any{"namespaces": result, "count": len(result)})
}

func (k *k8sServer) handleScaleDeployment(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")
	replicas := getIntArg(args, "replicas", -1)

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if replicas < 0 {
		return nil, fmt.Errorf("replicas is required")
	}

	scale, err := k.clientset.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	oldReplicas := scale.Spec.Replicas
	scale.Spec.Replicas = int32(replicas)

	_, err = k.clientset.AppsV1().Deployments(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"deployment":  name,
		"namespace":   ns,
		"oldReplicas": oldReplicas,
		"newReplicas": replicas,
		"status":      "scaled",
	})
}

func (k *k8sServer) handleRestartDeployment(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	deployment, err := k.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Add restart annotation
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = k.clientset.AppsV1().Deployments(ns).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"deployment": name,
		"namespace":  ns,
		"status":     "rollout restarted",
	})
}

// Helper functions
func getStringArg(args map[string]any, key, defaultVal string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func getIntArg(args map[string]any, key string, defaultVal int) int {
	if v, ok := args[key].(float64); ok {
		return int(v)
	}
	if v, ok := args[key].(int); ok {
		return v
	}
	return defaultVal
}

func getBoolArg(args map[string]any, key string, defaultVal bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return defaultVal
}

func getRestarts(containers []corev1.ContainerStatus) int32 {
	var total int32
	for _, c := range containers {
		total += c.RestartCount
	}
	return total
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	if d.Hours() >= 24*365 {
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
	if d.Hours() >= 24 {
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	if d.Hours() >= 1 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d.Minutes() >= 1 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func kindToGVR(kind string) schema.GroupVersionResource {
	switch kind {
	case "pod", "pods":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	case "service", "services", "svc":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	case "deployment", "deployments", "deploy":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	case "configmap", "configmaps", "cm":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	case "secret", "secrets":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	case "ingress", "ingresses", "ing":
		return schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	case "namespace", "namespaces", "ns":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	case "node", "nodes":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
	case "pv", "persistentvolume", "persistentvolumes":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumes"}
	case "pvc", "persistentvolumeclaim", "persistentvolumeclaims":
		return schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}
	case "statefulset", "statefulsets", "sts":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}
	case "daemonset", "daemonsets", "ds":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}
	case "job", "jobs":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	case "cronjob", "cronjobs", "cj":
		return schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}
	default:
		return schema.GroupVersionResource{}
	}
}

func isNamespaced(kind string) bool {
	switch kind {
	case "namespace", "namespaces", "ns", "node", "nodes", "pv", "persistentvolume", "persistentvolumes":
		return false
	default:
		return true
	}
}

func canonicalKindForEvents(kind string) string {
	switch strings.ToLower(kind) {
	case "pod", "pods":
		return "Pod"
	case "deployment", "deployments", "deploy":
		return "Deployment"
	case "statefulset", "statefulsets", "sts":
		return "StatefulSet"
	case "daemonset", "daemonsets", "ds":
		return "DaemonSet"
	case "service", "services", "svc":
		return "Service"
	case "configmap", "configmaps", "cm":
		return "ConfigMap"
	case "secret", "secrets":
		return "Secret"
	case "namespace", "namespaces", "ns":
		return "Namespace"
	case "node", "nodes":
		return "Node"
	case "pvc", "persistentvolumeclaim", "persistentvolumeclaims":
		return "PersistentVolumeClaim"
	case "pv", "persistentvolume", "persistentvolumes":
		return "PersistentVolume"
	case "job", "jobs":
		return "Job"
	case "cronjob", "cronjobs", "cj":
		return "CronJob"
	default:
		if kind == "" {
			return ""
		}
		return strings.ToUpper(kind[:1]) + kind[1:]
	}
}

// Event handler
func (k *k8sServer) handleListEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	ns := getStringArg(args, "namespace", "default")
	fieldSelector := getStringArg(args, "field_selector", "")
	limit := getIntArg(args, "limit", 50)

	opts := metav1.ListOptions{
		FieldSelector: fieldSelector,
		Limit:         int64(limit),
	}

	var events *corev1.EventList
	var err error

	if ns == "all" {
		events, err = k.clientset.CoreV1().Events("").List(ctx, opts)
	} else {
		events, err = k.clientset.CoreV1().Events(ns).List(ctx, opts)
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, e := range events.Items {
		result = append(result, map[string]any{
			"namespace":       e.Namespace,
			"name":            e.Name,
			"type":            e.Type,
			"reason":          e.Reason,
			"message":         e.Message,
			"count":           e.Count,
			"first_timestamp": e.FirstTimestamp.Format(time.RFC3339),
			"last_timestamp":  e.LastTimestamp.Format(time.RFC3339),
			"involved_object": map[string]any{
				"kind":      e.InvolvedObject.Kind,
				"name":      e.InvolvedObject.Name,
				"namespace": e.InvolvedObject.Namespace,
			},
			"source": map[string]any{
				"component": e.Source.Component,
				"host":      e.Source.Host,
			},
		})
	}

	return mcp.JSONResult(map[string]any{"events": result, "count": len(result)})
}

// ConfigMap handler
func (k *k8sServer) handleGetConfigMap(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	cm, err := k.clientset.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"name":       cm.Name,
		"namespace":  cm.Namespace,
		"data":       cm.Data,
		"binaryData": cm.BinaryData,
		"age":        formatAge(cm.CreationTimestamp.Time),
	})
}

// Secret handler
func (k *k8sServer) handleGetSecret(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")
	decode := getBoolArg(args, "decode", true)

	if name == "" {
		return nil, fmt.Errorf("name is required")
	}

	secret, err := k.clientset.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	data := make(map[string]string)
	for k, v := range secret.Data {
		if decode {
			data[k] = string(v)
		} else {
			data[k] = fmt.Sprintf("%x", v)
		}
	}

	return mcp.JSONResult(map[string]any{
		"name":      secret.Name,
		"namespace": secret.Namespace,
		"type":      string(secret.Type),
		"data":      data,
		"age":       formatAge(secret.CreationTimestamp.Time),
	})
}

// Ingress handler
func (k *k8sServer) handleListIngresses(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	ns := getStringArg(args, "namespace", "default")

	gvr := schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}

	var ingresses *unstructured.UnstructuredList
	var err error

	if ns == "all" {
		ingresses, err = k.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	} else {
		ingresses, err = k.dynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, ing := range ingresses.Items {
		spec, _, _ := unstructured.NestedMap(ing.Object, "spec")
		rules, _, _ := unstructured.NestedSlice(spec, "rules")

		var hosts []string
		for _, rule := range rules {
			if ruleMap, ok := rule.(map[string]any); ok {
				if host, ok := ruleMap["host"].(string); ok {
					hosts = append(hosts, host)
				}
			}
		}

		result = append(result, map[string]any{
			"name":      ing.GetName(),
			"namespace": ing.GetNamespace(),
			"hosts":     strings.Join(hosts, ", "),
			"age":       formatAge(ing.GetCreationTimestamp().Time),
		})
	}

	return mcp.JSONResult(map[string]any{"ingresses": result, "count": len(result)})
}

// Describe resource handler
func (k *k8sServer) handleDescribeResource(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return nil, err
	}

	kind := strings.ToLower(getStringArg(args, "kind", ""))
	name := getStringArg(args, "name", "")
	ns := getStringArg(args, "namespace", "default")

	if kind == "" || name == "" {
		return nil, fmt.Errorf("kind and name are required")
	}

	gvr := kindToGVR(kind)
	if gvr.Resource == "" {
		return nil, fmt.Errorf("unknown kind: %s", kind)
	}

	// Get the resource
	var obj *unstructured.Unstructured
	var err error

	if isNamespaced(kind) {
		obj, err = k.dynamicClient.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	} else {
		obj, err = k.dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}

	// Get related events
	fieldSelector := fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=%s", name, canonicalKindForEvents(kind))
	events, _ := k.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
		Limit:         20,
	})

	var eventList []map[string]any
	if events != nil {
		for _, e := range events.Items {
			eventList = append(eventList, map[string]any{
				"type":           e.Type,
				"reason":         e.Reason,
				"message":        e.Message,
				"count":          e.Count,
				"last_timestamp": e.LastTimestamp.Format(time.RFC3339),
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"resource": obj.Object,
		"events":   eventList,
	})
}

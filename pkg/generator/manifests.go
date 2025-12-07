package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
	"gopkg.in/yaml.v3"
)

// GenerateManifests generates Kubernetes manifests for the MCP Hub.
func GenerateManifests(reg *registry.Registry, outputDir string, namespace string, imageRegistry string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	var generatedServers []string

	for _, server := range reg.Servers {
		if server.IsLocalOnly() {
			continue
		}

		k8sName := sanitizeName(server.Name)
		serverDir := filepath.Join(outputDir, k8sName)
		if err := os.MkdirAll(serverDir, 0755); err != nil {
			return fmt.Errorf("create server dir: %w", err)
		}

		// Deployment
		deploy, err := createDeployment(server, namespace, imageRegistry)
		if err != nil {
			return fmt.Errorf("create deployment %s: %w", server.Name, err)
		}
		if err := writeYaml(filepath.Join(serverDir, "deployment.yaml"), deploy); err != nil {
			return err
		}

		// Service
		svc := createService(server, namespace)
		if err := writeYaml(filepath.Join(serverDir, "service.yaml"), svc); err != nil {
			return err
		}

		// ConfigMap
		cm := createConfigMap(server, namespace)
		if cm != nil {
			if err := writeYaml(filepath.Join(serverDir, "configmap.yaml"), cm); err != nil {
				return err
			}
		}

		generatedServers = append(generatedServers, k8sName)
	}

	// Kustomization
	kust := createKustomization(generatedServers, namespace)
	if err := writeYaml(filepath.Join(outputDir, "kustomization.yaml"), kust); err != nil {
		return err
	}

	return nil
}

func sanitizeName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

func getServerType(spec *registry.TargetSpec) string {
	cmd := spec.Command
	args := spec.Args

	if cmd == "npx" || (len(args) > 0 && fmt.Sprintf("%v", args[0]) == "npx") {
		return "npx"
	}
	if cmd == "python3" || cmd == "python" || strings.HasSuffix(cmd, ".py") {
		return "python"
	}
	if cmd == "uvx" {
		return "uvx"
	}
	if strings.HasSuffix(cmd, ".sh") {
		return "shell"
	}
	return "custom"
}

func createDeployment(server *registry.Server, namespace, imageRegistry string) (map[string]any, error) {
	k8sName := sanitizeName(server.Name)
	spec := server.Common
	if spec == nil {
		spec = &registry.TargetSpec{}
	}
	serverType := getServerType(spec)

	var image string
	switch serverType {
	case "npx":
		image = fmt.Sprintf("%s/node-server:latest", imageRegistry)
	case "python", "uvx", "shell":
		image = fmt.Sprintf("%s/python-server:latest", imageRegistry)
	default:
		image = fmt.Sprintf("%s/custom-server:latest", imageRegistry)
	}

	// Env vars
	envVars := []map[string]string{}
	for k, v := range spec.Env {
		envVars = append(envVars, map[string]string{
			"name":  k,
			"value": ResolveTokens(v, "", "cluster"),
		})
	}

	// Add defaults if missing
	defaults := map[string]string{
		"MCP_SERVER_NAME": server.Name,
		"MCP_TRANSPORT":   "websocket",
		"MCP_WS_PORT":     "8080",
		"LOG_LEVEL":       "info",
	}
	existing := make(map[string]bool)
	for _, e := range envVars {
		existing[e["name"]] = true
	}
	for k, v := range defaults {
		if !existing[k] {
			envVars = append(envVars, map[string]string{"name": k, "value": v})
		}
	}

	// Command and Args
	var containerCmd []string
	var containerArgs []string

	if serverType == "npx" {
		containerArgs = ResolveArgs(spec.Args, "", "cluster")
	}

	// MCP_SERVER_COMMAND injection for wrappers
	if serverType != "npx" {
		cmdParts := []string{ResolveTokens(spec.Command, "", "cluster")}
		cmdParts = append(cmdParts, ResolveArgs(spec.Args, "", "cluster")...)
		fullCmd := strings.Join(cmdParts, " ")
		if fullCmd != "" {
			envVars = append(envVars, map[string]string{
				"name":  "MCP_SERVER_COMMAND",
				"value": fullCmd,
			})
		}
	}

	// Resources
	resources := map[string]any{
		"requests": map[string]string{"cpu": "50m", "memory": "128Mi"},
		"limits":   map[string]string{"cpu": "200m", "memory": "256Mi"},
	}
	for _, cat := range server.Categories {
		if cat == "kubernetes" || cat == "operations" {
			resources = map[string]any{
				"requests": map[string]string{"cpu": "100m", "memory": "256Mi"},
				"limits":   map[string]string{"cpu": "500m", "memory": "512Mi"},
			}
			break
		}
	}

	replicas := 1
	if serverType == "npx" || serverType == "python" || serverType == "uvx" {
		replicas = 2
	}
	// HA overrides
	if server.Name == "ops_mcp" || server.Name == "k8s_apps_k3s" {
		replicas = 2
	}

	container := map[string]any{
		"name":            k8sName,
		"image":           image,
		"imagePullPolicy": "Always",
		"env":             envVars,
		"ports":           []map[string]any{{"containerPort": 8080, "name": "websocket"}},
		"resources":       resources,
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": false,
			"readOnlyRootFilesystem":   false,
			"capabilities":             map[string]any{"drop": []string{"ALL"}},
		},
		"livenessProbe": map[string]any{
			"httpGet":             map[string]any{"path": "/health", "port": 8080},
			"initialDelaySeconds": 10,
			"periodSeconds":       30,
		},
		"readinessProbe": map[string]any{
			"httpGet":             map[string]any{"path": "/ready", "port": 8080},
			"initialDelaySeconds": 5,
			"periodSeconds":       10,
		},
	}

	if len(containerCmd) > 0 {
		container["command"] = containerCmd
	}
	if len(containerArgs) > 0 {
		container["args"] = containerArgs
	}

	deploy := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      k8sName,
			"namespace": namespace,
			"labels": map[string]string{
				"app":             k8sName,
				"component":       "mcp-server",
				"mcp.server/name": server.Name,
				"mcp.server/type": serverType,
			},
		},
		"spec": map[string]any{
			"replicas": replicas,
			"selector": map[string]any{"matchLabels": map[string]string{"app": k8sName}},
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]string{
						"app":             k8sName,
						"component":       "mcp-server",
						"mcp.server/name": server.Name,
					},
					"annotations": map[string]string{
						"mcp.server/description": spec.Description,
						"mcp.server/categories":  strings.Join(server.Categories, ","),
					},
				},
				"spec": map[string]any{
					"securityContext": map[string]any{
						"runAsNonRoot": true,
						"runAsUser":    1000,
						"fsGroup":      1000,
					},
					"containers": []any{container},
				},
			},
		},
	}

	return deploy, nil
}

func createService(server *registry.Server, namespace string) map[string]any {
	k8sName := sanitizeName(server.Name)
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      fmt.Sprintf("mcp-%s", k8sName),
			"namespace": namespace,
			"labels": map[string]string{
				"app":       k8sName,
				"component": "mcp-server",
			},
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": map[string]string{"app": k8sName},
			"ports": []map[string]any{
				{
					"name":       "websocket",
					"port":       8080,
					"targetPort": 8080,
					"protocol":   "TCP",
				},
			},
		},
	}
}

func createConfigMap(server *registry.Server, namespace string) map[string]any {
	k8sName := sanitizeName(server.Name)
	spec := server.Common
	if spec == nil {
		return nil
	}

	serverConfig := map[string]any{
		"name":         server.Name,
		"description":  spec.Description,
		"categories":   server.Categories,
		"timeout":      spec.Timeout,
		"always_allow": spec.AlwaysAllow,
	}

	yamlData, _ := yaml.Marshal(serverConfig)

	return map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      fmt.Sprintf("%s-config", k8sName),
			"namespace": namespace,
			"labels": map[string]string{
				"app":       k8sName,
				"component": "mcp-server",
			},
		},
		"data": map[string]string{
			"server.yaml": string(yamlData),
		},
	}
}

func createKustomization(servers []string, namespace string) map[string]any {
	resources := []string{}
	for _, s := range servers {
		resources = append(resources, fmt.Sprintf("%s/deployment.yaml", s))
		resources = append(resources, fmt.Sprintf("%s/service.yaml", s))
	}
	sort.Strings(resources)

	return map[string]any{
		"apiVersion": "kustomize.config.k8s.io/v1beta1",
		"kind":       "Kustomization",
		"namespace":  namespace,
		"resources":  resources,
		"commonLabels": map[string]string{
			"app.kubernetes.io/part-of":    "mcp-hub",
			"app.kubernetes.io/managed-by": "kustomize",
		},
	}
}

func writeYaml(path string, data any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := yaml.NewEncoder(f)
	enc.SetIndent(2)
	return enc.Encode(data)
}

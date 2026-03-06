package main

import "testing"

func TestFormatApp(t *testing.T) {
	input := map[string]any{
		"metadata": map[string]any{
			"name":      "my-app",
			"namespace": "argocd",
		},
		"spec": map[string]any{
			"project": "default",
			"source": map[string]any{
				"repoURL":        "https://github.com/example/repo.git",
				"path":           "k8s/",
				"targetRevision": "main",
			},
			"destination": map[string]any{
				"server":    "https://kubernetes.default.svc",
				"namespace": "production",
			},
		},
		"status": map[string]any{
			"sync": map[string]any{
				"status": "Synced",
			},
			"health": map[string]any{
				"status": "Healthy",
			},
		},
	}

	result := formatApp(input)

	if result["name"] != "my-app" {
		t.Errorf("expected name my-app, got %v", result["name"])
	}
	if result["namespace"] != "argocd" {
		t.Errorf("expected namespace argocd, got %v", result["namespace"])
	}
	if result["project"] != "default" {
		t.Errorf("expected project default, got %v", result["project"])
	}
	if result["repo_url"] != "https://github.com/example/repo.git" {
		t.Errorf("expected repo_url, got %v", result["repo_url"])
	}
	if result["path"] != "k8s/" {
		t.Errorf("expected path k8s/, got %v", result["path"])
	}
	if result["target_revision"] != "main" {
		t.Errorf("expected target_revision main, got %v", result["target_revision"])
	}
	if result["dest_server"] != "https://kubernetes.default.svc" {
		t.Errorf("expected dest_server, got %v", result["dest_server"])
	}
	if result["dest_namespace"] != "production" {
		t.Errorf("expected dest_namespace production, got %v", result["dest_namespace"])
	}
	if result["sync_status"] != "Synced" {
		t.Errorf("expected sync_status Synced, got %v", result["sync_status"])
	}
	if result["health_status"] != "Healthy" {
		t.Errorf("expected health_status Healthy, got %v", result["health_status"])
	}
}

func TestFormatAppMinimal(t *testing.T) {
	// Test with missing optional fields to verify no panics.
	input := map[string]any{
		"metadata": map[string]any{
			"name": "minimal-app",
		},
		"spec":   map[string]any{},
		"status": map[string]any{},
	}

	result := formatApp(input)
	if result["name"] != "minimal-app" {
		t.Errorf("expected name minimal-app, got %v", result["name"])
	}
}

func TestFormatAppDetailed(t *testing.T) {
	input := map[string]any{
		"metadata": map[string]any{
			"name":      "my-app",
			"namespace": "argocd",
		},
		"spec": map[string]any{
			"project": "default",
		},
		"status": map[string]any{
			"sync": map[string]any{
				"status": "OutOfSync",
			},
			"health": map[string]any{
				"status": "Degraded",
			},
			"resources": []any{
				map[string]any{"kind": "Deployment", "name": "web", "status": "Synced"},
				map[string]any{"kind": "Service", "name": "web-svc", "status": "OutOfSync"},
			},
			"conditions": []any{
				map[string]any{"type": "SyncError", "message": "failed to sync"},
			},
			"operationState": map[string]any{
				"phase":      "Failed",
				"message":    "sync failed",
				"startedAt":  "2025-01-01T00:00:00Z",
				"finishedAt": "2025-01-01T00:01:00Z",
			},
		},
	}

	result := formatAppDetailed(input)

	if result["resource_count"] != 2 {
		t.Errorf("expected resource_count 2, got %v", result["resource_count"])
	}

	statusCounts, ok := result["resource_status"].(map[string]int)
	if !ok {
		t.Fatalf("expected resource_status map[string]int, got %T", result["resource_status"])
	}
	if statusCounts["Synced"] != 1 {
		t.Errorf("expected 1 Synced resource, got %d", statusCounts["Synced"])
	}
	if statusCounts["OutOfSync"] != 1 {
		t.Errorf("expected 1 OutOfSync resource, got %d", statusCounts["OutOfSync"])
	}

	conditions, ok := result["conditions"].([]map[string]any)
	if !ok {
		t.Fatalf("expected conditions slice, got %T", result["conditions"])
	}
	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}

	lastSync, ok := result["last_sync"].(map[string]any)
	if !ok {
		t.Fatalf("expected last_sync map, got %T", result["last_sync"])
	}
	if lastSync["phase"] != "Failed" {
		t.Errorf("expected phase Failed, got %v", lastSync["phase"])
	}
}

func TestFormatProject(t *testing.T) {
	input := map[string]any{
		"metadata": map[string]any{
			"name": "team-backend",
		},
		"spec": map[string]any{
			"description": "Backend team project",
			"destinations": []any{
				map[string]any{"server": "https://kubernetes.default.svc"},
			},
			"sourceRepos": []any{
				"https://github.com/example/*",
			},
		},
	}

	result := formatProject(input)

	if result["name"] != "team-backend" {
		t.Errorf("expected name team-backend, got %v", result["name"])
	}
	if result["description"] != "Backend team project" {
		t.Errorf("expected description, got %v", result["description"])
	}
	if result["destinations"] != 1 {
		t.Errorf("expected 1 destination, got %v", result["destinations"])
	}
	if result["source_repos"] != 1 {
		t.Errorf("expected 1 source repo, got %v", result["source_repos"])
	}
}

func TestFormatResource(t *testing.T) {
	input := map[string]any{
		"group":     "apps",
		"kind":      "Deployment",
		"name":      "web",
		"namespace": "default",
		"version":   "v1",
		"health": map[string]any{
			"status": "Healthy",
		},
		"createdAt": "2025-01-01T00:00:00Z",
	}

	result := formatResource(input)

	if result["kind"] != "Deployment" {
		t.Errorf("expected kind Deployment, got %v", result["kind"])
	}
	if result["name"] != "web" {
		t.Errorf("expected name web, got %v", result["name"])
	}
}

func TestFormatRepo(t *testing.T) {
	input := map[string]any{
		"repo":            "https://github.com/example/repo.git",
		"type":            "git",
		"name":            "example-repo",
		"connectionState": map[string]any{"status": "Successful"},
		"inheritedCreds":  false,
		"insecure":        false,
		"enableLfs":       false,
	}

	result := formatRepo(input)

	if result["repo"] != "https://github.com/example/repo.git" {
		t.Errorf("expected repo URL, got %v", result["repo"])
	}
	if result["type"] != "git" {
		t.Errorf("expected type git, got %v", result["type"])
	}
}

func TestFormatCluster(t *testing.T) {
	input := map[string]any{
		"server":     "https://kubernetes.default.svc",
		"name":       "in-cluster",
		"namespaces": []any{"default", "kube-system"},
	}

	result := formatCluster(input)

	if result["server"] != "https://kubernetes.default.svc" {
		t.Errorf("expected server, got %v", result["server"])
	}
	if result["name"] != "in-cluster" {
		t.Errorf("expected name in-cluster, got %v", result["name"])
	}
}

func TestGetNestedString(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		keys []string
		want string
	}{
		{
			name: "single key found",
			m:    map[string]any{"version": "1.0"},
			keys: []string{"version"},
			want: "1.0",
		},
		{
			name: "single key not found",
			m:    map[string]any{"other": "val"},
			keys: []string{"version"},
			want: "",
		},
		{
			name: "single key wrong type",
			m:    map[string]any{"version": 42},
			keys: []string{"version"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getNestedString(tt.m, tt.keys...)
			if got != tt.want {
				t.Errorf("getNestedString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestToolDefinitions(t *testing.T) {
	expectedTools := map[string]string{
		"argocd_list_apps":     "List all ArgoCD applications",
		"argocd_get_app":       "Get details of a specific application",
		"argocd_app_resources": "List resources managed by an application",
		"argocd_app_manifests": "Get rendered manifests for an application",
		"argocd_app_diff":      "Get diff between live and desired state",
		"argocd_sync_app":      "Sync an application to its target state",
		"argocd_refresh_app":   "Refresh application state from Git",
		"argocd_app_history":   "Get sync history for an application",
		"argocd_list_projects": "List all ArgoCD projects",
		"argocd_get_project":   "Get details of a specific project",
		"argocd_list_repos":    "List configured Git repositories",
		"argocd_get_repo":      "Get details of a specific repository",
		"argocd_list_clusters": "List configured Kubernetes clusters",
		"argocd_get_cluster":   "Get details of a specific cluster",
		"argocd_settings":      "Get ArgoCD server settings",
		"argocd_version":       "Get ArgoCD server version",
	}

	for name, desc := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
		if desc == "" {
			t.Errorf("tool %q must have a non-empty description", name)
		}
	}

	if len(expectedTools) != 16 {
		t.Errorf("expected 16 tools, got %d", len(expectedTools))
	}
}

func TestArgocdURLDefault(t *testing.T) {
	if argocdURL == "" {
		t.Error("argocdURL should have a default value")
	}
}

func TestArgocdHTTPClientInitialized(t *testing.T) {
	if httpClient == nil {
		t.Error("httpClient should be initialized via init()")
	}
}

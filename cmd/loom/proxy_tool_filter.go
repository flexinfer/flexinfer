package main

import (
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

const (
	proxyToolProfileAntigravityCore = "antigravity-core"
	proxyToolLimitAntigravity       = 100
)

// filterProxyTools applies per-client tool shaping at the proxy boundary.
// This keeps global daemon tool caches unchanged while allowing platform-
// specific caps (like Antigravity's 100-tool ceiling).
func filterProxyTools(tools []mcp.Tool, agentHint, profile string, maxTools int) []mcp.Tool {
	if len(tools) == 0 {
		return tools
	}

	resolvedProfile, resolvedLimit := resolveProxyToolFilter(agentHint, profile, maxTools)
	if resolvedProfile == proxyToolProfileAntigravityCore {
		return selectAntigravityCoreTools(tools, resolvedLimit)
	}

	if resolvedLimit > 0 && len(tools) > resolvedLimit {
		return append([]mcp.Tool(nil), tools[:resolvedLimit]...)
	}
	return tools
}

func resolveProxyToolFilter(agentHint, profile string, maxTools int) (string, int) {
	resolvedProfile := strings.ToLower(strings.TrimSpace(profile))
	if resolvedProfile == "" && strings.EqualFold(strings.TrimSpace(agentHint), "antigravity") {
		resolvedProfile = proxyToolProfileAntigravityCore
	}
	if resolvedProfile == proxyToolProfileAntigravityCore && maxTools <= 0 {
		maxTools = proxyToolLimitAntigravity
	}
	return resolvedProfile, maxTools
}

func selectAntigravityCoreTools(tools []mcp.Tool, limit int) []mcp.Tool {
	if limit <= 0 {
		limit = proxyToolLimitAntigravity
	}

	selected := make([]mcp.Tool, 0, min(limit, len(tools)))
	seen := make(map[string]struct{}, len(tools))

	addTool := func(tool mcp.Tool) bool {
		if len(selected) >= limit {
			return false
		}
		if _, ok := seen[tool.Name]; ok {
			return true
		}
		seen[tool.Name] = struct{}{}
		selected = append(selected, tool)
		return true
	}

	addByPattern := func(pattern string) bool {
		if strings.Contains(pattern, "__") {
			for _, tool := range tools {
				if tool.Name == pattern {
					return addTool(tool)
				}
			}
			return true
		}

		suffix := "__" + pattern
		for _, tool := range tools {
			if tool.Name == pattern || strings.HasSuffix(tool.Name, suffix) {
				return addTool(tool)
			}
		}
		return true
	}

	requiredPatterns := []string{
		"git__git_status",
		"git__git_diff",
		"git__git_log",
		"git__git_show",
		"git__git_add",
		"git__git_commit",
		"git__git_checkout",
		"git__git_branch",
		"git__git_pull",
		"git__git_push",
		"git__git_stash",
		"git_worktree__git_worktree_list",
		"git_worktree__git_worktree_add",
		"git_worktree__git_worktree_remove",
		"github__get_repo",
		"github__list_issues",
		"github__get_issue",
		"github__list_prs",
		"github__get_pr",
		"github__get_file_contents",
		"github__search_code",
		"gitlab__get_project",
		"gitlab__list_projects",
		"gitlab__list_issues",
		"gitlab__list_merge_requests",
		"gitlab__list_pipelines",
		"gitlab__pipeline_summary",
		"gitlab__get_pipeline",
		"gitlab__list_pipeline_jobs",
		"gitlab__get_job_trace",
		"gitlab__poll_pipeline",
		"gitlab__create_merge_request",
		"gitlab__create_issue",
		"gitlab__update_issue",
		"codebase_memory__codebase_search",
		"codebase_memory__codebase_text_search",
		"codebase_memory__codebase_get_definition",
		"codebase_memory__codebase_get_context",
		"codebase_memory__codebase_get_references",
		"codebase_memory__codebase_find_callers",
		"codebase_memory__codebase_find_callees",
		"codebase_memory__codebase_call_graph",
		"codebase_memory__codebase_module_graph",
		"codebase_memory__codebase_stats",
		"quality__quality_check",
		"quality__quality_lint",
		"quality__quality_test",
		"quality__quality_security",
		"quality__quality_coverage",
		"agent_context__agent_session_start",
		"agent_context__agent_session_list",
		"agent_context__agent_recall",
		"agent_context__agent_context_recall_enhanced",
		"agent_context__agent_context_add",
		"agent_context__agent_task_add",
		"agent_context__agent_task_update",
		"agent_context__agent_task_list",
		"agent_context__agent_handoff_create",
		"agent_context__agent_handoff_accept",
		"agent_context__agent_handoff_inbox",
		"agent_context__agent_worktree_allocate",
		"agent_context__agent_presence_register",
		"agent_context__agent_presence_heartbeat",
		"agent_context__agent_session_end",
		"tavily__search",
		"tavily__search_news",
		"tavily__extract",
		"context7__resolve-library-id",
		"context7__get-library-docs",
		"time__get_current_time",
		"time__convert_timezone",
		"time__add_duration",
		"docker__docker_ps",
		"docker__docker_logs",
		"docker__docker_exec",
		"devbox__devbox_status",
		"devbox__devbox_exec",
		"devbox__devbox_exec_poll",
		"k8s_apps_k3s__k8s_getPods",
		"k8s_apps_k3s__k8s_logs",
		"k8s_apps_k3s__k8s_get",
		"k8s_apps_k3s__k8s_describe",
		"k8s_harvester_infra__k8s_getPods",
		"k8s_harvester_infra__k8s_logs",
		"k8s_harvester_infra__k8s_get",
		"longhorn_k3s__k8s_getPods",
		"longhorn_k3s__k8s_logs",
		"longhorn_k3s__k8s_get",
		"flux__flux_get_kustomizations",
		"flux__flux_get_sources",
		"flux__flux_reconcile",
		"prometheus__query",
		"prometheus__query_range",
		"loki__loki_query",
		"loki__loki_query_range",
		"grafana__grafana_search",
		"grafana__grafana_get_dashboard",
		"helm__helm_list",
		"helm__helm_status",
		"cloudflare__cf_list_zones",
		"cloudflare__cf_list_dns_records",
		"release__release_status",
		"release__release_validate",
	}
	for _, pattern := range requiredPatterns {
		if !addByPattern(pattern) {
			return selected
		}
	}

	toolsByServer := make(map[string][]mcp.Tool)
	for _, tool := range tools {
		server := serverFromToolName(tool.Name)
		toolsByServer[server] = append(toolsByServer[server], tool)
	}

	serverOrder := []string{
		"git", "git_worktree", "github", "gitlab", "codebase_memory", "quality",
		"agent_context", "devbox", "docker", "k8s_apps_k3s", "k8s_harvester_infra",
		"longhorn_k3s", "flux", "prometheus", "loki", "grafana", "helm",
		"cloudflare", "tavily", "context7", "time", "release",
	}
	serverQuota := map[string]int{
		"git":                 12,
		"gitlab":              16,
		"codebase_memory":     12,
		"agent_context":       14,
		"k8s_apps_k3s":        8,
		"k8s_harvester_infra": 8,
		"longhorn_k3s":        8,
		"devbox":              6,
		"docker":              5,
		"flux":                6,
		"prometheus":          5,
		"loki":                5,
		"grafana":             5,
		"helm":                5,
		"cloudflare":          5,
	}

	for _, server := range serverOrder {
		quota := serverQuota[server]
		if quota == 0 {
			quota = 4
		}
		added := 0
		for _, tool := range toolsByServer[server] {
			if len(selected) >= limit {
				return selected
			}
			if _, ok := seen[tool.Name]; ok {
				continue
			}
			if !addTool(tool) {
				return selected
			}
			added++
			if added >= quota {
				break
			}
		}
	}

	for _, tool := range tools {
		if len(selected) >= limit {
			break
		}
		if _, ok := seen[tool.Name]; ok {
			continue
		}
		if !addTool(tool) {
			break
		}
	}

	return selected
}

func serverFromToolName(name string) string {
	if idx := strings.Index(name, "__"); idx > 0 {
		return name[:idx]
	}
	return ""
}

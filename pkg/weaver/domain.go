package weaver

import (
	"fmt"
	"sync"
)

// SubAgent defines a domain-specific orchestration agent with a curated tool set.
type SubAgent struct {
	Name         string   `json:"name" yaml:"name"`
	Description  string   `json:"description" yaml:"description"`
	SystemPrompt string   `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Tools        []string `json:"tools" yaml:"tools"`
	Model        string   `json:"model,omitempty" yaml:"model,omitempty"`
	TokenBudget  int      `json:"token_budget,omitempty" yaml:"token_budget,omitempty"`
	MaxTokens    int      `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
	// Write declares the domain performs mutating operations. Auto-compose
	// refuses to implicitly include write domains; they must be explicitly
	// selected by the caller.
	Write bool `json:"write,omitempty" yaml:"write,omitempty"`
}

// DomainRegistry manages available orchestration domains.
type DomainRegistry struct {
	mu      sync.RWMutex
	domains map[string]SubAgent
}

// NewDomainRegistry creates an empty DomainRegistry.
func NewDomainRegistry() *DomainRegistry {
	return &DomainRegistry{
		domains: make(map[string]SubAgent),
	}
}

// Register adds or replaces a domain in the registry.
func (r *DomainRegistry) Register(agent SubAgent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.domains[agent.Name] = agent
}

// Get returns a domain by name and whether it exists.
func (r *DomainRegistry) Get(name string) (SubAgent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.domains[name]
	return a, ok
}

// List returns all registered domains.
func (r *DomainRegistry) List() []SubAgent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SubAgent, 0, len(r.domains))
	for _, a := range r.domains {
		out = append(out, a)
	}
	return out
}

// Names returns all registered domain names.
func (r *DomainRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.domains))
	for name := range r.domains {
		out = append(out, name)
	}
	return out
}

// ToolToDomains returns a map from tool name to the domains that use it.
func (r *DomainRegistry) ToolToDomains() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := make(map[string][]string)
	for _, a := range r.domains {
		for _, t := range a.Tools {
			m[t] = append(m[t], a.Name)
		}
	}
	return m
}

// ValidateTools checks that all tools referenced by registered domains
// exist in the available tool set. Returns warnings for missing tools.
func (r *DomainRegistry) ValidateTools(lister ToolLister) []string {
	available, err := lister.ListTools()
	if err != nil {
		return []string{fmt.Sprintf("failed to list tools: %v", err)}
	}
	avSet := make(map[string]bool, len(available))
	for _, t := range available {
		avSet[t.Name] = true
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var warnings []string
	for _, d := range r.domains {
		for _, tool := range d.Tools {
			if !avSet[tool] {
				warnings = append(warnings, fmt.Sprintf("domain %q references missing tool %q", d.Name, tool))
			}
		}
	}
	return warnings
}

// DefaultDomains returns the built-in orchestration domains.
func DefaultDomains() []SubAgent {
	return []SubAgent{
		{
			Name:         "agent-fleet",
			Description:  "Agent presence, sessions, tasks, and recall across the fleet",
			SystemPrompt: agentFleetSystemPrompt,
			Tools: []string{
				"agent_context__agent_presence_list",
				"agent_context__agent_session_list",
				"agent_context__agent_task_list",
				"agent_context__agent_recall",
			},
		},
		{
			Name:         "ci-pipeline",
			Description:  "CI/CD pipeline status, merge requests, and job results",
			SystemPrompt: ciPipelineSystemPrompt,
			Tools: []string{
				"gitlab__list_pipelines",
				"gitlab__get_pipeline",
				"gitlab__list_merge_requests",
				"gitlab__pipeline_summary",
				"gitlab__list_pipeline_jobs",
				"gitlab__get_job_trace",
			},
		},
		{
			Name:         "cluster-ops",
			Description:  "Kubernetes cluster health, pods, deployments, services, and logs",
			SystemPrompt: clusterOpsSystemPrompt,
			Tools: []string{
				"k8s_apps_k3s__k8s_getPods",
				"k8s_apps_k3s__k8s_get",
				"k8s_apps_k3s__k8s_describe",
				"k8s_apps_k3s__k8s_logs",
				"k8s_apps_k3s__k8s_listNamespaces",
				"ops_mcp__k8s_get_nodes",
			},
		},
		{
			Name:         "codebase",
			Description:  "Git repository state, diffs, logs, branch information, and semantic code search",
			SystemPrompt: codebaseSystemPrompt,
			Tools: []string{
				"git__git_status",
				"git__git_diff",
				"git__git_log",
				"git__git_show",
				"git__git_branch",
				"codebase_memory__codebase_search",
				"codebase_memory__codebase_get_definition",
				"codebase_memory__codebase_find_callers",
			},
		},
		{
			Name:         "infra-ops",
			Description:  "Flux GitOps kustomizations, Helm releases, and infrastructure status",
			SystemPrompt: infraOpsSystemPrompt,
			Tools: []string{
				"flux__flux_get_kustomizations",
				"flux__flux_get_helmreleases",
				"flux__flux_logs",
				"helm__helm_list",
				"helm__helm_status",
			},
		},
		{
			Name:         "observability",
			Description:  "Prometheus metrics, alerts, Grafana dashboards, and Loki log queries",
			SystemPrompt: observabilitySystemPrompt,
			Tools: []string{
				"prometheus__query",
				"prometheus__list_alerts",
				"grafana__grafana_search",
				"alertmanager__am_list_alerts",
				"loki__loki_query_range",
				"loki__loki_labels",
			},
		},
	}
}

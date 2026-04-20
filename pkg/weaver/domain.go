package weaver

import (
	"fmt"
	"sync"
	"time"
)

// Backend identifiers for SubAgent.Backend. Default ("" or BackendFlexInfer)
// routes subagents through the local FlexInfer client. Non-flexinfer values
// route through a SpawnBridge that creates real headless agent pods.
const (
	BackendFlexInfer = "flexinfer"
	BackendClaude    = "claude-code"
	BackendCodex     = "codex"
	BackendGemini    = "gemini"
)

// SpawnOverrides lets a SubAgent tune the spawn.Request it produces when
// dispatched via a non-flexinfer backend. Zero values fall through to the
// bridge's defaults so most domains can leave this nil.
type SpawnOverrides struct {
	Timeout      time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	MaxCostUSD   float64       `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
	MaxTurns     int           `json:"max_turns,omitempty" yaml:"max_turns,omitempty"`
	Project      string        `json:"project,omitempty" yaml:"project,omitempty"`
	UseSDKDriver bool          `json:"use_sdk_driver,omitempty" yaml:"use_sdk_driver,omitempty"`
}

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
	// Backend selects the execution path for this domain's subagent. Empty
	// or "flexinfer" routes through the local FlexInfer client (default,
	// backward-compatible). "claude-code", "codex", or "gemini" route
	// through a SpawnBridge that creates a real headless agent pod.
	Backend string `json:"backend,omitempty" yaml:"backend,omitempty"`
	// SpawnOverrides tunes the spawn.Request produced when Backend is a
	// non-flexinfer value. Nil means use bridge defaults.
	SpawnOverrides *SpawnOverrides `json:"spawn,omitempty" yaml:"spawn,omitempty"`
	// RequiresSpawn is a safety gate: non-flexinfer Backend values MUST
	// set this true. Daemon-level handlers enforce that callers of a
	// RequiresSpawn domain hold ScopeAgentSpawn. Prevents unintended
	// pod creation from untrusted code paths.
	RequiresSpawn bool `json:"requires_spawn,omitempty" yaml:"requires_spawn,omitempty"`
}

// IsFlexInferBackend reports whether the subagent runs on the local
// FlexInfer client (the default, backward-compatible path).
func (s SubAgent) IsFlexInferBackend() bool {
	return s.Backend == "" || s.Backend == BackendFlexInfer
}

// Validate enforces the safety rules on SubAgent fields. Returns an error
// describing the first violation; callers should return it to the operator
// so misconfiguration is surfaced at load time rather than at dispatch.
func (s SubAgent) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("subagent: name is required")
	}
	if s.IsFlexInferBackend() {
		return nil
	}
	switch s.Backend {
	case BackendClaude, BackendCodex, BackendGemini:
		// ok
	default:
		return fmt.Errorf("subagent %q: unknown backend %q (want %q, %q, %q, or empty for flexinfer)",
			s.Name, s.Backend, BackendClaude, BackendCodex, BackendGemini)
	}
	if !s.RequiresSpawn {
		return fmt.Errorf("subagent %q: backend %q requires requires_spawn: true to opt into real-agent dispatch",
			s.Name, s.Backend)
	}
	return nil
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

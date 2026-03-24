package orchestration

import "sync"

// PolicyConfig holds the complete set of orchestration policies.
type PolicyConfig struct {
	Dispatch DispatchPolicy `json:"dispatch"`
	Load     LoadPolicy     `json:"load"`
	Conflict ConflictPolicy `json:"conflict"`
}

// DispatchPolicy controls how tasks are auto-assigned to agents.
type DispatchPolicy struct {
	Enabled         bool    `json:"enabled"`
	Mode            string  `json:"mode"` // "capacity", "expertise", "affinity", "balanced"
	CapacityWeight  float64 `json:"capacity_weight"`
	ExpertiseWeight float64 `json:"expertise_weight"`
	AffinityWeight  float64 `json:"affinity_weight"`
	FreshnessWeight float64 `json:"freshness_weight"`
}

// LoadPolicy sets load thresholds per agent.
type LoadPolicy struct {
	MaxConcurrentTasks int `json:"max_concurrent_tasks"`
	TokenBudgetCeiling int `json:"token_budget_ceiling"`
	IdleTimeoutSeconds int `json:"idle_timeout_seconds"`
}

// ConflictPolicy controls conflict detection behavior.
type ConflictPolicy struct {
	FileClaimPreCheck    bool `json:"file_claim_pre_check"`
	NamespaceIsolation   bool `json:"namespace_isolation"`
	SharedBranchBlocking bool `json:"shared_branch_blocking"`
}

// DefaultPolicyConfig returns sensible defaults for all policies.
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		Dispatch: DispatchPolicy{
			Enabled:         true,
			Mode:            "balanced",
			CapacityWeight:  0.40,
			ExpertiseWeight: 0.30,
			AffinityWeight:  0.20,
			FreshnessWeight: 0.10,
		},
		Load: LoadPolicy{
			MaxConcurrentTasks: 5,
			TokenBudgetCeiling: 100000,
			IdleTimeoutSeconds: 300,
		},
		Conflict: ConflictPolicy{
			FileClaimPreCheck:    true,
			NamespaceIsolation:   false,
			SharedBranchBlocking: true,
		},
	}
}

// PolicyStore provides thread-safe read/write access to the policy config.
type PolicyStore struct {
	mu     sync.RWMutex
	config PolicyConfig
}

// NewPolicyStore creates a PolicyStore with default policies.
func NewPolicyStore() *PolicyStore {
	return &PolicyStore{config: DefaultPolicyConfig()}
}

// Get returns a copy of the current policy config.
func (s *PolicyStore) Get() PolicyConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

// Set replaces the policy config.
func (s *PolicyStore) Set(cfg PolicyConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = cfg
}

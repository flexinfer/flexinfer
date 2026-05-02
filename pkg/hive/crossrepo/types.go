// Package crossrepo is the Hive v2 cross-repo coordination layer. A single
// backlog item can span multiple repos, each with its own MR, gated for
// atomic merge or atomic revert. This package owns the on-disk repo
// registry and (in later slices) the multi-repo planner + integrator.
//
// The on-disk source of truth for the registry is
// `platform/gitops/k3s/hive/repos.yaml`. The Loader watches that file,
// validates it, and exposes an atomic snapshot of the current set of
// `RepoEntry` rows. The Planner (slice 4.2) and Integrator (slice 4.3)
// consult the snapshot at routing time; they never re-parse YAML inline.
//
// See `.loom/93-product-spec-hive-v2-hierarchical-swarm-2026-05-02.md`
// §"Cross-Repo Federation" for the schema and §"Cross-repo coordination
// flow" for the runtime semantics.
package crossrepo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// APIVersion is the only registry apiVersion this package recognises.
const APIVersion = "hive.loom.dev/v1"

// Kind is the only registry kind this package recognises.
const Kind = "RepoRegistry"

// Registry is the YAML on-disk shape of `repos.yaml`, mirroring spec
// §"Cross-Repo Federation".
type Registry struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   RegistryMeta `yaml:"metadata"`
	Spec       RegistrySpec `yaml:"spec"`
}

// RegistryMeta holds object identity. Only Name is consumed today.
type RegistryMeta struct {
	Name string `yaml:"name"`
}

// RegistrySpec holds the list of repos this workspace federates.
type RegistrySpec struct {
	// Repos is the federated repo set. Each entry must have a unique Name.
	Repos []RepoEntry `yaml:"repos"`
}

// RepoEntry describes a single repo eligible for cross-repo coordination.
// Names must be unique within a registry; the Planner uses Name as the
// stable key for `backlog_items.repos[].project` and for HUD display.
type RepoEntry struct {
	// Name is the short slug ("loom-core", "loom", "flexdeck"). Required.
	// Used as the registry key and HUD label.
	Name string `yaml:"name"`

	// URL is the git remote URL ("git@gitlab.flexinfer.ai:services/loom-core.git").
	// Required. Consumed by the worktree allocator (slice 4.2) when a
	// backlog item references this repo by name.
	URL string `yaml:"url"`

	// ProjectID is the GitLab project ID (47, 51, 53, …). Required for any
	// repo whose MRs go through GitLab CI; the integrator (slice 4.3) uses
	// this to call mcp-gitlab. Zero means "not GitLab-tracked".
	ProjectID int64 `yaml:"project_id,omitempty"`

	// DefaultBranch is the trunk branch ("main"). Defaults to "main" when
	// the registry omits it. Used by the planner when the backlog item
	// does not pin a base branch per repo.
	DefaultBranch string `yaml:"default_branch,omitempty"`

	// AutoMerge controls whether the integrator may request auto-merge on
	// MRs targeting this repo. Default false; new repos opt-in
	// per-V2-D4 once dogfooding proves the cross-repo path safe.
	AutoMerge bool `yaml:"auto_merge,omitempty"`

	// ProtectedPaths is per-repo glob list of paths that must require
	// human review even when AutoMerge is true. Layered on top of the
	// global `policy.pipeline.protected_paths`. Empty means "use global
	// only".
	ProtectedPaths []string `yaml:"protected_paths,omitempty"`
}

// Parse reads + validates a repo registry from raw YAML.
func Parse(data []byte) (*Registry, error) {
	var r Registry
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("crossrepo: parse: %w", err)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("crossrepo: validate: %w", err)
	}
	r.applyDefaults()
	return &r, nil
}

// Validate enforces structural rules a malformed registry must trip on.
//
// Required:
//   - apiVersion exactly "hive.loom.dev/v1"
//   - kind exactly "RepoRegistry"
//   - every repo has a non-empty Name
//   - every repo has a non-empty URL
//   - repo names are unique within the registry
//   - every protected_paths entry parses as a valid doublestar glob
func (r *Registry) Validate() error {
	if r == nil {
		return errors.New("registry is nil")
	}
	if strings.TrimSpace(r.APIVersion) != APIVersion {
		return fmt.Errorf("apiVersion: must be %q, got %q", APIVersion, r.APIVersion)
	}
	if strings.TrimSpace(r.Kind) != Kind {
		return fmt.Errorf("kind: must be %q, got %q", Kind, r.Kind)
	}
	seen := make(map[string]struct{}, len(r.Spec.Repos))
	for i, repo := range r.Spec.Repos {
		name := strings.TrimSpace(repo.Name)
		if name == "" {
			return fmt.Errorf("spec.repos[%d].name is required", i)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("spec.repos[%d].name %q is duplicated", i, name)
		}
		seen[name] = struct{}{}
		if strings.TrimSpace(repo.URL) == "" {
			return fmt.Errorf("spec.repos[%d] (%s).url is required", i, name)
		}
		for j, pat := range repo.ProtectedPaths {
			if strings.TrimSpace(pat) == "" {
				return fmt.Errorf("spec.repos[%d] (%s).protected_paths[%d] is empty", i, name, j)
			}
			if !doublestar.ValidatePattern(pat) {
				return fmt.Errorf("spec.repos[%d] (%s).protected_paths[%d] %q is not a valid glob", i, name, j, pat)
			}
		}
	}
	return nil
}

// applyDefaults fills in defaults that are stable across calls (e.g.,
// `default_branch: "main"` when omitted). Mutates the registry in place.
// Safe to call only after Validate passed.
func (r *Registry) applyDefaults() {
	if r == nil {
		return
	}
	for i := range r.Spec.Repos {
		if strings.TrimSpace(r.Spec.Repos[i].DefaultBranch) == "" {
			r.Spec.Repos[i].DefaultBranch = "main"
		}
	}
}

// Repos returns a defensive copy of the registry's repo entries. Safe for
// the caller to mutate.
func (r *Registry) Repos() []RepoEntry {
	if r == nil || len(r.Spec.Repos) == 0 {
		return nil
	}
	out := make([]RepoEntry, len(r.Spec.Repos))
	for i, repo := range r.Spec.Repos {
		out[i] = repo.clone()
	}
	return out
}

// Find returns the repo with the given name, or (zero, false) if absent.
func (r *Registry) Find(name string) (RepoEntry, bool) {
	if r == nil {
		return RepoEntry{}, false
	}
	for _, repo := range r.Spec.Repos {
		if repo.Name == name {
			return repo.clone(), true
		}
	}
	return RepoEntry{}, false
}

func (e RepoEntry) clone() RepoEntry {
	out := e
	if len(e.ProtectedPaths) > 0 {
		out.ProtectedPaths = append([]string(nil), e.ProtectedPaths...)
	}
	return out
}

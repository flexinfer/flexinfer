// Package squads is the Hive v2 hierarchical-swarm tactical layer. A squad
// is a persistent, domain-owning ensemble whose manifest declares what
// paths it owns, which tests apply, which gates are required vs. advisory,
// what ensemble it prefers, and whether it can spawn recursive sub-runs.
//
// The on-disk source of truth for squad configuration is
// `platform/gitops/k3s/hive/squads/*.yaml`. The Loader watches that
// directory, validates manifests, and reflects each one into the canonical
// `squads` table (added by migration 002_v2.sql). The Router consults the
// table at routing time; it never re-parses YAML inline.
//
// See `.loom/93-product-spec-hive-v2-hierarchical-swarm-2026-05-02.md`
// §"Squad manifest (YAML)" for the schema and §"Squad routing flow" for
// the routing semantics.
package squads

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"gopkg.in/yaml.v3"
)

// APIVersion is the only manifest apiVersion this package recognises.
const APIVersion = "hive.loom.dev/v1"

// Kind is the only manifest kind this package recognises.
const Kind = "Squad"

// FallbackName is the well-known squad name used by the router when no
// configured squad matches with sufficient confidence. The on-disk seed
// `_default.yaml` (or operator policy) provides its concrete settings; if
// no row exists for it, the router treats fallback as "use v1 defaults".
const FallbackName = "_default"

// Manifest is the YAML on-disk shape, mirroring spec §"Squad manifest".
// Every field is optional unless validation says otherwise.
type Manifest struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   ManifestMeta `yaml:"metadata"`
	Spec       ManifestSpec `yaml:"spec"`
}

// ManifestMeta holds object identity. Only Name is consumed today.
type ManifestMeta struct {
	Name string `yaml:"name"`
}

// ManifestSpec holds the squad's routing + execution configuration.
type ManifestSpec struct {
	// Paths are doublestar glob patterns (relative to repo root) the
	// squad considers "in scope". Routing matches an incoming backlog
	// item if any of its slice files match any path glob.
	Paths []string `yaml:"paths"`

	// Tests names the devbox quality_gate lanes the pipeline runs for
	// items routed to this squad (e.g., "pnpm-typecheck", "pnpm-vitest").
	Tests []string `yaml:"tests"`

	// Gates declares which gate names are required vs. advisory for items
	// this squad routes. The keys are "required" and "advisory"; values
	// are gate-name slices. Gates not listed inherit the policy default.
	Gates map[string][]string `yaml:"gates"`

	// Ensemble declares the squad's preferred editor / reviewers / judge
	// agents. The shape is intentionally untyped (map[string]any) so
	// model rotation can land via manifest edit alone.
	Ensemble map[string]any `yaml:"ensemble"`

	// BudgetShare is the fraction of the daily pipeline budget reserved
	// for this squad. Sum across all squads should be ≤ 1.0; the
	// remainder is the generic queue's share.
	BudgetShare float64 `yaml:"budget_share"`

	// RecursionEnabled lets pipeline workers in this squad spawn child
	// runs (Hive v2 bounded recursion). Default false.
	RecursionEnabled bool `yaml:"recursion_enabled"`

	// Enabled is the kill switch for this manifest. Default true; set to
	// false to park the squad without removing the file.
	Enabled *bool `yaml:"enabled,omitempty"`
}

// IsEnabled reports whether the manifest's kill switch is set. nil treats
// as enabled (= match the YAML field's "default" semantic).
func (m *Manifest) IsEnabled() bool {
	if m == nil {
		return false
	}
	if m.Spec.Enabled == nil {
		return true
	}
	return *m.Spec.Enabled
}

// Parse reads + validates a squad manifest from raw YAML.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("squads: parse: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("squads: validate: %w", err)
	}
	return &m, nil
}

// Validate enforces structural rules a malformed manifest must trip on.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("manifest is nil")
	}
	if strings.TrimSpace(m.APIVersion) != APIVersion {
		return fmt.Errorf("apiVersion: must be %q, got %q", APIVersion, m.APIVersion)
	}
	if strings.TrimSpace(m.Kind) != Kind {
		return fmt.Errorf("kind: must be %q, got %q", Kind, m.Kind)
	}
	if strings.TrimSpace(m.Metadata.Name) == "" {
		return errors.New("metadata.name is required")
	}
	for i, p := range m.Spec.Paths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("spec.paths[%d] is empty", i)
		}
		if !doublestar.ValidatePattern(p) {
			return fmt.Errorf("spec.paths[%d] %q is not a valid glob", i, p)
		}
	}
	if m.Spec.BudgetShare < 0 || m.Spec.BudgetShare > 1 {
		return fmt.Errorf("spec.budget_share must be in [0,1], got %v", m.Spec.BudgetShare)
	}
	for k, v := range m.Spec.Gates {
		if k != "required" && k != "advisory" {
			return fmt.Errorf("spec.gates: unknown bucket %q (allowed: required, advisory)", k)
		}
		for i, g := range v {
			if strings.TrimSpace(g) == "" {
				return fmt.Errorf("spec.gates.%s[%d] is empty", k, i)
			}
		}
	}
	return nil
}

// MatchesPath returns the squad's most specific (longest) glob pattern that
// matches the given file path, or "" if none match. "Most specific" is a
// proxy for selectivity — when two patterns both match, the longer one is
// usually the more deliberate scope.
func (m *Manifest) MatchesPath(path string) string {
	if m == nil {
		return ""
	}
	best := ""
	for _, pat := range m.Spec.Paths {
		ok, err := doublestar.Match(pat, path)
		if err != nil || !ok {
			continue
		}
		if len(pat) > len(best) {
			best = pat
		}
	}
	return best
}

// MatchesAny returns the most specific path glob that matches any of the
// candidate paths, or "" if none match. Used by the router to decide
// whether a backlog item falls inside this squad's scope.
func (m *Manifest) MatchesAny(paths []string) string {
	if m == nil {
		return ""
	}
	best := ""
	for _, p := range paths {
		match := m.MatchesPath(p)
		if len(match) > len(best) {
			best = match
		}
	}
	return best
}

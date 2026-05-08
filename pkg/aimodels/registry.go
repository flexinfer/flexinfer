// Package aimodels is the single place Loom resolves "what model should
// this role use?" Roles like weaver-router or mills-judge map to a
// primary model name plus an ordered list of fallbacks. Defaults are
// baked in so a fresh checkout works without configuration; per-host
// overrides come from ~/.config/loom/aimodel-roles.yaml.
//
// The intent is to stop scattering literal model names like "qwen3-8b"
// across pkg/weaver, pkg/mills, internal/hud/coordinator, and
// internal/hud/autofix. Each call site asks the resolver for a Role and
// gets back whatever the operator currently wants for that role.
//
// See .loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md
// (MR-001) for the design motivation and role table.
package aimodels

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Role identifies a logical model consumer in Loom. Adding a new Role
// requires extending defaultRoles below and any consuming call site.
type Role string

const (
	// RoleWeaverRouter is the small/fast model that classifies queries
	// and decides which subagents to dispatch.
	RoleWeaverRouter Role = "weaver-router"
	// RoleWeaverSubagent is the model individual weaver subagents call
	// when their Backend is FlexInfer (the default).
	RoleWeaverSubagent Role = "weaver-subagent"
	// RoleMillsJudge is the model the Mills RubricJudge runs against.
	RoleMillsJudge Role = "mills-judge"
	// RoleMillsResearch is the model Mills uses for the research stage
	// before delegation to weaver. Acts as a fallback when weaver
	// delegation is disabled or unreachable.
	RoleMillsResearch Role = "mills-research"
	// RoleCoordinatorDefault is the default model the HUD coordinator
	// uses for summarization, compaction, triage, extraction, planning.
	RoleCoordinatorDefault Role = "coordinator-default"
	// RoleAutofix is the model the HUD autofix subsystem runs against.
	RoleAutofix Role = "autofix"
)

// AllRoles enumerates every defined role. Used by the HUD aimodels
// endpoint and tests that walk the full role table.
func AllRoles() []Role {
	return []Role{
		RoleWeaverRouter,
		RoleWeaverSubagent,
		RoleMillsJudge,
		RoleMillsResearch,
		RoleCoordinatorDefault,
		RoleAutofix,
	}
}

// RoleSpec is the resolved configuration for a single role.
type RoleSpec struct {
	// Primary is the preferred model name or LiteLLM alias.
	Primary string `yaml:"primary"`
	// Fallbacks is the ordered list of alternates the resolver walks
	// when Primary is unavailable (circuit-broken or absent at
	// preflight).
	Fallbacks []string `yaml:"fallbacks,omitempty"`
}

// defaultRoles is the baked-in role table. Locked here so tests are
// deterministic and a fresh daemon never points at a model that doesn't
// exist on the cluster's FlexInfer proxy.
//
// Update history:
//   - 2026-05-08: initial table aligning to deployed FlexInfer models
//     (qwen3-8b-fast-7900xtx with planned alias "qwen3-8b", and
//     qwen3-1p7b-tools-radeonvii brought up Ready as router).
func defaultRoles() map[Role]RoleSpec {
	return map[Role]RoleSpec{
		RoleWeaverRouter: {
			Primary:   "qwen3-1p7b-tools-radeonvii",
			Fallbacks: []string{"qwen3-8b", "fast-text"},
		},
		RoleWeaverSubagent: {
			Primary:   "qwen3-8b",
			Fallbacks: []string{"qwen3-8b-fast-7900xtx", "fast-text"},
		},
		RoleMillsJudge: {
			Primary:   "qwen3-8b",
			Fallbacks: []string{"fast-text", "gpt-3.5-turbo"},
		},
		RoleMillsResearch: {
			Primary:   "qwen3-8b",
			Fallbacks: []string{"fast-text"},
		},
		RoleCoordinatorDefault: {
			Primary:   "qwen3-8b",
			Fallbacks: []string{"fast-text"},
		},
		RoleAutofix: {
			Primary:   "qwen3-8b",
			Fallbacks: []string{"fast-text"},
		},
	}
}

// Resolver maps Roles to RoleSpecs. Safe for concurrent use.
type Resolver struct {
	mu      sync.RWMutex
	roles   map[Role]RoleSpec
	logger  *slog.Logger
	metrics *Metrics
}

// Option configures a Resolver at construction time.
type Option func(*Resolver)

// WithLogger sets the slog.Logger used for warnings (malformed file,
// unknown role override, etc.). Defaults to slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(r *Resolver) {
		if l != nil {
			r.logger = l
		}
	}
}

// WithMetrics registers the resolver's Prometheus metrics. Pass nil to
// skip metric registration.
func WithMetrics(m *Metrics) Option {
	return func(r *Resolver) {
		r.metrics = m
	}
}

// DefaultResolver returns a Resolver populated with the baked-in role
// table. No file I/O is performed; tests use this for determinism.
func DefaultResolver(opts ...Option) *Resolver {
	r := &Resolver{
		roles:  defaultRoles(),
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// LoadResolver reads role overrides from a YAML file, layering them on
// top of the baked-in defaults. A missing file is not an error; a
// malformed file logs a warning and returns the defaults.
//
// path may be empty, in which case DefaultResolver() is returned.
func LoadResolver(path string, opts ...Option) *Resolver {
	r := DefaultResolver(opts...)
	if path == "" {
		return r
	}

	data, err := os.ReadFile(path) // #nosec G304 -- operator-controlled config path
	if err != nil {
		if !os.IsNotExist(err) {
			r.logger.Warn("aimodels: read roles file failed; using defaults", "path", path, "error", err)
		}
		return r
	}

	var file rolesFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		r.logger.Warn("aimodels: parse roles YAML failed; using defaults", "path", path, "error", err)
		return r
	}

	r.applyOverrides(file.Roles)
	return r
}

// rolesFile is the YAML schema for ~/.config/loom/aimodel-roles.yaml:
//
//	roles:
//	  weaver-router:
//	    primary: qwen3-1p7b-tools-radeonvii
//	    fallbacks: [qwen3-8b, fast-text]
type rolesFile struct {
	Roles map[Role]RoleSpec `yaml:"roles"`
}

// DefaultPath returns the conventional location for the role-override
// YAML. Empty string means we couldn't determine $HOME.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "loom", "aimodel-roles.yaml")
}

// applyOverrides merges file-supplied specs into the resolver. Missing
// fields fall through to the existing default for that role; specs for
// unknown roles log a warning and are ignored.
func (r *Resolver) applyOverrides(overrides map[Role]RoleSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()

	known := defaultRoles()
	for role, spec := range overrides {
		if _, ok := known[role]; !ok {
			r.logger.Warn("aimodels: unknown role in override file; ignoring",
				"role", string(role))
			continue
		}
		merged := r.roles[role]
		if spec.Primary != "" {
			merged.Primary = spec.Primary
		}
		if spec.Fallbacks != nil {
			merged.Fallbacks = spec.Fallbacks
		}
		r.roles[role] = merged
	}
}

// Resolve returns the primary model name for the role. Empty string if
// the role is unknown.
func (r *Resolver) Resolve(role Role) string {
	r.mu.RLock()
	spec, ok := r.roles[role]
	r.mu.RUnlock()
	if !ok {
		r.recordResolution(role, "", false)
		return ""
	}
	r.recordResolution(role, spec.Primary, false)
	return spec.Primary
}

// ResolveOrDefault returns the primary model for the role, or def if
// the role is unknown or has no primary.
func (r *Resolver) ResolveOrDefault(role Role, def string) string {
	if s := r.Resolve(role); s != "" {
		return s
	}
	return def
}

// ResolveWithFallbacks returns the ordered list of candidate models for
// the role: primary first, then each fallback. Empty slice if the role
// is unknown or has no primary.
//
// Use this when the call site can iterate (e.g., a circuit-breaker that
// tries each candidate before giving up). Single-shot consumers should
// use Resolve.
func (r *Resolver) ResolveWithFallbacks(role Role) []string {
	r.mu.RLock()
	spec, ok := r.roles[role]
	r.mu.RUnlock()
	if !ok || spec.Primary == "" {
		return nil
	}
	out := make([]string, 0, 1+len(spec.Fallbacks))
	out = append(out, spec.Primary)
	out = append(out, spec.Fallbacks...)
	return out
}

// Spec returns a copy of the RoleSpec for the role and whether it was
// found.
func (r *Resolver) Spec(role Role) (RoleSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.roles[role]
	if !ok {
		return RoleSpec{}, false
	}
	out := RoleSpec{Primary: spec.Primary}
	if spec.Fallbacks != nil {
		out.Fallbacks = append([]string(nil), spec.Fallbacks...)
	}
	return out, true
}

// Roles returns a snapshot of every role → spec mapping. Safe to mutate
// the returned map.
func (r *Resolver) Roles() map[Role]RoleSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[Role]RoleSpec, len(r.roles))
	for k, v := range r.roles {
		spec := RoleSpec{Primary: v.Primary}
		if v.Fallbacks != nil {
			spec.Fallbacks = append([]string(nil), v.Fallbacks...)
		}
		out[k] = spec
	}
	return out
}

// SetSpec overrides a role at runtime (used by tests and by the HUD
// admin API once UNIFY contracts are extended). Returns an error for
// unknown roles to prevent silent typos.
func (r *Resolver) SetSpec(role Role, spec RoleSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := defaultRoles()[role]; !ok {
		return fmt.Errorf("aimodels: unknown role %q", role)
	}
	r.roles[role] = spec
	return nil
}

// recordResolution increments the resolution counter when metrics are
// configured. Called by every Resolve / ResolveOrDefault path.
//
// fallbackUsed is reserved for future plumbing once the circuit-breaker
// integration in S8 lands; today it's always false on the read path.
func (r *Resolver) recordResolution(role Role, model string, fallbackUsed bool) {
	if r.metrics == nil {
		return
	}
	r.metrics.recordResolution(role, model, fallbackUsed)
}

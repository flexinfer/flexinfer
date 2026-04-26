// Package gates is the deterministic check library for Loom Hive's pipeline.
//
// Every auto_gate node in the hive-default-pipeline template (see
// cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml) resolves to one or
// more gate evaluations from this package. Each Gate is a pure function over
// the StageInput it sees — diff content, scope sidecar, item policy — and
// returns a deterministic Outcome. LLM-judged gates (spec_conformance,
// pr_self_review) live alongside but invoke FlexInfer; they share the same
// Gate interface so the reconciler doesn't care.
//
// The library is intentionally pure-Go and free of network I/O so the
// reconciler can evaluate gates without leaving the operator pod and so
// tests run as table cases without fixtures.
package gates

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/crb2nu/loom/pkg/hive"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// Outcome is the verdict of one gate evaluation. It mirrors the on-disk
// store.GateOutcome so the reconciler can persist it directly.
type Outcome struct {
	Pass    bool
	Reasons []string
	// JudgedBy identifies the evaluator: "go" for pure-Go gates, or
	// "flexinfer:<model>" for LLM-judged gates. Persisted into
	// gate_outcomes.judged_by for audit.
	JudgedBy string
}

// StageInput is the bundle of context every gate consumes. Fields are
// optional — a gate that doesn't need a particular input ignores it.
type StageInput struct {
	// Item is the backlog item the pipeline run is materialising. Carries
	// labels, slice scope, success criteria, budget, and item-level policy.
	Item *store.BacklogItem

	// Policy is the active policy snapshot at the moment the gate runs.
	// The runtime captures it so a hot-reload mid-stage doesn't change the
	// verdict for an in-flight check.
	Policy *hive.Policy

	// FilesChanged is the list of repo-relative paths the upstream stage
	// added or modified. Empty for stages that are gated before any
	// implement step has run.
	FilesChanged []string

	// LinesAdded / LinesRemoved are diff-stat totals for size gates.
	LinesAdded   int
	LinesRemoved int

	// DiffPatch is the raw unified diff produced by the implement stage.
	// May be nil for gates that don't need pattern matching (diff_size,
	// scope, path_policy don't; secret_scan and commit_format do).
	DiffPatch []byte

	// CommitMessages is the list of commit messages the implement stage
	// produced. Drives the commit_format gate.
	CommitMessages []string
}

// Gate is the contract every check satisfies. Implementations must be
// deterministic given (input, ctx) and side-effect-free; the reconciler
// runs them concurrently inside a single transaction.
type Gate interface {
	// Name is the stable identifier persisted into gate_outcomes.gate_name.
	Name() string
	// Evaluate runs the check. Returning a non-nil error means the check
	// itself failed (e.g. an LLM call timed out); the reconciler treats
	// that as an infrastructure error, not a gate fail. A successful run
	// always returns (Outcome, nil) regardless of pass/fail.
	Evaluate(ctx context.Context, in StageInput) (Outcome, error)
}

// Registry resolves gate names to implementations. It is concurrent-safe
// and the operator constructs one at startup; tests can build their own.
type Registry struct {
	mu    sync.RWMutex
	gates map[string]Gate
}

// NewRegistry returns an empty registry. Callers seed it via Register or
// the package-level Default() helper.
func NewRegistry() *Registry {
	return &Registry{gates: make(map[string]Gate)}
}

// Default returns a registry pre-populated with every pure-Go gate this
// package ships. LLM-judged gates are added by the operator later because
// they need a FlexInfer client wired in.
func Default() *Registry {
	r := NewRegistry()
	r.Register(&DiffSize{})
	r.Register(&Scope{})
	r.Register(&PathPolicy{})
	r.Register(&SecretScan{})
	r.Register(&CommitFormat{})
	return r
}

// Register adds g to the registry, panicking on a duplicate name. Names are
// part of the persisted contract; collisions are programmer errors.
func (r *Registry) Register(g Gate) {
	if g == nil {
		panic("gates: cannot register nil gate")
	}
	name := g.Name()
	if name == "" {
		panic("gates: gate has empty Name()")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.gates[name]; dup {
		panic(fmt.Sprintf("gates: duplicate registration for %q", name))
	}
	r.gates[name] = g
}

// Get returns the gate registered under name, or ErrUnknownGate.
func (r *Registry) Get(name string) (Gate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.gates[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownGate, name)
	}
	return g, nil
}

// Names returns the list of registered gate names, sorted for deterministic
// iteration in tests + logs.
func (r *Registry) Names() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.gates))
	for n := range r.gates {
		out = append(out, n)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// EvaluateAll runs every named gate and returns the per-gate Outcomes plus
// an aggregate Pass that's true iff every gate passed and no infrastructure
// error fired. Order of returned outcomes mirrors the input slice; an
// unknown gate name produces an explicit fail Outcome rather than a panic
// so callers can render the error in HUD.
func (r *Registry) EvaluateAll(ctx context.Context, names []string, in StageInput) ([]NamedOutcome, bool, error) {
	out := make([]NamedOutcome, 0, len(names))
	allPass := true
	for _, n := range names {
		g, err := r.Get(n)
		if err != nil {
			out = append(out, NamedOutcome{Name: n, Outcome: Outcome{
				Pass: false, Reasons: []string{err.Error()}, JudgedBy: "go",
			}})
			allPass = false
			continue
		}
		o, err := g.Evaluate(ctx, in)
		if err != nil {
			return out, false, fmt.Errorf("gate %q: %w", n, err)
		}
		out = append(out, NamedOutcome{Name: n, Outcome: o})
		if !o.Pass {
			allPass = false
		}
	}
	return out, allPass, nil
}

// NamedOutcome pairs a gate name with its verdict so callers can persist
// gate_outcomes rows in one pass.
type NamedOutcome struct {
	Name    string
	Outcome Outcome
}

// ErrUnknownGate is returned by Registry.Get when no gate is registered
// under the requested name.
var ErrUnknownGate = errors.New("gates: unknown gate")

// pass / fail are tiny constructors shared by every gate so reasons[] stays
// nil for the happy path (and JSON-encodes as []).
func pass() Outcome { return Outcome{Pass: true, JudgedBy: "go"} }
func fail(reasons ...string) Outcome {
	return Outcome{Pass: false, Reasons: reasons, JudgedBy: "go"}
}

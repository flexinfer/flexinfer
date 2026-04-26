// Package mentatlab provides reusable Go utilities for MentatLab DAG flows.
//
// The autonomous validator asserts the F7 (autonomous-refactor) invariant:
// every write-op node must be preceded on every path from the flow start by a
// human review gate. The validator is a pure function so it can be called from
// a CLI, a test harness, or (eventually) the engine wrapper in
// cmd/mcp-mentatlab.
package mentatlab

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// writeOpNodeTypes enumerates node types that mutate external state. Any
// node of one of these types must have a gate upstream on every path from
// the flow start.
var writeOpNodeTypes = map[string]bool{
	"shell":       true,
	"agent_spawn": true,
}

// gateNodeTypes enumerates node types that satisfy the write-op invariant.
//
// Two flavours coexist:
//   - human_gate / review_gate — synchronous human approval (F7 default).
//   - auto_gate — deterministic machine check (lint, tests, diff-size,
//     scope, secret-scan, commit-format, plus FlexInfer-judged spec-
//     conformance and pr-self-review). The "dark factory" pivot from
//     .loom/78- + .loom/91- replaces human approval with an automated
//     verdict; failure escalates to a human handoff via the runtime
//     policy, but the *static* template is unblocked by the gate alone.
//
// A flow may mix the two — e.g. auto_gate between every stage with a
// human_gate immediately before merge — and the validator is happy as
// long as some gate sits on every start-to-write-op path.
var gateNodeTypes = map[string]bool{
	"human_gate":  true,
	"review_gate": true,
	"auto_gate":   true,
}

// flowDoc is the minimal YAML shape the validator needs. It intentionally
// ignores fields the validator does not use (agent, command, input, output,
// etc.) so the format can grow without breaking the check.
type flowDoc struct {
	ID          string     `yaml:"id"`
	Version     int        `yaml:"version"`
	Description string     `yaml:"description"`
	Nodes       []flowNode `yaml:"nodes"`
	Edges       []flowEdge `yaml:"edges"`
}

// succMap is a forward adjacency map built alongside the predecessor map so
// the validator can walk successors from a write-op node.
type graphIndex struct {
	nodes map[string]flowNode
	preds map[string][]string
	succs map[string][]string
}

type flowNode struct {
	ID    string `yaml:"id"`
	Type  string `yaml:"type"`
	After string `yaml:"after,omitempty"`
}

type flowEdge struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// ValidateAutonomousFlow parses the YAML flow description and asserts the
// write-edge-through-review-gate invariant. Returns nil on success, or a
// descriptive error for malformed input, missing nodes, or a write-op node
// whose upstream path bypasses every gate.
func ValidateAutonomousFlow(yamlBytes []byte) error {
	if len(yamlBytes) == 0 {
		return errors.New("empty flow document")
	}
	var doc flowDoc
	if err := yaml.Unmarshal(yamlBytes, &doc); err != nil {
		return fmt.Errorf("parse flow yaml: %w", err)
	}
	if len(doc.Nodes) == 0 {
		return errors.New("flow has no nodes")
	}

	// Index nodes and accumulate predecessor + successor edges from both
	// `edges:` and the shorthand `after:` field on a node.
	g := graphIndex{
		nodes: make(map[string]flowNode, len(doc.Nodes)),
		preds: make(map[string][]string, len(doc.Nodes)),
		succs: make(map[string][]string, len(doc.Nodes)),
	}
	for _, n := range doc.Nodes {
		if n.ID == "" {
			return errors.New("node with empty id")
		}
		if _, dup := g.nodes[n.ID]; dup {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		g.nodes[n.ID] = n
	}
	for _, n := range doc.Nodes {
		if n.After != "" {
			if _, ok := g.nodes[n.After]; !ok {
				return fmt.Errorf("node %q references unknown after=%q", n.ID, n.After)
			}
			g.preds[n.ID] = append(g.preds[n.ID], n.After)
			g.succs[n.After] = append(g.succs[n.After], n.ID)
		}
	}
	for _, e := range doc.Edges {
		if _, ok := g.nodes[e.From]; !ok {
			return fmt.Errorf("edge from unknown node %q", e.From)
		}
		if _, ok := g.nodes[e.To]; !ok {
			return fmt.Errorf("edge to unknown node %q", e.To)
		}
		g.preds[e.To] = append(g.preds[e.To], e.From)
		g.succs[e.From] = append(g.succs[e.From], e.To)
	}

	// For every write-op node N, require that every start-to-terminal path
	// passing through N crosses a gate. That reduces to: every upstream path
	// from N to a start is gated, OR every downstream path from N to a
	// terminal is gated. If both directions are ungated, some path through N
	// hits no gate at all and the invariant fails.
	for _, n := range doc.Nodes {
		if !IsWriteOpType(n.Type) {
			continue
		}
		if gatedUpstream(n.ID, g) {
			continue
		}
		if gatedDownstream(n.ID, g) {
			continue
		}
		return fmt.Errorf(
			"node %q (type=%s) has no upstream gate (human_gate, review_gate, or auto_gate) on its path to the start",
			n.ID, n.Type,
		)
	}
	return nil
}

// gatedUpstream reports true if every path from nodeID back to a start
// crosses a gate node. A "start" is a node with no predecessors.
func gatedUpstream(nodeID string, g graphIndex) bool {
	return allPathsGated(nodeID, g, true)
}

// gatedDownstream reports true if every path from nodeID forward to a
// terminal crosses a gate node. A "terminal" is a node with no successors.
func gatedDownstream(nodeID string, g graphIndex) bool {
	return allPathsGated(nodeID, g, false)
}

// allPathsGated returns true when every path from origin in the requested
// direction contains a gate node strictly between origin and the boundary.
// direction=true walks predecessors (upstream), false walks successors
// (downstream). Flows are assumed acyclic (DAG); an inFlight set guards
// against accidental cycles by treating them as unsatisfied.
func allPathsGated(origin string, g graphIndex, upstream bool) bool {
	memo := make(map[string]bool)
	inFlight := make(map[string]bool)
	var visit func(id string) bool
	visit = func(id string) bool {
		if v, ok := memo[id]; ok {
			return v
		}
		if inFlight[id] {
			// Cycle — treat as unsatisfied so the error path surfaces.
			return false
		}
		inFlight[id] = true
		defer delete(inFlight, id)

		var neighbors []string
		if upstream {
			neighbors = g.preds[id]
		} else {
			neighbors = g.succs[id]
		}
		if len(neighbors) == 0 {
			// Reached the boundary without crossing a gate.
			memo[id] = false
			return false
		}
		for _, nb := range neighbors {
			// If the neighbor is a gate, this branch is satisfied; otherwise
			// recurse past it.
			if gateNodeTypes[g.nodes[nb].Type] {
				continue
			}
			if !visit(nb) {
				memo[id] = false
				return false
			}
		}
		memo[id] = true
		return true
	}
	return visit(origin)
}

// IsWriteOpType reports whether a node type is considered a write operation.
// Exported for callers (tests, tooling) that want to mirror the validator's
// taxonomy without hardcoding strings. Also matches any type with the
// `git_` prefix, per the F7 spec.
func IsWriteOpType(t string) bool {
	if writeOpNodeTypes[t] {
		return true
	}
	return strings.HasPrefix(t, "git_")
}

package agentloop

import "fmt"

// Registry holds the session's fixed tool set. Insertion order is preserved
// so Definitions() emits a stable `tools` array every turn — order changes
// would bust the prefix cache.
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry builds a registry from the given tools, in order. A duplicate
// tool name is an error: two tools answering to the same name would make
// dispatch ambiguous.
func NewRegistry(tools ...Tool) (*Registry, error) {
	r := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		name := t.Definition().Function.Name
		if name == "" {
			return nil, fmt.Errorf("registry: tool with empty name")
		}
		if _, dup := r.tools[name]; dup {
			return nil, fmt.Errorf("registry: duplicate tool name %q", name)
		}
		r.tools[name] = t
		r.order = append(r.order, name)
	}
	return r, nil
}

// Get returns the tool registered under name, or ok=false.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns the tool schemas in registration order, for the
// request's immutable `tools` array.
func (r *Registry) Definitions() []ToolDef {
	if len(r.order) == 0 {
		return nil
	}
	defs := make([]ToolDef, 0, len(r.order))
	for _, name := range r.order {
		defs = append(defs, r.tools[name].Definition())
	}
	return defs
}

// Len reports how many tools are registered.
func (r *Registry) Len() int { return len(r.order) }

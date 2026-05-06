package render

import (
	"fmt"
	"strings"
)

// Filter is a row-level selector built from comma-separated key=value pairs.
//
// All comparisons are case-insensitive on both keys and values. An empty
// Filter matches every row.
type Filter map[string]string

// ParseFilter parses spec into a Filter.
//
// The grammar is `key=value(,key=value)*`. Whitespace around keys, values,
// and the separating commas is trimmed. Empty pairs (e.g. trailing commas)
// are ignored. Duplicate keys are last-write-wins.
//
// Returns an empty Filter (and nil error) when spec is the empty string or
// contains only whitespace. Returns a descriptive error when any non-empty
// pair lacks an `=` separator or has an empty key after trimming.
func ParseFilter(spec string) (Filter, error) {
	out := Filter{}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return out, nil
	}
	for _, raw := range strings.Split(spec, ",") {
		pair := strings.TrimSpace(raw)
		if pair == "" {
			continue
		}
		idx := strings.IndexByte(pair, '=')
		if idx < 0 {
			return nil, fmt.Errorf("render: filter %q is missing '=' (expected key=value)", pair)
		}
		key := strings.TrimSpace(pair[:idx])
		value := strings.TrimSpace(pair[idx+1:])
		if key == "" {
			return nil, fmt.Errorf("render: filter %q has an empty key", pair)
		}
		out[strings.ToLower(key)] = strings.ToLower(value)
	}
	return out, nil
}

// Match reports whether row satisfies every key=value pair in f.
//
// Each filter key must be present in row (case-insensitive) and the values
// must compare equal (also case-insensitive). A row missing any filter key
// is rejected. An empty Filter matches every row, including nil.
func (f Filter) Match(row map[string]string) bool {
	if len(f) == 0 {
		return true
	}
	// Build a case-insensitive view of row keys.
	lower := make(map[string]string, len(row))
	for k, v := range row {
		lower[strings.ToLower(k)] = strings.ToLower(v)
	}
	for k, want := range f {
		got, ok := lower[k]
		if !ok {
			return false
		}
		if got != want {
			return false
		}
	}
	return true
}

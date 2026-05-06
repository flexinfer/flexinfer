package render

import (
	"encoding/json"
	"io"
)

// JSON writes v to w as pretty-printed JSON (two-space indent) followed by
// a trailing newline. HTML escaping is disabled so the output preserves
// characters such as <, >, and & literally.
//
// JSON mirrors the conventions used by `loom status` and `loom catalog`:
// the encoder is configured with SetIndent("", "  ") and SetEscapeHTML(false)
// so subcommands migrating to this helper produce byte-identical output.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// JSONCompact writes v to w as compact JSON (no indentation) followed by a
// trailing newline. HTML escaping is disabled.
//
// Use JSONCompact for machine-readable surfaces where size matters, e.g.
// log lines or single-record streaming endpoints. For human-facing CLI
// output prefer JSON.
func JSONCompact(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

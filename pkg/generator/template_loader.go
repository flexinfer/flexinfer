package generator

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"text/template"

	"github.com/crb2nu/loom/pkg/registry"
)

//go:embed all:templates
var templatesFS embed.FS

var (
	templateCache   = map[string]*template.Template{}
	templateCacheMu sync.Mutex
)

// templateContext is passed to hook templates as the dot value. Templates
// that need access to the registry, profile, or loom binary path use the
// fields on this struct or the closure-bound custom funcs returned by
// hookTemplateFuncs.
type templateContext struct {
	Profile    *PlatformProfile
	LoomBinary string
}

// renderHookTemplate executes a template under pkg/generator/templates/
// with hook-aware custom funcs. The rendered output is parsed as JSON and
// returned as a map[string]any so the caller can pass it to the same
// json.Encoder used by the legacy Go builders. This guarantees byte-identical
// settings.json output across the legacy and template-driven paths.
//
// Returns ("", false, err) on any template-side error so the caller can
// optionally fall back to legacy behavior. Returns (config, true, nil) on
// success.
func renderHookTemplate(reg *registry.Registry, profile *PlatformProfile, loomBinary string) (map[string]any, bool, error) {
	if profile == nil || profile.Hooks.Template == "" {
		return nil, false, nil
	}

	tmpl, err := loadTemplate(profile.Hooks.Template, hookTemplateFuncs(reg, profile, loomBinary))
	if err != nil {
		return nil, false, fmt.Errorf("load template %q: %w", profile.Hooks.Template, err)
	}

	var buf bytes.Buffer
	ctx := templateContext{Profile: profile, LoomBinary: loomBinary}
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, false, fmt.Errorf("execute template %q: %w", profile.Hooks.Template, err)
	}

	var config map[string]any
	if err := json.Unmarshal(buf.Bytes(), &config); err != nil {
		return nil, false, fmt.Errorf("template %q produced invalid JSON: %w\nrendered:\n%s", profile.Hooks.Template, err, buf.String())
	}
	return config, true, nil
}

// loadTemplate reads, parses, and caches a template from the embedded FS.
// FuncMap must be passed at parse time because text/template binds funcs
// during Parse rather than Execute.
func loadTemplate(relPath string, funcs template.FuncMap) (*template.Template, error) {
	cacheKey := relPath
	templateCacheMu.Lock()
	defer templateCacheMu.Unlock()

	if cached, ok := templateCache[cacheKey]; ok {
		// Cached templates have funcs from their first registration. To allow
		// per-call closures we always re-parse when funcs are non-nil. The
		// cache only short-circuits when the caller passed nil funcs (used
		// in tests for parse-validity checks).
		if funcs == nil {
			return cached, nil
		}
	}

	path := filepath.Join("templates", relPath)
	data, err := templatesFS.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read embedded template %s: %w", path, err)
	}

	tmpl := template.New(filepath.Base(relPath))
	if funcs != nil {
		tmpl = tmpl.Funcs(funcs)
	}
	tmpl, err = tmpl.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse template %s: %w", path, err)
	}

	if funcs == nil {
		templateCache[cacheKey] = tmpl
	}
	return tmpl, nil
}

// hookTemplateFuncs returns the FuncMap available inside hook templates.
// Each func closes over the registry/profile/loomBinary so templates can
// stay declarative. Adding a new helper here is the seam that lets future
// CONFIG-N slices migrate Claude/Gemini wrappers off Go and into templates.
func hookTemplateFuncs(reg *registry.Registry, profile *PlatformProfile, loomBinary string) template.FuncMap {
	return template.FuncMap{
		// json marshals a value as compact JSON for inline use in a template.
		// Output is re-encoded by the caller via the canonical encoder, so
		// compact form here is fine.
		"json": func(v any) (string, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return string(b), nil
		},

		// jsonIndent marshals a value as indented JSON. Useful when the
		// template wants to keep the rendered output human-readable before
		// the canonical re-encoding step. Same indent as the canonical
		// encoder (2 spaces) for consistency.
		"jsonIndent": func(v any) (string, error) {
			b, err := json.MarshalIndent(v, "", "  ")
			if err != nil {
				return "", err
			}
			return string(b), nil
		},

		// shellQuote escapes a string for safe inclusion inside single-quoted
		// shell commands. Mirrors the existing pkg/generator/common.go helper.
		"shellQuote": shellQuote,

		// regexEscape returns the regexp.QuoteMeta of a string. Used when
		// templates build hook command grep patterns from registry data.
		"regexEscape": regexp.QuoteMeta,

		// trim wraps strings.TrimSpace.
		"trim": strings.TrimSpace,

		// buildHooks invokes the shared hook-building pipeline (events +
		// policies + extras). Templates that just want the standard hooks
		// block can render this directly via {{ buildHooks | json }}.
		"buildHooks": func() map[string]any {
			hooks := buildPlatformHooks(reg, profile.Hooks, loomBinary)
			appendHookPolicies(hooks, reg, profile.Hooks)
			appendHookExtras(hooks, profile.Hooks, loomBinary)
			return hooks
		},

		// registrySettings returns the settings map from
		// platform_permissions[platform] in the registry, or nil if absent.
		// Templates dereference specific fields like {{ index (registrySettings "claude") "default_mode" }}.
		"registrySettings": func(platform string) map[string]any {
			pp := registryPlatformPerms(reg, platform)
			if pp == nil {
				return nil
			}
			return pp.Settings
		},

		// hasField returns true when the named field is present and non-empty
		// in a map. Avoids template-author footguns around nil maps and
		// zero-value strings.
		"hasField": func(m map[string]any, key string) bool {
			if m == nil {
				return false
			}
			v, ok := m[key]
			if !ok {
				return false
			}
			switch vv := v.(type) {
			case string:
				return vv != ""
			case []string:
				return len(vv) > 0
			case []any:
				return len(vv) > 0
			case nil:
				return false
			default:
				return true
			}
		},
	}
}

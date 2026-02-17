package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/templatevars"
)

type registryResolution struct {
	Path       string   `json:"path,omitempty"`
	Found      bool     `json:"found"`
	Source     string   `json:"source,omitempty"`
	Precedence []string `json:"precedence,omitempty"`
}

type registryCandidate struct {
	Path  string
	Label string
}

type templateReference struct {
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	HasDefault bool   `json:"has_default,omitempty"`
}

type unresolvedTemplateRef struct {
	Server   string `json:"server"`
	Location string `json:"location"`
	Kind     string `json:"kind"`
	Key      string `json:"key"`
}

type profileTemplateDiagnostic struct {
	Profile    string                  `json:"profile"`
	OK         bool                    `json:"ok"`
	Count      int                     `json:"count"`
	Unresolved []unresolvedTemplateRef `json:"unresolved,omitempty"`
}

type envConventionWarning struct {
	Key        string   `json:"key"`
	Issue      string   `json:"issue"`
	Suggestion string   `json:"suggestion"`
	Servers    []string `json:"servers,omitempty"`
}

var envKeyCanonicalPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func buildRegistryCandidates(cwd, home, workspaceRoot string) []registryCandidate {
	candidates := []registryCandidate{
		{
			Path:  filepath.Join(cwd, "mcp", "context", "registry.yaml"),
			Label: "cwd:mcp/context/registry.yaml",
		},
		{
			Path:  filepath.Join(home, ".config", "fi-mcp", "registry.yaml"),
			Label: "~/.config/fi-mcp/registry.yaml",
		},
		{
			Path:  filepath.Join(home, ".config", "loom", "registry.yaml"),
			Label: "~/.config/loom/registry.yaml",
		},
		{
			Path:  filepath.Join(home, "workspace", "gitops", "mcp", "context", "registry.yaml"),
			Label: "~/workspace/gitops/mcp/context/registry.yaml",
		},
		{
			Path:  filepath.Join(home, "workspace", "platform", "gitops", "mcp", "context", "registry.yaml"),
			Label: "~/workspace/platform/gitops/mcp/context/registry.yaml",
		},
	}

	if workspaceRoot != "" {
		candidates = append(candidates, registryCandidate{
			Path:  filepath.Join(workspaceRoot, "platform", "gitops", "mcp", "context", "registry.yaml"),
			Label: "workspace:platform/gitops/mcp/context/registry.yaml (fallback)",
		})
	}

	seen := make(map[string]bool, len(candidates))
	deduped := make([]registryCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.Path == "" || seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		deduped = append(deduped, c)
	}
	return deduped
}

func resolveRegistryForDiagnostics(workspaceRoot string) registryResolution {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	candidates := buildRegistryCandidates(cwd, home, workspaceRoot)

	precedence := make([]string, 0, len(candidates))
	for _, c := range candidates {
		precedence = append(precedence, c.Label)
		if _, err := os.Stat(c.Path); err == nil {
			return registryResolution{
				Path:       c.Path,
				Found:      true,
				Source:     c.Label,
				Precedence: precedence,
			}
		}
	}

	return registryResolution{Found: false, Precedence: precedence}
}

func defaultTemplateProfiles(reg *registry.Registry) []string {
	base := []string{"codex", "claude", "gemini", "kilocode", "vscode", "zed", "opencode", "antigravity"}
	seen := make(map[string]bool, len(base))
	out := make([]string, 0, len(base)+4)
	for _, p := range base {
		seen[p] = true
		out = append(out, p)
	}

	extras := make([]string, 0, 8)
	for _, srv := range reg.Servers {
		if srv == nil || srv.Targets == nil {
			continue
		}
		for profile := range srv.Targets {
			if profile == "" || seen[profile] {
				continue
			}
			seen[profile] = true
			extras = append(extras, profile)
		}
	}
	sort.Strings(extras)
	return append(out, extras...)
}

func collectTemplateDiagnostics(reg *registry.Registry, profiles []string) []profileTemplateDiagnostic {
	if reg == nil {
		return nil
	}

	expander := templatevars.New(
		templatevars.WithRegistry(reg),
		templatevars.WithLazySecrets(),
	)

	diags := make([]profileTemplateDiagnostic, 0, len(profiles))
	for _, profile := range profiles {
		diag := profileTemplateDiagnostic{Profile: profile, OK: true}
		seen := make(map[string]bool)
		for _, srv := range reg.Servers {
			if srv == nil {
				continue
			}
			spec, err := reg.GetServerSpec(srv.Name, profile)
			if err != nil {
				continue
			}
			if spec.Env != nil {
				for envKey, value := range spec.Env {
					collectUnresolvedTemplateRefs(expander, srv.Name, "env."+envKey, value, seen, &diag.Unresolved)
				}
			}
			for i, arg := range spec.Args {
				argStr, ok := arg.(string)
				if !ok {
					continue
				}
				collectUnresolvedTemplateRefs(expander, srv.Name, fmt.Sprintf("args[%d]", i), argStr, seen, &diag.Unresolved)
			}
		}

		sort.Slice(diag.Unresolved, func(i, j int) bool {
			a, b := diag.Unresolved[i], diag.Unresolved[j]
			if a.Server != b.Server {
				return a.Server < b.Server
			}
			if a.Location != b.Location {
				return a.Location < b.Location
			}
			if a.Kind != b.Kind {
				return a.Kind < b.Kind
			}
			return a.Key < b.Key
		})

		diag.Count = len(diag.Unresolved)
		diag.OK = diag.Count == 0
		diags = append(diags, diag)
	}
	return diags
}

func collectUnresolvedTemplateRefs(
	expander *templatevars.Expander,
	serverName, location, value string,
	seen map[string]bool,
	out *[]unresolvedTemplateRef,
) {
	refs := extractTemplateReferences(value)
	for _, ref := range refs {
		if templateRefResolved(expander, ref) {
			continue
		}
		id := strings.Join([]string{serverName, location, ref.Kind, ref.Key}, "|")
		if seen[id] {
			continue
		}
		seen[id] = true
		*out = append(*out, unresolvedTemplateRef{
			Server:   serverName,
			Location: location,
			Kind:     ref.Kind,
			Key:      ref.Key,
		})
	}
}

func extractTemplateReferences(s string) []templateReference {
	refs := make([]templateReference, 0, 4)
	refs = append(refs, extractTemplateReferencesByPrefix(s, "env")...)
	refs = append(refs, extractTemplateReferencesByPrefix(s, "keychain")...)
	refs = append(refs, extractTemplateReferencesByPrefix(s, "secret")...)
	return refs
}

func extractTemplateReferencesByPrefix(s, kind string) []templateReference {
	prefix := "${" + kind + ":"
	tmp := s
	refs := make([]templateReference, 0, 2)

	for {
		start := strings.Index(tmp, prefix)
		if start == -1 {
			break
		}
		rest := tmp[start+len(prefix):]
		end := strings.Index(rest, "}")
		if end == -1 {
			break
		}
		raw := strings.TrimSpace(rest[:end])
		key := raw
		hasDefault := false
		if kind == "env" {
			if idx := strings.Index(raw, ":-"); idx != -1 {
				key = strings.TrimSpace(raw[:idx])
				hasDefault = true
			}
		}
		if key != "" {
			refs = append(refs, templateReference{
				Kind:       kind,
				Key:        key,
				HasDefault: hasDefault,
			})
		}
		tmp = rest[end+1:]
	}

	return refs
}

func templateRefResolved(expander *templatevars.Expander, ref templateReference) bool {
	switch ref.Kind {
	case "env":
		if ref.HasDefault {
			return true
		}
		return expander.Expand("${env:"+ref.Key+"}") != ""
	case "keychain":
		return expander.Expand("${keychain:"+ref.Key+"}") != ""
	case "secret":
		return expander.Expand("${secret:"+ref.Key+"}") != ""
	default:
		return true
	}
}

func templateDiagnosticChecks(diags []profileTemplateDiagnostic) []checkResult {
	if len(diags) == 0 {
		return nil
	}

	results := make([]checkResult, 0, len(diags))
	hasWarnings := false
	for _, diag := range diags {
		if diag.OK {
			continue
		}
		hasWarnings = true
		results = append(results, checkResult{
			Name:     "templates_" + diag.Profile,
			OK:       false,
			Severity: "warn",
			Message:  fmt.Sprintf("profile '%s' has %d unresolved template reference(s)", diag.Profile, diag.Count),
			Fix:      templateFixForProfile(diag),
		})
	}
	if !hasWarnings {
		results = append(results, checkResult{
			Name:    "templates",
			OK:      true,
			Message: fmt.Sprintf("no unresolved env/keychain/secret template references across %d profile(s)", len(diags)),
		})
	}
	return results
}

func templateFixForProfile(diag profileTemplateDiagnostic) string {
	if len(diag.Unresolved) == 0 {
		return ""
	}

	type keyRef struct {
		Kind string
		Key  string
	}
	seen := make(map[keyRef]bool)
	keys := make([]keyRef, 0, len(diag.Unresolved))
	for _, ref := range diag.Unresolved {
		k := keyRef{Kind: ref.Kind, Key: ref.Key}
		if seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].Key < keys[j].Key
	})

	lines := make([]string, 0, 8)
	for i, key := range keys {
		if i >= 6 {
			break
		}
		if key.Kind == "env" && !looksLikeSecretKey(key.Key) {
			lines = append(lines, "export "+key.Key+"=<value>")
		} else {
			lines = append(lines, "loom secrets set "+key.Key)
		}
	}
	lines = append(lines, "loom sync all --regen")
	if len(keys) > 6 {
		lines = append(lines, fmt.Sprintf("... and %d more key(s)", len(keys)-6))
	}
	return "Run:\n  " + strings.Join(lines, "\n  ")
}

func collectEnvConventionWarnings(reg *registry.Registry) []envConventionWarning {
	if reg == nil {
		return nil
	}

	uses := make(map[string]map[string]bool)
	record := func(serverName, key string) {
		if key == "" {
			return
		}
		if uses[key] == nil {
			uses[key] = make(map[string]bool)
		}
		uses[key][serverName] = true
	}

	for _, srv := range reg.Servers {
		if srv == nil {
			continue
		}
		if srv.Common != nil && srv.Common.Env != nil {
			for key := range srv.Common.Env {
				record(srv.Name, key)
			}
		}
		for _, target := range srv.Targets {
			if target == nil || target.Env == nil {
				continue
			}
			for key := range target.Env {
				record(srv.Name, key)
			}
		}
	}

	keys := make([]string, 0, len(uses))
	for key := range uses {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	warnings := make([]envConventionWarning, 0, len(keys))
	for _, key := range keys {
		servers := make([]string, 0, len(uses[key]))
		for srv := range uses[key] {
			servers = append(servers, srv)
		}
		sort.Strings(servers)

		switch {
		case !envKeyCanonicalPattern.MatchString(key):
			warnings = append(warnings, envConventionWarning{
				Key:        key,
				Issue:      "non-canonical key format; prefer UPPER_SNAKE_CASE",
				Suggestion: canonicalEnvKeyName(key),
				Servers:    servers,
			})
		case timeoutKeyMissingUnit(key):
			warnings = append(warnings, envConventionWarning{
				Key:        key,
				Issue:      "timeout key omits explicit unit suffix",
				Suggestion: key + "_SECONDS",
				Servers:    servers,
			})
		case strings.HasSuffix(key, "_PAT"):
			warnings = append(warnings, envConventionWarning{
				Key:        key,
				Issue:      "prefer explicit token naming over *_PAT",
				Suggestion: strings.TrimSuffix(key, "_PAT") + "_PERSONAL_ACCESS_TOKEN",
				Servers:    servers,
			})
		}
	}

	return warnings
}

func canonicalEnvKeyName(key string) string {
	replaced := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	replaced = strings.ToUpper(replaced)
	replaced = strings.Trim(replaced, "_")
	for strings.Contains(replaced, "__") {
		replaced = strings.ReplaceAll(replaced, "__", "_")
	}
	return replaced
}

func timeoutKeyMissingUnit(key string) bool {
	if !strings.Contains(key, "TIMEOUT") {
		return false
	}
	units := []string{
		"_SECONDS",
		"_MILLISECONDS",
		"_MS",
		"_MINUTES",
		"_HOURS",
		"_DURATION",
	}
	for _, unit := range units {
		if strings.Contains(key, unit) {
			return false
		}
	}
	return strings.HasSuffix(key, "_TIMEOUT")
}

func envConventionCheck(warnings []envConventionWarning) checkResult {
	if len(warnings) == 0 {
		return checkResult{
			Name:    "env_conventions",
			OK:      true,
			Message: "registry env key naming checks passed",
		}
	}

	lines := make([]string, 0, 10)
	limit := 6
	for i, w := range warnings {
		if i >= limit {
			break
		}
		lines = append(lines, fmt.Sprintf("%s -> %s (servers: %s)", w.Key, w.Suggestion, strings.Join(w.Servers, ",")))
	}
	lines = append(lines, "loom sync all --regen")
	lines = append(lines, "loom doctor")
	if len(warnings) > limit {
		lines = append(lines, fmt.Sprintf("... and %d more key(s)", len(warnings)-limit))
	}

	return checkResult{
		Name:     "env_conventions",
		OK:       false,
		Severity: "warn",
		Message:  fmt.Sprintf("found %d env key naming warning(s) in registry", len(warnings)),
		Fix:      "Suggested updates:\n  " + strings.Join(lines, "\n  "),
	}
}

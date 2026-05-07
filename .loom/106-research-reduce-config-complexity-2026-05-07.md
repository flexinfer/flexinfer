---
type: research
date: 2026-05-07
title: EPIC 3 — Reduce config complexity with data-driven profiles
issue: https://gitlab.flexinfer.ai/services/loom-core/-/issues/67
related:
  - .loom/105-planning-roadmap-reconciliation-and-next-epics-2026-05-07.md
  - pkg/generator/platform_profiles.yaml
  - pkg/generator/platform_profile.go
  - pkg/generator/configs.go
  - mcp/context/registry.yaml
---

# Research: Reduce config complexity with data-driven profiles (EPIC 3)

## Goal

Replace remaining hardcoded per-platform branches in `pkg/generator/` with
data-driven configuration. New platform support should require **only a YAML
entry** (or YAML + a small template), not a new Go file.

## Methodology

- Walked `pkg/generator/*.go` (8.3k LOC, 5k test LOC) on 2026-05-07.
- Cross-referenced `pkg/generator/platform_profiles.yaml` (263 LOC, 9 platforms)
  against per-platform Go source files.
- Sampled the dispatch sites in `configs.go`, `configs_formats.go`,
  `configs_hooks.go`.
- Read [Issue #67](https://gitlab.flexinfer.ai/services/loom-core/-/issues/67)
  acceptance criteria.

## Current state — the chassis is already in place

The data-driven foundation already shipped:

- `pkg/generator/platform_profiles.yaml` defines 9 platforms with
  `config_format`, `config_file`, `config_root`, `hooks` (events + extras +
  enforcement), `loom_proxy` (agent_hint, tool_profile, max_tools, env),
  `capabilities`, and `features` (command_format, env_key, timeout_unit,
  timeout_field, supports_*).
- `pkg/generator/platform_profile.go` provides `GetPlatformProfile(name)`,
  `AllPlatformNames()`, and `ToVendorCapabilities()`. Profiles are embedded
  via `//go:embed`.
- `GenerateConfigsWithPath` ([configs.go:146](pkg/generator/configs.go:146))
  dispatches on `profile.ConfigFormat` (json/toml) — not on platform name
  — for the *server registration* serialization path. That part is
  already correctly data-driven.
- `generateJSONConfig` reads `profile.ConfigRoot`, `profile.Features.EnvKey`,
  `profile.Features.TimeoutField`, `profile.Features.SupportsTimeout`, and
  `profile.Features.CommandFormat` to drive serialization without a
  platform switch.

**Net**: ~70% of the config-generation code is already profile-driven for
the *MCP server registration* surface. The hand-written tail remaining
(~30%) is concentrated in the *hooks*, *permissions*, *preamble*, and
*validator* paths.

## Hand-written tails (the actual EPIC 3 scope)

### Tail 1: Hook config generation has explicit platform branches

[`configs_formats.go:315-320`](pkg/generator/configs_formats.go:315) dispatches:

```go
case "claude": config = claudeHooksConfig(reg, profile, loomBinary)
case "gemini": config = geminiHooksConfigFromRegistry(reg, profile, loomBinary)
default:       // Generic JSON hooks stub
```

- `claudeHooksConfig` ([configs_claude.go](pkg/generator/configs_claude.go),
  325 LOC) hand-rolls Claude's nested `hooks.SessionStart[].hooks[].command`
  shape, `hooks.PostToolUse[].matcher`, and `permissions.allow/deny/ask`
  rules.
- `geminiHooksConfigFromRegistry` ([configs_gemini.go](pkg/generator/configs_gemini.go),
  87 LOC) hand-rolls Gemini's flatter `BeforeTool`/`AfterTool` map shape.
- Codex's hooks are inlined into the TOML preamble via
  `codexNotifyCommand` ([configs_codex.go](pkg/generator/configs_codex.go),
  230 LOC).

The shared piece (`buildPlatformHooks` in
[`configs_hooks.go:528`](pkg/generator/configs_hooks.go)) already drives
events from `profile.Hooks.Events`, but each platform formats the result
into its own JSON/TOML shape with hand-written code.

### Tail 2: Hook extras live in Go, not YAML

[`configs_hooks.go:appendHookExtras`](pkg/generator/configs_hooks.go) walks
`profile.Hooks.Extras` (a `[]string` like `["postToolUse_formatters",
"postToolUse_taskSync", "telemetry_eventEmit"]`) and dispatches each name
to a hand-written Go function (`claudePostToolUseExtras`,
`claudePostToolUseTaskSyncHook`, `appendTelemetryEventEmitHooks`).

This is an indirection through a registry of Go functions — adding a new
extra requires adding both the YAML string AND a Go function. The Go
function bodies are templated text that could live in YAML too.

### Tail 3: Policy refs (gitops_flux) are hardcoded

`profile.Hooks.PolicyRefs` is a `[]string` (e.g. `["gitops_flux"]`). The
single supported policy ref `"gitops_flux"` is implemented inline in
[`configs_claude.go:gitopsFluxGuardrailPolicyFromRegistry`](pkg/generator/configs_claude.go)
and emits a hardcoded set of deny-rules + regex.

Adding a second policy ref (e.g. `"secrets_scan"`) requires Go code, not
YAML. This is the smallest tail, but the most opaque to operators.

### Tail 4: Validator dispatch is per-platform, not per-format

[`configs_formats.go:357-365`](pkg/generator/configs_formats.go:357):

```go
case "claude": result = validator.ValidateClaudeSettings(...)
case "gemini": result = validator.ValidateGeminiSettings(...)
default:       return // no validator
```

`pkg/validator/` has Claude- and Gemini-specific schema validators. The
profile already declares `config_format` (json/toml) and `hooks.file` —
validators could load a JSON Schema referenced from the profile rather
than have a per-platform Go entrypoint.

### Tail 5: Codex preamble is a separate world

`configs_codex.go:emitCodexPreamble` (lines 1–80) writes an opinionated
TOML preamble: workspace root, MCP server profile path, agent definitions,
notify hook command. This whole file is platform-coupled. Codex is the
only platform with `requires_preamble: true` in YAML, but the preamble
content itself is in Go.

A "preamble template" field on the profile (or a side-file
`platform_preambles/codex.toml.tmpl`) would let the generator do
text-template rendering with profile vars.

### Tail 6: OpenCode plugin TS output

`generateOpenCodeJSONConfig` writes an `opencode.json` *and* a
`plugins/loom-hooks.ts` typescript hook file. The TS content is
hand-written. OpenCode is the only platform with
`hooks.type: "typescript"`.

This tail is small and arguably platform-essential (TypeScript can't be
trivially YAML-templated), but the *generation entrypoint* could still be
unified.

## Quantified scope

| Tail | LOC affected | Risk | Operator pain today |
|---|---|---|---|
| 1. Hook generation dispatch (Claude/Gemini/Codex) | ~640 | High — most-touched code in CI breaks | Adding a 7th hook event requires editing 3 Go files |
| 2. Hook extras library | ~150 | Medium — text templates are stable | Adding `postToolUse_lint` requires Go change + YAML change |
| 3. Policy refs (gitops_flux) | ~80 | Low — single policy today | Operators can't define new guardrails without engineering |
| 4. Validator dispatch | ~40 + schema files | Low | New platforms have no validation by default |
| 5. Codex preamble | ~80 | Medium — Codex format churns | Codex TOML changes require Go edits |
| 6. OpenCode TS plugin | ~60 + TS template | Low | OpenCode is single-platform; defer |

**Total residual hand-written surface**: ~1,050 LOC across 4 files.
**Realistic data-drivable subset**: tails 1, 2, 3, 5 (~950 LOC).
**Defer or accept**: tails 4 (small + low pain) and 6 (TS template needs
language-aware tooling).

## Prior-art scan

- **Helm**: chart values + Go templates render YAML. Strong precedent for
  text-templated config generation.
- **Kustomize**: declarative overlays + patches. Useful mental model for
  per-platform diffs, but heavier than needed here.
- **Cargo / pnpm workspaces**: single source of truth + per-target overrides
  via TOML/JSON. Closer to current `registry.yaml` + `platform_profiles.yaml`
  pattern.

The closest fit is a **profile-driven Go text/template** approach: keep
`platform_profiles.yaml` as data, add embedded `.tmpl` files for the
shapes that don't fit a single struct (Claude's nested hooks, Codex's
preamble), and drive everything else from struct fields.

## Open questions

1. **Template language**: Go `text/template` (built-in, simple) or
   `html/template` (auto-escape) or external (e.g. CUE for stronger
   typing)?
   - Recommendation: Go `text/template` with custom funcs. Zero new
     deps. Matches existing `pkg/skills/scripts/` template approach.

2. **Profile schema versioning**: existing YAML has no `version:` field.
   Should we add one before changing shape?
   - Recommendation: yes. Add `version: 1` to the top of
     `platform_profiles.yaml`; bump to `2` when introducing template
     fields in the next batch.

3. **Per-platform overrides for shared templates**: should profiles
   inherit from a base template and patch fields, or define their own
   from scratch each time?
   - Recommendation: each profile gets its own template path; the
     template can `{{ template "shared/hook_command" . }}` for shared
     fragments. Avoids inheritance subtleties.

4. **Validator strategy**: load JSON Schema per-platform, or generate
   schemas from profile data?
   - Recommendation: defer (tail 4 is low pain). Add a single optional
     `schema_file:` field on the profile for v1; generate-from-profile
     in a follow-up.

5. **Backwards compatibility**: keep `claudeHooksConfig`/etc. as fallback
   during migration, or rip-and-replace?
   - Recommendation: rip-and-replace per-platform with golden-file
     tests. The current generators have full test coverage
     (`configs_test.go` + `configs_hooks_test.go` together = 3.1k LOC).
     Goldens give us a high-confidence diff.

## Risks

- **Generated-file diff churn**: any template change can produce subtly
  different JSON ordering / whitespace. Lock down JSON encoding
  (canonical ordering, fixed indent) and run goldens before each merge.
- **CI staticcheck regression**: the recent `bridge.*` deprecated-alias
  pattern (commit `d97cf28b`) shows how easy it is to leave
  staticcheck-deprecated paths in place. If this epic introduces
  `// Deprecated:` annotations, migrate callers in the same MR.
- **Profile schema bloat**: `platform_profiles.yaml` is already 263 LOC
  for 9 platforms. Adding template references could push it past the
  point where YAML alone is readable. Watch for this and split into
  per-platform files if needed.

## Recommended scope for first batch

CONFIG-1 (highest ROI): **Migrate hooks generation off platform branches.**
- Replace `claudeHooksConfig` + `geminiHooksConfigFromRegistry` with a
  single `renderHooksConfig(profile, reg, loomBinary)` driven by an
  embedded template per platform (`hooks_claude.json.tmpl`,
  `hooks_gemini.json.tmpl`).
- Remove the platform switch in `generateHooksConfig`.
- Goldens: existing `*.golden` files in
  `pkg/generator/testdata/` provide before/after.

CONFIG-2: **Move hook extras into YAML.**
- Convert each Go-defined extra (`postToolUse_formatters` etc.) to a
  template fragment under `pkg/generator/templates/extras/`.
- `appendHookExtras` becomes a template-renderer loop.

CONFIG-3: **Move policy refs (gitops_flux) into YAML data.**
- Move deny-rules + regex from Go into
  `pkg/generator/policies/gitops_flux.yaml`.
- `gitopsFluxGuardrailPolicyFromRegistry` reads the YAML.

CONFIG-4: **Templatize Codex preamble.**
- Move `emitCodexPreamble` body into
  `pkg/generator/templates/codex_preamble.toml.tmpl`.
- Reduces `configs_codex.go` from 230 LOC to ~50 LOC.

Defer (post-EPIC-3): tails 4 (validator) and 6 (OpenCode TS).

## Acceptance signals

- After CONFIG-1: removing Claude support requires deleting 1 YAML
  entry + 2 template files. Today it requires deleting `configs_claude.go`
  (325 LOC) + edits in 3 other files.
- After CONFIG-2: adding a new hook extra requires 1 template file +
  1 YAML reference. Today: 1 Go function + 1 dispatch case + 1 YAML
  reference.
- After CONFIG-3: adding a new policy ref requires 1 YAML file. Today:
  ~80 LOC of Go.
- After CONFIG-4: Codex preamble shape is visible to operators in a
  template, not a Go function.

## References

- Profile loader: [`pkg/generator/platform_profile.go`](pkg/generator/platform_profile.go)
- Profile data: [`pkg/generator/platform_profiles.yaml`](pkg/generator/platform_profiles.yaml)
- Dispatch entrypoint: [`pkg/generator/configs.go:146`](pkg/generator/configs.go:146)
- Hook generation: [`pkg/generator/configs_hooks.go`](pkg/generator/configs_hooks.go)
- Issue: [#67](https://gitlab.flexinfer.ai/services/loom-core/-/issues/67)

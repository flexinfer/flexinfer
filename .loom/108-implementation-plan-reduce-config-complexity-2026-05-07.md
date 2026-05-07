---
type: implementation-plan
date: 2026-05-07
title: EPIC 3 — Reduce config complexity (implementation plan)
issue: https://gitlab.flexinfer.ai/services/loom-core/-/issues/67
related:
  - .loom/106-research-reduce-config-complexity-2026-05-07.md
  - .loom/107-product-spec-reduce-config-complexity-2026-05-07.md
---

# Implementation plan: Reduce config complexity (EPIC 3)

## Overview

Four slices, each independently shippable. CONFIG-1 lays the template
infrastructure; CONFIG-2/3/4 reuse it. Total estimated effort: 4–6
sessions across ~1 week.

```
CONFIG-1 (template infra + hook migration) ─┐
                                             ├─► CONFIG-2 (extras)
                                             ├─► CONFIG-3 (policies)
                                             └─► CONFIG-4 (Codex preamble)
```

CONFIG-1 must land first (introduces the template loader). CONFIG-2/3/4
can then run in parallel worktrees.

## Pre-flight (before CONFIG-1)

1. Bump `platform_profiles.yaml` to `version: 2` with no behavior change.
   Loader rejects `version: 3+` with a clear error.
2. Add `pkg/generator/templates/` directory with a `.gitkeep` so the
   `//go:embed` machinery is in place.
3. Add a CI test (`pkg/generator/templates_test.go`) that walks all
   files under `templates/` and verifies each is parseable as a
   `text/template` (or YAML for `templates/policies/`).

Estimated: 1 short session. **Branch**: `chore/generator-template-scaffold`.

---

## Slice CONFIG-1 — Template infrastructure (REVISED 2026-05-07)

> **Scope-narrowing note (2026-05-07)**: original CONFIG-1 plan called for
> deleting `claudeHooksConfig` and `geminiHooksConfigFromRegistry` outright.
> Inspection showed those functions are called from 11 sites in
> `configs_test.go` (~3,100 LOC of tests). Migrating in one MR would force
> a massive concurrent test rewrite. Narrowed CONFIG-1 to **infrastructure
> only**: ship the template loader, custom funcs, and a generic example
> template that future platforms can adopt. Per-platform migration of
> Claude / Gemini moves to **CONFIG-1.5** (see below). Per-platform
> migration of Codex preamble stays in CONFIG-4 unchanged.

### Goal (revised)
Lay the template-rendering infrastructure that future platforms (and
CONFIG-1.5/2/3/4 follow-ups) consume. Keep all current platforms on the
existing Go-builder paths so output stays byte-identical.

### Scope (revised — infrastructure only)
- New: `pkg/generator/template_loader.go` — embedded `templates/` FS,
  `renderHookTemplate(reg, profile, loomBinary)` returning
  `(map[string]any, ok bool, err)`. Closure-bound `template.FuncMap` with
  `json`, `jsonIndent`, `shellQuote`, `regexEscape`, `trim`, `buildHooks`,
  `registrySettings`, `hasField`.
- New: `pkg/generator/templates/hooks/generic.json.tmpl` — minimal
  hooks-only example template (`{"hooks": {{ buildHooks | json }}}`)
  proving the path is wired end-to-end.
- New: `pkg/generator/template_loader_test.go` — exercises
  no-op/empty/render/missing-template paths and the json funcmap helper.
- Modify: `pkg/generator/platform_profile.go` — add `Template string`
  field on `HookProfile`.
- Modify: `pkg/generator/configs_formats.go:generateHooksConfig` —
  call `renderHookTemplate` first; if `ok=true`, use that map; otherwise
  fall through to existing switch (`claude`/`gemini`/generic stub).
- Modify: `pkg/generator/templates_test.go` — register hook template
  funcs as stubs so the parse-validity walker can parse files that use
  `buildHooks`, `json`, etc.

### What CONFIG-1 deliberately does NOT do
- Does **not** add `template:` to any platform's profile yaml. All 9
  existing platforms still use Go builders for byte-identical output.
- Does **not** delete `claudeHooksConfig`, `geminiHooksConfigFromRegistry`,
  or any other Go builder. Those land in CONFIG-1.5.
- Does **not** migrate hook extras (CONFIG-2), policies (CONFIG-3), or
  Codex preamble (CONFIG-4) — those slices remain unchanged in scope.

### Steps

1. Add `template:` field to `HookProfile` in
   [`platform_profile.go`](pkg/generator/platform_profile.go) (yaml:
   `template`).
2. Create `pkg/generator/template_loader.go`:
   - `//go:embed all:templates`
   - `RenderTemplate(name string, ctx any) ([]byte, error)`
   - Custom funcs: `shellQuote`, `regexEscape`, `jsonEncode`, `tomlEncode`.
3. Create `templates/hooks/claude.json.tmpl` — capture today's
   `claudeHooksConfig` output as a template. Use `{{ range .Events }}`
   blocks for each hook event.
4. Create `templates/hooks/gemini.json.tmpl` — similar.
5. Add `template:` to `claude:` and `gemini:` profiles in
   `platform_profiles.yaml`.
6. Rewrite `generateHooksConfig` (configs_formats.go:303-325) to:
   ```go
   if profile.Hooks.Template != "" {
       rendered, err := RenderTemplate(profile.Hooks.Template, ctx)
       // write rendered to profile.Hooks.File
   } else {
       // existing generic stub path
   }
   ```
7. Delete dead code in `configs_claude.go` / `configs_gemini.go`.
8. Run `go test ./pkg/generator/...` — all goldens should pass
   byte-identical.
9. If goldens diff: review template output, adjust template, repeat.
   Do **not** touch `*.golden` files unless intentional shape change.

### Acceptance gates (revised)
- `go test ./pkg/generator/...` passes (all goldens unchanged — Claude/
  Gemini Go builders still in play).
- `golangci-lint run ./pkg/generator/...` clean.
- `templates/hooks/generic.json.tmpl` renders successfully against a
  cloned Claude profile in `TestRenderHookTemplate_GenericTemplate`.
- All 4 new template-renderer tests + 1 funcmap test pass.
- `pkg/generator/configs_claude.go` and `configs_gemini.go` LOC unchanged
  (deferred to CONFIG-1.5).

### Estimated effort
1 session (was 2). **Branch**: `feat/config-1-template-hooks`.

---

## Slice CONFIG-1.5 — Migrate Claude + Gemini hook builders to templates (NEW 2026-05-07)

### Goal
Replace `claudeHooksConfig` and `geminiHooksConfigFromRegistry` with
template-driven equivalents using the infrastructure shipped in CONFIG-1.
Migrate the 11 test sites in `configs_test.go` accordingly.

### Scope
- New: `templates/hooks/claude.json.tmpl` — full Claude settings.json
  shape ($schema + permissions + hooks).
- New: `templates/hooks/gemini.json.tmpl` — full Gemini settings.json
  shape (agentConfig + hooks + hooksConfig + optional general/tools/
  security from registry settings).
- Modify: `platform_profiles.yaml` — add `template: hooks/claude.json.tmpl`
  and `template: hooks/gemini.json.tmpl` to claude/gemini profiles.
- Modify: `configs_test.go` — 11 sites: replace
  `claudeHooksConfig(reg, profile, "")` /
  `geminiHooksConfigFromRegistry(reg, profile, "")` calls with a single
  helper `renderHookConfigForTest(t, reg, profile, loomBinary)` that
  exercises the same code path as `generateHooksConfig`.
- Delete: `pkg/generator/configs_claude.go:claudeHooksConfig`,
  `claudeHooks`. Keep `claudePermissions`, `claudePermissionRuleRegexp`,
  `filterClaudePermissionRules` (used by templates via custom func or
  inlined).
- Delete: `pkg/generator/configs_gemini.go:geminiHooksConfig`,
  `geminiHooksConfigFromRegistry`, `geminiHooks` entirely. The whole file
  shrinks to <30 LOC or disappears.
- Modify: `configs_formats.go:generateHooksConfig` — drop the
  `case "claude"` / `case "gemini"` legs of the switch. Template path
  becomes the only path for those platforms.

### Strategy for the 11 test sites
1. Add `renderHookConfigForTest(t, reg, profile, loomBinary)` helper in
   `configs_test.go` that wraps `renderHookTemplate` + Unmarshal+Marshal
   round trip.
2. Mechanical sed for the 11 callers — same arguments, new function name.
3. Confirm assertion shapes still hold (each test asserts on
   `config["hooks"]`, `config["permissions"]`, etc. — all still present).
4. Run goldens — diff scope should be **zero bytes** because the template
   produces the same map shape that the encoder already canonicalizes.

### Acceptance gates
- All goldens pass byte-identical.
- `pkg/generator/configs_claude.go` < 200 LOC (was 325).
- `pkg/generator/configs_gemini.go` deleted or < 30 LOC.
- 11 test sites migrated with no assertion changes.

### Estimated effort
1–2 sessions. **Branch**: `feat/config-1-5-claude-gemini-templates`.

### Why split CONFIG-1.5 from CONFIG-1
A single MR mixing infrastructure + 11-call-site migration + test rewrite
is hard to review. Splitting lets reviewers verify the template loader
in isolation before judging the migration shape.

---

## Slice CONFIG-2 — Hook extras as template fragments

### Goal
Move `claudePostToolUseExtras`, `claudePostToolUseTaskSyncHook`, and
`appendTelemetryEventEmitHooks` from Go to template fragments.

### Scope
- New: `templates/extras/post_tool_use_formatters.tmpl`
- New: `templates/extras/post_tool_use_task_sync.tmpl`
- New: `templates/extras/telemetry_event_emit.tmpl`
- Modify: `pkg/generator/configs_hooks.go:appendHookExtras` — replace
  switch with template-renderer loop.
- Modify: `platform_profiles.yaml` — confirm `extras:` lists reference
  template names directly (already do).

### Steps

1. Create the 3 `.tmpl` files. Use the existing Go function bodies
   (`claudePostToolUseExtras` etc.) as the source of truth.
2. Define a tiny ExtraContext struct with `LoomBinary`, `Profile`,
   `Registry`, `WorkspaceRoot`.
3. Rewrite `appendHookExtras` to a template-render loop.
4. Verify each extra renders identically to its Go-function
   predecessor — diff JSON output.
5. Delete unused Go functions.
6. **Bonus** (proves extensibility): ship `templates/extras/post_tool_use_lint.tmpl`
   as a new extra. Wire it into the Gemini profile (which has no
   formatters today). Verify it appears in `gemini/settings.json`.

### Acceptance gates
- Existing goldens still pass.
- New `post_tool_use_lint` extra appears in Gemini's generated
  `settings.json` and runs `make lint` (or equivalent) when added to
  `extras:` list.
- `pkg/generator/configs_claude.go` is now < 50 LOC.

### Estimated effort
1 session. **Branch**: `feat/config-2-extras-templates`.

---

## Slice CONFIG-3 — Policy refs as YAML data

### Goal
Move `gitops_flux` policy rules from Go (`gitopsFluxGuardrailPolicyFromRegistry`,
`gitopsFluxGuardrailDenyRules`, `gitopsFluxGuardrailRegex`) to a YAML
file at `templates/policies/gitops_flux.yaml`.

### Scope
- New: `pkg/generator/templates/policies/gitops_flux.yaml` — deny
  rules + allow exceptions + regex.
- New: `pkg/generator/policies.go` — `LoadPolicy(name)` reads YAML,
  returns struct.
- Modify: `pkg/generator/configs_claude.go` — drop hardcoded policy,
  call `LoadPolicy`.
- Modify: `pkg/generator/configs_hooks.go:appendHookPolicies` — same.

### Steps

1. Define `Policy` struct in `policies.go` matching D4 schema.
2. Create `templates/policies/gitops_flux.yaml` — verbatim from
   today's Go code.
3. Add `LoadPolicy(name string) (*Policy, error)` — embedded YAML
   read, cached behind `sync.Once`.
4. Replace direct deny-rule generation with `LoadPolicy("gitops_flux")`
   loop.
5. Delete old Go functions.
6. **Bonus** (proves extensibility): add
   `templates/policies/secrets_scan.yaml` with deny rules for `*.key`,
   `*.pem`, `*.sops.yaml`. Reference it from one platform's
   `policy_refs:`. Verify deny rule appears.

### Acceptance gates
- Goldens unchanged for `gitops_flux`.
- New `secrets_scan` policy works end-to-end.
- `gitopsFlux*` Go functions deleted.

### Estimated effort
1 session. **Branch**: `feat/config-3-policies-yaml`.

---

## Slice CONFIG-4 — Codex preamble as template

### Goal
Move `emitCodexPreamble`, `codexNotifyCommand`, `emitCodexAgents` from
Go into one template at `templates/hooks/codex.toml.tmpl`.

### Scope
- New: `templates/hooks/codex.toml.tmpl` — full Codex `config.toml`
  preamble + agents block + notify hook.
- Modify: `pkg/generator/configs_codex.go` — replace 230 LOC of TOML
  emit with template render.
- Modify: `platform_profiles.yaml` — add `template:` to codex hooks.

### Steps

1. Build the template by capturing today's `emitCodexPreamble` output
   for a sample registry.
2. Confirm `{{ if .TelemetryEnabled }}` block produces correct output
   for both notify-only and notify+telemetry cases. Cover both in
   goldens.
3. Replace `emitCodexPreamble` with `RenderTemplate("hooks/codex.toml.tmpl", ctx)`.
4. Delete `codexNotifyCommand`, `emitCodexAgents`, `containsString`
   (the latter is only used here).
5. Verify against existing Codex golden file.

### Acceptance gates
- Codex golden unchanged.
- `pkg/generator/configs_codex.go` is < 60 LOC.
- `loom sync codex --regen` produces a runnable `config.toml`.

### Estimated effort
1 session. **Branch**: `feat/config-4-codex-template`.

---

## Sequencing summary

| Slice | Sessions | Depends on | Parallel-with |
|---|---|---|---|
| Pre-flight scaffold | 1 short | — | — |
| CONFIG-1 template infra | 1 | Pre-flight | — |
| CONFIG-1.5 Claude+Gemini migration | 1–2 | CONFIG-1 | CONFIG-2/3/4 (after CONFIG-1) |
| CONFIG-2 extras | 1 | CONFIG-1 | CONFIG-1.5, CONFIG-3, CONFIG-4 |
| CONFIG-3 policies | 1 | CONFIG-1 | CONFIG-1.5, CONFIG-2, CONFIG-4 |
| CONFIG-4 Codex preamble | 1 | CONFIG-1 | CONFIG-1.5, CONFIG-2, CONFIG-3 |

**Total**: 6–7 sessions of focused work (was 6). The CONFIG-1.5 split
adds a session for clean review surface but no new functional scope.

## Cross-cutting concerns

### Deterministic output
Templates produce `[]byte`; pass through `json.MarshalIndent` (for JSON
targets) before write to guarantee canonical key ordering and 2-space
indent. TOML targets render directly (TOML key order is by definition
position-sensitive — templates control it).

### Test strategy
- Golden-file tests for every existing platform (already present in
  `configs_test.go`).
- New `templates_test.go` walks `templates/` and verifies parse
  validity.
- One end-to-end test per slice: bump a profile field, run
  `GenerateConfigsWithPath`, diff output.

### Migration ordering with concurrent work
- UNIFY-1d (deferred close-out) touches `internal/hud/handlers/` —
  zero overlap with `pkg/generator/`.
- Spectator Phase 6 (deferred) adds `loom spectate` — zero overlap.
- Harbor #1 final touches `platform/gitops/` — zero overlap.
- Safe to run all four close-outs in parallel with CONFIG-1 if the
  user wants to maximize throughput.

### CI gates per MR
1. `go test ./pkg/generator/...` (goldens)
2. `golangci-lint run ./...` (no `// Deprecated:` regressions)
3. `make sync-status` (smoke: all 9 platforms still generate without error)
4. Diff-budget: each MR description includes expected golden diff scope
   ("whitespace only", "no diff", "added X to platform Y").

## Reuse / leverage from already-shipped work

- `platform_profiles.yaml` is already 70% data-driven — we're not
  starting from scratch.
- EPIC 2 Batch A–D shipping pattern (one MR per slice with shared
  prefix `feat(visibility):` / `feat(generator):`) carries directly
  over.
- `bridge.*` deprecated-alias migration ([d97cf28b](https://gitlab.flexinfer.ai/services/loom-core/-/commit/d97cf28b))
  taught us: do not leave deprecated paths in place; rip-and-replace
  with goldens.

## Open questions for user before CONFIG-1 starts

- **Q1** (D7 / OD3): Big MR or 4 small MRs?
  → recommendation: 4 small. Confirm.
- **Q2** (OD2): Ship `secrets_scan` policy in CONFIG-3 as
  extensibility proof?
  → recommendation: yes, ~30 min add.
- **Q3** (D8/D9): Defer OpenCode TS plugin and validator dispatch?
  → recommendation: yes, both.
- **Q4**: Should pre-flight scaffold land as a solo MR or as part of
  CONFIG-1?
  → recommendation: solo. Keeps CONFIG-1 review surface to the actual
  hook migration.

## References

- Spec: [.loom/107-product-spec-reduce-config-complexity-2026-05-07.md](.loom/107-product-spec-reduce-config-complexity-2026-05-07.md)
- Research: [.loom/106-research-reduce-config-complexity-2026-05-07.md](.loom/106-research-reduce-config-complexity-2026-05-07.md)
- Issue: [#67](https://gitlab.flexinfer.ai/services/loom-core/-/issues/67)
- Reconciliation context: [.loom/105-planning-roadmap-reconciliation-and-next-epics-2026-05-07.md](.loom/105-planning-roadmap-reconciliation-and-next-epics-2026-05-07.md)

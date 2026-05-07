---
type: product-spec
date: 2026-05-07
title: EPIC 3 — Reduce config complexity (product spec)
issue: https://gitlab.flexinfer.ai/services/loom-core/-/issues/67
related:
  - .loom/106-research-reduce-config-complexity-2026-05-07.md
---

# Product spec: Reduce config complexity (EPIC 3)

## Problem statement

Adding or modifying a platform's MCP/hook config today requires editing 3+
Go files and one YAML file. Operators can't customize policy guardrails or
hook extras without engineering. The data-driven foundation is in place;
~1,050 LOC of per-platform Go code is the residual tail.

## User stories

### As a Loom maintainer
- **U1**: I want to add a 7th platform (e.g. "windsurf") with one new
  YAML entry and a `hooks_<platform>.json.tmpl`, **not** a new Go file.
  Acceptance: existing CI golden tests still pass; `loom sync windsurf`
  generates a valid config.
- **U2**: I want to update Claude's `PostToolUse` matcher pattern by
  editing one template file, not a Go function. Acceptance: gold-file
  diff matches the template change exactly.

### As an operator
- **U3**: I want to define a new policy guardrail (e.g. "block writes to
  `secrets/`") by adding a YAML entry, not by filing an issue.
  Acceptance: `loom sync` picks up new policy and emits matching deny
  rules to platforms with `enforcement: native`.
- **U4**: I want to enable a hook extra on Gemini (e.g.
  `postToolUse_formatters`) by adding it to the platform's `extras:` list.
  Today this is Claude-only because the Go function is hardcoded.

### As an agent integrator (downstream consumer)
- **U5**: When a new platform ships, I want the same config-shape
  guarantees as the existing 6. No "Claude has X but Gemini doesn't"
  surprises that aren't documented in the profile.

## Decisions

### D1: Template engine — Go `text/template` with embedded templates

Use Go's standard library `text/template` with `//go:embed` for template
files. Custom funcs map for shared helpers (`shellQuote`, `regexEscape`,
`policyDenyRules`).

**Rationale**: zero new deps, matches `pkg/skills/scripts/` precedent,
debuggable. CUE was considered but adds toolchain weight without clear
ROI for this surface.

### D2: Profile schema bumps to `version: 2`

Add `version: 2` field at the top of `platform_profiles.yaml`. Loader
rejects unknown versions with a clear error. New `templates:` and
`policies:` fields land under `version: 2`.

**Rationale**: today's YAML has no version. Future-proofing makes
breaking changes safer. The bump signals migration is happening.

### D3: Per-platform templates live in `pkg/generator/templates/`

```
pkg/generator/
├── platform_profiles.yaml
├── templates/
│   ├── hooks/
│   │   ├── claude.json.tmpl
│   │   ├── gemini.json.tmpl
│   │   ├── codex.toml.tmpl       (preamble + hooks combined)
│   │   └── opencode.ts.tmpl
│   ├── extras/
│   │   ├── post_tool_use_formatters.tmpl
│   │   ├── post_tool_use_task_sync.tmpl
│   │   └── telemetry_event_emit.tmpl
│   └── policies/
│       └── gitops_flux.yaml      (data, not a template)
```

Profiles reference templates by relative path:

```yaml
claude:
  hooks:
    template: "hooks/claude.json.tmpl"
```

**Rationale**: keeps template files browsable as a directory tree;
matches operator mental model.

### D4: Policies are YAML data, not templates

Policies don't need a template engine — they're rule lists. Each
policy YAML file declares:

```yaml
# templates/policies/gitops_flux.yaml
name: gitops_flux
description: "Block direct writes to GitOps-managed paths"
deny:
  - path: "platform/gitops/**"
    operations: [write, delete]
  - path: "**/*.sops.yaml"
    operations: [write]
allow:
  - path: "platform/gitops/.kube/**"   # exempt kubeconfigs
regex: ".*\\.(sops|enc)\\.ya?ml$"
```

The hooks template loop pulls this struct in and emits the
platform-specific shape (Claude's `permissions.deny[]`,
Gemini's `tool_filters`).

### D5: Hook extras are template fragments

Each extra is a `text/template` snippet that takes
`(profile, registry, loomBinary)` and produces a JSON or TOML
fragment. The `appendHookExtras` Go function becomes a template
loop:

```go
for _, extra := range profile.Hooks.Extras {
    tmpl := loadExtraTemplate(extra)   // pkg/generator/templates/extras/<name>.tmpl
    rendered, err := renderTemplate(tmpl, ctx)
    // ... merge rendered into hooks ...
}
```

### D6: Backwards compatibility — rip-and-replace per slice

No `// Deprecated:` annotations. Each CONFIG-N slice fully replaces its
target hand-written code path with template-driven equivalent + golden
tests. CI staticcheck stays green by removing the old code in the same
commit.

**Rationale**: bridge.* alias migration (commit `d97cf28b`) shows the
cost of keeping deprecated paths — caller migration drags. A clean
cut is cheaper given golden-file coverage.

### D7: Codex preamble is one template (preamble + hooks combined)

Codex's TOML preamble + hooks live in *the same TOML file*. Splitting
them across two templates would force string concatenation. One
template per platform output file.

### D8: OpenCode TS plugin stays Go-rendered (deferred)

Tail 6 (OpenCode TypeScript hook plugin) is excluded from EPIC 3.
Generating valid TypeScript with a Go template invites escaping bugs;
the OpenCode plugin is single-platform and rarely touched. Revisit
post-EPIC-3 if OpenCode usage grows.

### D9: Validator dispatch deferred

Tail 4 (validator dispatch by platform) is excluded from EPIC 3 v1.
Add an optional `schema_file:` field on the profile in v1 (no behavior
change), wire it up in a follow-up. Operators see no regression.

## Out of scope

- New platform support (windsurf, etc.) — intentionally not part of
  EPIC 3 itself; EPIC 3 *unblocks* future additions.
- Registry.yaml schema changes — this epic only touches
  `platform_profiles.yaml` and `pkg/generator/`.
- Generated-config validation rewrite — keeps existing
  `pkg/validator/` Claude+Gemini validators untouched.
- HUD config-management UI — out of scope; CLI sync is the only
  surface.

## Success criteria

- All 9 platforms in `platform_profiles.yaml` generate byte-identical
  configs before and after EPIC 3 (golden-file parity).
- `pkg/generator/configs_claude.go` shrinks to ~50 LOC (loader +
  thin glue), down from 325 LOC.
- `pkg/generator/configs_gemini.go` shrinks to ~30 LOC, down from 87
  LOC.
- `pkg/generator/configs_codex.go` shrinks to ~50 LOC, down from 230
  LOC.
- Adding a new policy ref requires editing one YAML file under
  `templates/policies/`. Verified by example: ship `secrets_scan`
  policy alongside CONFIG-3.
- Adding a new hook extra requires one template file + one YAML
  reference. Verified by example: ship `postToolUse_lint` template
  alongside CONFIG-2.

## Migration plan

| Slice | Removes | Adds | Goldens? |
|---|---|---|---|
| CONFIG-1 | `claudeHooksConfig`, `geminiHooksConfigFromRegistry`, hook dispatch switch | `templates/hooks/claude.json.tmpl`, `templates/hooks/gemini.json.tmpl`, `renderHooksConfig` | Yes — all existing hook goldens |
| CONFIG-2 | `claudePostToolUseExtras`, `claudePostToolUseTaskSyncHook`, `appendTelemetryEventEmitHooks`, dispatch in `appendHookExtras` | `templates/extras/*.tmpl` (3 files), `renderExtras` | Yes |
| CONFIG-3 | `gitopsFluxGuardrailPolicyFromRegistry`, `gitopsFluxGuardrailDenyRules`, `gitopsFluxGuardrailRegex` | `templates/policies/gitops_flux.yaml`, `LoadPolicy(name)` | Yes |
| CONFIG-4 | `emitCodexPreamble`, `codexNotifyCommand`, `emitCodexAgents` | `templates/hooks/codex.toml.tmpl` | Yes |

Each slice is independently shippable + revertible.

## Risk register

| Risk | Mitigation |
|---|---|
| Template rendering produces non-deterministic JSON output | Use `json.MarshalIndent` after template render; sort map keys |
| Codex preamble template has to handle both notify-only and notify+telemetry cases | Use `{{ if .TelemetryEnabled }}` blocks; cover both in goldens |
| Embedded templates are forgotten in `//go:embed` directive | CI test that walks `templates/` dir and asserts each file is loadable |
| Operators write a bad policy YAML and `loom sync` panics | Validate policy YAML on load with explicit error messages, not panic |
| Gold-file diffs in CI are huge and unreviewable | Each slice's MR description includes a 5-line summary of expected diffs (e.g. "whitespace only", "added field X") |

## Open product decisions

These need user input before CONFIG-1 starts:

- **OD1**: Should `templates/policies/*.yaml` files be checked into the
  loom-core repo or live in `platform/gitops/` (operator-owned)?
  Recommendation: **loom-core** for v1 — operators consume via `loom
  sync`. GitOps-owned policies are a future enhancement.
- **OD2**: Should we ship a new policy in CONFIG-3 (proving extensibility)
  or stop at parity with `gitops_flux`?
  Recommendation: **ship `secrets_scan` example** as a smoke test of the
  new schema. ~30 min added.
- **OD3**: Should EPIC 3 land in one big MR or 4 small MRs (one per CONFIG-N)?
  Recommendation: **4 small MRs**, one per slice, mirroring the EPIC 2
  Batch A/B/C/D pattern. Easier review.

## References

- Research: [.loom/106-research-reduce-config-complexity-2026-05-07.md](.loom/106-research-reduce-config-complexity-2026-05-07.md)
- Issue: [#67](https://gitlab.flexinfer.ai/services/loom-core/-/issues/67)
- Profile data: [`pkg/generator/platform_profiles.yaml`](pkg/generator/platform_profiles.yaml)
- Hook generation: [`pkg/generator/configs_hooks.go`](pkg/generator/configs_hooks.go)

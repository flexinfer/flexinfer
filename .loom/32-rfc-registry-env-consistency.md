# RFC: Registry and Env Consistency Hardening

Date: 2026-02-17
Status: Draft
Owner: loom-core

## Problem

Registry-driven env behavior is not fully consistent across runtime and CLI paths.

- Daemon startup merges default env aliases, but some non-daemon paths load the registry without that merge.
- Reload behavior risks drift between current daemon registry state and process expansion behavior.
- Diagnostics are useful but do not yet provide a complete, profile-specific view of unresolved template tokens and naming drift.
- Platform settings rely on flexible maps, which increases key drift and weakens validation.

This can produce hard-to-debug differences between `loomd`, `loom generate`, `loom sync`, `loom doctor`, and `loom check`.

## Goals

- Make env alias behavior deterministic across all registry load paths.
- Ensure runtime behavior after reload matches current registry state.
- Improve operator diagnostics for template/env issues before failures.
- Introduce stricter config conventions without breaking existing users.

## Non-Goals

- Breaking env var renames in a single release.
- Full registry schema redesign in one milestone.
- Immediate removal of legacy fallback names.

## Proposed Plan

### Phase 0: Alias Bootstrap Parity (Safe)

- Add one shared registry load helper that always merges default env aliases.
- Use it in all primary loom-core registry load callsites (daemon, sync/generate, check/doctor, manifests/config generation).
- Add tests proving parity for alias-backed resolution across CLI and daemon-adjacent paths.

Expected user impact:
- No breaking changes.
- Fewer "works in daemon but not in generator/check" cases.

### Phase 1: Reload Correctness

- Ensure daemon reload updates both:
  - active registry used for routing and tool metadata
  - process expansion behavior for env/template evaluation
- If needed, introduce explicit process manager API for re-binding runtime config cleanly.

Expected user impact:
- Reload becomes reliable for env alias and template changes.

### Phase 2: Diagnostics UX Hardening

- Extend `loom check`/`loom doctor` to include:
  - effective registry source and precedence explanation
  - unresolved template token checks by target profile
  - env convention warnings with direct fix commands

Expected user impact:
- Faster triage, fewer silent misconfigurations.

### Phase 3: Env Naming Convention Hardening

- Define canonical naming by concern class:
  - auth/token keys
  - timeout keys
  - response-size caps
  - kubeconfig/context keys
- Keep old names as compatibility aliases initially.
- Add lint/doctor warnings for non-canonical names.

Expected user impact:
- Better consistency without sudden breakage.

### Phase 4: Typed Platform Settings Evolution

- Incrementally replace ad-hoc `platform_permissions.*.settings` keys with typed fields.
- Keep map-based compatibility layer during transition.
- Warn on deprecated keys with explicit replacement guidance.

Expected user impact:
- Safer schema evolution and clearer validation errors.

## Rollout and Deprecation Policy

Policy for env aliases and setting keys:

1. Introduce
- New canonical key/field added.
- Legacy key remains supported via alias or compatibility mapping.
- Documentation updated with canonical name first.

2. Warn
- After at least one stable release cycle, emit warnings in diagnostics for legacy key usage.
- Warnings include exact replacement text.

3. Soft-enforce
- New examples/templates/config generation emit canonical names only.
- Legacy keys still accepted at runtime.

4. Remove
- Remove legacy acceptance only after:
  - minimum two stable release cycles of warnings
  - release notes and migration guidance published
  - replacement path verified in doctor/check output

## Risks

- Hidden dependencies on unmerged alias behavior in tests or scripts.
- Reload refactor may interact with process lifecycle under load.
- Overly strict diagnostics could create noise if not tuned by severity.

## Mitigations

- Keep Phase 0 narrowly scoped and behavior-preserving.
- Add focused tests for alias parity and reload semantics.
- Introduce diagnostics as warnings first, not hard failures.

## Acceptance Criteria

- Phase 0:
  - Shared helper exists and is used by all primary registry load callsites.
  - Alias parity test coverage added and passing.
- Phase 1:
  - Reload updates env/template expansion behavior deterministically.
  - Regression test demonstrates post-reload behavior matches new registry.
- Phase 2+:
  - `loom check`/`loom doctor` produce actionable, low-noise guidance.

## Implementation Notes

- Keep compatibility-first posture.
- Prefer additive helper APIs over broad behavioral rewrites.
- Land in small PR-sized slices to minimize operational risk.

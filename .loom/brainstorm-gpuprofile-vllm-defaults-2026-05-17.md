# Brainstorm: GPUProfile vLLM Defaults Feature Gap

**Date**: 2026-05-17
**Triggered by**: Continue the RALPH loop after the gfx906 tool-router row moved to conditional and choose the next feature gap deliberately.
**Constraints noted**: Keep the next slice small, source-backed, and aligned with the existing gfx1100/gfx906 roadmap. Preserve root checkout dirt and avoid reopening the completed qwen3-1p7b row.

## Phase 1 - Framings

### F1 - Promotion Evidence First

Treat the gap as missing evidence rather than missing code: run the next live canary, backfill the validation matrix, and leave implementation unchanged unless the canary fails. This keeps risk low and moves the promotion table forward.

- **Bet**: The next useful unlock is reviewer confidence, not runtime behavior.
- **Risk**: It may create another documentation-only loop while declared profile fields remain inert.

### F2 - GPUProfile Defaults Become Real

Treat the gap as a contract gap: GPUProfiles already declare vLLM-safe defaults, but controller and runtime load paths do not apply them. The smallest feature is to consume those defaults without overriding explicit Model config.

- **Bet**: The durable improvement is reducing repeated per-model config and making the profile the real arch contract.
- **Risk**: Applying defaults broadly can change serving behavior if a Model relied on omitted config.

### F3 - gfx906 vLLM Promotion Gate

Treat the gap as the next gfx906 revive step: the qwen3-1.7B vLLM canary manifest exists, so focus on the pass/fail gate and, only if it passes, promote `vllm.support` from experimental to full.

- **Bet**: The open question is not how defaults work, but whether the vLLM canary can actually serve coherently on Radeon VII.
- **Risk**: Live validation can be blocked by node pressure, image availability, or cold-start time rather than a code gap.

### F4 - Scheduler Truth Cleanup

Treat the gap as stale fallback truth: the legacy backend compatibility table still says vLLM on gfx906 is full support even though the GPUProfile marks it experimental. Align the fallback to avoid unsafe behavior on nodes without a cached profile.

- **Bet**: The biggest correctness risk is a no-profile path overclaiming support.
- **Risk**: The legacy table may still be intentionally optimistic for non-profile scheduler paths.

### F5 - Runtime Pause and Capacity

Treat the gap as operational capacity: before more code, verify the Radeon VII runtime DaemonSet, disk pressure, and image footprint are sustainable enough to run the vLLM canary.

- **Bet**: Feature work is premature if the node cannot reliably host the canary runtime.
- **Risk**: This drifts into platform operations and may not produce a clean FlexInfer MR.

### F6 - Operator Event Semantics

Treat the gap as operator feedback: experimental/canary backends should emit clearer reasons that include profile evidence and defaulted config, so a user can understand why vLLM on gfx906 is guarded.

- **Bet**: Better events make the revive path safer without changing runtime behavior.
- **Risk**: Events alone do not close the defaults or canary execution gap.

## Phase 2 - Cross-Pollinations & Tensions

### Combinations

- **F2 + F3**: Make GPUProfile defaults effective first, so the vLLM canary can rely on the same arch contract that a promoted model would use.
- **F2 + F6**: Apply profile defaults and keep explicit user config authoritative, then let existing BackendCanary and ExperimentalGPUSupport events explain the profile posture.

### Tensions

- **F1 vs. F2**: Evidence-only progress is safest, but it leaves a known schema-consumer gap. Code-first progress carries more behavior risk, but narrows future model manifests.
- **F3 vs. F5**: Running the canary answers the promotion question; capacity work makes the answer repeatable. The current repo-local slice should avoid platform drift unless live validation blocks.

## Phase 3 - Convergence

### Recommended: F2 + F3

Make GPUProfile vLLM defaults effective in the controller and runtime payload paths, with tests proving Model config wins. This is the smallest feature gap that directly supports the revived gfx906 canary while improving all profile-driven vLLM lanes.

### Runner-up: F4

Fallback compatibility cleanup would be the right choice if tests show the profile cache is bypassed in important deployment paths. It is narrower but less useful while the profile already exists for managed clusters.

### Open question

After this slice lands, should the next RALPH turn spend live time on the `qwen3-1.7b-vllm-radeonvii` smoke or keep reducing profile-contract drift first?

## Handoff

- If chosen -> next step is: `roadmap-spec-ralph-loop`
- Linked spec/plan doc: `.loom/ralph-gpuprofile-vllm-defaults-2026-05-17.md`

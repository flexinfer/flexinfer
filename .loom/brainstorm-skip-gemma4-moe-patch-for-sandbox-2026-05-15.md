# Brainstorm: skip Gemma4 MoE patch for vLLM 0.19 sandbox

**Date**: 2026-05-15
**Triggered by**: V1 sandbox canary still exited during init after Z1 startupProbe, Z2 prewarm, and the rms_norm patch; logs showed the legacy `vllm_gemma4_moe_gptq_patch.py` partially applied to vLLM 0.19.1.
**Constraints noted**: Keep production stable on V0, avoid another canary until the image is clean, preserve legacy production images that still need the 0.17-era Gemma4 patch, and make the sandbox rebuild unambiguously exercise upstream-native FusedMoE.

## Phase 1 — Framings

### F1 — Profile-level build skip

Add a `skip_gemma4_moe_patch` profile field, translate it to `SKIP_GEMMA4_MOE_PATCH`, and set it only on `gfx1100-sandbox-019`. This is the minimal continuation of Claude's branch and keeps the default behavior unchanged for legacy runtime images.

- **Bet**: The only dangerous path is the sandbox profile's build-time patch layer.
- **Risk**: The runtime entrypoint or a future profile can still run the patch after build, recreating the same partial-apply failure.

### F2 — Runtime env skip

Bake the skip flag into `/etc/flexinfer/runtime.env` and have `runtime-entrypoint.sh` respect it before invoking the patch script. This turns the profile choice into a startup-time invariant, so the pod does not self-corrupt after a clean build.

- **Bet**: The canary's actual failure path can happen at container init, not only Docker build time.
- **Risk**: A second Dockerfile or direct patch invocation can bypass the entrypoint.

### F3 — Version-aware patch script

Teach `vllm_gemma4_moe_gptq_patch.py` to detect installed vLLM and no-op on `>=0.19.0`, unless `FLEXINFER_FORCE_LEGACY_GEMMA4_MOE_PATCH=1` is set. This makes the patch script self-defending even when invoked from old Dockerfile layers, ad hoc shells, or runtime startup hooks.

- **Bet**: vLLM `0.19.0` is the real compatibility boundary because native Gemma4 FusedMoE landed there.
- **Risk**: An unusual source build could report a nonstandard version and fall through; the profile skip still covers the known sandbox.

### F4 — Remove runtime patching entirely

Delete the entrypoint's automatic Gemma4 patch invocation and rely only on build-time patching. Runtime mutation of installed Python packages is operationally spooky and makes pod startup nondeterministic.

- **Bet**: Every supported image is built through `build/build-runtime.sh` or CI, so build-time patching is sufficient.
- **Risk**: Legacy images that were intentionally left unpatched at build time may regress, and emergency pod-side patching disappears.

### F5 — Split legacy and native runtime families

Stop sharing one Dockerfile path for vLLM 0.17-patched and vLLM 0.19-native. Create distinct `runtime:gemma4-legacy` and `runtime:vllm019-native` images with separate patch scripts and entrypoints.

- **Bet**: The operational cost of duplicated build paths is lower than the cost of accidental cross-contamination.
- **Risk**: More images and CI jobs increase maintenance load, and this is too large for the current emergency branch.

### F6 — Make the patch atomic

Refactor the legacy patch script so it validates every required hunk before writing any file. If one required 0.17-era hunk is missing, the script exits without modifying the image at all.

- **Bet**: Partial application is the real failure mode, and atomicity prevents corrupt hybrid states across versions.
- **Risk**: This is more invasive than the current fix and may break legitimate "some hunks already upstreamed" behavior in the production image.

### F7 — Let canary evidence decide, not code inference

Do not change patch logic further; rebuild Claude's branch, run one more V1 canary, and only patch more if the canary fails. This keeps the branch tiny.

- **Bet**: The build-time skip is enough, and additional guardrails could introduce new variance.
- **Risk**: It ignores the entrypoint path visible in the repo and spends another risky canary cycle on a fix we can already see is incomplete.

## Phase 2 — Cross-Pollinations & Tensions

### Combinations

- **F1 + F2 + F3**: Profile intent flows from `runtime.yaml` to Docker build and container startup, while the script itself blocks unsafe direct invocation on vLLM 0.19+. This gives a narrow sandbox fix plus a general compatibility guard.
- **F3 + F6**: Version-aware skip handles the known 0.19 boundary; atomic patching would harden unknown future boundaries. Good follow-up, too much for the current emergency branch.

### Tensions

- **F1 vs. F3**: Per-profile control is explicit but easy to forget; script self-defense is implicit but protects every call path.
- **F4 vs. legacy production safety**: Removing runtime patching is cleaner architecture, but the live V0 path depends on conservative behavior. The real axis is operational cleanliness versus no-surprises production continuity.
- **F7 vs. repo evidence**: A canary is empirical, but another canary without fixing the visible entrypoint path is avoidable risk.

## Phase 3 — Convergence

### Recommended: F1 + F2 + F3

Use the existing branch's profile skip, extend it into the baked runtime env, teach the entrypoint to honor it, and make the patch script no-op on vLLM `>=0.19.0`. This is the smallest solution that closes both known invocation paths: Docker build and pod startup. It preserves the legacy default for production V0 images while making the sandbox image a clean upstream-native FusedMoE test.

### Runner-up: F3 + F6

If the V1 sandbox still fails after the runtime skip, the next move is to make the legacy patch script atomic and contract-tested against both a known 0.17 fixture and the 0.19 source layout. That would turn partial-apply from a runtime surprise into a build-time refusal, but it is more code than this branch needs before the next rebuild.

### Open question

Should the legacy runtime entrypoint patch remain enabled by default for V0 production images, or should a later cleanup make all Gemma4 Python patching build-time only?

## Handoff

- If chosen -> next step is: `small-change-loop`
- Linked spec/plan doc: `.loom/slice3-v1-sandbox-rms-norm-falsified-2026-05-15.md`

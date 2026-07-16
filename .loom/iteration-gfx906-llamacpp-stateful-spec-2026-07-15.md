# Rapid Dev Iteration Log: gfx906 Stateful llama.cpp + Local Speculation

## Scope

- Iteration goal: Retire the load-bearing assumption that the pinned Radeon VII llama.cpp lane can combine restart-persistent KV slots, two parallel slots, q4 KV cache, and local n-gram speculation without ROCm faults or throughput regression.
- Current blocker: Stateful slot persistence and local speculation were present in upstream llama.cpp `b8173`, but had not been exercised together on the live `gfx906` hardware/image/model combination.
- Hypothesis: The pinned shimmed image already contains the required server mechanisms; enabling `--slot-save-path` and `--spec-type ngram-simple` should work without another image build.

## Artifact Pinning

- Branch: `master`
- Files touched:
  - `deploy/debug/gfx906-llamacpp-stateful-spec-probe.yaml`
  - `.loom/iteration-gfx906-llamacpp-stateful-spec-2026-07-15.md`
- Build profile: llama.cpp ROCm, `AMDGPU_TARGETS=gfx906`, VMM unavailable, q4_0 K/V cache, flash attention enabled, two parallel slots.
- Image tag: `registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim`
- Image digest: `sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`
- Upstream ref/fork: llama.cpp `b8173`, runtime version commit `2e7e638`.
- Probe manifest: `deploy/debug/gfx906-llamacpp-stateful-spec-probe.yaml`
- Target node: `cblevins-radeonvii`
- Cache/storage path: `/var/lib/flexinfer/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`
- Model: Qwen3 8B Q4_K_M, 5,027,783,488 bytes.

## Change

- Narrow patch point: Add a suspended-by-default, digest-pinned standalone Job that launches llama-server sequentially in baseline and `ngram-simple` modes, drives the slot APIs, and asserts runtime evidence from the HTTP responses and server logs.
- Why this patch is the minimal test: No runtime image, controller, CRD, GPUProfile, or production Model manifest changed. The probe uses the exact previously soaked image and node-local model while varying only slot persistence and speculation flags.

## Probe

- Commands:
  - Apply the manifest with `spec.suspend: true`.
  - Suspend Flux owner `apps` first, then `flexinfer-models` and `flexinfer-system`.
  - Patch parent Models `bge-large-radeonvii` and `bge-reranker-radeonvii` to `spec.serverless.minReplicas: 0`; scale parent Deployment `pyannote-diarization` to zero.
  - Verify both Models are `Idle`, pyannote has no pod, and VRAM usage is 19 MB.
  - Patch the Job to `spec.suspend: false` and follow its logs to terminal state.
- Pod/job: Job `gfx906-llamacpp-stateful-spec-probe`; terminal pod `gfx906-llamacpp-stateful-spec-probe-ltdvj`.
- Confirmed image ID: `registry.harbor.lan/library/llamacpp@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`
- Expected success condition:
  - Save/restore a real slot across a complete server restart.
  - Complete requests concurrently in slots 0 and 1.
  - Accept nonzero local draft tokens.
  - Preserve the overlapping greedy token sequence, allowing at most one final draft batch of prefix-only underfill.
  - Reach at least 90% of baseline decode throughput.
  - Emit no HSA, aperture, GPU-fault, segfault, HIP error, or abort markers.

## Result

- Outcome: success
- Exact success evidence:
  - Runtime detected `AMD Radeon VII, gfx906:sramecc+:xnack- (0x906), VMM: no, Wave Size: 64` with `HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, and no `HSA_OVERRIDE_GFX_VERSION`.
  - Slot 1 saved 536 tokens to a 22,238,464-byte file. After a full llama-server restart, slot 0 restored all 536 tokens; the modified request reused 531 cached tokens and processed only 5 prompt tokens versus 536 cold.
  - Concurrent requests completed in slots 0 and 1 with 32 generated tokens each.
  - Baseline median decode: `71.69489341919295 tok/s`.
  - `ngram-simple` median decode: `214.2486511819942 tok/s`.
  - Speculative/baseline ratio: `2.9883390708080633` (2.99x).
  - Draft tokens: 528 generated, 528 accepted; acceptance ratio `1.0`.
  - All three runs within each arm produced one stable output hash. The speculative output was an exact 1,047-character prefix of baseline with no sequence divergence and a seven-word final-batch underfill, below `draft-max=16`.
  - Server-log fault scan passed for all three server lifecycles.
  - Terminal evidence: `PROBE_RESULT PASS`, pod phase `Succeeded`, exit code `0`.
- Relevant logs / stack frame:
  - Diagnostic iteration initially failed at `probe.py:371` with `AssertionError('greedy output changed when ngram-simple speculation was enabled')`.
  - Exact-output capture narrowed this to prefix-only length accounting: baseline 1,092 characters / 177 words; speculative 1,047 characters / 170 words; common prefix 1,047 characters.
  - The final gate therefore rejects any divergent token but treats up to one draft batch of prefix-only underfill as a known b8173 behavior.
- Restoration evidence:
  - Restored both Model parents to `minReplicas: 1` and pyannote to one replica.
  - `bge-large-radeonvii` and `bge-reranker-radeonvii` returned `Ready` with endpoints on ports 8000 and 8001; both `/health` calls returned `{"status":"ok"}`.
  - Pyannote returned `{"status":"ok","cuda":true,"device":"AMD Radeon VII"}`.
  - Flux `apps`, `flexinfer-models`, and `flexinfer-system` were resumed and `Ready=True` at their prior revisions.
- Operational finding:
  - Suspending only `flexinfer-models` and `flexinfer-system` was reverted by their parent `apps` Kustomization. The safe maintenance order is suspend `apps`, then child Kustomizations; restore workload specs, resume children, then resume `apps`.
  - Host-level `/dev/kfd` consumers do not claim `amd.com/gpu`, so device-plugin allocatable capacity is not an exclusivity signal.

## Next

1. Wire certificate-gated llama.cpp config for `slotSavePath`, `specType`, and draft limits through the FlexInfer backend/CRD surface, keeping speculation opt-in.
2. Expose the b8173 prefix-underfill caveat in the compatibility certificate and metrics; do not promise exact `max_tokens` filling with n-gram speculation.
3. Run protected long-form, tool-call, and non-repetitive workload arms before enabling speculation in a default gfx906 profile.

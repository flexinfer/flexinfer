# Rapid iteration: Qwen3.6-27B Fable GPTQ on gfx1100

## Scope

- Iteration goal: prove that the exact-source Fable Fusion W4/G128 artifact
  serves coherently on the shuffle-guarded FlexInfer gfx1100 vLLM runtime.
- Current blocker: structural GPTQ validation cannot prove live GDN/logit
  coherence on AMD; the artifact has not yet run on physical gfx1100.
- Hypothesis: the prior Qwen3.6 token corruption came from ROCm
  `gptq_shuffle`, so this full-GDN artifact will be coherent when loaded by the
  current shuffle-guarded runtime.

## Artifact pinning

- Branch: `codex/qwen36-fable-gfx1100-canary`
- Files touched: digest-pinned cache/Model activation plus temporary GPU-window
  and completed-build-window restoration manifests.
- Build profile: text-only GPTQ W4/G128, 128 x 1024 calibration.
- Image digest: `registry.harbor.lan/flexinfer/runtime@sha256:6ee8b3ed6bd0f80ba669f9a5a8525c9323592d1aa84fdf64b2a48f061fa4220e`
- Artifact digest: `sha256:285a044529f6321f954353c93c5a91f771cfdfc7445ad25af5d64b7926d83710`
- Upstream ref: `nightmedia/Qwen3.6-27B-Architect-Polaris2-Fable-B-F451@5ae530c3ab85033856e75cb1efc63fb1bf82a133`
- Probe manifest: `deploy/models/qwen36-27b-fable-gptq.yaml`
- Target node: `cblevins-5930k` (`gfx1100`)
- Cache/storage path: `pvc://qwen36-27b-fable-oci/qwen36-27b-fable`
- Model: `qwen36-27b-fable-gptq`

## Change

- Narrow patch point: enable only the digest-pinned pull cache and unique,
  demand-only Model; park the existing 5930k GPU leader for an isolated smoke.
- Why this is minimal: it changes no runtime image, quantization recipe,
  default alias, or production routing group.
- Isolation correction: `minReplicas: 0` alone did not park WAN because its
  explicit `warmPolicy: primary` still created a backend pod. Set that policy
  to `ondemand` for the canary window; restore both fields after the verdict.
- Proxy correction: the first cold-start request was rejected because the
  Gemma4 and Qwen3.5 text leaders still had `warmPolicy: primary` in the same
  `5930k-textgen` shared group. Temporarily make both demand-only as well; this
  changes no aliases, priorities, artifacts, or serving parameters.
- Runtime correction: the first scheduled pod inherited the generic gfx1100
  `vllm@sha256:a9b306...` profile image and exited before load because it does
  not recognize `--gdn-prefill-backend` or `--scheduler-reserve-full-isl`.
  Explicitly pin `spec.image` to the already-recorded Qwen3.6-capable unified
  `runtime@sha256:6ee8b3...` so image identity is no longer profile-dependent.
- Flag correction: the pinned unified runtime is vLLM 0.17 and also rejects
  those two newer CLI options. Omit only `gdnPrefillBackend` and
  `schedulerReserveFullISL`; retain the runtime's built-in ROCm GPTQ reference
  fallback, eager mode, text-only architecture override, and AIter disablement.

## Probe

- Commands: deterministic greedy direct/proxy completions, five prompt quality
  smoke, and a short throughput/VRAM snapshot.
- Pod/job: pending Flux reconciliation.
- Confirmed image ID: pending runtime pod.
- Expected success: stable English answers across repeated greedy prompts, no
  token salad, no engine errors, and VRAM fit below the 24 GiB card ceiling.

## Result

- Outcome: pending.
- Evidence: pending.

## Next

1. Reconcile the digest-pinned cache and wait for Ready.
2. Trigger the demand-only Model and verify image/artifact identity.
3. Run coherence/quality smokes, record the verdict, then restore the warm WAN
   leader regardless of outcome.

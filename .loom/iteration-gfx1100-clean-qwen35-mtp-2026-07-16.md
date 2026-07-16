# Rapid Dev Iteration Log: clean Qwen3.5 35B native MTP on gfx1100

## Riskiest assumption + kill-test

**Load-bearing assumption**: The exact clean Qwen3.5-35B-A3B GDN-preserving
GPTQ artifact and vLLM 0.23 ROCm runtime currently serving FlexInfer contain a
loadable native MTP head that fits one 24 GiB gfx1100 in graph mode with FP8 KV
at 32K, remains greedy-lossless, and produces a workload-stratified decode win.

**Kill test**: Run the suspended, digest-pinned Job
`deploy/debug/gfx1100-qwen35-mtp-kill-test.yaml` on
`cblevins-7900xtx`. It must first prove MTP config and weight keys exist, then
serve the same artifact sequentially with speculation off and with
`{"method":"qwen3_5_mtp","num_speculative_tokens":1}`. Require exact greedy parity on
three short strongly constrained continuations and at least 60% target-verified
draft acceptance. Across short Q/A, code, and a long-context summary, require at
least 1.08x median decode throughput with no workload below 0.95x, positive
graph-dispatch evidence, and no ROCm/HSA/OOM fault markers. This split is needed
because gfx1100 graph/MoE execution is not bit-stable across long baseline
generations. Confirm the pod image ID matches the pinned runtime digest before
accepting the result.

**Failure mode if the assumption is wrong**: FlexInfer would add certificate and
promotion machinery around an artifact whose MTP tensors were removed or whose
draft path either cannot fit the 24 GiB envelope, silently loads incorrect expert
weights, changes output, or costs more verification time than it saves.

**Status**: **FAILED 2026-07-16 for the plain-BF16 MTP artifact**. Attempt 1 proved the artifact contract
but rejected the probe's initial `gpuMemoryUtilization: 0.90`: baseline graph
capture left 0.03 GiB KV while 32K requires 0.35 GiB. Attempt 2 proved baseline
startup at the qualified `0.94` setting, then exposed a probe-only parity bug:
the hash included nondeterministic SSE chunk boundaries. Attempt 3 canonicalizes
the final reasoning/content streams before exact hashing, but still found that
full thinking/code completions are not bit-stable even within the baseline arm.
Attempt 4 narrows the candidate certificate to Qwen's explicit non-thinking
greedy lane while preserving exact hashes, but the long baseline code response
still diverges. Attempt 5 uses exact constrained-continuation parity plus
target-verified accepted-token counters, while keeping long responses solely as
stratified performance workloads. Attempt 5 reached baseline serving but its
five-token literal response tripped the performance helper's eight-token floor;
attempt 6 gives constrained probes their own one-token minimum. Attempt 6 then
proved the full baseline arm, but pinned vLLM 0.23 rejected the newer `mtp`
method alias. Attempt 7 uses the runtime-era `qwen3_5_mtp` spelling in MTP-only
mode before another full A/B. Attempt 7 exposed a vLLM 0.23/text-overlay config
gap. Plugin v0.2.0 repairs it; CI job 185922 published candidate digest
`sha256:1b35e3e83cfb4c68b34c06262943b2d0911725a56369609ad08233484bcec04b`.
Attempt 8 proved that config repair live, then failed inside the native MTP
expert loader with `KeyError: layers.0.mlp.experts.w2_weight`: target GPTQ was
incorrectly inherited by the intentionally plain MTP head. Plugin v0.3.0 now
prefix-scopes unquantized Linear/MoE methods to `mtp.*`; target GPTQ remains
unchanged. Attempt 9 proved that repair: all six shards loaded into the native
MTP class, then the engine failed while padding the unquantized expert weights.
It needed another 1.06 GiB with only 716 MiB free (23.03 GiB already allocated).
The load-bearing graph+32K/24-GiB fit assumption is therefore false, and no
certificate or full A/B may ship for the plain artifact. The next independent
kill-test is a surgically quantized MTP-expert artifact; it is not a relaxation
of this failed gate.

### Positive and disconfirming evidence

- Positive: vLLM's current MTP documentation defines the native-head contract
  and recommends `{"method":"mtp","num_speculative_tokens":1}` as the smallest
  online configuration:
  <https://github.com/vllm-project/vllm/blob/main/docs/features/speculative_decoding/mtp.md>.
- Positive: vLLM's current speculative config recognizes `qwen3_5_mtp` and maps
  Qwen3.5 MoE config to `Qwen3_5MoeMTP`:
  <https://github.com/vllm-project/vllm/blob/main/vllm/config/speculative.py>.
- Positive: upstream PR #39475 identifies the same plain-MTP versus inherited
  quantization mismatch and reports successful Qwen3.5 GPTQ MTP validation from
  two users. Its proposed fix scopes unquantized methods to the MTP prefix:
  <https://github.com/vllm-project/vllm/pull/39475>.
- Positive: the upstream BF16 Qwen3.5-35B-A3B config declares one native MTP
  layer and its safetensors index contains MTP weights:
  <https://huggingface.co/Qwen/Qwen3.5-35B-A3B/blob/main/config.json> and
  <https://huggingface.co/Qwen/Qwen3.5-35B-A3B/blob/main/model.safetensors.index.json>.
- Negative: the official Qwen3.5 recipe says MTP-1 on AMD GPUs remains under
  development and trades loaded throughput for low-concurrency TPOT:
  <https://github.com/vllm-project/recipes/blob/main/Qwen/Qwen3.5.md>.
- Negative: a current Qwen3.5 report shows that a model may initialize with MTP
  yet produce 0% acceptance, so successful startup is not the gate:
  <https://github.com/vllm-project/vllm/issues/36331>.
- Negative: upstream issue #36954 reproduces the exact
  `layers.0.mlp.experts.w2_weight` KeyError when a Qwen3.5 GPTQ checkpoint's
  plain MTP head inherits target MoE quantization:
  <https://github.com/vllm-project/vllm/issues/36954>.
- Negative: Qwen's official GPTQ artifact deliberately excludes MTP modules
  from quantization, while its compatibility discussion records 785 retained
  MTP tensors and a vLLM loader failure on quantized expert names. The local
  GDN-preserving quantizer excludes only linear-attention modules, so the probe
  reports both the MTP tensor suffixes and the artifact's dynamic quantization
  policy before startup:
  <https://huggingface.co/Qwen/Qwen3.5-35B-A3B-GPTQ-Int4/discussions/1>.
- Local history: BF16 MTP reached 82.4% acceptance but could not fit graph+32K;
  the surgically quantized draft later produced the existing approximately 9%
  win. See commits `da1952857` and `fdaeefba1` plus
  `deploy/models/qwen36-35b-mtp-uncensored-5930k.yaml`.

## Scope

- Iteration goal: retire or sharply narrow the clean-workhorse native-MTP
  assumption before any gfx1100 capability certificate ships.
- Current result: plugin v0.6.0 and the surgical MTP-expert GPTQ artifact pass
  eager load, execution, and an audited 80.28% acceptance gate. Graph mode now
  compiles both the target backbone and MTP head, but vLLM's default size-4
  capture leaves 0.18 GiB for a 0.42 GiB 32K KV-cache requirement.
- Successor hypothesis: converting exactly the 256 x 3 MTP expert matrices to
  symmetric GPTQ W4G128 while retaining MTP attention/fc linears in BF16 frees
  at least the missing 1.06 GiB and preserves useful draft acceptance. This is
  gated by `build/scripts/quantize_mtp_experts.py` plus the suspended builder
  `deploy/debug/gfx1100-qwen35-mtp-expert-quantize.yaml` before another live
  serving attempt.

## Artifact Pinning

- Branch: `codex/gfx1100-mtp-certificate`
- Files touched:
  - `deploy/debug/gfx1100-qwen35-mtp-kill-test.yaml`
  - `.loom/iteration-gfx1100-clean-qwen35-mtp-2026-07-16.md`
- Build profile: vLLM 0.23 ROCm Qwen3.5 text-only overlay, gfx1100, graph mode,
  GPTQ W4A16, FP8 E4M3 KV, one sequence, 32K context,
  `gpuMemoryUtilization: 0.96`, AITER disabled.
- Image tag: `registry.harbor.lan/flexinfer/vllm`
- Image digest:
  `sha256:f467e202987671b321e215142a7a6be7b910940c1323fced6243b573d27c8669`
- Upstream ref/fork: upstream vLLM 0.23 ROCm base plus
  `build/vllm-qwen35-text-plugin`.
- Probe manifest: `deploy/debug/gfx1100-qwen35-mtp-kill-test.yaml`
- Target node: `cblevins-7900xtx`, discrete gfx1100 ordinal 0.
- Cache/storage path:
  `/models/qwen35-35b-a3b-clean/gptq-w4-g128-gdn` on `llm-models-nfs`.
- Model: `Qwen/Qwen3.5-35B-A3B`, clean GDN-preserving GPTQ artifact.
- Artifact digest:
  `sha256:ed9cd20ffca20cdff54a581276c3b448aeb04fed234485995b308a10b360b6ba`.

## Change

- Narrow patch point: add one non-kustomized ConfigMap + suspended Job that
  performs artifact/runtime preflight and sequential baseline/MTP A/B testing.
- Why this patch is the minimal test: it changes no CRD, controller, GPUProfile,
  runtime image, production Model, or Flux-owned resource. A missing MTP head
  fails before vLLM startup; every later failure captures the exact server log.

## Probe

- Command(s): apply the manifest, confirm the target parent Model is idle, then
  unsuspend the parent Job and follow its logs. Record the pod `imageID` and
  terminal status.
- Pod/job: Job `gfx1100-qwen35-mtp-kill-test` in `flexinfer-system`.
- Confirmed image ID: pending live run.
- Expected success condition: `PROBE_RESULT PASS` with exact tuple, weight-key
  count, workload ratios, draft/accepted-token counters, graph evidence, and a
  clean fault scan.

## Result

- Outcome: in progress.
- Attempt 1 artifact evidence: one native MTP layer; 785 plain MTP weights; zero
  quantized MTP tensors; pinned runtime image ID confirmed.
- Attempt 1 failure: baseline startup rejected 32K because graph capture at
  `gpuMemoryUtilization: 0.90` left 0.03 GiB KV, below the required 0.35 GiB.
  This is a probe-tuple error; the production 128K sister's proven setting is
  `0.94`, so the next attempt changes only that value.
- Attempt 2 result: baseline reached `SERVER_READY` at 32K in graph mode. The
  first benchmark pass then found that SSE segmentation differed between
  otherwise greedy runs; hashing the chunk list made network framing part of
  the output contract. Attempt 3 hashes the canonical concatenated reasoning
  and content streams instead, preserving exact text parity.
- Attempt 3 result: baseline again reached `SERVER_READY`, but its canonical
  code transcripts still changed across repeated greedy requests. The failure
  predates MTP and therefore cannot detect speculative losslessness. Attempt 4
  explicitly sets `enable_thinking: false` and `top_p: 1`; any remaining
  mismatch logs all canonical transcripts before failing.
- Attempt 4 result: non-thinking baseline code still diverged across three
  graph-mode runs at normal code branch points. Exact full-response comparison
  is therefore not a valid losslessness oracle on this tuple. Attempt 5 requires
  repeatable exact parity within and across arms for three short constrained
  continuations, plus nonzero target-verified MTP acceptance; the longer
  workload suite gates performance only.
- Attempt 5 result: baseline reached `SERVER_READY`, then the first constrained
  literal correctly completed in five tokens and was rejected by the helper's
  performance-only eight-token minimum. Attempt 6 scopes that minimum to one
  token for parity probes; workload minimums remain unchanged.
- Attempt 6 result: baseline completed with stable constrained parity and
  medians of 78.98 tok/s short Q/A, 76.57 tok/s code, and 12.93 tok/s long
  context. MTP startup then failed immediately because pinned vLLM 0.23 does not
  recognize the newer `mtp` method alias. Attempt 7 uses the historical
  runtime-compatible method `qwen3_5_mtp` and MTP-only mode to isolate load,
  fit, acceptance, and graph execution before the next full A/B.
- Attempt 7 result: vLLM accepted `qwen3_5_mtp`, normalized it to `mtp`, then
  rejected the draft because its config override does not recognize FlexInfer's
  text-only `qwen3_5_moe_text` model type. The upstream override only handles
  `qwen3_5` and `qwen3_5_moe`. Plugin v0.2.0 adds the missing translation to
  `qwen3_5_mtp` with architecture `Qwen3_5MoeMTP`; its regression test fails
  without the hook and passes with it. CI job 185922 published the repaired
  digest-pinned overlay.
- Attempt 8 result: the v0.2.0 overlay reached `Qwen3_5MoeMTP.load_weights`,
  then failed after four of six shards with
  `KeyError: layers.0.mlp.experts.w2_weight`. The exact upstream root cause is
  quantized parameter registration (`w2_qweight`) for a checkpoint that stores
  the MTP head unquantized (`w2_weight`). Plugin v0.3.0 intercepts only
  AutoGPTQ `mtp.*` Linear and RoutedExperts prefixes and returns vLLM's native
  unquantized methods. A regression test proves MTP Linear/MoE exclusion and
  target-layer delegation. CI job 186124 published plugin v0.3.0 as digest
  `sha256:4511e86d655acfee68e37c9a06e189f23aa8b367cdf593fd97be713842b2d54b`.
- Attempt 9 result: the pinned v0.3.0 overlay attested plugin `0.3.0`, the exact
  runtime digest, 785 plain MTP weights, and zero quantized MTP tensors. The
  prior KeyError disappeared and vLLM constructed the native unquantized MTP
  experts. Startup then failed in
  `UnquantizedFusedMoEMethod._maybe_pad_weight` with `HIP out of memory`: a
  1.06 GiB allocation was requested with 716 MiB free on the 23.98 GiB device;
  PyTorch reported 23.03 GiB allocated and 32.31 MiB reserved. This is model
  residency, not a KV-cache or utilization knob failure. The probe resources
  were removed, Flux reconciliation was resumed, and
  `Model/wan21-t2v-1p3b-gfx1100` returned to `Ready` under its original primary
  warm policy.
- Attempt 10 result: the quantized artifact preflight passed and bound all four
  fused GPTQ tensors to contract digest `sha256:46d21a2b84818bd319bf774bd385ecd73eeeec4bdb5770df09cc9b8c8be0e78d`.
  Runtime startup then failed during plugin registration, before model load,
  because v0.4 patched `load_fused_expert_weights` on outer class
  `Qwen3_5MoeMTP`. In vLLM 0.23 that method lives on the inner
  `Qwen3_5MultiTokenPredictor`. Plugin v0.5 patches the real owner; its fake
  runtime contract now mirrors that upstream class topology. The next probe is
  explicitly `PROBE_MODE=mtp-only` so fit and acceptance precede a full A/B.
- Attempt 11 result: the pinned v0.5.0 overlay attested runtime digest
  `sha256:850d1548199ba6ec428983b8235062b7a354812be1776328aa5f1d3faf68281a`,
  passed the quantized-artifact preflight, registered the inner predictor
  repair, and loaded far enough to hold about 20.35 GiB on the active gfx1100.
  That crosses the residency boundary that failed at 23.03 GiB allocated plus
  a 1.06 GiB request for plain BF16 MTP experts. Startup then failed without an
  OOM during Dynamo fake-tensor compilation: a three-axis MRoPE position tensor
  reached generic `RotaryEmbedding.forward_static`, which flattened it to
  `3*N` before a `[3*N, -1, 256]` query reshape. The target text model receives
  an explicit non-MRoPE `--hf-overrides` block while the draft model reloads the
  artifact's raw `mrope_section`; this graph/config boundary is now the leading
  hypothesis. Production was restored through the parent `Model`, and
  `flexinfer-models` returned Ready at its original revision. Attempt 12 runs
  the same pinned tuple in explicit eager diagnostic mode to distinguish model
  execution from Dynamo lowering; eager success is evidence, not a graph-mode
  certificate.
- Attempt 12 result: eager mode disabled CUDAGraph and loaded the six shards in
  1.05 seconds after NFS page-cache warmup; vLLM reported 19.97 GiB of model
  memory and 90.02 seconds total load time. It then failed during the draft
  profile run in native HIP RoPE, not Dynamo:
  `qwen3_5_mtp.py -> qwen3_next.py -> RotaryEmbedding.forward_hip` rejected
  query/key and positions with different batch/sequence dimensions. This proves
  the draft runner is producing three-axis MRoPE positions while its text-only
  Qwen3Next attention owns generic RoPE. Plugin v0.6 now overwrites the draft
  `rope_parameters` with the exact non-MRoPE target contract and has a
  regression that starts from the artifact's raw `mrope_section`. The probe
  Job and ConfigMap were removed, the parent `Model` fields and Flux
  reconciliation were restored, and the production pod returned Ready after
  kubelet completed its normal digest-pinned image pull.
- Attempt 13 result: the pinned v0.6 runtime passed artifact/plugin/image
  preflight, crossed the prior native-HIP RoPE profile run, reached
  `SERVER_READY`, completed all eager MTP workloads, and emitted nonzero draft
  and accepted-token counters. The first result reported 1,604 accepted for 999
  drafts because the probe's fragment matcher summed both the canonical
  accepted-total counter and an identical per-position counter (and also
  selected `_created` timestamps). Auditing the recorded canonical totals gives
  802 accepted of 999 drafts, or 80.28%, above the 60% gate. The probe now
  selects only exact canonical total-counter names before graph mode. Eager
  throughput evidence was 41.03 tok/s for code, 41.01 tok/s for short QA, and
  24.98 tok/s for the long-context summary; it remains explicitly
  non-certifying. Production was restored and Ready after the parent Job and
  ConfigMap were removed.
- Attempt 14 result: graph mode attested the same immutable image, plugin, and
  artifact contract; loaded the model at 19.97 GiB; compiled the target
  backbone in 64.96 seconds; and compiled the MTP `eagle_head` in 14.02
  seconds. This crosses both prior RoPE failures and proves the repaired graph
  is compilable on gfx1100. Startup then rejected KV-cache initialization:
  32K requires 0.42 GiB, but the default candidate sizes `[1, 2, 4]` retained
  an unused size-4 graph and left only 0.18 GiB (estimated maximum context
  10,880). This is a deterministic
  capacity guard, not an OOM or HIP fault; peak sampled VRAM was 22.709 GiB.
  With one speculative token, the actual one-request target verification shape
  is two tokens. Attempt 15 pins `--cudagraph-capture-sizes 2`, removing the
  unused size-4 graph while preserving the exact graph path under test. Its
  kill-test is at least 0.24 GiB recovered plus graph evidence at 32K. The
  failed Job and ConfigMap were removed, Flux resumed, and the production
  parent Model returned Ready with its original policy.
- Attempt 15 result: vLLM accepted the explicit size-2 graph contract and
  reported `cudagraph_capture_sizes: [2]` plus
  `max_cudagraph_capture_size: 2`. It then reproduced Attempt 14 exactly:
  19.97 GiB model load, successful backbone and `eagle_head` compilation, and
  0.18 GiB available versus 0.42 GiB required for 32K. This falsifies the
  assumption that unused capture candidates consume the missing budget: vLLM
  profiles non-KV memory and validates KV capacity before graph capture. The
  production parent Model was restored Ready before the next change. Attempt
  16 retains the correct size-2 contract and raises utilization from 0.94 to
  0.96, adding about 0.48 GiB of allocator budget against the measured 0.24
  GiB shortfall while preserving 32K and roughly 0.96 GiB of physical-device
  headroom.
- Attempt 16 result: utilization 0.96 crossed the 32K KV-capacity guard and
  reached `SERVER_READY` in graph mode. The complete workload suite produced
  1,003 draft tokens with 800 accepted (79.76%), stable constrained parity,
  and medians of 94.23 tok/s code, 91.80 tok/s short QA, and 27.44 tok/s long
  context. The server exited cleanly with rc=0, but the probe rejected that
  evidence because vLLM logs its normal engine-client shutdown as
  `mode=abort timeout=0s`; the broad fault regex treated the lifecycle label as
  a crash. Attempt 17 excludes only that exact `[shutdown]` message while
  retaining all other abort, ROCm, HSA, OOM, and segmentation-fault matches,
  then reruns the unchanged inference tuple for a machine-emitted graph pass.
- Attempt 17 result: the immutable tuple emitted `MTP_ONLY_RESULT PASS` and the
  Job Succeeded. vLLM captured one PIECEWISE mixed prefill/decode graph and one
  FULL decode graph at size 2, served the full 32K envelope, and passed the
  corrected fault scan. It produced 1,011 audited draft tokens with 791
  accepted (78.24%); constrained parity hashes were stable; workload medians
  were 95.08 tok/s code, 92.43 tok/s short QA, and 27.66 tok/s long context.
  Before the full A/B, the baseline capture is corrected to size 1 while MTP
  retains size 2 so the control is not padded into the speculative verify
  shape. The existing host cache mount is also bound through `VLLM_CACHE_ROOT`
  so graph artifacts survive arm/process restarts without changing execution
  semantics.
- Attempt 18 result: the full A/B completed both arms with rc=0 and passed all
  inference gates before log classification. Baseline medians were 76.35
  tok/s code, 78.71 tok/s short QA, and 12.95 tok/s long context; MTP medians
  were 93.81, 93.22, and 27.89 tok/s respectively. Ratios were 1.229x, 1.184x,
  and 2.154x (median 1.229x), while 796 of 1,007 drafts were accepted (79.05%)
  and every constrained parity hash matched. The remaining rejection came from
  a second normal vLLM lifecycle message:
  `[shutdown] EngineCore: start mode=abort timeout=0s`. Attempt 19 generalizes
  the exemption only to `[shutdown] ... mode=abort timeout=0s`; untagged
  aborts, nonzero-timeout aborts, and every ROCm/HSA/OOM/fault marker remain
  fatal. Both arm-specific compilation caches now persist on the target host.
- Attempt 19 result: both arms loaded from the persistent compilation cache,
  but the old single 128-token warmup no longer covered the device/kernel
  ramp previously hidden by 70+ seconds of compiler activity. Baseline was
  stable at 76.83 tok/s code, 79.28 tok/s short QA, and 12.93 tok/s long
  context. MTP short QA remained near 63 tok/s, then code ramped from 66.86 to
  74.30 to 93.62 tok/s, and long context stabilized at 28.42 tok/s. Parity
  still matched and 797 of 1,006 drafts were accepted (79.22%). This is a
  reproducible cold-measurement defect, not grounds to relax the 0.95x floor.
  Attempt 20 applies a fixed, symmetric six-run/768-token untimed warmup before
  either arm's metrics and timings; it never adapts to observed throughput.

## Successor artifact gate

- Builder: `deploy/debug/gfx1100-qwen35-mtp-expert-quantize.yaml`, suspended by
  default and not included by any kustomization.
- Builder image: the first overlay exposed a non-contiguous fused-scale tensor
  at safetensors publication and failed before the atomic output rename. The
  regression-tested copy-only overlay from CI job 186429 pins the corrected
  script on the same proven gfx906 quantizer base as
  `sha256:cb7394a155381ac55143425759f1b6d9f181c5e42939527ef4a0f124d1b9cf32`.
- Surgery: require exactly one MTP layer and all 768 plain routed-expert
  matrices; quantize each matrix with symmetric RTN W4G128; fuse the result into
  exactly four vLLM GPTQ tensors; retain all MTP linears and every target-model
  tensor byte-for-byte.
- Publication safety: create a hard-linked sibling staging tree, rewrite only
  MTP-bearing shards with replace-on-write, enforce relative-L1 <= 0.18 and
  per-matrix cosine >= 0.97, verify source metadata hashes remain unchanged,
  then atomically rename the staging directory.
- Runtime contract: plugin v0.5 keeps MTP linears unquantized but delegates MTP
  routed experts to AutoGPTQ only when
  `FLEXINFER_QWEN35_MTP_EXPERTS_GPTQ=1`; the default remains the plain upstream
  artifact behavior. It also applies the GPTQ fused-expert name repair to the
  inner native `Qwen3_5MultiTokenPredictor` loader.
- Artifact result: builder Job `gfx1100-qwen35-mtp-expert-quantize` completed
  successfully with contract digest
  `sha256:46d21a2b84818bd319bf774bd385ecd73eeeec4bdb5770df09cc9b8c8be0e78d`.
  It replaced all 768 plain expert matrices with exactly four fused GPTQ
  tensors, reducing expert storage from 1,610,612,736 to 415,236,096 bytes
  (1,195,376,640 bytes freed, 3.879x compression). Aggregate relative L1 was
  0.13244 and the minimum per-matrix cosine was 0.984898, passing both quality
  gates. Source index and quantization-config hashes remained bound in the
  publication marker.
- Runtime evidence: CI job 186554 published plugin v0.5.0 as immutable digest
  `sha256:850d1548199ba6ec428983b8235062b7a354812be1776328aa5f1d3faf68281a`.
- Runtime candidate: plugin v0.6 aligns the speculative draft's RoPE config
  with the target text-only override. CI job 186804 published it as immutable
  digest
  `sha256:f467e202987671b321e215142a7a6be7b910940c1323fced6243b573d27c8669`;
  the first publish was canceled during a BuildKit rollout and one retry stalled
  during cold-cache extraction, while the fresh MR pipeline completed from the
  exact `8b9e20ed` source SHA.
- Kill-test: the output marker must prove at least 1.06 GiB was freed before the
  existing MTP-only graph+32K load/acceptance probe is repointed. If fit passes,
  require >=60% acceptance before returning to the full performance A/B.

## Next

1. Re-run the full cached A/B with the fixed 768-token warmup and require the
   machine-emitted `PROBE_RESULT PASS` certificate.

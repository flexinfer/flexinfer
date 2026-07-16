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

**Status**: live iteration in progress. Attempt 1 proved the artifact contract
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
unchanged. Downstream certificate work remains blocked until the full A/B
contract passes.

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
- Current blocker: plugin v0.3.0's prefix-scoped AutoGPTQ exclusion has not yet
  been exercised in the exact ROCm runtime.
- Hypothesis: forcing only `mtp.*` Linear and RoutedExperts modules through
  vLLM's unquantized methods resolves the plain-weight loader mismatch without
  changing target-model GPTQ or exceeding the 24 GiB graph+32K envelope.

## Artifact Pinning

- Branch: `codex/gfx1100-mtp-certificate`
- Files touched:
  - `deploy/debug/gfx1100-qwen35-mtp-kill-test.yaml`
  - `.loom/iteration-gfx1100-clean-qwen35-mtp-2026-07-16.md`
- Build profile: vLLM 0.23 ROCm Qwen3.5 text-only overlay, gfx1100, graph mode,
  GPTQ W4A16, FP8 E4M3 KV, one sequence, 32K context,
  `gpuMemoryUtilization: 0.94`, AITER disabled.
- Image tag: `registry.harbor.lan/flexinfer/vllm`
- Image digest:
  `sha256:4511e86d655acfee68e37c9a06e189f23aa8b367cdf593fd97be713842b2d54b`
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

## Next

1. Run the pinned v0.3.0 digest in MTP-only mode on the 7900xtx lane.
2. If the next failure is a plain-weight shape/name mismatch, patch only the
   Qwen3.5 MTP loader and retain the target GPTQ contract.
3. If MTP-only passes, run the full baseline/MTP A/B. Add certificate gating
   only after its parity, acceptance, graph-mode, and performance contract
   passes.

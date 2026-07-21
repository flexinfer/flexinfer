# Rapid Dev Iteration Log: Fable 27B gfx1100 RP replacement

## Scope

- Iteration goal: Qualify the exact Fable 27B W4/G128 artifact as a warm,
  thinking-off successor to `qwen35-9b-ablit-rp` on one RX 7900 XTX.
- Current blocker: The qualified Fable result covers only 8K eager/FP16-KV,
  one sequence, and demand-only placement on the sister card. Graph mode, 32K
  fit/recall, same-card behavior, and RP complete-answer latency are unproven.
- Hypothesis: The immutable vLLM 0.23 runtime can first reproduce the qualified
  8K result on `cblevins-7900xtx`, then serve 32K graph/FP8-KV with at least a
  15% median complete-answer latency improvement over a matched 32K eager
  control without recall, parity, or runtime faults.

## Artifact Pinning

- Branch: `codex/fable-rp-optimization-research`
- Files touched:
  - `deploy/modelcaches/qwen36-27b-fable-oci-7900xtx.yaml`
  - `deploy/tasks/model-eval-gauntlet/fable-rp-performance-cronjob.yaml`
  - `deploy/experiments/qwen36-27b-fable-rp-canary.yaml`
- Build profile: vLLM 0.23, text-only dense Qwen3.5, native RDNA3 W4A16,
  AITER disabled.
- Image tag: `registry.harbor.lan/flexinfer/runtime`
- Image digest: `sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`
- Upstream ref/fork: vLLM 0.23 plus `build/vllm-qwen35-text-plugin` v0.7.0.
- Probe manifest: `deploy/experiments/qwen36-27b-fable-rp-canary.yaml`
- Target node: `cblevins-7900xtx`, discrete gfx1100 ordinal 0.
- Cache/storage path: `qwen36-27b-fable-oci-7900xtx:qwen36-27b-fable`.
- Model: `nightmedia/Qwen3.6-27B-Architect-Polaris2-Fable-B-F451` exact-source
  GPTQ W4/G128.
- Artifact digest: `sha256:285a044529f6321f954353c93c5a91f771cfdfc7445ad25af5d64b7926d83710`.

## Change

- Narrow patch point: Add a digest-pinned artifact pull cache on the comparison
  node, a synthetic direct-service RP performance gauntlet, and one bounded
  `ModelExperiment` generation matching the qualified 8K control.
- Why this patch is the minimal test: The experiment controller owns and
  deletes the candidate, strips public aliases, records a typed verdict, and
  restores the existing 9B shared-group parent after the Job. Generation 1
  changes placement only; later generations change context and execution mode
  separately.

## Probe

- Command(s): Merge through GitOps, observe `ModelCache` readiness, capture the
  candidate pod `imageID`, then follow the retained gauntlet Job logs.
- Pod/job: `ModelExperiment/qwen36-27b-fable-rp-canary`; child names recorded
  in status.
- Confirmed image ID: `registry.harbor.lan/flexinfer/runtime@sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`.
- Expected success condition: exact literal response, zero reasoning tokens,
  coherent synthetic RP continuations, median decode at least 7.5 tok/s, p95
  TTFT at most 30s, no restart or ROCm/HSA/NaN/OOM markers, typed PASS verdict,
  and automatic restoration of `Model/qwen35-9b-ablit-rp` to Ready.

## Result

### Generation 1 — 8K eager/FP16-KV sentinel

- Outcome: success.
- Flux revision: `master@sha1:35ddefb1294c6239051e34bd67532a5c0b23f8d8`.
- Typed verdict: `Succeeded`, `pass=true`, Job
  `qwen36-27b-fable-rp-canary-gauntlet`.
- Runtime proof: vLLM 0.23, plugin registration reported 11 constrained FLA
  autotuners, and `AutoGPTQLinearMethod` selected
  `RDNA3W4A16LinearKernel`. The candidate pod used the pinned image digest
  above and restarted zero times.
- Thinking-off proof: every request reported `reasoning_chars=0`; the literal
  warmup returned exactly `ROCM_FABLE_OK`.
- RP dialogue: 192 tokens at 31.6611 and 31.3775 tok/s, with 0.1942s and
  0.1800s TTFT.
- 2,278-token multi-turn scene: 192 and 168 output tokens at 20.8414 and
  20.8205 tok/s, with 2.1753s and 1.8934s TTFT.
- Summary: median decode 26.1095 tok/s, p95 TTFT 2.1753s, no gauntlet
  failures. vLLM's own histogram recorded 745 inter-token observations in
  29.47099s (25.28 tok/s aggregate over the complete probe).
- Cleanup proof: the experiment controller deleted the candidate after the
  verdict and `Model/qwen35-9b-ablit-rp` returned to `Ready` through its parent
  CR without a direct scale or pod operation.

### Generation 2 — 32K eager/FP16-KV control

- Outcome: success.
- Change from generation 1: `maxModelLen` 8,192 -> 32,768,
  `maxInputTokens` 6,144 -> 28,672, and the gauntlet adds the 18K synthetic
  passphrase-recall workload. Runtime, dtype, KV dtype, eager mode, scheduler,
  artifact, image, and one-sequence posture are unchanged.
- Runtime fit: 16.68 GiB model residency, 3.93 GiB available KV, 62,066 GPU
  KV tokens, and 1.89x maximum concurrency at 32,768 tokens. The pod used the
  pinned image, selected `RDNA3W4A16LinearKernel`, and restarted zero times.
- RP dialogue: 192 tokens at 31.2434, 31.6134, and 31.8501 tok/s; median
  complete-answer latency 6.2241s and TTFT about 0.19s.
- 2,278-token multi-turn scene: 192, 192, and 168 output tokens at 19.7096,
  20.7970, and 20.8414 tok/s; median complete-answer latency 11.0635s and
  median TTFT 1.8850s.
- Long-context recall: all three 19,841-token prompts returned exactly
  `CINNABAR-48271`; median complete-answer latency 23.1980s and median TTFT
  21.3891s.
- Summary: median non-long-context decode 26.0424 tok/s, p95 TTFT 21.4266s,
  zero reasoning characters and no gauntlet failures. vLLM recorded 1,157
  inter-token observations in 50.65660s across the complete probe.
- Cleanup proof: typed PASS verdict and the 9B parent returned to `Ready`.

### Generation 3 — 32K graph/FP8-KV candidate

- Outcome: rejected by the relative kill gate. The typed gauntlet verdict was
  `Succeeded` because parity, recall, and absolute safety floors passed, but it
  is not a promotion verdict.
- Change from generation 2: `kvCacheDtype=fp8_e4m3`, `enforceEager=false`,
  graph capture bounded to shape one, GDN prefill pinned to Triton, and graph/KV
  metrics enabled at full sampling. Artifact, dtype, context, one-sequence
  scheduler, prompts, and fixed KV scales are unchanged.
- Runtime proof: the native `RDNA3W4A16LinearKernel` remained active, Dynamo
  compile took 7.87s (54.60s total compile), PIECEWISE mixed prefill/decode and
  FULL shape-one decode graphs were captured, and graph capture consumed
  0.32 GiB. The pod restarted zero times and emitted no ROCm/HSA/OOM/NaN fault.
- Capacity regression: only 1.52 GiB remained for KV, yielding 46,173 GPU KV
  tokens and 1.41x maximum concurrency at 32K, versus 3.93 GiB, 62,066 tokens,
  and 1.89x for eager/FP16-KV.
- RP dialogue: median decode 38.5112 tok/s and median complete-answer latency
  5.1362s, respectively 21.8% faster decode and 17.5% lower latency than the
  eager/FP16-KV control.
- 2,278-token multi-turn scene: median decode 17.4231 tok/s and median latency
  12.3625s, respectively 16.2% lower throughput and 11.7% higher latency than
  control. This breaches the 0.95 per-workload throughput floor.
- Long-context recall: all three 19,841-token prompts still returned exactly
  `CINNABAR-48271`, but median decode fell to 3.2216 tok/s and median latency
  rose to 29.9428s, respectively 41.7% lower throughput and 29.1% higher
  latency. Median TTFT was 26.8388s and p95 TTFT was 27.6322s.
- Summary: median non-long-context decode was 27.9436 tok/s, all requests kept
  `reasoning_chars=0`, and vLLM recorded 1,141 inter-token observations in
  54.91508s. The aggregate median hides the prompt-length regressions and is
  therefore not sufficient for promotion.

### Generation 4 — 32K graph/FP16-KV attribution

- Outcome: pending.
- Change from generation 3: restore `kvCacheDtype=auto` only. Graph mode,
  capture shape, context, scheduler, artifact, image, prompts, and metrics are
  unchanged.
- Purpose: determine whether FP8 KV caused the multi-turn, long-context, and KV
  capacity regressions while preserving bounded graph mode's short-dialogue
  gain.

## Next

1. Run generation 4 with bounded graphs and FP16 KV.
2. Compare its per-workload latency, TTFT, decode, recall, KV capacity, and
   faults with both generation 2 and generation 3.
3. Attempt concurrency only if a single-sequence tuple meets the 15% latency
   improvement target without breaching the 0.95 per-workload throughput floor.

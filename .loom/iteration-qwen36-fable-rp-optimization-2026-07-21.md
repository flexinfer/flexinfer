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
- Confirmed image ID: pending.
- Expected success condition: exact literal response, zero reasoning tokens,
  coherent synthetic RP continuations, median decode at least 7.5 tok/s, p95
  TTFT at most 30s, no restart or ROCm/HSA/NaN/OOM markers, typed PASS verdict,
  and automatic restoration of `Model/qwen35-9b-ablit-rp` to Ready.

## Result

- Outcome: pending.
- Exact failure or success evidence: pending.
- Relevant logs / stack frame: pending.

## Next

1. If the 8K sentinel passes, change only context to 32K while retaining eager
   mode and FP16 KV.
2. If the 32K control passes, change only execution/KV to bounded graph and
   FP8 E4M3 using fixed scales; dynamic warmup scale calculation is unsafe for
   this vLLM 0.23 hybrid recurrent path.
3. Compare complete-answer latency, TTFT, decode, recall, graph evidence, VRAM,
   and fault logs before attempting two/four-session scheduling.

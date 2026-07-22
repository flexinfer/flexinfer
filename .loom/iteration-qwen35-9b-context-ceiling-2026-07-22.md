# Rapid Dev Iteration Log: Qwen3.5-9B RP context-ceiling bisection

## Scope

- Iteration goal: determine whether the certified GPTQ + rank-64 RP lane has a
  usable recall window near 192K, between the 131K pass and 245K failure.
- Current blocker: native metadata advertises 262,144 tokens, but this exact
  abliterated W4/G128 artifact recalled 5/5 at 127,969 prompt tokens and 0/1 at
  roughly 245K. Metadata and coherent generation are not a recall certificate.
- Hypothesis: a 191K-192K five-depth prompt still recalls all needles on the
  exact production runtime and preserves the existing short/concurrent floors.

## Riskiest assumption + kill-test

**Load-bearing assumption**: the repaired Qwen3.5-9B W4/G128 model with its
rank-64 `nsfw-rp` adapter preserves exact depth-distributed recall near 192K on
the pinned vLLM 0.23 gfx1100 profile; raising only `maxModelLen` does not create
a nominal-but-unusable window.

**Kill test**: boot an owned candidate at `maxModelLen=196608`, prove the exact
runtime digest, native W4A16 kernel, GDN backend, graph sizes, FP16 KV, and
adapter. Require 5/5 exact recall from a calibrated prompt within two percent
of 192,000 tokens, no thinking leakage, the unchanged base/LoRA/concurrency
floors, zero restarts or runtime faults, and automatic restoration of the
production parent.

**Failure mode if the assumption is wrong**: the request misses one or more
needles, cannot reserve enough KV, stalls inside the nominal model length, or
regresses the certified RP lane. Keep production at 131,072 and use the failed
point as the upper half of the next bisection interval.

**Positive evidence**: the official
[Qwen3.5-9B model card](https://huggingface.co/Qwen/Qwen3.5-9B) states a native
262,144-token context and documents text-only vLLM serving at that ceiling.

**Disconfirming evidence**: the same model card warns operators to reduce the
window when memory is insufficient; vLLM has also documented a
[scheduler failure mode](https://github.com/vllm-project/vllm/issues/39734)
when a request is within `max_model_len` but exceeds real KV capacity. Most
importantly, this exact artifact already failed recall near 245K.

**Status**: 192K passed; 224K passed twice and is ready for promotion

## Artifact Pinning

- Branch: `codex/qwen35-9b-context-ceiling-192k`.
- Runtime digest:
  `sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`.
- Model: repaired abliterated Qwen3.5-9B GPTQ W4/G128.
- Adapter: `mirazrafi/NSFW-RP-RolePlay-LoRA-Qwen-3.5-9B`, rank 64.
- Target: `cblevins-7900xtx`, gfx1100 ordinal zero.
- Probe manifest:
  `deploy/experiments/qwen35-9b-context-ceiling-192k-canary.yaml`.
- Compile cache:
  `/var/lib/flexinfer/compile-cache-qwen35-9b-context-192k-v1`.

## Change

- Narrow patch point: clone the generation-4 candidate and change only the
  serving/input ceiling from 131,072/130,048 to 196,608/195,584; set the
  retained long-context workload target from 128,000 to 192,000.
- Why this is minimal: execution, quantization, adapter, KV dtype, graphs,
  batching, thinking default, artifact, image, and performance gates remain
  identical to the production certificate.

## Probe

- Local: render experiment and task Kustomizations, validate YAML, compile the
  embedded Python gauntlet, and run repository runtime-contract checks.
- Live: typed `ModelExperiment` result plus retained pod logs and metrics.
- Expected success: `Succeeded/GauntletPassed`, 5/5 recall near 192K, no
  performance failure, exact digest, zero restarts, and parent restored Ready.

## Result

- Outcome: 192K passed (`Succeeded/GauntletPassed`, generation 3).
- Recall: 5/5 exact needles at 191,959 prompt tokens; no missing needles.
- Long request: 249.253-second TTFT, 299.240 seconds elapsed.
- Short performance: 102.481 base decode tok/s; 59.681 LoRA dialogue
  median tok/s; 40.379 LoRA multi-turn median tok/s; 0.582 LoRA/base ratio;
  0.593-second short p95 TTFT.
- Concurrency: 56.352 aggregate tok/s median.
- Runtime: exact pinned digest, native `RDNA3W4A16LinearKernel`, Triton/FLA
  GDN, FP16 KV, graph sizes `[1,2,4]`, and zero candidate restarts.
- Capacity: 391,320 KV tokens, 1.99x maximum full-window concurrency at the
  196,608-token serving ceiling.
- Thinking behavior: default base and LoRA calls remained non-thinking; the
  explicit `enable_thinking=true` override still passed.
- Recovery: production returned `Ready`; the rank-64 adapter returned
  `Loaded 1/1`.

The first attempt was interrupted by an orchestration-only generation change:
the experiment controller materialized `repeatAfter` and cache defaults, and a
later Flux apply caused the controller to replace the candidate during prefill.
The stable generation-3 rerun produced the passing evidence above. New arms
make those defaults explicit so this cannot masquerade as a recall failure.

## 224K upper-half arm

- Hypothesis: the exact passing profile retains 5/5 recall near 224K without
  exhausting the measured 391,320-token KV capacity.
- Single model variable: raise `maxModelLen` from 196,608 to 229,376 and
  `maxInputTokens` from 195,584 to 228,352; move the retained long prompt from
  192,000 to 224,000 tokens. All runtime, quantization, adapter, execution, and
  performance gates remain unchanged.
- Kill test: require `Succeeded/GauntletPassed`, 5/5 exact depth-distributed
  recall within two percent of 224,000 tokens, the unchanged short/concurrent
  floors, exact runtime proof, zero restarts, and automatic production restore.
- Probe manifest:
  `deploy/experiments/qwen35-9b-context-ceiling-224k-canary.yaml`.
- Compile cache:
  `/var/lib/flexinfer/compile-cache-qwen35-9b-context-224k-v1`.
- Capacity: 391,320 KV tokens, or 1.71 full-window requests at the
  229,376-token serving ceiling.
- Run 1 (`context-ceiling-224k-v1`, generation 3):
  `Succeeded/GauntletPassed`; 5/5 exact recall at 223,969 prompt tokens;
  332.106-second TTFT and 389.499 seconds elapsed; 101.255 base tok/s;
  59.629 LoRA dialogue median tok/s; 40.483 LoRA multi-turn median tok/s;
  0.589 LoRA/base ratio; 55.476 concurrent aggregate tok/s median; 0.603-second
  short p95 TTFT; zero restarts.
- Run 2 (`context-ceiling-224k-v2`, generation 4):
  `Succeeded/GauntletPassed`; 5/5 exact recall at 223,969 prompt tokens;
  343.877-second TTFT and 403.128 seconds elapsed; 101.330 base tok/s;
  59.131 LoRA dialogue median tok/s; 40.101 LoRA multi-turn median tok/s;
  0.584 LoRA/base ratio; 55.353 concurrent aggregate tok/s median; 0.598-second
  short p95 TTFT; zero restarts.
- Both runs proved the pinned digest, native `RDNA3W4A16LinearKernel`,
  Triton/FLA GDN, FP16 KV, graph sizes `[1,2,4]`, default non-thinking base and
  LoRA behavior, and a working explicit thinking override.
- The first v1 attempt was interrupted at the five-minute Flux interval before
  the successful generation-3 run. Decoded desired/live specs were identical,
  but Flux server-side apply advanced the CR generation and the old controller
  treated that as a real spec edit. MR
  [!934](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/934)
  fixes this by fencing runs on a canonical spec fingerprint plus stable
  execution generation; real spec changes still invalidate old evidence.
- Status: passed twice; promotion condition satisfied.

## Next

1. Promote production to `maxModelLen=229376` and
   `maxInputTokens=228352` after the controller fix rolls out.
2. Re-run the production smoke/LoRA readiness check after rollout.
3. Keep the older roughly 245K miss as the current unsafe upper bound; bisect
   between 224K and 245K only if the extra window is operationally valuable.

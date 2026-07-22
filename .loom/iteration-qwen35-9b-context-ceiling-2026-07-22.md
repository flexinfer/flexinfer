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

**Status**: not run

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

- Outcome: pending.
- Exact evidence: pending.

## Next

1. If 192K passes, test 224K as the upper-half midpoint.
2. If 192K fails, test 160K as the lower-half midpoint.
3. Promote no context increase until a new ceiling passes twice.

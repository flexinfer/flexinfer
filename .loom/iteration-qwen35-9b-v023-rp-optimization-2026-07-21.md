# Rapid Dev Iteration Log: Qwen3.5-9B vLLM 0.23 RP optimization

## Scope

- Iteration goal: Preserve the repaired 9B lane's verified 131K context while
  moving its exact W4/G128 artifact and rank-64 `nsfw-rp` adapter to the native
  gfx1100 W4A16 + bounded-graph vLLM 0.23 execution path.
- Current blocker: vLLM 0.17 eager mode caps the warm lane near 4.8 output
  tok/s; disabling eager mode on that image crash-looped at graph capture.
- Hypothesis: the already-qualified vLLM 0.23 dense Qwen3.5 plugin and native
  W4A16 operator can capture shapes 1/2/4 with active LoRA while retaining
  enough FP16 KV/state capacity for one 131K request.

## Artifact Pinning

- Branch: `codex/qwen35-9b-v023-canary`
- Files touched:
  - `deploy/experiments/qwen35-9b-v023-rp-canary.yaml`
  - `deploy/tasks/model-eval-gauntlet/qwen35-9b-rp-performance-cronjob.yaml`
  - the two owning `kustomization.yaml` files
- Build profile: vLLM 0.23, text-only dense Qwen3.5, native RDNA3 W4A16,
  bounded FULL_AND_PIECEWISE graphs, AITER disabled.
- Image tag: `registry.harbor.lan/flexinfer/runtime`
- Image digest: `sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`
- Upstream ref/fork: vLLM 0.23 plus `build/vllm-qwen35-text-plugin` v0.7.0.
- Probe manifest: `deploy/experiments/qwen35-9b-v023-rp-canary.yaml`
- Target node: `cblevins-7900xtx`, discrete gfx1100 ordinal 0.
- Cache/storage path: `qwen35-9b-oci-gfx1100:qwen35-9b` plus fresh host
  compilation cache `/var/lib/flexinfer/compile-cache-qwen35-9b-v023-v1`.
- Model: repaired abliterated Qwen3.5-9B GPTQ W4/G128 plus
  `mirazrafi/NSFW-RP-RolePlay-LoRA-Qwen-3.5-9B` rank 64.

## Change

- Narrow patch point: Add one owned ModelExperiment and one retained gauntlet;
  do not edit the production `Model/qwen35-9b-ablit-rp` manifest.
- Why this patch is the minimal test: the controller temporarily grants the
  candidate the existing shared GPU lease, strips production aliases, records
  a typed verdict, deletes the candidate, and restores the current parent. The
  gauntlet exercises base numerics, LoRA hot-load, short/multi-turn throughput,
  two-session scheduling, and five-depth long recall under the exact candidate.

## Probe

- Command(s): Ship through GitOps, confirm the candidate pod's `imageID` and
  native kernel/graph logs, then follow the retained gauntlet Job.
- Local proof before shipping:
  - embedded gauntlet Python compiled successfully (`592` source lines);
  - experiment and task kustomizations rendered successfully;
  - kubeconform: `4` CronJob resources valid, `0` invalid;
  - rendered candidate assertions confirmed the immutable image, 131K, FP16
    KV, graphs `[1,2,4]`, rank-64 LoRA, and prefix caching disabled;
  - `make test-unit`, `go vet ./...`, and
    `make check-runtime-patch-contracts` passed.
- Pod/job: `ModelExperiment/qwen35-9b-v023-rp-canary`; child names are reported
  in status.
- Confirmed image ID:
  `registry.harbor.lan/flexinfer/runtime@sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`.
- Expected success condition: first-start Ready with zero restart; vLLM 0.23 +
  `RDNA3W4A16LinearKernel`; graph capture sizes only 1/2/4; coherent base and
  adapter output; median adapter decode >=15 tok/s and >=50% of base; two-stream
  aggregate >=20 tok/s; exact 5/5 recall near 128K input tokens; no traceback,
  OOM, ROCm/HSA, NaN, or abort marker; typed PASS and parent restoration.

## Result

### Generation 1

- Outcome: infrastructure and base-model gates passed; the gauntlet failed on
  a false-negative response parser before it could exercise the adapter.
- Candidate reached `Ready` on gfx1100 with zero restarts about 146 seconds
  after creation. The pod reported the exact pinned image digest.
- Runtime evidence: vLLM `0.23.0`, `RDNA3W4A16LinearKernel`, Triton/FLA GDN
  prefill, `enforce_eager=False`, FULL_AND_PIECEWISE graph sizes `1/2/4`, and
  graph capture completed in 14 seconds using 0.57 GiB.
- Capacity evidence: model weights used 7.56 GiB; 12.0 GiB remained for KV;
  vLLM reported 390,513 KV tokens and 2.98x maximum concurrency at 131,072
  tokens per request.
- Base evidence: literal warmup passed at 107.37 decode tok/s; the 192-token RP
  control was coherent at 103.17 decode tok/s with 0.27-second TTFT.
- Exact failure: the adapter download completed, then `post_json` called
  `json.loads` on vLLM's successful non-JSON response body and raised
  `JSONDecodeError: Expecting value`. The production LoRA controller already
  treats any 2xx response as success and does not require a JSON body.
- Narrow fix: preserve a non-JSON response as `{body: ...}` for both success
  and error responses, keep the HTTP status as the success gate, and retain
  `/v1/models` as the authoritative registration check. Bump the experiment
  revision to `131k-graph-fp16kv-lora-v2` so GitOps starts generation 2.

### Generation 2

- Outcome: every runtime, quality, absolute-throughput, concurrency, and long-
  context gate passed. The job failed only on a mis-specified relative metric.
- Warm candidate startup fell to about 76 seconds. The persistent cache loaded
  the compiled range in 2.61 seconds; total compile time fell from 45.90 to
  5.97 seconds, and graph capture fell from 14 to 6 seconds.
- Capacity improved to 13.77 GiB available KV, 448,077 KV tokens, and 3.42x
  reported concurrency at 131,072 tokens. The exact image remained restart-free.
- LoRA hot-load passed with HTTP 200 and `/v1/models` listed `nsfw-rp`.
  Dialogue median decode was 59.13 tok/s; 2,268-token multi-turn median decode
  was 40.14 tok/s. Outputs were coherent and emitted no reasoning text.
- Two concurrent 2,268-token sessions sustained a median 53.13 aggregate
  output tok/s with 4.90-second p95 stream elapsed time.
- Long context passed exactly: 127,969 prompt tokens, all five needles recalled
  at five depths, 121.17-second TTFT, and 154.99-second total elapsed time.
- Exact failure: the combined median of the short and ~28x-longer LoRA
  workloads was 49.63 tok/s, or 0.4885 relative to the short base control,
  narrowly below the 0.50 gate. Combining unlike prompt lengths made this an
  invalid adapter-overhead comparison.
- Narrow fix: compute the relative ratio from the matched 80-token base and
  LoRA dialogue workload (observed ratio 0.5821), while applying the absolute
  15 tok/s floor independently to both dialogue and multi-turn medians. Bump
  the experiment revision to `131k-graph-fp16kv-lora-v3`.

### Generation 3

- Outcome: PASS. `ModelExperiment/qwen35-9b-v023-rp-canary` generation 5
  reached the durable typed verdict `Succeeded / GauntletPassed` with an empty
  failure list. The candidate was deleted and the original parent restored.
- Exact image digest and zero-restart startup were reconfirmed on gfx1100.
- Base RP decode: 101.99 tok/s. Matched LoRA dialogue median: 59.59 tok/s
  (`0.5843` of base). Multi-turn median: 40.51 tok/s. Short p95 TTFT: 0.60s.
- Two-stream median aggregate output: 55.68 tok/s; p95 stream elapsed: 4.60s.
- Long context: exact 5/5 recall from 127,969 prompt tokens, 123.32-second
  TTFT, 156.57-second total, no reasoning text, OOM, fault, abort, or error.

### Production promotion

- Promote only the certified profile to `Model/qwen35-9b-ablit-rp`: exact
  runtime digest, dedicated Deployment, native W4A16, graph shapes 1/2/4,
  chunked 4K prefill, FP16 KV, 131K reservation, rank-64 LoRA injection, and a
  production-specific persistent compilation-cache namespace.
- Preserve the parent name, `nsfw-rp` adapter, LiteLLM registration, aliases,
  source artifact, shared-group policy, and warm-primary priority.
- First rollout storage blocker: `cache.strategy: Local` auto-enabled the
  flash-loader, which found 14,684.4 MB to copy but only 4,155.9 MB free in the
  node's persistent `/dev/shm`; the init container correctly refused to start.
  The canary loaded the same artifact directly from EXT4. Explicitly disable
  flash-loader on this model so the dedicated deployment mounts the already-
  staged Local cache directly and retains the certified storage path.
- Second rollout ownership blocker: after the model opted out of the persistent
  runtime, the controller created its dedicated Deployment without unloading
  the same model from that runtime. The old process held 22,495 MB of VRAM;
  vLLM correctly rejected the new process because only 2.03/23.98 GiB was free.
- Operational recovery unloaded only `qwen35-9b-ablit-rp` through the runtime
  API. GPU use fell to 26 MB and the existing dedicated pod completed startup.
  It confirmed vLLM 0.23, native RDNA3 W4A16, Triton GDN, graph capture in 14s,
  354,570 KV tokens (2.71x at 131,072), and the exact pinned image.
- Production `Model` reached `Ready`; `LoRAAdapter/qwen35-9b-nsfw-rp` reached
  `Loaded` on 1/1 replicas. Direct service smokes returned exact
  `PROD_BASE_OK` and `PROD_LORA_OK` with no reasoning output.
- Permanent controller fix: when a previously runtime-managed model opts into
  `dedicatedDeployment`, health-check and unload that same model, clear its
  runtime endpoints, and requeue before a Deployment can claim the GPU. A
  failed unload blocks the handoff; an already-absent model proceeds normally.

## Next

1. Validate and ship the runtime-to-dedicated ownership handoff fix.
2. Confirm controller CI and deployment health.
3. Reconfirm the production parent and adapter remain Ready/Loaded.

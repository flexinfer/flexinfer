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

## Next

1. Validate and ship the generation-2 response-parser fix.
2. Observe LoRA, concurrency, and 128K recall evidence through typed verdict.
3. If memory alone fails, reduce graph capture to shape one before considering
   a split fast-RP/long-context profile; do not introduce FP8 KV in this arm.

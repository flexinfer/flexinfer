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
- Confirmed image ID: pending live run.
- Expected success condition: first-start Ready with zero restart; vLLM 0.23 +
  `RDNA3W4A16LinearKernel`; graph capture sizes only 1/2/4; coherent base and
  adapter output; median adapter decode >=15 tok/s and >=50% of base; two-stream
  aggregate >=20 tok/s; exact 5/5 recall near 128K input tokens; no traceback,
  OOM, ROCm/HSA, NaN, or abort marker; typed PASS and parent restoration.

## Result

- Outcome: pending cluster connectivity and GitOps run.
- Exact failure or success evidence: pending.
- Relevant logs / stack frame: pending.

## Next

1. Validate and ship the experiment manifests.
2. Observe the exact runtime and retained gauntlet evidence.
3. If memory alone fails, reduce graph capture to shape one before considering
   a split fast-RP/long-context profile; do not introduce FP8 KV in this arm.

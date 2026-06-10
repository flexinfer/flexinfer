# RALPH Iteration Plan

## Review

- Roadmap milestone: F5 heterogeneous 72B lane.
- Spec section(s): `.loom/brainstorm-gfx906-sdpa-distributed-prefill-2026-06-09.md`; `.loom/32-iteration-plan-gfx906-toy-gptq-killtest-2026-06-09.md`.
- Prior decisions to preserve: toy kill-test proved the true failure site is vLLM KV-cache allocation on near-full Vega20; `gpu_memory_utilization` cuts and layer rebalance did not fix it; `num_gpu_blocks_override=256` did.

## Align

- Slice name: F5 72B 3-way relaunch with fixed KV block override.
- Scope in: patch `/Users/cblevins/workspace/tmp/f5-3way-validate.yaml` to add `--num-gpu-blocks-override 256`, run the 3-node graph-mode launch, smoke one OpenAI-compatible completion, capture logs/status/evidence, and restore daily-driver lanes.
- Scope out: image rebuilds, controller changes, promotion of gfx906 vLLM support, layer partition changes, and new model manifests.
- Acceptance criteria: Ray reports 3 GPUs, vLLM reaches serving, the 72B model returns one coherent HTTP 200 completion through the head service, and gemma4/bge/whisper daily-driver state is restored afterward.
- Dependencies/blockers: all three GPU nodes must be Ready; the stale `qwen3-1p7b-vllm-radeonvii` canary must be scaled to zero/deleted before the window; the launch temporarily displaces the warm text lanes.

## Land

- Planned file areas: local window manifest under `/Users/cblevins/workspace/tmp`; `.loom` evidence docs after the run.
- Implementation steps:
  1. Snapshot model/pod state and clean stale canary residue.
  2. Apply the patched 3-way manifest and monitor startup.
  3. Run one greedy completion plus a short throughput sample if serving succeeds.

## Prove

- Tests to run: `kubectl` pod/status watch, head logs, HTTP completion smoke against `f5-3way-head:8000`.
- Lint/static checks: `git diff --check` for repo docs if evidence files are committed.
- CI checks: not applicable unless the slice produces repo changes beyond `.loom`.

## Handoff/Harvest

- Docs to update: `.loom/60-validation-matrix.md` if 72B returns HTTP 200, otherwise the F5 brainstorm/iteration notes with the new failure signature.
- Agent-context entries to add: final verdict, restore status, and next-slice task.
- Next-slice candidates: if 256 is KV-capacity-limited, retry with 512; if serving succeeds, convert the window manifest into a reusable debug manifest/runbook.

## Result

2026-06-10 live window verdict: **BLOCKED, but decisive**.

- Window prep: suspended `flexinfer-models` Flux kustomization and `flexinfer` HelmRelease, scaled the controller to zero, patched both Gemma warm lanes to `minReplicas: 0` / `warmPolicy: ondemand`, deleted stale gfx906 vLLM canary residue, then applied `/Users/cblevins/workspace/tmp/f5-3way-validate.yaml` with `--num-gpu-blocks-override 256`.
- Positive progress: all three pods scheduled and joined Ray; `ray status` reported `3.0 GPU`; vLLM launched graph mode with the override visible in args; all 11 Qwen2.5-72B GPTQ shards loaded. Reported weight residency: head `16.1610 GB`, 5930k worker `14.2670 GB`, Radeon VII worker `13.4144 GB`.
- Failure: before serving, vLLM still called `determine_num_available_blocks -> profile_run` and the Radeon VII rank failed inside `vllm/_custom_ops.py:gptq_gemm` during Qwen2 MLP `gate_up_proj`, raising `RuntimeError: HIP error: invalid argument`. This is not a `torch.zeros` KV allocation failure in the full 72B path; `num_gpu_blocks_override=256` does not bypass the profiling forward in vLLM 0.6.3.
- Evidence: `.loom/local/validation/f5-3way-2026-06-10/{head.log,worker1.log,worker2.log,poll.log,f5-pods.yaml,head-describe.txt,events-after-fail.txt,f5-3way-validate-patched.yaml}`.
- Restore: F5 pods/configmap deleted; Gemma warm policies and `minReplicas: 1` restored; controller, HelmRelease, and `flexinfer-models` Flux kustomization resumed; final state confirmed `Ready` for `gemma4-26b-a4b-gptq`, `gemma4-26b-a4b-gptq-5930k`, `bge-large-radeonvii`, `bge-reranker-radeonvii`, and `qwen3-1p7b-tools-radeonvii`; `qwen3-1p7b-vllm-radeonvii` is `Idle` with `minReplicas: 0`.

Next-slice correction: do **not** spend another 72B window on `num_gpu_blocks_override=512` alone. The next cheapest proof should target the gfx906 `gptq_gemm` path directly: either rerun with `AMD_SERIALIZE_KERNEL=3` on the Radeon VII rank to confirm attribution, or test a ROCm GPTQ reference/fallback path or per-arch gfx906 worker image that avoids the stock fused GPTQ kernel.

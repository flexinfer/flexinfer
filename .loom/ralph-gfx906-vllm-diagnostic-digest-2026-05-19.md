# RALPH: gfx906 vLLM Diagnostic Digest

Date: 2026-05-19
Branch: `codex/gfx906-vllm-diagnostic-digest`

## Review

- Roadmap milestone: ROCm `gfx1100`/`gfx906` platform enhancements, with the
  required `gfx906` textgen canary lane still blocked on vLLM worker failure
  visibility.
- Spec/evidence inputs: `.loom/60-validation-matrix.md`,
  `.loom/ralph-gfx906-vllm-smoke-2026-05-17.md`, and
  `.loom/ralph-gfx906-vllm-worker-diagnostics-2026-05-19.md`.
- Prior decisions to preserve:
  - Keep `gfx906` vLLM `experimental` until a real HTTP 200 canary response.
  - Do not retry more Qwen/profile flag variants until OPT-125M either serves
    or prints the child-process failure from the diagnostic image.

## Align

- Slice name: gfx906 vLLM diagnostic digest pin and OPT canary.
- Scope in:
  - Trigger the post-merge manual `publish_vllm_rocm_gfx906` job from pipeline
    `10513`.
  - Pin the published immutable digest in the gfx906 GPUProfile and Helm value.
  - Rerun the isolated OPT-125M Radeon VII canary and record the outcome.
- Scope out:
  - Promoting `gfx906` vLLM support.
  - Changing the canary model family or vLLM flags.
  - Making `opt-125m-vllm` a default/user-facing chat alias.

## Land

- Published digest:
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:020e737330f7e6355634ffc7d606d294806c65988e7f48f3099f6013fda07964`.
- Previous standalone canary digest:
  `sha256:84f0ae2bb1ea46163885aad55181540bee9995b4b4b0c656f3943b7580e07e1e`.
- Files updated:
  - `deploy/gpuprofiles/gfx906.yaml`
  - `deploy/system/values-k3s.yaml`
  - `deploy/models/qwen3-1p7b-vllm-radeonvii.yaml`
  - `.loom/60-validation-matrix.md`
  - `.loom/ralph-gfx906-vllm-smoke-2026-05-17.md`

## Prove

- Local checks:
  - `kubectl kustomize deploy/models`
  - `git diff --check`
  - `python3 scripts/check-runtime-patch-contracts.py --run-script-tests`
- Live checks:
  - Confirm `publish_vllm_rocm_gfx906` job `109838` succeeded.
  - Resolve `rocm-gfx906-3c5a856f` to the published digest.
  - Reconcile/pull the pinned digest, run the OPT canary, and capture whether
    it returns HTTP 200 or the newly surfaced worker traceback.
- Live result:
  - The diagnostic image pulled successfully in 8m33s
    (`10460235176` bytes), and the Radeon VII node stayed `Ready=True` with
    `DiskPressure=False`, `MemoryPressure=False`, and `PIDPressure=False`.
  - The OPT cache path was not the blocker: cache staging skipped/copied 13
    files / 718.2 MB with roughly 55 GB free in `/dev/shm`.
  - The proxy canary still failed before HTTP 200 with `curl: (52) Empty reply
    from server`.
  - The diagnostics hook captured the root failure: a fatal Python segfault in
    the child engine process during OPT `Embedding` initialization, through
    `torch.nn.init._no_grad_normal_`, `vllm/model_executor/models/opt.py`,
    `model_loader/loader.py`, and `worker/model_runner.py:1112 load_model`.
  - Cleanup restored the live lane: Flux was resumed, the vLLM canary returned
    to `Idle`, the llama.cpp fallback returned to `Ready`, and the fallback
    proxy smoke returned `Blue`.

## Handoff

- `gfx906` vLLM remains blocked; the diagnostic digest did its job by surfacing
  the child-worker stack, not by serving.
- The next slice should patch or work around the exact Python/Torch/vLLM OPT
  initialization path named by the traceback. Do not change model family,
  scheduler flags, cache strategy, or GPUProfile env again until that path has
  been addressed.

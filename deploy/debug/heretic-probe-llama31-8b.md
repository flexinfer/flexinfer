# Heretic ROCm gfx1100 Probe

A one-off debug Job that runs [`p-e-w/heretic`](https://github.com/p-e-w/heretic)
(Optuna-driven automated abliteration) against
`meta-llama/Llama-3.1-8B-Instruct` on `cblevins-7900xtx`
(Radeon RX 7900 XTX, gfx1100, 24 GiB VRAM).

This is a probe — pass/fail tells us whether to commit Heretic as our
abliteration tool for the new Qwen3-30B-A3B candidate (and other models).

## What it tests

1. `pip install heretic-llm` works on top of our `gptq-rocm-gfx1100` quantizer
   image (`rocm/pytorch:rocm6.4.1_ubuntu22.04_py3.10_pytorch_release_2.6.0`).
2. `bitsandbytes ~=0.49` imports cleanly with the gfx1100 wheel — no source
   rebuild required — and a blockwise quantize on a CUDA tensor returns.
3. Heretic runs end-to-end and emits a Hugging-Face-compatible artifact
   (`config.json` + sharded `*.safetensors`).
4. The output loads via `transformers.AutoModelForCausalLM.from_pretrained`
   and produces clean text on five benign prompts (cookies, weather, poetry,
   sourdough, haiku) with no NaN/Inf logits and no token collapse.

## Prerequisites

- Free 7900 XTX on `cblevins-7900xtx`. Pause/scale-down conflicting model
  deployments first. Verify with `kubectl get pods -A -o wide | grep 7900xtx`.
- `hf-token` Secret in `flexinfer-system` with key `HF_TOKEN`. Llama 3.1 is
  gated — the download will refuse without it.

## Run

```bash
# Apply the manifest (NOT included in deploy/debug/kustomization.yaml — this
# is intentionally apply-on-demand).
kubectl apply -f deploy/debug/heretic-probe-llama31-8b.yaml

# Stream logs (~45-90 min on 7900 XTX, comparable to Heretic's 3090 baseline).
kubectl logs -f job/heretic-probe-llama31-8b -n flexinfer-system

# After completion, the Job auto-cleans via ttlSecondsAfterFinished: 7200.
# Force cleanup early:
kubectl delete -f deploy/debug/heretic-probe-llama31-8b.yaml
```

## Success criteria

- Heretic exits 0.
- `${HERETIC_OUT}/config.json` exists alongside one or more `*.safetensors`.
- Transformers loads the artifact without error.
- All five benign prompts return non-empty text without NaN/Inf logits and
  without token collapse (`<unused...`, repeated single chars).

The Job exits non-zero on any of the above.

## Cleanup

- Job + Pod + ConfigMap go away on `kubectl delete -f ...`.
- The 80 GiB `emptyDir` workspace (HF cache + heretic output) is reclaimed
  when the Pod is removed. Any artifact you want to keep must be copied off
  before deletion (e.g. `kubectl cp`).

## Open questions this probe will resolve

- **numpy 2.x vs ROCm PyTorch 2.6**: heretic claims `numpy~=2.2` but our
  base image was built against numpy 1.x. Script first tries `--no-deps`
  install on existing numpy; if `import heretic` fails, falls back to
  `numpy>=2.2,<3` and re-tests. Outcome is logged.
- **bitsandbytes gfx1100 wheel**: do the upstream wheels include working
  gfx1100 HIP kernels, or do we need a source build like we did for gfx906?
- **Output shape**: does heretic emit a full HF model directory (drop-in
  for vLLM) or only a LoRA adapter that needs merging? Step 6 fails fast
  on "config.json missing" so we will know from a single run.

## Resource footprint

- Requests: `cpu=8, memory=32Gi, amd.com/gpu=1`.
- Limits: `cpu=16, memory=64Gi, amd.com/gpu=1`.
- `emptyDir` workspace: 80 GiB (HF cache for the 16 GiB Llama 3.1 download
  plus the abliterated output).
- `/dev/shm`: 8 GiB tmpfs (transformers/optuna multiprocessing).

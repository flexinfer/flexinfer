# Runtime Promotion Validation Matrix

This is the canonical canary and runtime-promotion evidence table for GPU
model/runtime work. It connects planning specs, roadmap items, build artifacts,
runtime canaries, observed failure modes, and promotion decisions so a reviewer
can audit a promotion without reading chat history.

Scope:

- Primary GPU class: AMD Radeon RX 7900 XTX / ROCm `gfx1100`.
- Secondary validation class: AMD Radeon VII / ROCm `gfx906` for runtime
  compatibility canaries and comparison rows.
- Primary roadmap/spec link: SD-3 in
  `docs/planning/spec-driven-delivery.md` and
  `docs/planning/next-roadmap.md`.
- Existing Gemma4 and Qwen evidence remains in this file; rows may stay `TBD`
  until the artifact reaches a real validation layer.

## Validation Contract

Every runtime or canary row must capture these audit fields before it can be
promoted:

| Field | Required value |
|---|---|
| `artifact` | ModelCache name, model family, PVC path, or artifact label being evaluated. |
| `context_length` | Runtime `maxModelLen`, benchmark context, or explicit `n/a` with reason. |
| `gpu_class` | Hardware class such as `gfx1100/7900xtx`, `gfx906/radeonvii`, or `sm_52/maxwell`. |
| `backend` | Runtime engine: `vllm`, `diffusers`, `llamacpp`, `ollama`, `mlc`, or `n/a` for offline artifact rows. |
| `support_level` | Lifecycle posture: `supported`, `experimental`, `deprecated`, `unsupported`, or `n/a`. Mirrors the `flexinfer.ai/runtime-support` posture in GPUProfile defaults. |
| `runtime_image` | Image digest, immutable OCI ref, or temporary tag plus follow-up to pin digest. |
| `oci_ref` | Published model OCI ref or PVC/local artifact ref when OCI is not used. |
| `observed_failure_mode` | `none` for a clean canary, or the concrete scheduler/runtime/cache failure. |
| `canary_command` | Reproducible smoke command (curl / `kubectl exec`) or `script:` path that proves Ready + coherence. `TBD: <reason>` while no command has been captured. |
| `rollback_digest` | Previous known-good runtime image digest or model OCI ref to revert to if the row regresses. `TBD: <reason>` until a known-good predecessor is recorded. |
| `spec_roadmap_link` | Spec, roadmap item, issue, MR, or commit proving why the row exists. |
| `promotion_decision` | `promote`, `conditional`, `block`, `fail`, `skip`, or `pending`. |

Timing and throughput fields (`smoke.ready_minutes`, `smoke.cold_load_min`,
`smoke.decode_tps`, `smoke.prompt_tps`, image generation seconds) are captured
in the Runtime Smoke section below rather than duplicated as table columns,
so a row can stay narrow while still pointing at canonical evidence.

Promotion rules:

- `promote`: metadata validation passes, runtime canary is coherent, the model
  reaches Ready, and the runtime image or model artifact is pinned by digest or
  immutable OCI ref.
- `conditional`: the canary serves successfully but has a documented warning,
  temporary tag, manual family override, or follow-up that does not block the
  current operator outcome.
- `block`: evidence is incomplete or a known prerequisite is missing.
- `fail`: the attempted runtime cannot satisfy the target context or returns
  incoherent/error responses.
- `skip`: intentionally not a target for this GPU class or slice.
- `pending`: no runtime evidence has been captured yet.

## Validation Layers

The codebase exposes two separate validation surfaces. Keep them separate in
the row notes:

1. **Artifact metadata validator**: `build/scripts/validate_quantized_artifact.py`
   through `flexinfer quantize validate-artifact`. Offline, no GPU required.
   Checks layout, shapes, module coverage, optional generation repetition probe,
   and emits structured `checks` JSON. It does not measure cosine or perplexity.
2. **Dense-module cosine gate**: quantize-time gate when `denseModulePolicy:
   validate` and `denseModuleCosineThreshold` are set on a `ModelCache`.
   Requires re-quantization; it does not run against already-published artifacts.
3. **Runtime canary**: cluster deployment or direct runtime smoke that proves
   the target artifact/image reaches Ready and serves coherent output at the
   target context length.

## Field Capture Reference

### Metadata validator

| Field | How captured |
|---|---|
| `val.status` | `PASS` / `FAIL` from validator stdout. |
| `val.layout` | Resolved layout: `vllm-gptq`, `compressed-tensors`, or `hf-native`. |
| `val.family` | Detected or forced family id. |
| `val.shards` | `checks.shard_mode` and shard count from index. |
| `val.mods_shape` | `checks.modules_in_block_to_quantize_shape` (`nested` or `flat`). |
| `val.declared_missing_qweight` | `checks.declared_modules_without_qweight` length/list. |
| `val.quantized_modules` | Count of distinct module families with qweight tensors. |
| `val.gen_ok` | `checks.generation_probe.ok` when `--run-generation` is used. |
| `val.warnings` | Count of warnings. |
| `val.errors` | Count of errors. |

### Quantize-time cosine gate

| Field | How captured |
|---|---|
| `cos.min` | Per-layer minimum cosine across dense modules. |
| `cos.mean` | Per-layer mean cosine. |
| `cos.layers_below_threshold` | Count of layers below `denseModuleCosineThreshold`. |

### Runtime smoke

| Field | How captured |
|---|---|
| `smoke.ready_minutes` | ModelCache phase timing: download, ablit, quant, publish, runtime Ready. |
| `smoke.cold_load_min` | Fresh activation time from demand/scale-up to runtime Ready. |
| `smoke.decode_tps` | vLLM: `decode_tokens / decode_seconds`. |
| `smoke.prompt_tps` | vLLM prompt processing tokens/sec. |
| `smoke.coherent` | Manual review of smoke prompt output. |
| `runtime_image` | Pod image ID digest, Helm value digest, or OCI runtime image ref. |
| `oci_ref` | `status.publish.ociRef`, model registry ref, or PVC/local artifact ref. |

## Promotion Matrix

The four required canary lanes (`gfx1100` textgen, `gfx1100` imagegen,
`gfx906` textgen/quantization, `gfx906` imagegen/offload) are each represented
by at least one row even when evidence is incomplete.

| `artifact` | `context_length` | `gpu_class` | `backend` | `support_level` | `runtime_image` | `oci_ref` | `validation_evidence` | `observed_failure_mode` | `canary_command` | `rollback_digest` | `spec_roadmap_link` | `promotion_decision` |
|---|---:|---|---|---|---|---|---|---|---|---|---|---|
| `gemma4-26b-a4b-gptq` attnfp16-clean active artifact | 8192 | `gfx1100/7900xtx` | `vllm` | `experimental` | TBD | `pvc:///models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean`; OCI TBD | `val.status=PASS`, `val.layout=vllm-gptq`, forced `val.family=gemma4-26b-a4b`, flat modules warning, 2 MoE module families quantized, no dense cosine, runtime args captured from live pod | Family auto-detection returns `None`; flat `modules_in_block_to_quantize`; runtime image digest not recorded | TBD: live canary script not yet captured to a tracked file | TBD: no prior known-good vLLM digest pinned for this artifact | SD-3 / Issue #57; `.loom/30-implementation-plan.md`; raw 2026-04-18 evidence below | `conditional` |
| `gemma4-26b-a4b-gptq` hybrid-v10 on PVC | n/a, not served | `gfx1100/7900xtx` | `n/a` | `experimental` | n/a | `pvc:///models/gemma4-26b-a4b-gptq/gptq-w4-g128-hybrid-v10`; OCI n/a | `val.status=PASS`, `val.layout=vllm-gptq`, forced `val.family=gemma4-26b-a4b`, 9 module families, no dense cosine | Not served; `self_attn.v_proj` present on only 25/30 layers, likely `attention_k_eq_v` but not promotion-ready | n/a: artifact-only, never served | n/a: artifact-only | SD-3 / Issue #57; raw 2026-04-18 evidence below | `block` |
| `gemma4-26b-a4b-gptq-long` fp16-KV canary | 32768 target; observed max estimate 8896 | `gfx1100/7900xtx` | `vllm` | `experimental` | TBD | Model ref inherits hybrid/long cache; OCI n/a | Inherits validator evidence from hybrid line; live canary loaded weights in 56.69s with 17.74 GiB model memory | vLLM KV memory ceiling: 1.87 GiB available for KV, 6.88 GiB required for 32768 tokens; blocks 16K/32K promotion on fp16-KV lane | TBD: failing canary; recapture once KV ceiling fix lands | TBD: no prior 16K/32K success to roll back to | SD-3 / Issue #57; `.loom/gemma4-26b-31b-gptq-turboquant-plan.md`; raw 2026-04-26 evidence below | `fail` |
| `gemma4-26b-a4b-gptq-dense` dense validate rebuild | TBD: rebuild not yet completed | `gfx1100/7900xtx` | `n/a` | `experimental` | n/a | Dense-validated cache; OCI n/a | Dense cosine not reached; re-quant required with `denseModulePolicy=validate` | 4h abliteration deadline stopped at harmful prompt 80/128; retry restarts partial harmful pass | TBD: rebuild not yet completed | n/a: artifact never reached runtime | SD-3 / Issue #57; raw 2026-04-26 evidence below | `block` |
| `gemma4-31b-gptq` keqv recovery | 2048 | `gfx1100/7900xtx`, 2 GPUs | `vllm` | `experimental` | TBD | `pvc://gemma4-31b-gptq/gemma4-31b-gptq/gptq-w4-g128-keqv`; OCI n/a | Postprocess/copy succeeded; `val.status=PASS`; direct smoke returned HTTP 200 with answer `4` in 0.158s, then 0.304s after restoring 31B | Runtime image digest not recorded; context only proven at 2048 | `kubectl port-forward svc/gemma4-31b-gptq 8000:8000` then `/v1/completions` greedy `2+2` smoke (raw 2026-04-26 evidence below) | TBD: runtime image digest not yet pinned | SD-3 / Issue #57; raw 2026-04-26 evidence below | `conditional` |
| `gemma4-e4b-gptq` | TBD: not yet built | `gfx1100/7900xtx` | TBD: backend not chosen | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| `omnicoder-9b-gptq` | TBD: not yet served | `gfx1100/7900xtx` | TBD: backend not chosen | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| `qwen35-9b-gptq-gfx1100` | TBD: not yet served | `gfx1100/7900xtx` | `vllm` | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| **Required canary: `gfx1100` textgen** — `qwen36-27b-gptq` abliterated GPTQ W4_G128 | 8192 | `gfx1100/5930k` | `vllm` | `experimental` | `registry.harbor.lan/flexinfer/vllm:rocm-gfx1100-qwen35-patched-nodiag-textcfg` (digest TBD) | `registry.harbor.lan/flexinfer/qwen36-27b:gptq-w4-g128-gfx1100@sha256:fe3a6bea0cd2cdf254a5db6194e01402f1f7f93c4b86d8c717695470fdd3849d` | Cache Ready; vLLM reached Ready with `quantization=gptq`, `kvCacheDtype=auto`, `maxNumSeqs=2`; direct proxy and service smoke returned HTTP 200; quarantined from reconciled serving manifests on 2026-05-07; DEBT-302 adds warning-first publish validation with `layout=vllm-gptq`, `family=qwen36-27b`, and `checks.gdn_gptq_policy` to surface any `linear_attn.*.qweight` tensors before OCI publish | First activation exposed proxy `lastActiveTime` conflict; cold start was dominated by 17.6GB image pull; `fp8_e4m3` KV crashed Triton cache update; `gptq_marlin` rejected because artifact config declares `gptq`; current `gptq` runtime serves incoherent output (`!!!!!!!!!!!!` / multilingual junk), flat punctuation logprobs, and live profile traffic like `-current Lockheedпуст劳逸...`; too slow for the 5930k shared lane | Direct `/v1/completions` greedy smoke against `qwen36-27b-gptq:8000` for `The color of the sky is` (raw 2026-05-06 evidence below); publish validator gate runs during next qwen36 ModelCache publish | TBD: failing canary; predecessor `qwen3-14b-abliterated` GPTQ digest is referenced in MR !247 but has no captured success on 5930k to roll back to | MR !247 replacement; MR !248 runtime hardening; MR !253/!254 quiet runtime; 2026-05-05, 2026-05-06, 2026-05-07 smoke evidence; DEBT-302 validator tests | `fail` |
| `qwen3-14b-gptq` | TBD: not yet served | `gfx1100/5930k` | `vllm` | `experimental` | TBD | TBD | TBD: no validation run | Evidence not captured | TBD | TBD | SD-3 / Issue #57 | `pending` |
| **Required canary: `gfx1100` imagegen** — `gonzalomo-fluxpony-imagegen` FLUX Schnell text-to-image | n/a, 512x512 + 1024x1024 warmup resolutions | `gfx1100/5930k` | `diffusers` | `supported` | TBD: diffusers runtime digest not yet pinned in this matrix; see `deploy/models/gonzalomo-fluxpony-imagegen.yaml` | `HF://black-forest-labs/FLUX.1-schnell` (Apache 2.0); manifest `deploy/models/gonzalomo-fluxpony-imagegen.yaml` | NF4 + bfloat16 compute dtype; `WARMUP_RESOLUTIONS=512x512,1024x1024` precompiles MIOpen kernels; `MIOPEN_FIND_MODE=2` works around ROCm#4729 VAE crash; primary imagegen on `5930k-imagegen-textgen` shared lane (priority 200) per current model layout | TBD: live cold-load + 512/1024 generation timings not yet captured to a tracked artifact in this matrix | TBD: capture `curl /v1/images/generations` once runtime digest is pinned | TBD: no prior diffusers runtime digest recorded for this lane | RG-4 / `.loom/gfx1100-gfx906-platform-enhancements-plan.md` Slice 4; `docs/user/backends-rocm-gfx1100.md:344-460` | `pending` |
| `gemma4-31b-gptq` Radeon VII comparison | n/a | `gfx906/radeonvii` | `n/a` | `unsupported` | n/a | n/a | n/a | Off-gfx1100 comparison row; VRAM ceiling for this promotion lane | n/a: not a target | n/a | SD-3 / Issue #57 | `skip` |
| **Required canary: `gfx906` textgen/quantization** — Qwen3.5 GPTQ Radeon VII pipeline (`docs/user/gptq-quantization-runbook.md`) | TBD: gfx906 runtime currently paused (DiskPressure) so no live serving canary | `gfx906/radeonvii` | `vllm` | `deprecated` | TBD: gfx906 vLLM runtime is paused via `flexinfer.ai/runtime-paused=true` after the digest pull repeatedly hit DiskPressure | TBD: 31B GPTQ artifact reused from gfx1100 (`pvc:///gemma4-31b-gptq/gptq-w4-g128-keqv`) | GPTQ runbook documents abliteration + GPTQ flow on Radeon VII (`docs/user/gptq-quantization-runbook.md`); 2026-05-07 evidence below records DaemonSet pause + DiskPressure history. CPU loading + community PyTorch wheel restore allocations under 16 GiB. Live serving canary not currently runnable on radeonvii. | Root-backed containerd fills to 100% on first pull of the 17 GiB digest-pinned `runtime` image, evicting kubelet workloads. The replacement `qwen3-1p7b-tools-radeonvii` llama.cpp lane is queued precisely because vLLM cannot run here today. | TBD: re-enable canary after storage relocation; recapture before lifting `runtime-paused` | `registry.harbor.lan/flexinfer/runtime@sha256:7c05960614517dbd5d6453944125a01e78f0451f6695467a8eaf6a6859d461dd` (last gfx906 runtime digest before the `dd0a1936...` promotion that hit DiskPressure) | `.loom/gfx1100-gfx906-platform-enhancements-plan.md` Slice 5; `docs/user/gptq-quantization-runbook.md`; 2026-05-07 gfx906 runtime digest promotion evidence below | `pending` |
| **Required canary: `gfx906` imagegen/offload** — `sdxl-inpainting-radeonvii` Diffusers inpaint | n/a, 512x512 image edit | `gfx906/radeonvii` | `diffusers` | `experimental` | `registry.harbor.lan/flexinfer/runtime@sha256:94045d0ca4b12deb3c46bb22070f67bfedad8b719bb992e5d3ce128ad27ad597` | `local:///models/flexinfer-system/sdxl-inpainting-radeonvii` | Slim runtime image (cycle 2: `Dockerfile.runtime-gfx906` on `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` base, 36.9 GB extracted vs prior 59.2 GB) promoted via MR !282 after MR !281. DaemonSet pod Ready on `cblevins-radeonvii`; cold-start `/v1/images/edits` smoke returned HTTP 200 in 107.7s with one 512x512 PNG, `b64_len=252372`. Pre-pull verified root holds at 65% (78G/127G used) post-image-pull; bind-mounted `/var/lib/flexinfer/models` to `/mnt/nvme/longhorn/flexinfer/models` via fstab, reclaiming 21G on root. | None on the runtime path. Cold-start latency increased from prior 48.35s warm to 107.7s cold (deployment scale-up + weights load from freshly bind-mounted NVMe path). Failed pull on root LVM exposed pull-time peak ~1.5x final extracted size. | Multipart `POST /model/sdxl-inpainting-radeonvii/v1/images/edits` through `flexinfer-proxy` with 512x512 PNG image+mask (raw 2026-05-07 evidence below) | `registry.harbor.lan/flexinfer/runtime@sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97` (the prior 59.2 GB digest replaced by this promotion) | RG-4 / `.loom/gfx1100-gfx906-platform-enhancements-plan.md`; `.loom/gfx1100-gfx906-next-round-plan.md` Track B-3; 2026-05-07 Radeon VII evidence below | `conditional` |
| `qwen3-1p7b-tools-radeonvii` GGUF tool-router | 8192 | `gfx906/radeonvii` | `llamacpp` | `experimental` | `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` (digest TBD) | `HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF` / `qwen3-1.7b-q4_k_m.gguf` | Manifest enabled 2026-05-07 as the safe Radeon VII utilization lane after gfx906 runtime image pulls filled root-backed containerd storage. Expected image is about 2.5 GiB compressed versus about 17 GiB for diffusers/runtime. | Runtime smoke pending after Flux applies cache/PVC and activation. Keep `tool-router` and `qwen3-1.7b` aliases only; do not make this the default chat route unless coherence and latency pass. The Kubernetes object uses `1p7b` because Service names cannot contain dots. | TBD: smoke pending Flux activation | TBD: first manifest of this lane; no prior llama.cpp gfx906 digest pinned in this matrix | 2026-05-07 fast-chat recovery and gfx906 disk-pressure follow-up | `pending` |

## Artifact Layout Notes

- `--layout hf-native`: standard HF safetensors, no quantization metadata
  (source or abliteration artifact).
- `--layout vllm-gptq`: GPTQ with vLLM-style
  `modules_in_block_to_quantize` in `config.json`.
- `--layout compressed-tensors`: RedHatAI / compressed-tensors pack; currently
  about 2 tok/s on gfx1100, avoid for promotion unless a slice specifically
  targets that layout.

## Raw Evidence Archive

Large JSON outputs and smoke transcripts go under
`.loom/local/validation/<family>/<timestamp>/`. Summaries only belong in this
tracked file. Each archive should include the exact command, artifact path,
runtime image digest or OCI ref when available, and smoke response transcript.

### 2026-04-18 gemma4-26b-a4b-gptq findings (Slice A1-lite)

- Active serving artifact:
  `/models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` (confirmed from
  vLLM cmdline `--model ...` on pod
  `gemma4-26b-a4b-gptq-87c45466d-wpkg6`).
- Serving args: `--quantization gptq --attention-backend TRITON_ATTN
  --enforce-eager --max-num-seqs 1 --gpu-memory-utilization 0.95
  --max-model-len 8192`.
- Clean variant: 30 layers x 2 MoE module families quantized
  (`moe.down_proj`, `moe.gate_up_proj`); 30
  `attention-fp16-layer-NN.safetensors` shards hold the FP16 attention weights
  alongside 4 `model-X-of-00004.safetensors` base shards; total 777 tensors,
  34 shard files.
- Hybrid-v10 variant (on-PVC but not served): fully quantized layout (MoE +
  MLP + attention q/k/v/o) with `self_attn.v_proj` only present on 25/30 layers.
  This is consistent with `attention_k_eq_v: true` in the config, but it must be
  confirmed before promoting this variant.
- Validator follow-ups:
  1. Family auto-detection gap: `detected_family: null` for both variants.
     `FAMILY_PROFILES` in `build/scripts/validate_quantized_artifact.py`
     matches tensor-name hints; add a `model_type: gemma4_text` or architecture
     signal so the CLI works without a forced `--family` flag.
  2. Flat `modules_in_block_to_quantize` warning is always-on for vLLM-serving
     artifacts. Either silence it for that layout or make it informational-only.
- No re-quant or cosine gate ran. `denseModulePolicy: validate` remains
  commented out in `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml`.

Raw outputs:
`.loom/local/validation/gemma4-26b-a4b-gptq/20260418-085841/{clean.json,clean.txt,hybrid-v10.json}`
(gitignored).

### 2026-05-05 qwen36-27b-gptq smoke findings

- Artifact pipeline completed: ModelCache `qwen36-27b-gptq-gfx1100`
  abliterated 3 layers in `1h53m20s`, quantized GPTQ `W4_G128` in `1h19m30s`,
  and published
  `registry.harbor.lan/flexinfer/qwen36-27b:gptq-w4-g128-gfx1100@sha256:fe3a6bea0cd2cdf254a5db6194e01402f1f7f93c4b86d8c717695470fdd3849d`.
- Replacement Model `qwen36-27b-gptq` activated on `cblevins-5930k`. Initial
  proxy activation returned 503 because `LastActiveTime` status updates hit a
  conflict after scale-up had already started.
- Cold activation reached the pod quickly, but kubelet spent `12m33s` pulling
  the 17.6GB ROCm vLLM image. Cache flash from hostPath to `/dev/shm` took
  about 9 seconds.
- Runtime config `kvCacheDtype: fp8_e4m3` failed during vLLM KV warm-up:
  Triton reported `type fp8e4nv not supported in this architecture`. Live
  canary with `kvCacheDtype: auto`, `calculateKvScales: false`, and
  `maxNumSeqs: 2` reached Ready.
- `gptq_marlin` was tested as a coherence fix but vLLM rejected it because the
  model config declares quantization method `gptq`.
- Direct FlexInfer proxy and direct service requests returned HTTP 200, but
  output was incoherent: exact-answer prompts produced repeated exclamation
  marks and multilingual junk. Treat this as a model artifact/runtime blocker,
  not a routing success.
- Follow-up direct safetensor check on `cblevins-5930k` mounted
  `qwen36-27b-gptq-gfx1100` and dequantized representative GPTQ attention
  tensors against the post-abliteration FP16 parent. Layers 11 and 15
  `q/k/v/o` had no NaNs/Infs, sane weight stats, cosine about `0.99`, and
  relative L2 about `0.13-0.16`, so the cache is not broadly corrupt.
- Next runtime fix: Qwen3.5-patched vLLM must use the ROCm GPTQ reference
  fallback already proven necessary for Gemma4. The Qwen patch stack now adds a
  `GPTQLinearMethod.apply` ROCm/4-bit slow path so the next rebuilt runtime can
  test coherence without the fused `gptq_gemm` kernel.

### 2026-05-06 qwen36-27b-gptq quality recheck

- Runtime image was quiet and digest-pinned:
  `registry.harbor.lan/flexinfer/vllm@sha256:cb6d92c956ee150b4b8210e625586140e1b5da4c204caa422b1965e953de78e8`.
- Greedy chat smoke through `flexinfer-proxy`:
  `{"model":"qwen36-27b","messages":[{"role":"user","content":"Answer with exactly one word: blue"}],"max_tokens":24,"temperature":0,"top_p":1}`
  returned HTTP 200 with `!!!!!!!!!!!!!!!!!!!!!!!!`.
- Direct `/v1/completions` smoke against `qwen36-27b-gptq:8000` for prompt
  `The color of the sky is` returned `!!!!!!!!!!!!!!!!` with identical
  top-logprobs for punctuation tokens (`!`, `"`, `#`, `$`, `%`) at each
  generated position (`-12.422473907470703`), indicating a flat/collapsed
  logits distribution rather than a sampling or chat-template problem.
- Pod logs confirmed the quiet patch applied the ROCm GPTQ reference fallback,
  naive FLA kernels, RMSNorm native path, and direct `gdn_attention_core`
  bypass, so the remaining blocker is deeper artifact/runtime math or
  quantized-weight interpretation.
- Serving posture: keep `qwen36-27b-gptq` as a direct canary only. Do not expose
  replacement labels such as `qwen3-coder` or `qwen3-30b-a3b` until a coherent
  deterministic smoke passes.
- 2026-05-07 operator traffic confirmed the failure is user-visible, not just a
  synthetic prompt problem: the `qwen36-27b` profile returned mixed token soup
  beginning `-current Lockheedпуст...`. The model was manually scaled to zero
  and removed from reconciled `deploy/models/kustomization.yaml` because it is
  both incoherent and slow on the `5930k-imagegen-textgen` shared lane.
- 2026-05-07 fast-chat recovery posture: remove `qwen36-27b-gptq`,
  `gemma4-31b-gptq`, and `gemma4-26b-a4b-gptq-long` from the reconciled model
  set so slow or incoherent canaries stop owning user-facing aliases. Promote
  `qwen3-8b-fast-7900xtx` as the warm `fast-chat` / `gpt-3.5-turbo` MLC route
  on `7900xtx-textgen`. A 5930k fallback was attempted but removed from the
  reconciled set: the node-local MLC PVC lacks the model directory, and the
  shared NFS copy lacks `mlc-chat-config.json`, so MLC exits before serving.
  The attempted `qwen3-14b-abliterated-v2-5930k` fallback stayed disabled
  because its GPTQ source PVC is absent in the live cluster.
- 2026-05-06 Track H static triage (`.loom/local/qwen36-coherence-triage.md`):
  ranked three hypotheses against the published `model.safetensors.index.json`
  and live ModelCache spec. Most likely: GDN linear-attention sub-modules
  (`in_proj_qkvz`, `in_proj_ba`, `conv1d`) were int4-quantized because
  `spec.quantization.dynamicExclusion: "none"` on `qwen36-27b-gptq-gfx1100`.
  Earlier dequant cosine sanity at layers 11/15 only covered q/k/v/o
  (full-attention modules), so GDN weight quality was never measured. Section
  16 fixup in `build/scripts/vllm_qwen35_patches.py` only reverts to
  `nn.Linear` when `.qweight` is missing from the index, so degraded but
  present GDN qweights bypass the safety net. Confirming experiment: dump
  `model.safetensors.index.json` from PVC `qwen36-27b-oci`, grep for
  `model.layers.0.linear_attn.in_proj_ba.qweight`; if present, dequant vs FP16
  parent and check cosine threshold 0.98. Re-quant fix is one line at
  `deploy/modelcaches/qwen36-27b-gptq-gfx1100.yaml:87`:
  `dynamicExclusion: "gdn"`. Hypothesis 1 (`text_config`/vocab corruption) and
  hypothesis 3 (lm_head abliteration) eliminated: published config has
  `model_type=qwen3_5_text` + `vocab_size=248320` + `tie_word_embeddings=false`,
  and ModelCache spec has `ablitateLmHead=false` with `refusalDirNorm=41`
  (under the 100 abort threshold).

### 2026-05-06 qwen36-27b-gptq Track D-1 root cause confirmed

- PVC `qwen36-27b-oci` was inspected directly on `cblevins-5930k` via a
  busybox debug pod mounting `/models/qwen36-27b/` (the published GPTQ
  artifact at digest `sha256:fe3a6bea...`).
- `model.safetensors.index.json` contains `.qweight` tensors for all 48
  GDN linear-attention layers. Three modules per layer were quantized:
  `linear_attn.in_proj_qkv.qweight`, `linear_attn.in_proj_z.qweight`, and
  `linear_attn.out_proj.qweight`. Counts: 48 each (one per GDN layer).
- `linear_attn.conv1d` kept `.weight` (1D conv, not a `nn.Linear`, so
  GPTQ skipped it as expected).
- `quant_log.csv` confirms layer 0 (a GDN layer per the `layer_types`
  schedule) recorded GPTQ losses for `linear_attn.in_proj_qkv` (loss
  0.00524), `linear_attn.in_proj_z` (loss 0.00343), and
  `linear_attn.out_proj` (loss ~3.9e-6).
- Earlier dequant cosine sanity (2026-05-05) only covered q/k/v/o on
  layers 11/15, both *full*-attention layers. The GDN sub-modules were
  never measured; their weight quality is unknown by this experiment but
  the quantization-then-GDN-runtime path is architecturally wrong (GDN
  GatedDeltaNet expects FP weights for in_proj_qkv/in_proj_z/out_proj).
- Module names differ from Track H's hypothesized
  `in_proj_qkvz`/`in_proj_ba`: this artifact uses the defused
  `in_proj_qkv`/`in_proj_z` split. The fix still applies — switch
  `dynamicExclusion` from `none` to `gdn` so GPTQModel skips
  `linear_attn.*` patterns on the next quantization run.
- ModelCache CRD updated in MR (Track D-1) to set
  `quantization.dynamicExclusion: "gdn"`. Re-quant has not been run yet;
  serve coherence smoke and dequant cosine on a non-GDN layer remain
  required before the matrix row flips.
- 2026-05-09 DEBT-302 added a reusable artifact validator policy for
  `family=qwen36-27b`: `linear_attn.*` qweight tensors are reported in
  `checks.gdn_gptq_policy.quantized_gdn_modules` and emitted as warnings.
  The qwen36 ModelCache publish gate starts with `failOnWarnings=false` so
  the recovery cycle records evidence without unexpectedly blocking publish.

### 2026-05-06 sdxl-inpainting-radeonvii runtime smoke

- Model `sdxl-inpainting-radeonvii` was Ready through the direct runtime path:
  `phase=Ready`, Ready reason `RuntimeReady`, message `Model ready via runtime`.
- Runtime pod `flexinfer-runtime-gfx906-dh8st` ran digest-pinned image
  `registry.harbor.lan/flexinfer/runtime@sha256:7c05960614517dbd5d6453944125a01e78f0451f6695467a8eaf6a6859d461dd`.
- Runtime load selected local model path
  `/models/flexinfer-system/sdxl-inpainting-radeonvii`,
  `StableDiffusionXLInpaintPipeline`, dtype `float32`, fixed VAE, CPU offload,
  and attention slicing. Warmup completed in 60.7s.
- The initial request to `/v1/images/generations` returned HTTP 500 because an
  SDXL inpaint pipeline requires an input image and mask. The runtime remained
  healthy and the failure was not a GPU crash.
- Correct multipart smoke through `flexinfer-proxy`:
  `/model/sdxl-inpainting-radeonvii/v1/images/edits` with 512x512 PNG image
  and mask returned HTTP 200 in 48.35s, one image, `b64_len=24152`.
- Runtime logs recorded 22 denoise steps in 40s and
  `POST /v1/images/edits HTTP/1.1` 200 OK. Decoded response artifact:
  `/private/tmp/sdxl-radeonvii-edits-output.png`, PNG, 1024x1024 RGB.
- Promotion posture: conditional pass for the gfx906 runtime lane. Keep the row
  conditional because this canary depends on CPU offload and uses the image-edit
  endpoint only; text/image-generation endpoint parity is not implied.

### 2026-05-07 gfx906 runtime digest promotion

- Built and pushed `registry.harbor.lan/flexinfer/runtime:rocm-gfx906` from
  `master@d8c75658`, producing digest
  `sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97`.
- Smoke checked the image entrypoint with:
  `docker --context 7900xtx run --rm --entrypoint /usr/local/bin/flexinfer-runtime registry.harbor.lan/flexinfer/runtime:rocm-gfx906 --help`.
  The binary reported default `-gpu-vendor amd` and `-gpu-arch gfx906`.
- Promoted the digest with
  `scripts/promote-runtime-digest.sh gfx906 --digest sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97 --apply`.
- Promotion corrected existing drift: `deploy/gpuprofiles/gfx906.yaml` had
  `sha256:ba4570f5...`, while `deploy/system/values-k3s.yaml` had
  `sha256:7c059606...`; both now point at `sha256:dd0a1936...`.
- Validation before merge: `scripts/check-runtime-profile-consistency.sh`,
  `scripts/test-promote-runtime-digest.sh`, `git diff --check`, and targeted
  runtime image digest resolution with `crane digest`.
- Follow-up during fast-chat recovery: applying the digest-pinned gfx906 runtime
  to `cblevins-radeonvii` repeatedly filled root-backed containerd storage to
  100% and triggered kubelet `DiskPressure` evictions. The live DaemonSet was
  paused and `deploy/system/values-k3s.yaml` now mirrors that pause with
  `flexinfer.ai/runtime-paused=true` on the gfx906 runtime profile until the
  image/storage issue is fixed.
- To keep Radeon VII useful without repeating the pull failure, the next
  reconciled workload is `qwen3-1p7b-tools-radeonvii`: llama.cpp GGUF,
  `HF://rippertnt/Qwen3-1.7B-Q4_K_M-GGUF` /
  `qwen3-1.7b-q4_k_m.gguf`, `tool-router` aliases only, and the much smaller
  `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` runtime path.
  The live Kubernetes object avoids dots in the name so generated Services pass
  DNS-1035 validation. The original upstream `Qwen/Qwen3-1.7B-GGUF` source was
  not a valid Q4_K_M cache source for this manifest; the prefetcher matched
  zero files before this correction. Chat serving also disables Qwen3 thinking
  output with `reasoningFormat: none` and `reasoningBudget: 0` so LiteLLM
  aliases behave like low-latency utility routes instead of returning hidden
  reasoning markers.

### 2026-05-07 gfx906 slim runtime promotion + cold-start canary (Track B-3)

- Round 2 of the next-round parallel plan closed the gfx906 disk-pressure block
  end-to-end. The deployed runtime digest `dd0a1936...` (built from
  `Dockerfile.runtime` for the gfx906 profile, 59.2 GB extracted) repeatedly
  drove `cblevins-radeonvii` root LVM (127 GB) to 100% on pull and triggered
  kubelet `DiskPressure` evictions. Track B-1 (drain + bind-mount containerd
  to NVMe LVM) was abandoned because the node hosts 194 pods including
  Prometheus/Loki/Tempo/Langfuse-Clickhouse and 11 StatefulSets — drain blast
  radius too broad for an unscheduled maintenance window.
- Track B-3 cycle 1 (MR !280) slimmed `Dockerfile.unified-gfx906` from
  33.1 → 32.8 GB, but that is the batch quantization image (entrypoint
  `/bin/bash`, no `flexinfer-runtime` Go binary) — not the runtime DaemonSet
  image that is actually deployed. Real win came from cycle 2 (MR !281):
  introduced a per-profile `dockerfile:` override in `build/runtime.yaml` and
  `build/build-runtime.sh`, added `build/Dockerfile.runtime-gfx906` mirroring
  the multi-stage pattern (go-builder, llamacpp-builder, ollama-builder) with
  the runtime stage on `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3`. Combined
  with cycle 1 techniques (≤5 RUN layers, `__pycache__`/`.py[co]` strip,
  `pip cache purge`, `apt-get clean`, `setuptools<78` for bnb 0.49.2),
  pushed digest `sha256:94045d0ca4b12deb3c46bb22070f67bfedad8b719bb992e5d3ce128ad27ad597`
  at 36.9 GB extracted — a 38% reduction.
- First pre-pull on radeonvii failed at root 100% / `available: 0`. The
  36.9 GB final number was correct but pull-time peak (compressed content
  tarballs + extracting layers concurrently) was ~1.5x final ≈ 55 GB, which
  exceeded the 55 GB free root we had. Containerd auto-cleaned the partial
  extraction on failure; root recovered to 47%. SDXL inpaint cache-stage Job
  pod was evicted (controller-managed, no data loss).
- Discovered 21 GB of model weights at `/var/lib/flexinfer/models/flexinfer-system/sdxl-inpainting-radeonvii`
  on root LVM. Bind-mounted that path to `/mnt/nvme/longhorn/flexinfer/models`
  (fstab entry, no k3s restart, no drain). 22 GB rsynced in 2m17s; `diff -r`
  confirmed integrity; `.old` reclaimed. Root went 57 → 36 GB used / 85 GB
  free.
- Re-pull succeeded in 8m48s. Post-pull root: 78 GB used / 44 GB free / 65%.
  No DiskPressure transition.
- Promotion (MR !282): `scripts/promote-runtime-digest.sh gfx906 --digest sha256:94045d0c... --apply`
  updated `deploy/gpuprofiles/gfx906.yaml` and `deploy/system/values-k3s.yaml`,
  and dropped the `flexinfer.ai/runtime-paused: "true"` annotation on the
  gfx906 nodeSelector. `scripts/check-runtime-profile-consistency.sh` passed.
- Flux reconciled to `master@sha1:5aae1f34`. DaemonSet `flexinfer-runtime-gfx906`
  came up as `flexinfer-runtime-gfx906-ff4p6` (1/1 Running) within ~30s of
  reconcile; logs confirm
  `runtimeProfile=gfx906`, `runtimeDigest=sha256:94045d0c...`,
  `backends=[ollama, steam, vllm, vllm-omni, comfyui, diffusers, llamacpp, mlc-llm]`.
  Non-fatal entrypoint warning:
  `vllm_gemma4_moe_gptq_patch.py: Cannot find vLLM installation` — expected
  because vLLM is intentionally `false` for the gfx906 profile (memory note);
  follow-up to suppress.
- Cold-start canary: `Model/sdxl-inpainting-radeonvii` was `Idle` (serverless
  pattern — no Deployment exists when idle). Multipart
  `POST /model/sdxl-inpainting-radeonvii/v1/images/edits` through
  `flexinfer-proxy` (port-forward to `flexinfer-system/flexinfer-proxy:80`)
  with 512x512 PNG image+mask + prompt
  `"a vibrant orange flower with green leaves, photorealistic"` returned
  **HTTP 200 in 107.7s** with one PNG result, `b64_len=252372`. Cold-start
  latency includes deployment scale-up + pod start + weights load from the
  freshly bind-mounted NVMe path + GPU warmup. Compared to the 2026-05-06
  warm canary (HTTP 200 in 48.35s, `b64_len=24152`), the +60s is consistent
  with cold-start overhead and the bigger b64 is a more visually complex
  generation, not an error.
- Post-canary disk: root 71 GB used / 51 GB free / 59%; NVMe LVM 338 GB used
  / 409 GB free / 46%.
- Round-2 net: gfx906 lane unblocked, runtime digest pinned by digest, model
  state on NVMe instead of root, dynamic `runtime_profile`/`runtime_digest`
  metric labels now populated for the radeonvii lane, MR !282 merged.

### 2026-04-26 gemma4 26B/31B execution findings

- Live Flux truth before hot validation: `flexinfer-system` and
  `flexinfer-models` were Ready at
  `master@sha1:50cf1d977d502357df1c5c6b998c05b1dc05f429`; !193 and !194 were
  already merged.
- `gemma4-31b-gptq` was Ready/Active with `minReplicas: 1`, `priority: 250`,
  `gpu.count: 2`, `warmPolicy: primary`, and `maxModelLen: 2048`. The direct
  smoke through a port-forward returned HTTP 200 with answer `4` in 0.158s.
- After the long-canary hot test, 31B was restored to Ready/Running and a second
  direct smoke returned HTTP 200 with answer `4` in 0.304s.
- `gemma4-26b-a4b-gptq-long` has the safe dGPU selector, cache Ready,
  `minReplicas: 0`, and `maxModelLen: 32768`, but the fp16-KV long-context
  canary failed at engine initialization. vLLM loaded the weights successfully
  (`17.74 GiB`, `56.69s`) but reported only `1.87 GiB` available for KV while
  `32768` tokens required `6.88 GiB`; the logged estimated maximum model length
  was `8896`. This blocks both 16K and 32K promotion on the current
  hybrid/fp16-KV lane.
- The 26B dense-validated cache did not reach the cosine gate. Its latest retry
  reached only harmful prompt `80/128` before the 4h abliteration deadline; the
  checkpoint remained in `stage: harmful_activations`. Because `abliterate.py`
  resumes only completed activation payloads, each retry restarts the partial
  harmful pass. The manifest now raises abliteration and quantization deadlines
  to 24h so the next Flux-managed rebuild can reach dense cosine validation.
- TurboQuant primitive sharing is implemented behind `TQ4_SHARE_PRIMITIVES=1`
  and the patcher was verified idempotent against upstream `turboquant-vllm`
  commit `9d19b87cef462cf0abd5643f6d052ac5a3bc99b6`. Runtime canaries still
  require a rebuilt image carrying the patched profile.

## Schema Change Log

Track schema rotations of this matrix here. Each entry records the date,
the columns added or retired, and the rationale so a reader can audit how
older rows mapped to newer columns.

### 2026-05-06: rotate matrix into canonical runtime-promotion shape (Track E)

- Promoted this file from a gfx1100-only quantization tracker to the canonical
  canary and runtime-promotion table for `gfx1100` and `gfx906`.
- Added explicit audit columns to the Validation Contract and the Promotion
  Matrix header: `backend`, `support_level`, `canary_command`,
  `rollback_digest`.
- Did not duplicate timing/throughput fields (`smoke.ready_minutes`,
  `smoke.cold_load_min`, `smoke.decode_tps`, `smoke.prompt_tps`, image
  generation seconds) into table columns; the Runtime Smoke section under
  Field Capture Reference remains the canonical source.
- Backfilled the four required canary lanes called out in the Track E spec:
  `gfx1100` textgen (`qwen36-27b-gptq`), `gfx1100` imagegen
  (`gonzalomo-fluxpony-imagegen` FLUX Schnell), `gfx906` textgen/quantization
  (Qwen3.5 GPTQ runbook lane, currently paused under DiskPressure), and
  `gfx906` imagegen/offload (`sdxl-inpainting-radeonvii`).
- Cells without captured evidence are written as `TBD: <reason>` so the
  promotion rules can keep treating them as `pending` instead of fabricating
  a `promote`.
- Source plan: Track E in the gfx1100/gfx906 next-round plan, picking up
  Slice 6 of `.loom/gfx1100-gfx906-platform-enhancements-plan.md`.

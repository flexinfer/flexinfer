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
| `runtime_image` | Image digest, immutable OCI ref, or temporary tag plus follow-up to pin digest. |
| `oci_ref` | Published model OCI ref or PVC/local artifact ref when OCI is not used. |
| `observed_failure_mode` | `none` for a clean canary, or the concrete scheduler/runtime/cache failure. |
| `spec_roadmap_link` | Spec, roadmap item, issue, MR, or commit proving why the row exists. |
| `promotion_decision` | `promote`, `conditional`, `block`, `fail`, `skip`, or `pending`. |

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

| `artifact` | `context_length` | `gpu_class` | `runtime_image` | `oci_ref` | `validation_evidence` | `observed_failure_mode` | `spec_roadmap_link` | `promotion_decision` |
|---|---:|---|---|---|---|---|---|---|
| `gemma4-26b-a4b-gptq` attnfp16-clean active artifact | 8192 | `gfx1100/7900xtx` | TBD | `pvc:///models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean`; OCI TBD | `val.status=PASS`, `val.layout=vllm-gptq`, forced `val.family=gemma4-26b-a4b`, flat modules warning, 2 MoE module families quantized, no dense cosine, runtime args captured from live pod | Family auto-detection returns `None`; flat `modules_in_block_to_quantize`; runtime image digest not recorded | SD-3 / Issue #57; `.loom/30-implementation-plan.md`; raw 2026-04-18 evidence below | `conditional` |
| `gemma4-26b-a4b-gptq` hybrid-v10 on PVC | n/a, not served | `gfx1100/7900xtx` | n/a | `pvc:///models/gemma4-26b-a4b-gptq/gptq-w4-g128-hybrid-v10`; OCI n/a | `val.status=PASS`, `val.layout=vllm-gptq`, forced `val.family=gemma4-26b-a4b`, 9 module families, no dense cosine | Not served; `self_attn.v_proj` present on only 25/30 layers, likely `attention_k_eq_v` but not promotion-ready | SD-3 / Issue #57; raw 2026-04-18 evidence below | `block` |
| `gemma4-26b-a4b-gptq-long` fp16-KV canary | 32768 target; observed max estimate 8896 | `gfx1100/7900xtx` | TBD | Model ref inherits hybrid/long cache; OCI n/a | Inherits validator evidence from hybrid line; live canary loaded weights in 56.69s with 17.74 GiB model memory | vLLM KV memory ceiling: 1.87 GiB available for KV, 6.88 GiB required for 32768 tokens; blocks 16K/32K promotion on fp16-KV lane | SD-3 / Issue #57; `.loom/gemma4-26b-31b-gptq-turboquant-plan.md`; raw 2026-04-26 evidence below | `fail` |
| `gemma4-26b-a4b-gptq-dense` dense validate rebuild | TBD | `gfx1100/7900xtx` | n/a | Dense-validated cache; OCI n/a | Dense cosine not reached; re-quant required with `denseModulePolicy=validate` | 4h abliteration deadline stopped at harmful prompt 80/128; retry restarts partial harmful pass | SD-3 / Issue #57; raw 2026-04-26 evidence below | `block` |
| `gemma4-31b-gptq` keqv recovery | 2048 | `gfx1100/7900xtx`, 2 GPUs | TBD | `pvc://gemma4-31b-gptq/gemma4-31b-gptq/gptq-w4-g128-keqv`; OCI n/a | Postprocess/copy succeeded; `val.status=PASS`; direct smoke returned HTTP 200 with answer `4` in 0.158s, then 0.304s after restoring 31B | Runtime image digest not recorded; context only proven at 2048 | SD-3 / Issue #57; raw 2026-04-26 evidence below | `conditional` |
| `gemma4-e4b-gptq` | TBD | `gfx1100/7900xtx` | TBD | TBD | TBD | Evidence not captured | SD-3 / Issue #57 | `pending` |
| `omnicoder-9b-gptq` | TBD | `gfx1100/7900xtx` | TBD | TBD | TBD | Evidence not captured | SD-3 / Issue #57 | `pending` |
| `qwen35-9b-gptq-gfx1100` | TBD | `gfx1100/7900xtx` | TBD | TBD | TBD | Evidence not captured | SD-3 / Issue #57 | `pending` |
| `qwen36-27b-gptq` abliterated GPTQ W4_G128 canary | 8192 | `gfx1100/5930k` | `registry.harbor.lan/flexinfer/vllm:rocm-gfx1100-qwen35-patched-nodiag-textcfg` | `registry.harbor.lan/flexinfer/qwen36-27b:gptq-w4-g128-gfx1100@sha256:fe3a6bea0cd2cdf254a5db6194e01402f1f7f93c4b86d8c717695470fdd3849d` | Cache Ready; vLLM reached Ready with `quantization=gptq`, `kvCacheDtype=auto`, `maxNumSeqs=2`; direct proxy and service smoke returned HTTP 200; quarantined from reconciled serving manifests on 2026-05-07 | First activation exposed proxy `lastActiveTime` conflict; cold start was dominated by 17.6GB image pull; `fp8_e4m3` KV crashed Triton cache update; `gptq_marlin` rejected because artifact config declares `gptq`; current `gptq` runtime serves incoherent output (`!!!!!!!!!!!!` / multilingual junk), flat punctuation logprobs, and live profile traffic like `-current Lockheedпуст劳逸...`; too slow for the 5930k shared lane | MR !247 replacement; MR !248 runtime hardening; MR !253/!254 quiet runtime; 2026-05-05, 2026-05-06, and 2026-05-07 smoke evidence below | `fail` |
| `qwen3-14b-gptq` | TBD | `gfx1100/5930k` | TBD | TBD | TBD | Evidence not captured | SD-3 / Issue #57 | `pending` |
| `gemma4-31b-gptq` Radeon VII comparison | n/a | `gfx906/radeonvii` | n/a | n/a | n/a | Off-gfx1100 comparison row; VRAM ceiling for this promotion lane | SD-3 / Issue #57 | `skip` |
| `sdxl-inpainting-radeonvii` Diffusers inpaint canary | n/a, 512x512 image edit | `gfx906/radeonvii` | `registry.harbor.lan/flexinfer/runtime@sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97` | `local:///models/flexinfer-system/sdxl-inpainting-radeonvii` | Direct runtime path selected `flexinfer-runtime-gfx906-dh8st`; Model Ready via runtime; 512x512 multipart `/v1/images/edits` returned HTTP 200 in 48.35s with one 1024x1024 PNG result, `b64_len=24152`; runtime logged 22 denoise steps in 40s and POST 200; rebuilt/pushed `gfx906` runtime digest `dd0a1936...` from `d8c75658` and promoted GPUProfile/Helm consumers | `/v1/images/generations` is the wrong endpoint for SDXL inpaint and returned HTTP 500 with a Diffusers input-format error; corrected `/v1/images/edits` canary succeeded. Runtime uses CPU offload and detected Radeon VII as `gfx900` under the gfx906 lane | RG-4 / `.loom/gfx1100-gfx906-platform-enhancements-plan.md`; 2026-05-06 Radeon VII evidence below | `conditional` |

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
  on `7900xtx-textgen`, and add `qwen3-14b-abliterated-v2-5930k` as an
  on-demand 5930k text lane that shares with imagegen via
  `5930k-imagegen-textgen`.

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

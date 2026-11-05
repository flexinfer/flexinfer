# Result: S3, vLLM 0.33.0-rocm72 parity on the 7900 XTX (2026-11-05)

Slice S3 of `10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` (§6.1, owner decision P1).
Matrix row `xtx-vllm-v033-parity-2026-11-05` holds the pass rule, written before the run.

**Status: PASS.** On gfx1100, vLLM 0.33.0-rocm72 reproduces 0.32.0 exactly at the workhorse's S4 posture:
- the same three greedy hashes;
- decode within 2 % at every size;
- MTP acceptance unchanged.

Per §6.2 and owner decision P2, **KT-QOS (D3) and KT-SO (D4) are to be judged on this image**,
`runtime@sha256:29378fe0…`. The G6 arm says to keep wvSplitK on the `lm_head`: turning skinny GEMM off cost 7–11 % of
decode.

Nothing live moved: no lane, workhorse or runtime DaemonSet repin. The XTX card was back with the warm lane at 02:54:43Z.

## What was built and merged

| MR | Merge | What |
|---|---|---|
| !1284 | `cbd5bc14` | The ISA verifier reads the target list of every offload bundle, so it passes on 0.33's zstd `CCOB` `_rocm_C`. Build profile `gfx1100-serving-v033`. diffusers 0.42.1 (GHSA-4prv-5hj7-c2xr) for both serving personas |
| !1285 | `a19547f2` | XTX prewarm of the 0.33 image (container `image-8`, Ready at 01:02Z); canary manifest; pending matrix row |

The image is `registry.harbor.lan/flexinfer/runtime@sha256:29378fe072741a9be5c23333762e37759c027dde5e445e9bc4a1b37bd730cf69`.
- Tags: `rocm-gfx1100-serving-v033-bebfb52e`, `rocm-gfx1100-serving-v033`, `pinned-29378fe07274`; 11.88 GB.
- Built from `build/Dockerfile.runtime-serving-v032` at bebfb52e on the 7900xtx docker host. The base is
  `vllm/vllm-openai-rocm:v0.33.0-rocm72@sha256:4fe7bc12…`.
- The in-build verifiers passed:
  - gfx1100 code is in all four extensions, including the 18 compressed `_rocm_C` bundles;
  - `flexinfer_qwen35_text` `register()` runs with the segmented patch installed;
  - the frozen stack has 266 packages unchanged and 12 added;
  - the bitsandbytes HIP 7.2 library and the ffmpeg binary are present.

## The canary

`deploy/debug/qwen38-27b-v033-parity-canary.yaml` was applied by the owner at ~02:04Z. It placed two Models on the XTX
warm lane's slot (count 2 / `hipVisibleDevices 0`, `7900xtx-textgen`, priority 480). Both ran the 5930k workhorse's S4
posture: ROCM_SEGMENTED_ATTN + autotune, fp16 KV, MTP K=2 with the checkpoint head, 32K, two seats, 0.96, GDN prefix
caching. Source revision `2d7425f7`.

- **Arm A** `qwen38-27b-v033-parity-a`: 0.32.0, `runtime@b06b06fe`.
- **Arm B** `qwen38-27b-v033-parity-b`: 0.33.0, `runtime@29378fe0`, plus `vllmVersion: "0.33.0"`.
- **Arm C**: arm B with `spec.config.env.VLLM_ROCM_USE_SKINNY_GEMM=0`, patched over MCP.

Harness: `scripts/verify-vllm-mtp.py --phase measure --contexts 512,2048,16384 --soak-pairs 4`, run twice per arm.
The second run is the measured one.

### Timeline (UTC)

| Time | Event |
|---|---|
| 02:04 | Both Models created; the warm lane is preempted by arm A (480). Both Local caches stage in parallel (19.0 GB each, 11 min) |
| 02:15:23 | Arm A pod starts (cold compile cache) |
| 02:18:33 | Arm A's first engine exits: `Available KV cache memory: 1.19 GiB`, 2.45 GiB needed for 32K. This is the cold-compile profile pattern; the container restarts warm |
| 02:26:48 | Controller `KVPoolRecheck` relaunches the pod (42,490 tokens, 1.30x < 1.5x) |
| 02:27:48 | Arm A Ready (warm relaunch: init engine 10.3 s); the relaunch reproduced the same pool, so it is this card's ceiling |
| 02:28–02:31 | Arm A probes ×2 |
| 02:31 | Arm A deleted over MCP; arm B set to `minReplicas 1` |
| 02:34:02 | Arm B's first engine exits the same way (1.36 GiB available) |
| 02:42:56 | Arm B Ready after the same recheck relaunch (44,650 tokens, 1.36x; init engine 11.8 s) |
| 02:43–02:46 | Arm B probes ×2 |
| 02:46 | Arm C patch (env change, so a new compile hash) → the same cold crash + relaunch → Ready at 02:49:42 (43,930 tokens) |
| 02:50–02:53 | Arm C probes ×2 |
| 02:53 | Arm B deleted; one proxied `xtx-warm` chat request (200 in 80 s) |
| 02:54:43 | `qwen38-27b-xtx-warm-canary` Ready again. **The XTX is free** |

### Measured runs (the second run of each arm)

| | Arm A 0.32.0 | Arm B 0.33.0 | B / A | Arm C 0.33, skinny off | C / B |
|---|---:|---:|---:|---:|---:|
| decode 512 (tok/s) | 69.2 | 69.8 | 1.01 | 63.5 | 0.91 |
| decode 2K | 66.6 | 66.5 | 1.00 | 59.0 | 0.89 |
| decode 16K | 63.8 | 62.7 | 0.98 | 58.4 | 0.93 |
| decode short median / 2K probe | 67.04 / 66.50 | 66.92 / 66.50 | | 60.93 / 60.61 | |
| pos0 acceptance 512 / 2K / 16K | 0.838 / 0.843 / 0.802 | 0.837 / 0.843 / 0.806 | | 0.837 / 0.793 / 0.811 | |
| pos0 overall / mean acceptance length | 0.816 / 2.38 | 0.817 / 2.38 | | 0.811 / 2.37 | |
| TTFT 512 / 2K / 16K (s) | 0.99 / 1.93 / 15.87 | 0.64 / 1.86 / 15.80 | | 0.64 / 1.87 / 15.78 | |
| soak, 4 pairs: median / min per-request decode | 56.6 / 55.7 | 56.8 / 55.9 | | 54.2 / 51.3 | |
| KV pool at 32K | 42,490 (1.30x) | 44,650 (1.36x) | +5.1 % | 43,930 | |
| greedy parity `code` / `explain` / `list` | `8174a49e…` / `f1f04ef3…` / `ad819b11…` | identical | | identical | |
| probes `42` / Birch JSON / `MAPLE-4827` | ok | ok | | ok | |

The first (warm-up) runs agree within single-sample noise:
- A: 69.7 / 66.5 / 63.8 tok/s;
- B: 69.7 / 64.6 / 63.9 tok/s;
- C: 63.6 / 60.6 / 58.4 tok/s.

The 5930k reference from row `...-s3b-ttft` was 71.8 / 66.5 / 66.1 tok/s, pos0 0.84 / 0.79 / 0.80, `parity_code`
`8174a49e`.

## Pass rule vs outcome

| Criterion (as pre-registered) | Outcome |
|---|---|
| (1) B's three greedy hashes equal A's; A's `parity_code` vs `8174a49e` is reported | **pass**: B = A on all three. A = the 5930k reference, so output is identical across the two cards and the two releases |
| (2) decode ≥ 66 tok/s at each size; where A < 66 on this capped card, ≥ 0.97 × A | **pass**: 69.8 and 66.5. At 16K, A was 63.8, so the bar is 61.9, and B made 62.7 (63.9 on its first run) |
| (3) pos0 ≥ 0.75 over the run and at each context | **pass**: 0.817 overall; 0.837 / 0.843 / 0.806 per context |
| (4) clean plugin boot line, draft loads, Ready, 0 restarts through arm C, no host GPU errors | **pass, with one deviation stated plainly**. Plugin line: `fla_autotuners=11, fla_autotune=single, segmented_runtime_tables=off, mtp_head=checkpoint`, no traceback. The 0.33 engine builds the MTP draft config through the plugin's B2 `hf_config_override` passthrough. Host `journalctl -k` 02:00–02:55Z: no SMU, fault, reset or bus errors (two benign `amdgpu` workqueue CPU-hog notices); `gpu.health=ok`. **Deviation:** each arm's first boot after a compile-hash change crashed once on the KV check before Ready, then came back through the warm restart and the controller's recheck relaunch. That includes A on 0.32 and C after its env patch. Every pod had 0 restarts from Ready through its probes. The pattern is identical on the 0.32 baseline, so I judge it a property of a cold compile at 0.96 / 32K on this card, not of the image |

## Findings

1. **0.33-rocm72 is a drop-in for 0.32 on gfx1100.**
   - Same output, same speed, same acceptance.
   - The KV pool is 5 % larger.
   - Warm short-prompt TTFT was lower on 0.33: 0.64 s against 0.99 s at 512. That is one sample per run. Arm B's
     warm-up run took 1.32 s (first-use JIT), while both arm C runs took 0.64 s.
2. **G6: keep wvSplitK.** `VLLM_ROCM_USE_SKINNY_GEMM=0` moves the 3-token MTP-verify `lm_head` (248,320 × 5,120 fp16,
   2.54 GB) and `in_proj_a/b` onto torch GEMM, and cost 9 / 11 / 7 % decode. Greedy output was unchanged.
   - 0.33.0 does not ship main's `VLLM_SKINNY_GEMM_MAX_WEIGHT_BYTES` guard (it is absent from the image's `envs.py`).
     That guard is calibrated on MI355X and routes large `lm_head` weights off wvSplitK.
   - When a release ships it, set it above 2.54 GB on gfx1100 lanes. Otherwise the bump loses part of this margin.
   - Arm C is an upper bound: it also moves the small skinny shapes. The guard alone would cost less than 7–11 %, but
     the direction is settled.
3. **This card's pool at the S4 posture is 16–20 % smaller than the 5930k's** (42,490–44,650 against 52,932 tokens), and
   `KVPoolShort` fires on every boot. The card has the same SKU and the same 0.96. The XTX also exposes the iGPU, but
   `hipVisibleDevices` pins the dGPU, and the only other KFD process on it held 2 MB. This is not a 0.33 issue. It
   matters if the S4 posture ever runs on the XTX (D1 in-pod, or a failover twin): two 16K requests still fit, two 32K
   requests do not.
4. **The cold-compile first boot fails the 32K KV check on this card**: 1.2–1.4 GiB available, 2.45 GiB needed.
   - It costs one restart plus a recheck relaunch, about 3–4 min per compile-hash change.
   - The S4 workhorse on the 5930k has the same exposure after any image or env change, so a roll should warm the
     compile cache first or expect the relaunch.
5. **The live controller (`20261102-144015`) predates !1281.** It ignored `vllmVersion`.
   - Arm B ran with the two dead env names `VLLM_ROCM_USE_AITER_PAGED_ATTN` and `VLLM_ROCM_USE_AITER_FP4_ASM_GEMM`,
     which only produced warnings.
   - It also ran with `--mamba-cache-mode align`. 0.33.0 warns that the flag has no effect, because Mamba prefix caching
     follows `--enable-prefix-caching`.
   - Master's renderer drops exactly those three for B. **A controller roll with !1281 must precede any 0.33 lane.**
6. **The controller launches `python -m vllm.entrypoints.openai.api_server`, which 0.33 deprecates**, with the message
   "Please use `vllm serve`". It still works on 0.33. Move it before 0.34.

## Follow-ups (not done here)

- Roll the controller to a master build that includes !1281 before repinning anything to 0.33. That is an owner window.
- KT-QOS and KT-SO canaries: repin their debug manifests to `runtime@29378fe0` with `vllmVersion: "0.33.0"`.
  - The image is prewarmed on the XTX only. Their manifests target the 5930k, which had 194 GB free, below the 200 GB
    rule. Either prewarm it there after freeing space, or run those canaries on the XTX slot, where the pool is
    smaller (finding 3).
- G6: record the `VLLM_SKINNY_GEMM_MAX_WEIGHT_BYTES` override for gfx1100 lanes in the next bump's checklist.
- Controller: switch the vLLM launch to `vllm serve` before 0.34. A CI publish job for `gfx1100-serving-v033` is
  needed if the image becomes a promotion target.
- `Dockerfile.runtime-gfx906` still floats `diffusers>=0.30.0` (the gfx906 half of B1).

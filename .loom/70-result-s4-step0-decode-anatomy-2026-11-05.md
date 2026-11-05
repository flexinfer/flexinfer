# Result: S4 step 0, decode-step anatomy and plugin flags Q2 / Q3 (2026-11-05)

Slice S4 "step 0" of `10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` §5.3: where does
the Qwen3.8-27B workhorse's decode step spend its time on gfx1100, and do the S2 plugin flags earn
their place? Measured on a canary only; the 5930k workhorse and the XTX layout were not changed.
Rules were pre-registered in `60-validation-matrix.md` (rows
`gfx1100-s4-step0-decode-anatomy-2026-11-05` and `gfx1100-s4-plugin-flags-q2-q3-2026-11-05`,
commit `07c76f759`, pushed 2026-11-05 ~00:45Z) before any data.
Raw evidence (bench JSON, the four traces and their analyses, jit sweeps, TTFT and verify runs,
pod logs) is local-only under `/Users/cblevins/workspace/.loom/local/flexinfer/s4-step0-2026-11-05/`.

**Verdicts.**

| Rule (pre-registered) | Measured | Verdict |
|---|---|---|
| **G1** draft lm_heads ≥ 12 % of step GPU time (c = 1, 2K) | **17.0 %** (5.42 of 31.85 ms busy) | **go: S5 proceeds** |
| **G4** Σt(M=4) ≤ 0.95 × Σt(M=3) (and 8 vs 6) | **1.08×** (8 vs 6: **1.10×**) | **no-go: Q1 does not ship** |
| **G5** wvSplitK n = 1 < 75 % of the read ceiling | **1.00×** (935 vs 934 GB/s) | **no-go: nothing to send upstream** |
| Fusion target: GPU idle ≥ 10 % of the c = 1 step | **16.0 %** profiled, **10.7 %** against the unprofiled period | **written up** (§4) |
| **Q2** zero `jit_monitor` lines in the first 100 requests after a cold boot | **16** lines (13 segmented + 3 sampler), against **15** without the flag | **fail: no-go as shipped**; cause found (§5), plugin fix first |
| **Q3** 16K prefill ≥ 3 % faster, boot ≤ +60 s | 16K TTFT **1.004×** the baseline (16.010 vs 15.940 s); boot **+24 s** | **fail: no-go** |

Plus one finding outside the G list: the RDNA3 W4A16 GEMMs are the step's largest cost (62 % of GPU
time) and read at only 63–73 % of the bandwidth ceiling (§3).

## 1. Setup

| Item | Value |
|---|---|
| Canary | `qwen38-27b-s4-step0-canary` (`deploy/debug/qwen38-27b-s4-step0-canary.yaml`), applied by the operator 05:32Z, deleted 06:44:30Z |
| Card | cblevins-7900xtx, borrowed from the warm lane at priority 520 with `gpu.forcePromotion`; PCIe Gen3 x8, 294 W power cap (rocm-smi) |
| Image | `runtime@sha256:50d3f899…` = `rocm-gfx1100-serving-v032-511a9f68` (pipeline 37714, job 467912): vLLM 0.32.0+rocm723, torch 2.13, HIP 7.2.53211, text plugin 0.10.0, plugin-common 0.3.0. Same `vllm/vllm-openai-rocm:v0.32.0` base and kernels as the workhorse's `b06b06fe`. Master pushes auto-canceled the waiting publish job three times; a watcher re-queued it |
| Posture | The workhorse recipe: `ROCM_SEGMENTED_ATTN`, fp16 KV, MTP K = 2, 32K, 0.96, `maxNumSeqs 2`, `FULL_AND_PIECEWISE` graphs, prefix caching align / bf16 SSM; `vllmVersion` 0.32.0, `cudagraphMetrics`, torch profiler and `--jit-monitor-verbose` through the command wrapper |
| Same outputs as production | Greedy parity hash `8174a49e` in every run, the workhorse's promotion hash; probes `42` / Birch / `MAPLE-4827` |

Arms, all by `flexinfer_update_model` config merge patches on the one Model:

| Arm | Patch | Pod start → Ready |
|---|---|---|
| H (hold) | as applied, `S4_MODE=hold`: no engine | 05:41:50 (cache staged 05:32–05:41) |
| A0 cold boot, no flags | `S4_MODE: null` | first container failed at KV allocation (below); restart 05:47:11 → 05:54:58 = **7 m 47 s** (segmented autotune 105 workloads, 379 s) |
| A1 warm reboot, no flags | `S4_ARM: b0-warm` | 06:08:40 → 06:09:40 = **60 s** |
| Q3 | `FLEXINFER_FLA_AUTOTUNE: relaxed` | 06:17:12 → 06:18:36 = **84 s** |
| Q2 | `FLEXINFER_SEGMENTED_RUNTIME_TABLES: 1`, fresh caches (`S4_CACHE_SUBDIR: q2-cold`) | first container failed at KV allocation as A0's did; restart 06:30:11 → 06:35:27 = **5 m 16 s** (autotune 199 s + winner precompile 29.8 s) |

## 2. Decode on this card

Unprofiled, `scripts/verify-vllm-mtp.py --phase measure --contexts 512,2048,16384`, three runs on A0:

| Context | Decode tok/s (3 runs) | Mean acceptance length | Position-0 acceptance | TTFT s |
|---|---|---|---|---|
| 512 | 70.39 / 69.74 / 69.74 | 2.45–2.48 | 0.84 | 0.46–0.56 |
| 2,048 | 66.48 / 66.50 / 66.50 | 2.370 | 0.843 | 1.94 |
| 16,384 | 60.95 / 61.48 / 61.49 | 2.31–2.34 | 0.75–0.79 | 15.8–15.9 |

That is 3 % below the workhorse's S4 numbers at 2K (68.5 on the 5930k) and 7 % at 16K (65.9).
Unprofiled step period at 2K = 2.3704 / 66.49 tok/s = **35.65 ms**.

## 3. Kernel microbench (hold arm, 05:42Z, two runs within 2 %)

`scripts/bench-gfx1100-decode-kernels.py`, HIP-event medians, inputs random.

- **Read ceiling 934.4 GB/s** (Triton streaming read over 2 GiB; fp32-accumulated `sum` 922.7,
  `amax` 917.2; device copy 770 GB/s read + write). The spec peak is 960 GB/s.
- **lm_head, fp16 [248320, 5120] = 2.54 GB.** `wvSplitK` at n = 1–5: 2.70–2.74 ms = 929–940 GB/s,
  0.99–1.01× the ceiling. `F.linear` (hipBLASLt) at n = 1–6: 3.60–3.65 ms ≈ 700 GB/s. vLLM routes
  n = 6 (the verify with two requests) to `F.linear`, because `wvSplitK` stops at n = 5.
- **`gptq_gemm_rdna3`**, the six GPTQ GEMMs vLLM 0.32 runs (the research's "seven layer shapes"
  counted in_proj_qkv and in_proj_z, which 0.32 merges into in_proj_qkvz), weights rotated past
  the 96 MB Infinity Cache:

| GEMM (K → N) | Calls / forward | MB | t(M=1) µs | t(M=3) | t(M=4) | t(M=6) | t(M=8) | GB/s at M=1 / 3 |
|---|---|---|---|---|---|---|---|---|
| attn qkv_proj 5120 → 14336 | 16 | 38.1 | 65.7 | 72.8 | 84.7 | 87.9 | 91.7 | 580 / 524 |
| attn o_proj + gdn out_proj 6144 → 5120 | 16 + 48 | 16.3 | 33.6 | 34.8 | 39.4 | 45.3 | 52.5 | 486 / 470 |
| gdn in_proj_qkvz 5120 → 16384 | 48 | 43.6 | 64.4 | 72.6 | 84.7 | 92.0 | 99.1 | 677 / 600 |
| mlp gate_up 5120 → 34816 | 64 | 92.6 | 131.9 | 141.5 | 144.8 | 168.6 | 183.1 | 702 / 654 |
| mlp down 17408 → 5120 | 64 | 46.3 | 70.4 | 84.6 | 92.2 | 105.8 | 121.1 | 658 / 547 |
| **Σ per target forward (12.64 GB)** | 256 | | **19.24 ms** | **21.35** | **23.11** | **26.28** | **29.05** | **657 / 592** |

Σt(M=4) / Σt(M=3) = **1.082** and Σt(M=8) / Σt(M=6) = **1.105** (run 1: 1.082 / 1.104). The
microbench times each call alone, so it carries a few µs of launch cost per call that the graph
replay does not (in the trace the same 256 M=3 GEMMs plus 4 for the MTP layer take 18.83 ms,
against 21.35 ms for the 256 here); that constant pulls both ratios toward 1, so the real padding
penalty is at least this large.

## 4. Decode-step anatomy (A0, 06:05–06:06Z)

Torch-profiler traces of exactly 20 engine steps of pure decode at a 2,048-token prompt
(`scripts/probe-vllm-decode-step.py profile`, after the 100-request warm sweep and the verify runs),
two traces per concurrency, 19 complete steps each; the two traces agree within 0.03 ms. Graph
replays carry per-kernel events (1,470 kernels per step), so no eager fallback was needed. The
profiled c = 1 period (37.91 ms) is 6.3 % above the unprofiled one (35.65 ms), inside the 15 % bound.

| Class (median per step) | c = 1 ms | % of busy | c = 2 ms | % of busy |
|---|---|---|---|---|
| GPTQ GEMMs (`gemm_q4_kernel_rdna3`) | 19.68 | 61.8 | 24.49 | 62.6 |
| **draft lm_heads (2×)** | **5.42** | **17.0** | 5.40 | 13.8 |
| target lm_head | 2.71 (wvSplitK n = 3) | 8.5 | 3.62 (hipBLASLt, n = 6) | 9.2 |
| Inductor / ATen elementwise, norms | 1.92 | 6.0 | 1.86 | 4.8 |
| segmented attention | 0.77 | 2.4 | 1.13 | 2.9 |
| GDN (gating delta rule, conv1d) | 0.70 | 2.2 | 1.04 | 2.7 |
| other skinny / BLAS GEMMs (in_proj_ba, MTP fc) | 0.49 | 1.5 | 1.43 | 3.7 |
| sampling, other | 0.16 | 0.5 | 0.17 | 0.4 |
| **GPU busy** | **31.85** | | **39.14** | |
| **GPU idle** | **6.06 (16.0 % of 37.91)** | | **6.48 (14.2 % of 45.62)** | |

- **The draft heads are what the research inferred**: two full-vocabulary `wvSplitK` calls of
  2.71 ms each, at the bandwidth ceiling. Pruning to V′ = 32,768 (335 MB) would take them to
  ~0.36 ms each and save ~4.7 ms of the 35.65 ms unprofiled step: about +15 % decode at equal
  acceptance (S5's stage-B bar is +8 % at 2K).
- **The body is the bigger lever.** 260 GPTQ calls per step (256 target + 4 for the first draft
  step's MTP layer, at M = 3, template `<half, 2>`) read ~12.83 GB in 18.83 ms: ~681 GB/s, 73 % of
  the ceiling. At 90 % they would take ~15.3 ms, ~3.5 ms per step. This is a compiled kernel
  (`csrc/rocm/q_gemm_rdna3.cu`), outside the plugin contract unless a plugin-side Triton W4A16
  GEMV can beat it in the microbench first.
- **c = 2** costs 45.6 ms per step for two streams. The verify `lm_head` at n = 6 runs on hipBLASLt
  (3.62 ms, 0.9 ms more than `wvSplitK` would take) and `in_proj_ba` at M = 6 also falls to
  hipBLASLt plus a GSU reduce kernel (48 + 48 launches).

**Fusion target (rule met).** The idle is not host time between steps (the lead gap is 0.017 ms):
it is 1,468 dispatch gaps per step inside the graph replay, 4.1 µs each. Charged to the kernel that
waited (c = 1, ms per step): `FillFunctor<int>` 0.95 (265 launches), the GPTQ GEMM itself 0.95
(260), ATen `elementwise_kernel_manual_unroll` 0.52 (144), Inductor fused norm / SiLU / zeros
kernels 1.60 (278), GDN conv / gating / cat 0.53 (144), `__amd_rocclr_copyBuffer` 0.27 (70),
`wvSplitK` for `in_proj_ba` 0.19 (48). The one
cheap, mechanical cut is the zero-fill: `launch_gemm_q4_deterministic` allocates its split-K arrival
tickets with `at::zeros` on every fp16 call (`q_gemm_rdna3.cu` ~v0.32 lines 880–890), although the
kernel already re-arms each ticket to 0 after use (line 733). A persistent per-stream ticket buffer
removes 260 fill kernels and their gaps, ~1.3 ms per step (~3.4 %). Upstream PR (compiled code);
the remaining ~4.5 ms is launch count, which only fewer, larger kernels remove.

## 5. Plugin flags (arm B)

### Q3: `FLEXINFER_FLA_AUTOTUNE=relaxed` (G10)

| | A1 baseline (warm reboot) | Q3 (same cache) |
|---|---|---|
| Plugin boot line | `fla_autotune=single` | `fla_autotune=relaxed`, `fla_autotuners=11` |
| Container start → Ready | 60 s | 84 s (**+24 s**, bar ≤ +60 s: pass) |
| 16K server TTFT, 6 reps (rep 1 is the warm-up) | 15.945 / 15.944 / 15.948 / 15.940 / 15.936 / 15.923 | 16.003 / 16.003 / 16.006 / 16.010 / 16.032 / 16.123 |
| Median of reps 2–6 | **15.940 s** | **16.010 s** = **1.004×** (bar ≤ 0.97×: **fail**) |
| KV pool | 42,490 tokens | 41,050 tokens |
| Parity hash, probes | `8174a49e`, coherent (A0) | `8174a49e`, coherent |

Both arms ran the same 100-request sweep before the TTFT reps. No Triton autotune line was printed
after the JIT monitor armed, so the 119-config search ran inside warmup (the +24 s). The GDN
autotune keys hold model constants only, so the one winner per kernel picked on the warmup's
shape serves every prefill; on this card it does not beat the single collapsed config. Prefill at
16K stays ~1,030 tokens/s either way.

### Q2: `FLEXINFER_SEGMENTED_RUNTIME_TABLES=1` (G7)

| | A0 (no flag, cold) | A1 (no flag, warm) | Q2 (flag, cold) |
|---|---|---|---|
| Caches at boot | fresh hostPath | A0's | fresh `q2-cold` subdirectory (autotune table written to `/cache/compile/vllm/q2-cold/…`) |
| Plugin boot line | `segmented_runtime_tables=off` | off | `on`; `segmented runtime tables: block-table width 80 (page 416), compiled 105 tuned winners (0 skipped) in 29.8s` |
| `jit_monitor` lines, first 100 requests | **15** (12 `_segmented_attention_stage`, 3 sampler) | **15** (same set) | **16** (13 `_segmented_attention_stage`, 3 sampler) |
| Sweep window | 05:55:27–06:00:46Z | 06:09:57–06:15:13Z | 06:35:48–06:41:07Z |
| Parity, 2K decode | `8174a49e`, 66.49 tok/s | — | `8174a49e`, 66.77 tok/s |

The control discriminates (15 lines without the flag), and the flag removes none of them, so Q2 fails as
pre-registered. Three findings explain it:

1. **Width was not the miss on this posture.** Every runtime variant already compiles at `T0 = 80`, the
   runtime width (page 416, because the MTP draft pushes the hybrid block from 400 to 416).
2. **12 of the 13 runtime variants are exactly a precompiled winner** on every tuned field (`QCAP`,
   `BM`, `BN`, `BK`, `SPLITS`, `PV_SPLIT`, `QK_PIPELINE`; compared against the q2-cold tuning table's
   105 records), and they still compiled or loaded on first use. The 13th is a winner the fresh
   tuning picked differently. All 13 are prefill stages (`QCAP` 64–4096, `SPLITS = 1`).
3. **The miss is Triton's pointer specialization.** The verbose key shows the live launch passes K and V
   as `('*fp16', 'D')` (16-byte aligned, no 32-bit pointer range), because they are views of the
   3.2 GiB KV cache, while Q and the tables are `'DS'`. `_compile_winners` compiles on
   `_make_inputs`' synthetic cache (at most ~136 MB at 32K), so its K and V are `'DS'` and every
   precompiled kernel has a different specialization key from the one traffic launches.

**Fix (plugin-common, not yet made):** compile the winners with K / V as views of the runner's real
per-layer KV cache tensors (the plugin already reads the runner for the widths), or with vLLM's
compile-only tensor metadata (`jit_warmup_triton_helper`, which models `ptr_range`) set to the
cache's range. The three sampler kernels (`_rejection_kernel`, `_resample_kernel`,
`_compute_local_logits_stats_kernel`) JIT on the first request whatever the flag; a re-test bar of
"zero lines" also needs them warmed (one MTP request inside warmup), or should count
`_segmented_attention_*` only and say so before the data. The flag costs 29.8 s of boot today.

## 6. Things this window found that are not rules

1. **A cold torch.compile boot of this posture cannot fit 32K on the XTX.** The first A0 container
   profiled 1.19 GiB for KV (2.45 GiB needed) and exited; the container restart, with the Inductor
   cache written, profiled 3.2 GiB = 42,490 tokens (1.30× at 32K). The workhorse's own status shows
   the same pattern (two `KVPoolRecheck` relaunches). The warm pool is ~20 % below the workhorse's
   52,932 on the 5930k (17.78 GiB weights + non-torch, 2.05 GiB peak activations, 1.15 GiB graphs).
   At 1.30× the controller's KVPoolShort recheck would delete the first Ready pod of each arm, so
   `kvPoolRecheck: false` was patched in before A0 turned Ready.
2. **vLLM 0.32 arms `jit_monitor` only after warmup**, and without `--jit-monitor-verbose` it logs
   each kernel function once (`warning_once`); the canary ran verbose. Triton disk-cache loads log
   like compiles: A0 (cold) and A1 (warm) both logged 15 events in the first 100 requests.
3. Three of those 15 are MTP sampler kernels (`_rejection_kernel`, `_resample_kernel`,
   `_compute_local_logits_stats_kernel`), all on the first request; the other 12 are
   `_segmented_attention_stage` at block-table width `T0 = 80` (`PAGE = 416` with the MTP draft).

## 7. Cleanup

- The canary Model is deleted (06:44:30Z); the controller ran its tmpfs cleanup Job. The XTX went
  straight to S6's `qwen38-27b-octave-kv-canary` (480 > the warm lane's 300), whose window follows;
  S6's end-of-window `xtx-warm` request hands the card back to the warm lane.
- Left on cblevins-7900xtx (217 GB free before the window): the Local cache
  `/var/lib/flexinfer/models/flexinfer-system/qwen38-27b-s4-step0-canary` (~18 GB) and the compile
  cache `/var/lib/flexinfer/compile-cache-qwen38-27b-s4-step0-7900xtx` (base plus `q2-cold`). They are
  reusable for S5's canary if it keeps this Model name and posture; otherwise remove both.
- The `rocm-gfx1100-serving-v032-511a9f68` image (`50d3f899`) is now on the XTX node, not in any
  prewarm list.

## 8. Go / no-go

| Item | Decision | Next |
|---|---|---|
| **S5** (G1, pruned-vocabulary draft head) | **go** | Stage A offline coverage at V′ = 32,768, then stage B on a canary, as research §5.3 candidate 1 |
| **Q1** (G4, row padding 3→4, 6→8) | **no-go** | Padding makes the scalar kernel slower; drop Q1 |
| **G5** upstream wvSplitK sweep | **no-go** | Already at the ceiling for n ≤ 5. Lower-priority upstream ask: `wvSplitK` for n = 6–8 (c = 2 verify falls to hipBLASLt, +0.9 ms) |
| Fusion target | **written up** (§4) | Upstream PR: persistent split-K tickets in `gptq_gemm_rdna3` (~3.4 %) |
| New: W4A16 body at 63–73 % of the ceiling | **research item** | Microbench a plugin-side Triton W4A16 GEMV against `gptq_gemm_rdna3` at M = 1 / 3; only a kernel that wins there is worth a canary |
| **Q2** (segmented runtime tables) | **no-go as shipped** | Plugin-common fix: precompile against the live KV cache's pointer specialization (§5); re-test with the same jit sweep, sampler kernels warmed or excluded by a rule written first |
| **Q3** (relaxed FLA autotune) | **no-go** | 0.4 % slower at 16K for +24 s of boot; leave `FLEXINFER_FLA_AUTOTUNE` unset (default `single`) |

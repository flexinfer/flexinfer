# Result: GPTQ-RDNA3, the W4A16 GEMM on the gfx1100 workhorse (2026-11-06)

Work item from S4's decode-step anatomy
(`.loom/70-result-s4-step0-decode-anatomy-2026-11-05.md`). The RDNA3 W4A16 GPTQ
GEMMs are 62 % of the Qwen3.8-27B decode step's GPU time on the 7900 XTX and
read at 63–73 % of the 934 GB/s ceiling.

- **Piece 1**, the per-call split-K ticket fill: a fix, its carry into our
  image, and an upstream PR draft.
- **Piece 2**, read efficiency: investigated without a GPU. The cause is
  structural, and a candidate kernel is ready to measure.

Rules were pre-registered in `60-validation-matrix.md` before any GPU data, as
rows `gfx1100-gptq-rdna3-tickets-2026-11-06` and
`gfx1100-gptq-rdna3-read-efficiency-2026-11-06`.

**Status: code done, GPU validation pending.** The XTX queue puts this work
last: owner window phase D (through D4 KT-SWAP), KT-QOS2, the workhorse-v2
promotion canary (!1311), S5, then this. Nothing live has changed.

| Item | State | Evidence |
|---|---|---|
| Where the kernel lives | **Upstream vLLM** `csrc/rocm/q_gemm_rdna3.cu`, shipped in the stock `vllm/vllm-openai-rocm` wheel. Not a FlexInfer patch: our serving image installs that wheel unmodified (`build/Dockerfile.runtime-serving-v032`). The file is byte-identical in v0.32.0, v0.33.0 and `main` (`5df63a72`) | §1 |
| Piece 1 fix | Written: a per-device ticket pool zeroed once. It reuses the slot scheme `skinny_gemms.cu` already ships for `wvSplitKrc` | §2 |
| Piece 1 carry | `build/vllm-rdna3-w4a16-plugin`, compiled in the serving image, swaps the kernel in at runtime. **Off** unless a lane sets `FLEXINFER_RDNA3_W4A16=tickets` | §2.2 |
| Piece 1 numerics | **Bit-identical by construction, checked without a GPU.** The vendored GEMM kernels disassemble to the same code as vLLM's `_rocm_C`: 9/9 on 0.32.0 and 0.33.0. The build verifier enforces this on every image build | §2.3 |
| Piece 1 speed | **pending** (XTX slot): microbench T1–T4, then canary arms A/B/A/B; bar ≥ +3 % decode at 2K | row `gfx1100-gptq-rdna3-tickets-2026-11-06` |
| Piece 2 cause | Structural. Split-K over 256-K slices, FP32 partials (+18 % bytes at M = 3), a serial end-of-launch reduce, M = 3 run as two `M_COUNT = 2` tiles over the same weights, and 12 waves/SIMD at `M_COUNT = 2` | §3 |
| Piece 2 candidate | `gemm_q4_kernel_dot2` from vllm-project/vllm#57925 (JartX's tracking branch), carried as plugin mode `dot2` with group alignment relaxed. 31–70 VGPRs = 16 waves/SIMD | §3.3 |
| Piece 2 speed | **pending** (same microbench run): D1–D4; target ≥ 85 % of the ceiling at M = 3 on the four largest shapes | row `gfx1100-gptq-rdna3-read-efficiency-2026-11-06` |
| Upstream PR | **Drafted, not posted** (owner's call): `build/vllm-rdna3-w4a16-plugin/upstream/` | §4 |

## 1. Where the code lives

`gptq_gemm_rdna3` is vLLM's own op, registered by `csrc/rocm/torch_bindings.cpp`
in `_rocm_C`. The source files are `csrc/rocm/q_gemm_rdna3.cu`, its WMMA sibling
for M ≥ 12, and `qdq_4_rdna3.cuh`. The RDNA3 kernels' author is JartX: #41394
(native kernel), #54706 (deterministic split-K), #44075 (MoE).

Since the 0.32 move (row `xtx-vllm-v032-2026-10-24`), our serving images run
the stock wheel. That move dropped the from-source rebuild, and the
runtime-patch contract forbids bringing it back. So "carry the patch until it
lands" cannot mean rebuilding vLLM. §2.2 is how it is carried instead.

Upstream state on 2026-11-06:

- **Draft vllm-project/vllm#57925** ("RDNA3 full inference stack — tracking
  branch for splitting into landable PRs", JartX) touches `q_gemm_rdna3.cu`
  twice:
  - a `__device__ int g_w4_tickets[65536]` global replaces the per-call fill,
    with the comment "Two scalar W4A16 GEMMs must not run concurrently on one
    device";
  - a new `gemm_q4_kernel_dot2` for fp16 M = 1..10, which removes split-K
    entirely (§3.3).
- Neither change has its own landable PR yet. #57925 says each piece will go
  out separately on request.

## 2. Piece 1: persistent split-K tickets

### 2.1 The change

The fp16 epilogue needs one arrival ticket per (x, y) tile, zero when a launch
starts, and it re-arms every ticket it used to zero. The launcher nevertheless
allocated them with `at::zeros`: a `FillFunctor<int>` kernel and a dispatch gap
in front of each of the 260 GEMMs per decode step.

The patch (`csrc/q_gemm_rdna3.cu` `split_k_tickets`, +122 −26 lines, all host
code) keeps the tickets in one per-device pool of 16 slots × 8192 ints:

- **Pool creation.** The pool is created and zeroed on the first call that is
  not being captured, and that stream is synchronized once, so no other stream
  can get a slot the fill has not cleared.
- **Eager streams** take one of 8 slots on first use and keep it. Launches on a
  stream are ordered; launches on different streams never share a slot.
- **Capturing streams** take one of 8 reserved slots. Nothing is allocated or
  enqueued for the tickets during capture.
  - Graphs captured on one stream share their slot. Their replays never overlap:
    vLLM replays one graph per step, and CUDA serializes launches of one graph.
- **Fallback.** Anything else gets the old per-call `at::zeros`, which stays
  correct even inside a capture. That covers no pool yet during capture, all
  slots taken, and a launch needing more than 8192 tiles. The largest need on
  this model is 68 tiles.

This is the scheme vLLM already ships for `wvSplitKrc` in `skinny_gemms.cu`,
with one difference: every miss falls back instead of raising.

**Why not #57925's `__device__` global.** vLLM 0.33 runs some linears on a
second stream: `maybe_execute_in_parallel` in LoRA's base linear and in the
DeepSeek-V4 and Inkling AMD MoE paths. One global ticket array shared by two
concurrent GEMMs would silently corrupt their reductions.

**Why not one buffer per capture created inside the capture.** Its zero fill
would exist only as a node in the first graph. A second graph sharing the slot
could replay before that graph ever has, and would read garbage tickets.

### 2.2 How it is carried: `build/vllm-rdna3-w4a16-plugin`

- `csrc/q_gemm_rdna3.cu` is vLLM v0.33.0's file plus the patch.
- `setup.py` compiles it into the package's own op library with `_rocm_C`'s
  flags: RelWithDebInfo `-O2 -g`, `-std=c++20`, torch's HIP flags and vLLM's
  four extra defines.
- Macros rename `vllm::` to `flexinfer_rdna3_vllm::` and the entry points, and
  the library links with `-Bsymbolic`. Nothing can collide with `_rocm_C`'s
  symbols, and in a trace the kernel shows up as
  `flexinfer_rdna3_vllm::gptq_rdna3::gemm_q4_kernel_rdna3<__half, 2>`.
  `probe-vllm-decode-step.py` still classifies it as GPTQ.
- M ≥ 12 forwards to vLLM's own `_rocm_C::gptq_gemm_rdna3_wmma` through the
  dispatcher.
- The general plugin `flexinfer_rdna3_w4a16` runs in every vLLM process. When
  `FLEXINFER_RDNA3_W4A16` is `tickets` (or `dot2`), it registers the library's
  function as the **CUDA kernel of `_rocm_C::gptq_gemm_rdna3`**, through a
  `torch::Library` IMPL owned by the library. vLLM's schema and fake kernel
  stay, so torch.compile and CUDA graphs see the same op.
  - The plugin reads the dispatch table back and raises if the swap did not
    take.
  - Unset or `off` does nothing.
  - Any other value fails plugin loading, so a typo cannot turn a B arm into a
    copy of A.
  - The plugin is version-gated to vLLM 0.32.0 and 0.33.0.
- In serving, rollback needs no image change: unset the env.
- The image build compiles it in about 30 s, after the native-W4A16 verifier
  (`build/Dockerfile.runtime-serving-v032`, shared by the 0.32 and 0.33
  personas). It is on the CI change lists of both publish jobs and in
  `scripts/check-runtime-patch-contracts.py`.

### 2.3 What is already proven without a GPU

All runs used the cached serving images on cblevins-7900xtx under
`ctr run --rm` (CPU only, no network): `29378fe0` (0.33.0) and `527e2714`
(0.32.0).

- **Same compiler as the wheel.** The stock and the rebuilt gfx1100 code
  objects both name AMD clang 22.0.0git, roc-7.2.3, commit `f58b06dc`.
- **Flags matter.** A first build at `-O3` produced different code: same
  source, different scheduling, and different unrolling at `M_COUNT = 8`
  (172 → 174 VGPRs, +20 instructions). With vLLM's RelWithDebInfo flags, all 9
  scalar kernels are instruction-for-instruction identical to the wheel's:
  - `gemm_q4_kernel_rdna3<{__half, __hip_bfloat16}, {1, 2, 4, 8}>` and
    `reduce_partials_rdna3<bf16>`;
  - the comparison is normalized disassembly hashes, with symbol names and
    comments dropped;
  - VGPR, SGPR and LDS match too.

  The patch touches only host code, so the persistent-ticket op computes vLLM's
  exact bits.
- **Enforced on every build.** `build/scripts/verify_vllm_rdna3_w4a16_plugin.py`
  re-proves this, and fails a build whose base kernel moved. It passed on both
  images: 0.33.0 in 9.6 s, 0.32.0 in 10.6 s.
- **Override round trip.** Next to the real `_rocm_C`, `install(1)` → our
  kernel active, then `install(2)`, then `install(0)` → `torch_bindings.cpp:14`
  active again. The dispatcher dump shows vLLM's kernel kept as
  `CUDA (inactive)`.
- **GPU-less hosts.** `register()` with `tickets` on a host with no GPU (the
  API-server case) exits cleanly.
- **Tests.** 16 CPU unit tests pass (`build/scripts/test_vllm_rdna3_w4a16_plugin.py`),
  and so does the contract checker with its script tests.

Still to measure on the GPU (row `gfx1100-gptq-rdna3-tickets-2026-11-06`):

- T1, bit equality: the static proof says yes; this checks the build and the
  pool;
- T2, the four graph and stream safety cases;
- T3, the fill census;
- T4, the forward-graph replay time;
- then the canary decode delta, parity and fill count per step.

**Expected size:** about −1.3 ms per 2K step, i.e. +3–4 % decode. That is the
265 fills and the gaps in front of them, from S4's trace attribution.

## 3. Piece 2: why 63–73 % of the read ceiling

### 3.1 Static facts (shipped code objects, gfx1100 wave32)

The 7900 XTX has 48 WGPs and 1,536 VGPRs per SIMD lane, allocated in granules
of 24, up to 16 waves per SIMD. Every block is 256 threads (8 waves) covering
1,024 columns × 256 K.

| fp16 `M_COUNT` | used for M | VGPRs | waves/SIMD | resident blocks | LDS |
|---|---|---|---|---|---|
| 1 | 1 | 84 | 16 | 384 | 532 B |
| 2 | 2–3 | 98 | **12** | 288 | 1,060 B |
| 4 | 4–7 | 133 | 10 | 240 | 2,116 B |
| 8 | 8–11 | 172 | 8 | 192 | 4,228 B |

Per-shape grid and the cost of split-K. Grid = ⌈N/1024⌉ × ⌈M/`M_COUNT`⌉ ×
K/256. "×res" is the grid as a multiple of the resident blocks. Partials are
the FP32 bytes written plus read back, as a share of the weight bytes. GB/s is
S4's isolated per-call rate.

| GEMM (K → N) | weights | M = 1: blocks (×res), GB/s | M = 3: blocks (×res), partials, GB/s |
|---|---|---|---|
| qkv 5120 → 14336 | 38.1 MB | 280 (0.73), 580 | 560 (1.94), +18 %, 524 |
| o_proj / out_proj 6144 → 5120 | 16.3 MB | **120 (0.31)**, 486 | 240 (0.83), +18 %, 470 |
| in_proj_qkvz 5120 → 16384 | 43.6 MB | 320 (0.83), 677 | 640 (2.22), +18 %, 600 |
| gate_up 5120 → 34816 | 92.6 MB | 680 (1.77), 702 | 1360 (4.72), +18 %, 654 |
| down 17408 → 5120 | 46.3 MB | 340 (0.89), 658 | 680 (2.36), +18 %, 547 |

### 3.2 Where the time goes

A least-squares fit of S4's isolated per-call times against weight bytes (five
shapes; residuals ≤ 5 µs) splits each call into a fixed part and a streaming
part:

| M | fixed per call | marginal streaming rate |
|---|---|---|
| 1 | 12.4 µs | 780 GB/s (83 % of 934) |
| 3 | 16.1 µs | 728 GB/s (78 %) |
| 4 | 26.4 µs | 756 GB/s |

The isolated timing includes the ticket fill and its gap (~5 µs, piece 1). S4's
graph trace (260 calls, 18.83 ms of kernel time for 12.83 GB) puts the
kernel-internal fixed part at ~4.7 µs per call, about 1.2 ms per step.

So two losses remain after piece 1:

1. **About 22 % slower streaming than the ceiling (M = 3).** Each block reads
   only 256 K (8 rounds of four 16-byte loads per thread), then writes FP32
   partials:
   - At M = 3 the partials are 18 % of the weight bytes, written and read back
     (mostly through L2 and the 96 MB Infinity Cache, not all of it DRAM).
   - M = 3 runs as two `M_COUNT = 2` row tiles that stream the same weights
     (the second read hits L2 at best), at 12 waves/SIMD.
2. **About 4.7 µs per call of ramp and tail.** The last z-block of each tile
   sums 20–68 slices serially (one L2 round trip per 8 slices), after all other
   blocks of that tile. Because blocks dispatch x-fastest then z, every tile's
   last slice is in the final wave, so all reductions land at the end of the
   launch. `down` (68 slices) shows it most: +4.8 µs over the fit at M = 3.
   Small grids add their own tail: o_proj at M = 1 fills 31 % of the card.

Neither loss is a tuning constant. The upstream author's own note rules out
the knobs: `BLOCK_KN_SIZE = 512` lost up to 40 % at M = 1 because it cuts the
block count. Row padding (Q1/G4) is already a no-go. Closing the gap takes a
different structure: full-K blocks, no partials, all M rows in one block,
higher occupancy.

### 3.3 The candidate: `gemm_q4_kernel_dot2` (#57925)

**Structure:**

- One 256–1,024-thread block per 64 output columns, covering all of K.
- Its 8/16/32 waves split K into contiguous parts and meet in LDS in a fixed
  order, so the result is deterministic.
- No partials in global memory, no tickets, no end-of-launch reduction.
- All M ≤ 10 rows in one block, with wave-uniform activations read through the
  scalar cache.
- The dequantization is exact: `q − zero` in fp16, v_dot2 into fp32, and the
  scale applied per group in fp32.
- Waves per block: 32 for N ≤ 2,048 at M ≤ 6, 16 up to N = 5,120, else 8.

**Changes in our copy:** `csrc/q_gemm_rdna3_dot2.cu` folds the scale at
absolute group boundaries plus once after the loop. That lets a wave's K part
start mid-group, so K = 17408 (136 groups) can use 16 waves instead of 8. When
the parts are group-aligned, the result is identical to the original's.

**Static numbers:** 31–70 VGPRs, 16 waves/SIMD at every M, no spills.

**Grids for our shapes:**

| GEMM | blocks × waves | waves |
|---|---|---|
| qkv | 224 × 8 | 1,792 |
| o_proj | 80 × 16 | 1,280 |
| in_proj_qkvz | 256 × 8 | 2,048 |
| gate_up | 544 × 8 | 4,352 |
| down | 80 × 16 | 1,280 |

The upstream author measured it on TP4 shapes of a 27B dense G32 model:
**−17 % W4A16 time at M = 4, −14 % at M = 5, −9 % at M = 8, −5 % at M = 10**,
with "the same result as dec16 … to 1 ulp". Nothing has been published for
M = 1–3 or for TP1 shapes. Our shapes are 4× wider and use G128.

**Not bit-identical to vLLM's kernel.** It accumulates in a different order.
So it is a measurement arm (`FLEXINFER_RDNA3_W4A16=dot2`), not a default.

**Pre-registered targets** (row `gfx1100-gptq-rdna3-read-efficiency-2026-11-06`),
measured in the same run as the read ceiling:

- D1: the 256-GEMM forward graph replays at M = 3 in ≤ 0.85× the tickets arm's
  time.
- D2: GEMM busy bandwidth ≥ 85 % of the ceiling at M = 3 on gate_up, qkvz,
  down and qkv.
- D3: no regression at M = 1 or 6.
- D4: error against an fp64 reference ≤ 1.25 × vLLM's.

All four pass → serving arm C (bar +5 % decode over tickets, acceptance length
within ±1.5 %). Even then nothing ships from this row: carrying a
non-bit-identical kernel ahead of upstream is an owner decision.

If it passes, the 2K step would gain up to ~2.7 ms beyond piece 1 (12.83 GB at
85 % of 934 GB/s = 16.2 ms, against 18.83 ms), i.e. about +8 % decode.

## 4. Upstream

`build/vllm-rdna3-w4a16-plugin/upstream/` holds the PR draft:

- `0001-rocm-rdna3-w4a16-zero-split-k-tickets-once.diff` against vLLM `main`
  `5df63a72` (4 files, +308 −31):
  - the `q_gemm_rdna3.cu` change, the same as our carry;
  - `warmup_rocm_rdna3_w4a16_tickets()` beside `warmup_rocm_skinny_gemm_workspaces()`,
    called before the KV-cache allocation;
  - `tests/kernels/quantization/test_rdna3_w4a16_tickets.py`: no fill per call,
    side-stream graph, two graphs on one stream, two eager streams, and a
    capture before the pool exists in a subprocess.
- `PR.md` with the title, the body, and a slot for the canary's numbers.

**Not posted.** Posting needs the owner's go and a DCO sign-off, and should
first be coordinated with JartX: #57925 carries the global-array variant, and
this PR explains why the slot variant is safer. The dot2 measurements, once
taken, belong in #57925's thread as data, not in this PR.

## 5. Next, in order

1. **MR (this branch).** It merges only after S5 confirms the shared Dockerfile
   edit is fine; S5 was messaged at 18:05Z. Merging publishes
   `rocm-gfx1100-serving-v033-<sha>` with the plugin installed but **off**.
   Re-queue the publish job if a later master push cancels it.
2. **Tag the image.** Record the digest, tag it `pinned-<12>`
   (`imagetools create --prefer-index=false`), and add both to both rows.
3. **Canary manifest.** Write `deploy/debug/qwen38-27b-gptq-rdna3-canary.yaml`
   at the !1311 posture with a `GPTQ_MODE=hold` wrapper (as S4's), on that
   digest. Reuse a staged Local-cache name of the same artifact.
4. **XTX slot** (after S5). Confirm "XTX free", then hand the owner the single
   apply command. Run the hold arm: `scripts/bench-gfx1100-gptq-rdna3.py`,
   about 5 min, one run for both rows. Then arms A1/B1/A2/B2 (and C if D1–D4
   pass), delete the Model, and post "XTX free".
5. **Pass.** Open a Draft workhorse MR stacked on !1311 (digest +
   `FLEXINFER_RDNA3_W4A16: tickets`), LIVE-IMPACTING, held for an owner window.
   Fill the numbers into the upstream PR draft.

Evidence from the CPU-side checks lives local-only under
`/Users/cblevins/workspace/.loom/local/flexinfer/gptq-rdna3-2026-11-06/`: the
ISA dumps and metadata, build and verify logs, and the cost-model fit.

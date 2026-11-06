# Upstream PR draft: zero the RDNA3 W4A16 split-K tickets once, not per call

Status: **drafted, not posted.** Posting to github.com/vllm-project/vllm is the
owner's call. The diff applies to vLLM `main` at `5df63a72` (2026-11-06), where
`csrc/rocm/q_gemm_rdna3.cu` is byte-identical to v0.33.0 and v0.32.0. vLLM
requires a DCO sign-off (`git commit -s`) from whoever submits it.

- Diff: `0001-rocm-rdna3-w4a16-zero-split-k-tickets-once.diff` (4 files, +308 −31).
- Our carried copy: `../csrc/q_gemm_rdna3.cu` is v0.33.0 plus exactly the
  `csrc/rocm/q_gemm_rdna3.cu` part of this diff. The Python warmup and the
  test are upstream-only: in our image the pool is created by vLLM's eager
  profile run, before any capture.
- Before posting, coordinate with the RDNA3 kernels' author (JartX). Their
  tracking branch, vllm-project/vllm#57925, solves the same fill with a
  `__device__ int g_w4_tickets[65536]` global and the comment "Two scalar W4A16
  GEMMs must not run concurrently on one device". This PR is the variant that
  keeps concurrent streams safe, using the slot scheme `skinny_gemms.cu` already
  ships for wvSplitKrc. vLLM 0.33 does overlap linears on a second stream
  (`maybe_execute_in_parallel` in LoRA, the DeepSeek-V4 and Inkling AMD paths).

## Title

[ROCm][RDNA3] W4A16: zero the split-K tickets once, not per call

## Body

### What

`gptq_gemm_rdna3`'s fp16 scalar path (gfx1100, M < 12, K > 256) allocates its
split-K arrival tickets with `at::zeros` on every call. The kernel already
re-arms every ticket it uses (`q_gemm_rdna3.cu`, end of the epilogue), so the
tickets only need zeroing once. This PR keeps them in a per-device pool zeroed
once, with the slot scheme `wvSplitKrc` uses in `csrc/rocm/skinny_gemms.cu`:

- the pool is created eagerly: on the first call that is not being captured,
  or by `warmup_rocm_rdna3_w4a16_tickets()` next to
  `warmup_rocm_skinny_gemm_workspaces()` before the KV cache allocation. It is
  synchronized once so every slot is zero before any stream can get one;
- an eager stream takes one of 8 slots on first use and keeps it. Launches on
  one stream are ordered; launches on different streams never share a slot;
- a capturing stream takes one of 8 reserved slots, so nothing is allocated or
  enqueued for the tickets during capture. Graphs captured on one stream share
  that slot; their replays do not overlap;
- otherwise the call falls back to today's per-call `at::zeros`. That covers no
  pool yet during capture, all slots taken, and more tiles than a slot's 8192.
  It is correct inside a capture too.

Unlike `wvSplitKrc`, nothing here raises: every miss falls back.

### Why

Each fill is an extra kernel launch plus a dispatch gap in front of every GEMM,
inside CUDA graphs too. On a 7900 XTX serving a Qwen3.8-27B W4A16 (AutoRound
GPTQ, g128) with MTP K=2, a torch-profiler trace of the graph-replayed decode
step at 2K context shows 265 `FillFunctor<int>` launches per step. The GPU sits
idle in 0.95 ms of dispatch gaps in front of them. Fill kernel plus gap is about
1.3 ms of a 35.65 ms step (~3.4 %).

### Correctness

The kernel is unchanged. Only where the ticket pointer comes from moves, so the
outputs are bit-identical. We built the patched file out of tree with `_rocm_C`'s
flags (RelWithDebInfo, C++20, the same ROCm 7.2.3 clang). Its 8 scalar
`gemm_q4_kernel_rdna3` instantiations and the bf16 reduce disassemble to the same
instruction streams as the v0.32.0 and v0.33.0 wheels' `_rocm_C`.

`tests/kernels/quantization/test_rdna3_w4a16_tickets.py` checks:

- no fill kernel per call once the pool exists;
- a side-stream graph replays bit-identically to eager, 25 times;
- two graphs on one capture stream, replayed alternately;
- eager calls racing on two streams;
- a capture made before the pool exists (in a subprocess).

### Measurements

[fill in from the XTX canary: decode tok/s at 2K before/after, fills per step
before/after, greedy parity unchanged]

## Not in this PR

The larger cost is that the scalar kernel reads at 63–73 % of the read
ceiling on these shapes. That is a kernel-structure question (split-K partials,
end-of-launch serial reduce, `M_COUNT=2` running M=3 twice over the same
weights), and the `gemm_q4_kernel_dot2` kernel in #57925 addresses it. Our
measurements of it on TP1 27B shapes go to that thread, not here.

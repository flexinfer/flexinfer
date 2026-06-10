# RALPH Iteration Plan — gfx906 `gptq_gemm` 2×2 kill-test

## Review

- Roadmap milestone: F5 heterogeneous 72B lane.
- Spec section(s): `.loom/brainstorm-gfx906-sdpa-distributed-prefill-2026-06-09.md` (2026-06-10 full 72B correction); `.loom/32-iteration-plan-f5-72b-3way-relaunch-2026-06-10.md` (Result).
- Prior decisions to preserve: `num_gpu_blocks_override` fixes the toy KV-alloc failure but does NOT bypass `determine_num_available_blocks -> profile_run` in vLLM 0.6.3; the full 72B blocker is the Radeon VII rank failing inside `vllm/_custom_ops.py:gptq_gemm` during Qwen2 MLP `gate_up_proj` with `HIP error: invalid argument` at ~13.4 GB weight residency.

## Riskiest assumption + kill-test

**Load-bearing assumption**: the gfx906 `gptq_gemm` failure is attributable to exactly one of two factors — (a) 72B-scale GEMM shapes (Qwen2-72B MLP `gate_up_proj`: in≈8192, out≈59136, group_size 128) exceeding a gfx906 kernel-launch or workspace limit, or (b) near-full Vega20 memory pressure (~13.4 GB resident) hitting the same VMM-less large-allocation failure the 2026-06-09 toy proved for `torch.zeros` — and a single-GPU test can separate them without a 3-node 72B window.

**Kill test**: one pod on `cblevins-radeonvii` running the unified image
(`registry.harbor.lan/flexinfer/vllm:rocm6.3.4-multiarch`), executing a 2×2 matrix with
`AMD_SERIALIZE_KERNEL=3`:

| | no fill | ~13.4 GB resident fill |
|---|---|---|
| **small shape** (1.5B-class: in 1536, out 17920) | cell A | cell B |
| **72B shape** (in 8192, out 59136) | cell C | cell D |

Each cell calls `vllm._custom_ops.gptq_gemm` (exllama path, synthetic int4 weights +
qzeros/scales/g_idx, group_size 128) directly — no Ray, no PP, no model download. The
06-09 toy harness (Qwen2.5-1.5B-GPTQ staged on llm-models-nfs + in-process fill) is the
starting point; only the op under test changes from `torch.zeros` KV alloc to `gptq_gemm`.

Pass/fail readout:
- C fails, A passes → shape-dependent: gfx906 kernel limit; fix = per-arch fallback kernel or column-split.
- D fails, C passes → pressure-dependent: same Vega20 alloc wall; fix = shard rebalance to shrink the gfx906 rank below the wall (e.g. `VLLM_PP_LAYER_PARTITION` with fewer middle layers) — KV override stays.
- C and D both fail, A/B pass → shape is sufficient; pressure framing retired for this op.
- All pass → attribution wrong; rerun the full window with `AMD_SERIALIZE_KERNEL=3` on the Radeon VII rank before any other spend.

**Failure mode if the assumption is wrong**: we build a per-arch gfx906 GPTQ fallback image (days of work) when a one-line layer-partition change would have cleared the wall, or vice versa.

**Status**: passed 2026-06-10 — verdict **PRESSURE** (see Result below; evidence `.loom/local/validation/gfx906-gptq-gemm-2026-06-10/`)

## Align

- Slice name: gfx906 `gptq_gemm` shape×pressure attribution kill-test.
- Scope in: single-GPU test pod on radeonvii (hostPath `/dev/kfd` bypass pattern if the GPU resource is held by live lanes), the 2×2 matrix above, serialized tracebacks captured to `.loom/local/validation/gfx906-gptq-gemm-2026-06-10/`, verdict doc.
- Scope out: any 3-node 72B window, image rebuilds, controller changes, layer-partition changes (those are the *outputs* of this test, not part of it).
- Acceptance criteria: all four cells produce an unambiguous PASS/FAIL with serialized kernel attribution; verdict names which factor (shape, pressure, both, neither) triggers `invalid argument`; live radeonvii lanes (`bge-large`, `bge-reranker`, `qwen3-1p7b-tools`) remain `Ready` throughout.
- Dependencies/blockers: radeonvii node Ready; enough headroom to fill ~13.4 GB for cells B/D — if bge/tools lanes hold too much HBM2, cells B/D need a brief lane scale-down (transient, restore after).

## Land

- Planned file areas: test script under `/Users/cblevins/workspace/tmp/` (window-local), evidence under `.loom/local/validation/`, verdict appended to `.loom/60-validation-matrix.md`.
- Implementation steps:
  1. Adapt the 06-09 toy harness: replace the KV-alloc op with direct `gptq_gemm` calls at the two shape points.
  2. Run cells A→C (no fill) first; only scale lanes down if B/D are needed and headroom is short.
  3. Capture serialized tracebacks per cell; restore any scaled lanes.

## Prove

- Tests to run: the 2×2 matrix itself; `kubectl get models` before/after to confirm lane state.
- Lint/static checks: `git diff --check` on doc deltas.
- CI checks: not applicable (no repo code changes expected).

## Handoff/Harvest

- Docs to update: `.loom/60-validation-matrix.md` with the cell matrix verdict; this plan's Status line.
- Agent-context entries: verdict finding + the chosen fix direction as a decision.
- Next-slice candidates: (shape-dependent) per-arch gfx906 GPTQ fallback in the unified image; (pressure-dependent) one 72B relaunch with a lighter gfx906 shard (`VLLM_PP_LAYER_PARTITION` rebalance, e.g. 29,22,29) + `num_gpu_blocks_override`.

## Result (2026-06-10, same day)

**Verdict: PRESSURE — reproduced and runtime-attributed, single-GPU, zero 72B windows.**

- Cells A/C/B/D all PASSED, including 72B shape (K=8192, N=59136) at ~2.9 GB free
  (gemm completed leaving 1.81 GB free). Shape alone is innocent.
- Ladder cells (exec'd in the same pod): **D2 (free→~1.9 GB) FAILED inside
  `ops.gptq_gemm` with `HIP error: invalid argument` — the exact window
  signature.** D3/D4 showed even a plain 0.5 GB `torch.empty` fails once it
  would push free below ~1.5 GB. D5 with `AMD_LOG_LEVEL=2` captured ROCm
  `memory.cpp:358 "Video memory allocation failed!"` at the gemm call.
- Mechanism: Vega20 (no VMM) refuses any allocation that would land free VRAM
  below a ~1.5–1.8 GB floor, surfacing `invalid argument` instead of OOM. The
  exllama reconstruct path allocates a `temp_dq` fp16 scratch of K×N×2 bytes
  (~968 MB at 72B `gate_up_proj` shape) per call; under the window's ~2.7 GB
  free minus forward transients, that alloc crossed the floor. Same wall as the
  06-09 toy's KV `torch.zeros` — one allocation later in the path.
- Fix lever for the next (and final) 72B relaunch: rebalance
  `VLLM_PP_LAYER_PARTITION` 27,26,27 → **29,22,29** (gfx906 shard 14.4 → ~12.2 GB,
  free ~4.9 GB ≫ floor + scratch), keep `--num-gpu-blocks-override 256`. Heads
  absorb +2 layers each (~+1.1 GB; head was 16.16 GB of 24 GB — fits).
- Live lanes (`bge-large`, `bge-reranker`, `qwen3-1p7b-tools`) confirmed Ready
  after teardown; probe pod + ConfigMap deleted.
- Evidence: `.loom/local/validation/gfx906-gptq-gemm-2026-06-10/{VERDICT.md,
  cells-ABCD.log,ladder-D2-D5.log,pod-run.log,manifest.yaml}`. Gotcha: neither
  `vllm:rocm6.3.4-multiarch` nor the `-serve` digest was still in the node image
  cache (GC pruned; canary is scale-to-zero) — budget ~10 min for the pull, and
  expect a possible TaintManagerEviction blip from CI contention mid-pull.

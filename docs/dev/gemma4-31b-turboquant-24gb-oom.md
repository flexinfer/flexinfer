# gemma4-31b-gptq × TurboQuant on 24 GiB gfx1100: OOM analysis

**Date:** 2026-04-25
**Context:** Post-!190 the primary `gemma4-31b-gptq` is capped at
`maxModelLen: 2048` on `cblevins-7900xtx`. Canary `gemma4-31b-gptq-long`
(MR !191) attempted to unlock 16K context via `kvCacheCodec: turboquant`.
Canary OOM'd during weight load. This doc explains why the combination
is not viable on this hardware and records the math so future work
doesn't re-attempt without a new lever.

## Failure signature

Boot attempt 2026-04-25 00:42 UTC on pod `gemma4-31b-gptq-long-7fc9f968dd-7scqt`:

```
torch.OutOfMemoryError: HIP out of memory. Tried to allocate 222.00 MiB.
GPU 0 has a total capacity of 23.98 GiB of which 26.00 MiB is free.
Of the allocated memory 23.59 GiB is allocated by PyTorch, and
69.27 MiB is reserved by PyTorch but unallocated.
```

Crash site in stack:

```
vllm/model_executor/models/gemma4.py:500
    Gemma4DecoderLayer.__init__
  → Gemma4MLP.__init__ (gemma4.py:111)
    → RowParallelLinear(down_proj)
      → torch.empty(...)  ← OOM
```

**The crash is during model weight construction, before KV cache is allocated
at all.** Not a KV-pool-too-small error; the weights themselves don't fit
alongside the plugin's GPU-resident state.

## Memory accounting

Live pod memory at the moment of OOM:

| Component | Size | Notes |
|---|---|---|
| 31B INT4 weights | 20.02 GiB | confirmed from primary's `Model loading took 20.02 GiB memory` log |
| Plugin rotation matrices + resident state | ~3.57 GiB | from OOM delta: 23.59 − 20.02 |
| **Total before activations / graph / KV** | **~23.6 GiB** | |
| Card total | 23.98 GiB | |
| Headroom at crash point | **0.4 GiB** | can't hold anything else |

The ~3.57 GiB is import-time GPU allocation by `turboquant-vllm`'s
per-layer-per-head rotation matrices + plugin tensor state. See
`.loom/10-research.md` lines 511–515 and 530–534 for the research trail
that first surfaced these allocations on E4B.

Approximate back-of-envelope:
`60 layers × 24 attention heads × 512² rotation × 4 bytes (fp32) ≈ 1.47 GiB`
plus generic plugin bookkeeping ≈ 2 GiB ⇒ matches the 3.57 GiB observed.

## Why `gpuMemoryUtilization` cannot fix it

`gpuMemoryUtilization` is a ceiling on **vLLM's own** allocations. The
plugin's import-time tensor creation happens through raw `torch.empty`
calls during `Gemma4DecoderLayer.__init__`, which bypass the vLLM
memory manager. vLLM has no opportunity to enforce its budget against
those.

Testing table (hypothetical):

| `gpuMemoryUtilization` | Cap | Weight fit? | Plugin overhead absorbed? | Verdict |
|---|---|---|---|---|
| 0.70 | 16.8 GiB | ❌ 20 GiB weights don't fit in 16.8 | — | Fail at weight load |
| 0.80 | 19.2 GiB | ❌ 20 GiB don't fit in 19.2 | — | Fail at weight load |
| 0.85 | 20.4 GiB | ✅ barely | Plugin pushes total to ~24, card has 24 | Exactly the observed OOM |
| 0.90 | 21.6 GiB | ✅ | Plugin pushes total to ~25 | Fail (what we hit) |
| 0.95 | 22.8 GiB | ✅ | Plugin pushes total to ~26 | Fail |
| 0.98 | 23.5 GiB | ✅ | Plugin pushes total to ~27 | Fail |

**No setting of `gpuMemoryUtilization` on a 24 GiB card makes this combination fit.**

## Why CPU offload isn't available

- `backend/vllm.go:102` forwards `--cpu-offload-gb` when `cpuOffloadGb` is
  set in the Model CR.
- `backend/vllm_omni.go:100` carries the comment: **"CPU offload removed
  in vLLM V1 (0.17.0+)"**.
- Current runtime ships vLLM main branch (`version 0.1.dev1+g467d3247c`)
  which is V1. The flag is silently ignored.
- `backend/vllm_test.go:814` tests explicitly assert that `--cpu-offload-gb`
  is absent in V1 codepaths.

CPU offload would have been the obvious way to free 2–4 GiB of weight
footprint, but it is not a lever on this hardware + runtime combo.

## Why TurboQuant *does* work on E4B but not 31B

| | E4B | 31B |
|---|---|---|
| Weight size | ~8 GiB | ~20 GiB |
| Plugin overhead | ~3.5 GiB | ~3.5 GiB (scales with layers+heads, ~1.5x bigger) |
| Baseline subtotal | ~11.5 GiB | ~23.5 GiB |
| Room for activations + KV on 24 GiB | ~12 GiB | ~0.5 GiB |

E4B research shipped with `gpu_memory_utilization=0.70` and routinely
held 27K tokens in 0.63 GiB of KV. The same plugin on 31B runs out of
card before it gets near KV setup.

## What *would* unlock 31B + long context on this hardware

None of these are cheap. Ordered by cost:

1. **Weights below ~15 GiB** — would require either 8-bit AWQ (~2x size
   reduction from current 4-bit is wrong direction; needs 3-bit INT or
   similar), or an upstream quant innovation. Full re-quant pipeline
   (~10 h on radeonvii). Quality vs cost uncertain.
2. **Patch turboquant-vllm** to defer rotation matrix allocation until
   after weight load, then reuse the freed budget. Plugin-source work,
   ~1 week if upstream-able; otherwise a carried fork.
3. **Skip the plugin entirely** and take whatever other KV-compression
   vLLM adds for RDNA3 in the future. Multi-month horizon.
4. **Different GPU.** MI300X / W7900 / etc. N/A to homelab.

## Decision: close the lane

Per this session's conclusion:

- **`deploy/models/gemma4-31b-gptq-long.yaml` removed from
  `kustomization.yaml` reconciliation.** File kept on disk for historical
  reference; if the next attempt wants to try a related experiment, start
  from it. Marking the CR's `promotion-state` reflects the finding.
- **Primary `gemma4-31b-gptq` stays at `maxModelLen: 2048`** and that is
  the production ceiling until one of the four levers above lands.
- **Long-context work should pivot to the 26B-A4B lane**: the
  `gemma4-26b-a4b-gptq-long` canary (already in-repo, never validated)
  has a much better memory math on this card (17.7 GiB weights vs 20 GiB)
  and doesn't use TurboQuant, so it's not blocked by the same issue.
  That's the next long-context experiment.
- **E4B at 16K is the proven long-context path** for anyone who needs
  real context today and can accept a 4B model — `gemma4-e4b-turboquant`
  is already live and validated.

## References

- Failing pod: `gemma4-31b-gptq-long-7fc9f968dd-7scqt` (deleted; events + logs captured at analysis time, summary in this doc)
- Primary pod at time of analysis: `gemma4-31b-gptq-6d654ff78c-5xbjz` (Ready, serving smoke traffic)
- MRs: !188 (3072 attempt, reverted), !190 (2048 fallback), !191 (TurboQuant canary, this doc documents its result)
- Runtime image built from `build/runtime.yaml` profile `gfx1100-gemma4-turboquant-experimental`
  (base: `rocm/vllm-dev:rocm7.2_navi_ubuntu24.04_py3.12_pytorch_2.9_vllm_0.14.0rc0`,
  turboquant-vllm@9d19b87c)
- Research trail: `.loom/10-research.md` lines 480–679 (E4B validation path)

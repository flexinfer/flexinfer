# Iteration Plan — Track B: Llama-3.3-70B abliterated self-quant (2026-06-10)

RALPH slice following the Track A verdict
([docs/f5-daily-driver-70b-eval-2026-06-10.md](../docs/f5-daily-driver-70b-eval-2026-06-10.md),
MR !603): self-quantize `huihui-ai/Llama-3.3-70B-Instruct-abliterated`
(141 GB FP16) with the proven GPTQ recipe, targeting the PP=2 gfx1100
serving topology.

## Riskiest assumption + kill-test

**Load-bearing assumption**: the in-house GPTQModel quantize path
(`build/scripts/quantize_gptq.py`, layer-at-a-time + `offload_to_disk`)
can process a 141 GB FP16 Llama-3.3-70B on cblevins-5930k (24 GB VRAM,
64 GB host RAM) with model + scratch on the `llm-models-nfs` RWX mount —
i.e. neither model load nor calibration OOMs, and NFS-backed offload I/O
does not stall the job past its 48 h timeout. In-house precedent tops out
at 27B-class (qwen36, 8–14 h on this node, local-NVMe PVC); 70B-over-NFS
is unproven.

**Kill test**: the quantization job reaches layer ≥ 5/80 with checkpoint
writes (`.flexinfer-gptq-cache/`) and no OOM within ~2 h of starting.
Observable in job logs (`layer_complete` progress events). Full proof =
artifact written to `gptq-w4-g128/` + ModelCache Ready.

**Failure mode if wrong**: OOM at load/calibration or NFS I/O stall. Plan
B is `force_direct_load` (manual sharded state-dict loader already in the
script) and/or re-enabling Longhorn scheduling on the 7900xtx 1.9 TB nvme
disk for a local-disk PVC (requires displacing the primary lane instead of
the twin).

**Status**: passed 2026-06-12 — artifact completed: 141.1 GB FP16 →
39.8 GB GPTQ INT4 (3.55×), 10 shards + `.save-complete`, 6.5 h quantize
wall on cblevins-5930k with model+scratch on `llm-models-nfs`. The
offload path held; the wall-clock was *better* than the local-disk
estimate. Three pre-quant blockers were found and fixed along the way
(all merged): unpinned `kernels` runtime dep (!605), HIP allocator
fragmentation on the 3.06 GiB down_proj Hessian (!606 —
`expandable_segments` + fraction 0.90; warning storms went to zero),
and a latent `hessian_repair` NameError in the resume fingerprint
(!608). Separate honest result: per-layer resume (!607 default-on) is
SAFE but currently a silent no-op on gptqmodel 7.0.0 — the Phase A
writer never fires (callback API drift since the v5.x-era code), so the
deliberate pod-kill test produced a full re-quantize, not a resume.
Follow-up task filed.

## Scope

- **In**: ModelCache `llama33-70b-abliterated-gptq`
  ([deploy/modelcaches/llama33-70b-abliterated-gptq.yaml](../deploy/modelcaches/llama33-70b-abliterated-gptq.yaml))
  — download → GPTQ INT4 (sym, descAct=false, g128, calib 2048/256) on
  cblevins-5930k, staged on `llm-models-nfs` via `existingClaimName`.
  Ops: twin-lane displacement for the quant window + restore.
- **Out**: serving the artifact (next slice: PP=2 window + gauntlet),
  promotion over the kaitchup stock quant, OCI publish, experiment-platform
  CRD work (task `dffb13608921b84f`, blocked on this).

## Key decisions

| Decision | Why |
|---|---|
| NFS via `existingClaimName` | Longhorn `nvme-1r-gpu` cannot provision: ALL GPU-node disks `allowScheduling=false`; 5930k local free is only ~168 GiB. NFS export has 869 GB free (probed 2026-06-10); mlc caches prove the `existingClaimName` pattern |
| Quant on cblevins-5930k | Displaces only the low-traffic twin lane (~5 req/day); primary on 7900xtx keeps serving Gemma; qwen36 quant precedent on this node |
| No abliteration block | Source is pre-abliterated by huihui-ai |
| No publish block | PP=2 lane reads weights directly from `llm-models-nfs` (same as eval windows); OCI publish deferred to productization |
| Calibration 2048/256 | Standard GPTQ calibration depth; 4096 seq quadruples activation cache cost on a 70B for marginal gain |
| Timeout 48 h | 27B took 8–14 h on this node on local NVMe; 70B + NFS overhead needs headroom |

## Test plan

1. Manifest merges; Flux reconciles; ModelCache enters Provisioning →
   download job writes to `/models/llama33-70b-instruct-abliterated/`.
2. Download completes (`.download_complete`); controller creates quant job
   (Pending until GPU freed).
3. Displace twin lane (scale 0); quant job schedules on 5930k.
4. **Kill-test** (above): layer ≥ 5/80 + checkpoint writes, no OOM.
5. On Ready: verify `gptq-w4-g128/` artifact config
   (`quant_method=gptq, sym=true, desc_act=false, group_size=128`),
   restore twin lane (`flexinfer_activate_model` — direct scale-down does
   not auto-reclaim).
6. Next slice: PP=2 smoke (kill-test 128 greedy + ladder) + eval gauntlet
   before any promotion decision.

## Dependencies / blockers

- GPU contention: quant blocks on twin-lane displacement (operator-safe:
  primary keeps serving).
- HF throttling on the 141 GB pull — `hf-token` secret wired; download is
  resumable (backoffLimit 3).
- Unblocks: experiment-platform directive (Tracks A+B were its blockers).

Slice S2 of `.loom/10-research-gfx1100-next-feature-2026-10-23.md`, the MTP
kill-test on the S1-qualified vLLM 0.32.0 gfx1100 digest, now has its
reproducible probe: `deploy/debug/qwen38-27b-v032-mtp-canary.yaml` (the S1
canary plus `speculativeConfig` method `mtp`, K=2, draft attention pinned to
`TRITON_ATTN`, and `FLEXINFER_QWEN35_MTP_HEAD=checkpoint` through the
`spec.config.command` wrapper) and `scripts/verify-vllm-mtp.py`, which reads
the vLLM spec-decode counters around a fixed workload and reports position-0
acceptance, the per-position vector and the mean acceptance length next to the
coherence probes, 256-token greedy parity hashes (`--compare` across an MTP-on
and an MTP-off run), decode throughput at short and ~2K context and a
concurrency-2 soak, failing closed on the §7 bar. Reading the v0.32.0 tree
showed the draft model is built from the target's `VllmConfig`, so the
artifact's INT4 MTP block loads through the same AutoGPTQ config as the target
and the on-disk `config.json` patch the 0.23 attempt needed is not required.

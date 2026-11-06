# Result: S7 and G3, two levers on the workhorse's KV pool (2026-11-05)

S7 is slice S7 of `10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` (§5.2 G2, §5.3 candidate 3): the input
embeddings in host memory. G3 (§5.2 G3) is fp8 KV on segmented attention, the fallback S6's decision table named for a
failed Octave.

Rows `xtx-s7-embed-host-2026-11-05` and `xtx-g3-segmented-fp8-gfx11-2026-11-05` in `60-validation-matrix.md` hold the
pass and kill rules. They were written and merged before any data, in !1298; the image and `vllmVersion` amendments
followed in !1304, still before the run.

Raw evidence is local-only, under `/Users/cblevins/workspace/.loom/local/flexinfer/s7-g3-kv-pool-2026-11-05/`: verify
JSON, TTFT probes, engine logs and the boot logs of every arm.

## Verdicts

| Row | Verdict | Why |
|---|---|---|
| **S7**, embeddings in mapped host memory | **`conditional`** | Pool, TTFT, parity and soak pass. The pool is **73,818 tokens against 42,490 (+31,328; bar +29,068)**, 2.25× at 32K. On the 5930k that projects to ≈ 84,260, against an 82,000 bar. Decode passes at 2K. At 16K, decode first read 0.984×, the pre-registered conditional band. The same-window rerun read 0.896×, because one request paid a first-use Triton compile (§3). The same-shape request in the next run was at parity. P2 is therefore not met as written, but nothing points at S7's own cost: step times are identical everywhere else. The confirming run moves to the workhorse-promotion canary, with a JIT warm sweep first. |
| **G3**, fp8 KV on segmented attention, gfx11 | **killed (K1)** | Decode at 16K is **0.740×** (the kill line is < 0.90×), and 0.670× at 28K. The pool is also short: 71,837 tokens = 1.69×, against a 1.8× bar. The gate lift itself works: more than 1,000 fp8 dispatches took the segmented kernel, with 0 fallbacks. |

**For the workhorse:** S7 is not promoted on this row alone. The workhorse-promotion session owns the combined repin Draft (vLLM 0.33 + S7 + the KT-SO certificate) and its canary, so this session opens no separate S7-only workhorse Draft. That canary's B0 → B1 arm, 0.33 without and with `FLEXINFER_EMBED_HOST`, is S7's confirming rerun. It should warm-sweep the segmented shapes first (`scripts/probe-vllm-decode-step.py jit-sweep`), or pre-register that a request which logs a JIT compile is excluded. G3 does not go to the workhorse.

## 1. Setup

| Item | Value |
|---|---|
| Canary | `qwen38-27b-pool-canary` (`deploy/debug/qwen38-27b-pool-canary.yaml`, !1304) on the XTX warm lane's slot: cblevins-7900xtx, `7900xtx-textgen`, count 2 / `hipVisibleDevices 0`, priority 480, `kvPoolRecheck: false`. The owner applied it at 19:53:53Z. It was deleted at 22:53Z. |
| Image | `runtime@sha256:316f818851c8…` (`rocm-gfx1100-v032-native-w4a16-b461833c`, job 470054): vLLM 0.32.0+rocm723, the workhorse's base and Dockerfile, text plugin 0.12.0, plugin-common 0.5.0. Two master pushes cancelled the queued master builds of !1298, so the image was built from master `b461833c` in an API pipeline on a throwaway branch at that commit. |
| Build verifier | It installed the embed-host patch into vLLM 0.32's `BaseModelLoader`, and found every name the fp8 gate lift wraps (`segmented fp8 gate-lift names present`). |
| Posture | The 5930k workhorse's S4 posture: `ROCM_SEGMENTED_ATTN` with autotune, MTP K=2 with the checkpoint head and the segmented draft backend, 32K, `maxNumSeqs 2`, 0.96, chunked prefill 4096, GDN prefix caching align with bf16 SSM. No `vllmVersion`, so it renders like the workhorse. |
| Harness | `scripts/verify-vllm-mtp.py --phase measure --contexts 512,2048,16384,28672 --soak-pairs 4 --apc-repeat 7600`, three runs; run 1 is the warm-up and runs 2–3 are measured. Then `scripts/probe-vllm-ttft-floor.py --sizes 256,1124,4096 --reps 7` (server TTFT medians). Arm 1 also had a 33-min soak: two runs of `--soak-pairs 200`. |

Arms, as `flexinfer_update_model` config merge patches on the one Model:

| Arm | Patch | Ready |
|---|---|---|
| 0, fp16 baseline | as applied | 20:10:03Z (one cold-compile restart before Ready, as in S3/S4/S6) |
| 1, S7 | `env.FLEXINFER_EMBED_HOST: "1"` | 20:19:22Z (no restart) |
| 2, G3 | `kvCacheDtype: fp8_e4m3`, `env.FLEXINFER_SEGMENTED_FP8_GFX11: "1"`, embed host off | 22:28:29Z: one cold-compile restart, then the startup probe killed the cold fp8 autotune at 22:25Z (§4), and it resumed |
| 0′, 1′ (S7 rerun) | back to arm 0 (`kvCacheDtype auto`, G3 flag removed), then `FLEXINFER_EMBED_HOST: "1"` | 0′ 22:38:46Z (it reused arm 0's ReplicaSet and booted warm in about 1 min); 1′ about 22:46Z. Pools 42,490 and 73,818 again |

Arm 3 (S7 + fp8 stacked) and phase L (64K) were cut. Arm 2's cold autotune took 45 min of the window, and both only
decide the 64K question if G3 is adopted, which it is not.

## 2. Memory and pool

| | Arm 0, fp16 | Arm 1, S7 | Arm 2, G3 |
|---|---:|---:|---:|
| Plugin line | `embed_host=off` | `embed_host: moved language_model.model.embed_tokens [248320, 5120] float16 (2.37 GiB)`; device allocated 16.84 → 14.47 GiB | `segmented_fp8_gfx11=on`; dispatches segmented ≥ 1,000, fallback 0 |
| `Model loading took` | 17.08 GiB, 9.0 s | **14.71 GiB**, 20.3 s | 17.08 GiB |
| Peak activation | 2.05 GiB | 2.04 GiB | not printed before the kill; available KV fell 0.12 GiB |
| `Available KV cache memory` | 3.2 GiB | **5.56 GiB** | 3.08 GiB |
| Attention block | 416 tokens | 416 | 832 |
| **KV pool** at 0.96 / 32K | **42,490** (1.30×) | **73,818** (2.25×) | **71,837** (2.19×) |
| Pool vs arm 0 | — | **+31,328 tokens, 1.737×** | 1.691× |
| Tokens per GiB of KV | 13.3K | 13.3K | 23.3K (1.76× fp16, not 2×: the GDN state pages share the pool) |
| Pod cgroup `memory.current` | — | 7.49 GB, including the 2.37 GiB mapped table | — |

- **S7 frees exactly the table.** The fp16 `embed_tokens` is 2.368 GiB. Model memory fell by 2.37 GiB and available KV
  rose by 2.36 GiB, with peak activation and CUDA graphs (1.15 GiB) unchanged.
- **S7's boot is cleaner.** Its cold-compile boot no longer exits on the 32K KV check: a 32K request needs 2.45 GiB,
  and the pool now has 5.56.
- **The cost is 11 s of load time,** for the device → host copy and the mapping.
- **G3's pool misses for two reasons.** fp8 halves the attention page, so the hybrid block doubles to 832 tokens.
  But the pool is shared with fixed-size GDN state pages, so density rises 1.76×, not 2×. Available KV also dropped
  by 0.12 GiB.

## 3. Speed, TTFT and correctness

Measured runs 2–3, means. Ratios are against arm 0 of the same window.

| | Arm 0 | Arm 1 (S7) | 1 / 0 | Arm 2 (G3) | 2 / 0 |
|---|---:|---:|---:|---:|---:|
| Decode 512 (tok/s) | 69.85 | 69.72 | 0.998 | 66.15 | 0.947 |
| Decode 2K | 65.67 | 65.55 | **0.998** | 60.79 | **0.926** |
| Decode 16K | 63.62 | 62.62 | **0.984** | 47.08 | **0.740** |
| Decode 28K | 60.83 | 59.99 | 0.986 | 40.75 | 0.670 |
| Short-decode median / `decode_2k` probe | 67.0 / 65.78 | 67.0 / 66.45 | 1.00 / 1.01 | 64.59 / 58.62 | 0.96 / 0.89 |
| Server TTFT 256 / 1,124 / 4,096 (s) | 0.280 / 1.155 / 3.673 | 0.281 / 1.158 / 3.688 | **1.002 / 1.003 / 1.004** | 0.284 / 1.048 / 4.013 | 1.011 / 0.907 / 1.093 |
| Cold sweep TTFT 2K / 16K / 28K (s) | 1.93 / 15.81 / 28.80 | 1.94 / 15.85 / 28.94 | 1.004 / 1.003 / 1.005 | 2.00 / 19.18 / 38.93 | 1.03 / **1.21** / 1.35 |
| Position-0 acceptance / mean acceptance length | 0.814 / 2.383 | 0.813 / 2.374 | 0.998 / 0.996 | 0.811 / 2.377 | 0.996 / 0.998 |
| Greedy hashes `code` / `explain` / `list` | `8174a49e` / `f1f04ef3` / `ad819b11` (= S3, S6, the workhorse) | identical; `--compare` passes, texts byte-equal | | `8174a49e` / `cbf7980d` / `7de9ed73` | |
| First divergence from fp16 (chars) | — | none | | — / 352 / 9 | |
| Probes `42` / Birch JSON / `MAPLE-4827` | ok | ok | | ok | |
| APC on the 7.6K repeat: hit tokens, warm/cold TTFT | 7,072, 0.078 | 7,072, 0.078; answers identical | | 6,656 (8 × 832), 0.148; answers identical | |
| Soak, concurrency 2 | 4 pairs: median 56.5 tok/s | **800 requests over 33 min**: medians 56.0 / 56.7, min 52.5 / 53.5 | | — | |

**S7's 16K read is acceptance noise, not a kernel cost.** Each sweep run lands on one of two acceptance variants: at
16K, mean acceptance length is 2.415 or 2.370. Both of arm 1's measured runs drew the low one, and both of arm 0's the
high one. The time per engine step, mean acceptance length ÷ decode tok/s, is identical:

| Step time (ms) at | 2K, runs 1 / 2 / 3 | 16K, runs 1 / 2 / 3 |
|---|---|---|
| Arm 0 | 35.63 / 35.47 / 35.61 | 37.88 / 37.83 / 38.10 |
| Arm 1 | 35.58 / 35.68 / 35.53 | 37.89 / 37.86 / 37.85 |

The embedding gather over Gen3 x8 costs nothing measurable, which was S7's riskiest assumption. The rule is applied as
written, though, so 0.984× is `conditional` and arms 0 and 1 reran in the same window:

S7 rerun, arms 0′ and 1′. Three verify runs each; the first is a warm-up and runs 2–3 are measured. No TTFT probe:
P3 had passed.

| | 512 | 2K | 16K | 28K |
|---|---:|---:|---:|---:|
| Arm 0′ decode (tok/s) | 69.80 | 64.75 | 63.48 | 59.15 |
| Arm 1′ decode (tok/s) | 69.59 | 65.35 | **56.88** | 59.00 |
| 1′ / 0′ | 0.997 | 1.009 | **0.896** | 0.998 |

The 16K mean is one request:

| Run at 16K | Prompt tokens | TTFT (s) | Decode (tok/s) | Step (ms) |
|---|---:|---:|---:|---:|
| 1′ warm-up | 16,325 | 15.96 | 63.60 | 37.97 |
| 1′ run 2 | **16,310** | **16.53** | **50.12** | **48.18** |
| 1′ run 3 | 16,310 | 15.86 | 63.64 | 37.95 |
| 0′, runs 2 / 3 | 16,325 / 16,309 | 15.86 / 15.79 | 63.16 / 63.81 | 37.73 / 37.85 |

- **Run 2 was the first 16,310-token prompt of the whole window.** Every other arm's 16K prompts were 16,309, 16,325
  or 16,327 tokens.
- **It is slow in both prefill (+0.6 s) and decode (+10 ms per step).** The identical prompt size in run 3 was at
  parity: 63.64 tok/s, step 37.95 ms.
- **That is the segmented attention's first-use compile** (gap G7: each new shape compiles on first use, 0.5–4.7 s on
  gfx1100), not a cost of S7.
- **The JIT monitor cannot confirm it directly.** Without `--jit-monitor-verbose` it logs each kernel name once, at
  22:46:21Z for `_segmented_attention_stage`.
- **Everything else is at parity.** Excluding that request, arm 1′ at 16K is 63.64 against arm 0′'s 63.48 (1.003×).
  Its other three sizes read 0.997–1.009×, parity, probes and APC are identical, and position-0 acceptance is
  0.813 against 0.814.
- **The verdict is `conditional`, not pass.** I do not drop a measured request after the fact: P2 as written is not
  met, and the confirming rerun goes to the promotion canary with a warm sweep.

**G3's cost grows with context.** Decode falls from 0.93× at 2K to 0.74× at 16K and 0.67× at 28K, and cold TTFT grows
to 1.35× at 28K. Acceptance and outputs barely move: mean acceptance length is 0.998×, and the code prompt is
identical. So this is the kernel, not the drafts. Software fp8 → fp16 conversion inside `_segmented_attention_stage` is
the riskiest assumption the row named (#56992), and it fails on gfx1100.

## 4. Pass and kill rules

| Rule (pre-registered) | S7 (arm 1) | G3 (arm 2) |
|---|---|---|
| S7 P1: pool gain ≥ 29,068 tokens | **pass**: +31,328 | — |
| S7 P2: decode ≥ 0.99× at 2K and 16K | **not met as written**: first read 0.998 / 0.984 (conditional), rerun 1.009 / 0.896. The rerun's 16K is one first-of-shape request (§3), and its same-shape repeat was at parity | — |
| S7 P3: TTFT ≤ 1.02× at 256 / 1,124 / 4,096 | **pass**: 1.002 / 1.003 / 1.004 | — |
| S7 P4: parity unchanged, acceptance within ±1 % | **pass**: hashes and texts identical, probes and APC identical; pos0 −0.2 %, mean acceptance length −0.4 % | — |
| S7 P5: 0 restarts, 0 HIP / illegal-access / `amdgpu` lines over ≥ 30 min | **pass**: 33.4 min, 800 requests, 0 restarts, 0 fault lines in the engine log, `journalctl -k` empty for 20:15–21:40Z | — |
| S7 kill: gain < 21,173, decode < 0.98×, TTFT > 1.02× | the rerun's 16K mean (0.896) crosses the decode line. **Not applied**: one first-use-compile request drives it, and S7's step time is unchanged. Recorded as `conditional`; the owner can overrule | — |
| G3 validity: segmented with fp8 for target and draft; fallback = 0 | — | **valid**: `ROCM_SEGMENTED_ATTN` selected with `fp8_e4m3`; dispatches segmented ≥ 1,000, fallback 0 |
| G3 K1: decode < 0.90× at 16K | — | **KILLED**: 0.740 |
| G3 K1: 16K cold TTFT > 1.30× | — | not met (1.21) |
| G3 K2: correctness | — | not met: probes, APC and determinism hold; 0 fault lines |
| G3 P1: decode ≥ 0.95× at 2K / 16K, ≥ 0.90× at 28K | — | fail: 0.926 / 0.740 / 0.670 |
| G3 P2: TTFT ≤ 1.15× at 1,124 / 4,096 / 16K | — | fail at 16K: 0.907 / 1.093 / 1.214 |
| G3 P3: pool ≥ 1.8× | — | **fail**: 1.691 |
| G3 P4: pos0 ≥ 0.75, mean acceptance length ≥ 0.95× | — | pass: 0.811, 0.998× |
| G3 P5: parity tolerance (2 of 3 prompts share ≥ 100 chars) | — | pass: identical / 352 / 9 |

## 5. Findings

1. **S7 is the lossless pool lever the research expected,** and Gen3 x8 is enough.
   - It adds +2.36 GiB of KV and +31.3K tokens on the XTX.
   - It costs no decode, TTFT or acceptance: step times are identical, TTFT is 1.00×, and the outputs are bit-identical.
   - The 5930k has the same model posture on a Gen3 x16 link, so the same absolute gain is expected there, a pool
     of about 84K tokens.
2. **A cold fp8 segmented autotune takes about 10× the fp16 one** on this card: 105 workloads in about 42 min, 20–60 s
   per workload, against about 3.6 s.
   - The 45-min startup probe (`coldStartTimeout`) killed it one workload short.
   - The tuner saves a record per workload to shard files, so the restart read `missing=1` and finished.
   - Any fp8 segmented lane needs either a seeded tuning table or `coldStartTimeout` ≥ 60 min on its first boot.
3. **The fp8 segmented kernel is slow on gfx11 because of software conversion, and more so at long context.**
   - The bandwidth halving does not pay: at 16K the stage kernel reads half the bytes and decode is still 26 % slower.
   - Upstream gates fp8 to gfx12, where hardware fp8 exists. These numbers are the evidence that the gate is right
     for gfx11 as the kernel stands. The plugin flag stays off.
   - Only a kernel that dequantizes with packed fp8 → fp16 conversion could change this, which would mean a compiled
     or Triton kernel change, not a gate lift.
4. **Hybrid models get less than 2× from an fp8 KV.** The GDN state pages are a fixed share of every block, so on
   Qwen3.8-27B the density gain is 1.76×. Any KV-format lever should be judged on pool tokens, not on bytes per token.
5. **The stacked S7 + G3 pool, estimated, not measured,** would be about 127K tokens: 5.44 GiB × 23.3K per GiB. It is
   moot while G3's decode is 0.74× at 16K.

## 6. Decision and next steps

| Item | Decision | Next |
|---|---|---|
| S7 on the workhorse | **`conditional`** | The workhorse-promotion canary re-measures it, B0 → B1 on 0.33, with a JIT warm sweep or a pre-registered exclusion of compile-paying requests. If it holds there, S7 rides in that session's combined repin Draft. This session opens no S7-only Draft. |
| G3 | **stop** (K1) | Keep `FLEXINFER_SEGMENTED_FP8_GFX11` off. It stays in plugin-common, inert and tested, for a future kernel or for vLLM releases that change the stage kernel. Post these numbers upstream on vLLM #59132 (the gate) and #56992 (software fp8 on gfx1100); that is public and needs the owner's OK. |
| Octave (S6) | unchanged | The issue draft on the ~1.9 GiB of activation, at `…/s7-g3-kv-pool-2026-11-05/upstream-issue-octave-activation-draft.md`, waits for the owner's OK to post. |
| Upstream S7 | prepared, not posted | It adds `--cpu-offload-embeddings` (`UVAOffloadConfig.cpu_offload_embeddings`): every untied, non-head `VocabParallelEmbedding` of the target is served from exact-size mapped host memory, inside the loader's profiling window. vLLM's own pre-commit passes (ruff, mypy, typos, config docstrings); the GPU test is written but not run. Patch at `…/s7-g3-kv-pool-2026-11-05/upstream-cpu-offload-embeddings.patch`; posting needs the owner's OK. |
| 64K on the workhorse | open | S7 alone gives 73.8K tokens on the XTX and about 84K on the 5930k. That is one 64K request at 1.13–1.29×, below the controller's 1.5× KVPoolShort line, so 32K stays. A 64K × 2 posture still needs about 2× more pool. Octave, if its activation is fixed upstream, is the only measured route there. |

## 7. Cleanup

- The canary Model is deleted (22:53Z). The controller ran its tmpfs cleanup Job. `qwen38-27b-xtx-warm-canary` was Ready again at 22:55:08Z, on its own (`minReplicas 1`). No other Model at priority ≥ 480 on `7900xtx-textgen` is non-Idle. **The XTX was free from 22:55Z**, 35 min ahead of the owner window's 23:30Z margin.
- Left on cblevins-7900xtx:
  - the Local cache `/var/lib/flexinfer/models/flexinfer-system/qwen38-27b-pool-canary` (~19 GB);
  - fp8 and fp16 segmented autotune tables, added to S4's compile cache
    `/var/lib/flexinfer/compile-cache-qwen38-27b-s4-step0-7900xtx`.
  The fp8 table is the 42-minute tune. Keep it if fp8 segmented is ever retried on this card. Otherwise remove both
  with the other stale canary caches: about 150 GB free.
- Image `rocm-gfx1100-v032-native-w4a16-b461833c` (`316f8188`) has no `pinned-*` tag. The workhorse-promotion session
  builds its own 0.33 image, so this one may age out of Harbor retention.

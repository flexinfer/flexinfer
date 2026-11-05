# Result: S6 KT-OCTAVE, vLLM 0.33 Octave KV on the 7900 XTX (2026-11-05)

Slice S6 of `10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` (§5.3 candidate 2, §6.1).
Matrix row `xtx-vllm-v033-octave-kv-2026-11-05` holds the pass and kill rules, written and merged before the run (!1293).

**Status: FAIL on the pool. Speed and correctness held.**

Both Octave arms cleared both kill rules:
- decode at 2K was 1.00× and 0.96× fp16;
- there was no hybrid or APC correctness mismatch.

Both arms failed P3. The bar was a KV pool ≥ 3.0× fp16:
- `octave_k3v3` reached 57,626 tokens (1.29×);
- `octave_k3v4` reached 53,248 tokens (1.19×).

A pool that small cannot hold one 64K request. Phase L (135,168 tokens, 3 × 128K needles) therefore could not run, and P6
fails with it.

The pool is lost to memory, not to the format. Octave holds about 3.1× more tokens per GiB than fp16 on this hybrid
model. But vLLM profiles **3.98 GiB of peak activation on every Octave engine against 2.05 GiB on fp16**, which leaves
1.39 GiB for KV instead of 3.37 GiB. The cost does not depend on the bit width, and halving the prefill chunk barely
moves it. Its cause is not identified.

**Recommendation: G3 fallback, as the pre-registered decision table says**, written up below and not run. The failure
is in the Octave path's memory, not in the shared hybrid KV path: block alignment and APC both worked. So the "stop"
branch does not apply. Octave is **not** recommended for the workhorse on vLLM 0.33.0. It re-qualifies cheaply if
upstream explains or removes the ~1.9 GiB: only the pool and phase L need re-running, against the same canary.

The pool is still the workhorse's bottleneck. That keeps **S7** (input embeddings in pinned host memory, about +2.37 GiB of
KV, lossless) as the strongest pool lever. S7 stacks with G3.

Nothing live moved. The XTX was handed back to `qwen38-27b-xtx-warm-canary`, which was Ready again at 07:32:31Z.

## The canary

`deploy/debug/qwen38-27b-octave-kv-canary.yaml` (!1293, `4df7b142`). The owner applied it at ~05:32Z, together with S4's
step-0 canary. It waited at `minReplicas 0` until S4's window closed.
- **Placement:** one Model on the XTX warm lane's slot. That is cblevins-7900xtx, `7900xtx-textgen`, count 2 /
  `hipVisibleDevices 0`, priority 480.
- **Image:** the 0.33 image that passed S3, `runtime@29378fe0`, with `vllmVersion: "0.33.0"`. The live controller
  `1286a2f1` ignores that key, so the render carried two dead `VLLM_*` env names and `--mamba-cache-mode`; both produce
  warnings only, as in S3.
- **Posture:** the workhorse's S4 posture throughout: MTP K=2 with the checkpoint head, 32K, `maxNumSeqs 2`, 0.96,
  chunked prefill 4096, GDN prefix caching align / bf16 SSM.

Arms were `flexinfer_update_model` merge patches on that one Model:

| Arm | KV | Attention | Block (tokens) | Pool | `Available KV` | Peak activation |
|---|---|---|---:|---:|---:|---:|
| 0 | fp16 (`auto`) | `ROCM_SEGMENTED_ATTN` + autotune | 416 | 44,650 | 3.37 GiB | 2.05 GiB |
| 1 | `octave_k3v3` | none set → `ROCM_OCTAVE` | 1,952 | 57,626 (1.29×) | 1.39 GiB | 3.98 GiB |
| 2 | `octave_k3v4` | none set → `ROCM_OCTAVE` | 1,696 | 53,248 (1.19×) | 1.39 GiB | 3.98 GiB |
| X (exploratory, not gated) | `octave_k3v3`, `maxNumBatchedTokens 2048` | `ROCM_OCTAVE` | 1,952 | 64,406 (1.44×) | 1.52 GiB | 3.86 GiB |

On all four arms, weights plus non-torch memory came to 17.61–17.66 GiB and CUDA graphs to 1.13–1.15 GiB.

The engine selected Octave from the dtype alone: `Found incompatible backend(s) [ROCM_ATTN, TRITON_ATTN, TURBOQUANT,
ULTRAQUANT] with AttentionType.DECODER. Overriding with ROCM_OCTAVE`. The MTP draft ran on it too: only `ROCM_OCTAVE`
accepts an `octave_*` cache. Arm 0's message that the draft backend "cannot update draft decode metadata in place" is
absent on the Octave arms.

### Timeline (UTC)

| Time | Event |
|---|---|
| 05:32 | Owner applies the canary (`minReplicas 0`); its Local cache stages while S4's canary (520) holds the card |
| 06:44:30 | S4's canary deleted; the election hands the XTX to this canary (480 > the warm lane's 300) |
| 06:47 | Pinned at `minReplicas 1` over MCP |
| 06:47:38 | Arm 0's first container restarts before Ready: the cold-compile KV-check exit S3 and S4 saw on this card |
| 06:55:23 | Controller `KVPoolRecheck` relaunch (1.36× < 1.5×) |
| 06:56:24 | Arm 0 Ready on the relaunch (44,650 tokens) |
| 06:59:45–07:03:57 | Arm 0 harness ×2 |
| 07:04 | Patch to `octave_k3v3`; Ready at 07:05:45. No crash and no recheck (1.76× > 1.5×); init engine 38 s with a cold compile |
| 07:09:43–07:14:01 | Arm 1 harness ×2 |
| 07:15 | Patch to `octave_k3v4`; Ready at 07:16:58 |
| 07:20:33–07:24:45 | Arm 2 harness ×2 |
| 07:25 | Exploratory patch (`octave_k3v3`, chunk 2048); Ready at 07:26:48; one harness pass at 2K/16K |
| 07:30 | Canary deleted over MCP |
| 07:32:31 | `qwen38-27b-xtx-warm-canary` Ready again on its own (`minReplicas 1`). **The XTX is free** |

Harness: `scripts/verify-vllm-mtp.py --phase measure --contexts 512,2048,16384,28672 --soak-pairs 4 --apc-repeat 7600`,
run twice per arm. The second run is the measured one. The `--apc-repeat` option was added in !1293.

## Measured runs (the second run of each arm)

| | Arm 0 fp16 | Arm 1 k3v3 | 1 / 0 | Arm 2 k3v4 | 2 / 0 |
|---|---:|---:|---:|---:|---:|
| decode 512 (tok/s) | 69.43 | 65.08 | 0.94 | 64.52 | 0.93 |
| decode 2K, sweep / `decode_2k` probe | 66.88 / 66.84 | 67.17 / 66.53 | 1.00 | 63.50 / 65.23 | 0.96 |
| decode 16K | 64.15 | 61.35 | 0.96 | 64.56 | 1.01 |
| decode 28K | 59.38 | 57.79 | 0.97 | 62.38 | 1.05 |
| short-decode median (3 × the same 400-word prompt) | 67.19 | 67.10 | 1.00 | 69.73 | 1.04 |
| TTFT 512 / 2K / 16K / 28K (s) | 0.64 / 1.85 / 15.81 / 28.83 | 0.49 / 2.02 / 15.84 / 28.67 | – / 1.09 / 1.00 / 0.99 | 0.47 / 1.95 / 15.78 / 28.45 | – / 1.05 / 1.00 / 0.99 |
| pos0 over the run / mean acceptance length | 0.815 / 2.381 | 0.796 / 2.352 | 0.98 / 0.99 | 0.805 / 2.364 | 0.99 / 0.99 |
| soak, 4 pairs: median / min (tok/s) | 56.6 / 53.6 | 54.0 / 52.6 | 0.95 | 55.4 / 51.3 | 0.98 |
| APC 7.6K repeat: hit tokens (blocks) | 7,488 (18 × 416) | 5,856 (3 × 1,952) | 0.78 | 6,784 (4 × 1,696) | 0.91 |
| APC cold / warm TTFT (s), ratio | 7.10 / 0.24, 0.034 | 7.15 / 1.70, 0.237 | | 7.10 / 0.86, 0.121 | |
| APC answers: code ok, warm₁ = warm₂, cold = warm | yes, yes, yes | yes, yes, yes | | yes, yes, yes | |
| probes `42` / Birch JSON / `MAPLE-4827` | ok | ok | | ok | |
| greedy hashes `code` / `explain` / `list` | `8174a49e` / `f1f04ef3` / `ad819b11` (= S3) | `dd0e167b` / `80c8b698` / `d45a4780` | | `29a85d6a` / `851a164c` / `2afe52f9` | |
| first divergence from fp16 (chars) | – | 685 / 137 / 9 | | 334 / 312 / 9 | |
| KV pool at 0.96 / 32K | 44,650 | 57,626 | **1.29** | 53,248 | **1.19** |

Notes on the table:
- **The decode rows are noisy.** Single-request decode at one prompt size moves with that output's MTP acceptance,
  and the Octave arms write different text from fp16. For example, k3v4 at 512 read 60.7 in run 1 and 64.5 in run 2.
  The short-decode median, three samples of one prompt, is the steadier read. On it, Octave decodes at fp16 speed on
  this card.
- **Every arm is deterministic run to run.** Each arm gave identical greedy hashes on both runs, and identical APC
  answers cold and warm.
- **The divergence from fp16 is gradual.** All six Octave parity texts are coherent, with no repeated sentence. The
  `list` prompt flips a near-tie on its first item (both "1. Batch …"). The others diverge after 137–685 characters.
- **The exploratory arm (chunk 2048) was barely different.** One pass gave TTFT 2.04 s at 2K and 16.20 s at 16K
  (+2 % against the chunk-4096 arm), and all probes passed.

## Pass rule vs outcome

| Rule (as pre-registered) | Arm 1 `octave_k3v3` | Arm 2 `octave_k3v4` |
|---|---|---|
| **K1** decode at 2K ≥ 0.90 × arm 0 | **pass**: 1.00 | **pass**: 0.96 |
| **K2** hybrid/APC correctness: (a) APC failure, (b) probe, (c) degenerate parity text, (d) traceback / NaN / HIP fault, (e) warm answers differ while arm 0's match | **pass**: none | **pass**: none |
| **P1** decode ≥ 0.90 × at 512 / 2K / 16K | **pass**: 0.94 / 1.00 / 0.96 | **pass**: 0.93 / 0.96 / 1.01 |
| **P2** TTFT ≤ 1.15 × at 2K / 16K | **pass**: 1.09 / 1.00 | **pass**: 1.05 / 1.00 |
| **P3** pool ≥ 3.0 × arm 0 | **fail**: 1.29 | **fail**: 1.19 |
| **P4** pos0 ≥ 0.75 and ≥ 0.95 ×; mean acceptance length ≥ 0.97 × | **pass**: 0.796 (0.98×); 0.99× | **pass**: 0.805 (0.99×); 0.99× |
| **P5** warm TTFT ≤ 0.5 × cold on the 7.6K repeat | **pass**: 0.237 | **pass**: 0.121 |
| **P6** phase L boots at 135,168 and finds 3/3 needles | **fail, not runnable**: a 57,626-token pool cannot hold a 64K request | **fail, not runnable**: 53,248 tokens |
| **P7** Ready on every patch; 0 restarts after Ready; `ROCM_OCTAVE` for target and draft; no `amdgpu` SMU / ring / fault lines | **pass** | **pass** |

P7 evidence:
- Neither Octave arm restarted, before or after Ready. Arm 0's one pre-Ready restart is the known cold-compile pattern.
- `journalctl -k` on cblevins-7900xtx for 06:40–07:33Z has no line outside container networking (veth/cni).

## Findings

1. **Octave on gfx1100 is fast and correct with hybrid GDN, prefix caching and MTP.** That is the riskiest assumption in
   §5.3, and it held:
   - decode at fp16 speed, short-decode median 1.00× and 1.04×;
   - TTFT within 1.09×;
   - MTP acceptance within 2 %;
   - APC hits correct and deterministic, on 4.1–4.7× larger hybrid blocks.

   Upstream had published no speed numbers. This is the first measurement on one card with this recipe.
2. **The pool gain is eaten by a fixed ~1.9 GiB of profiled activation.**
   - **The density gain is real.** Octave packs 41.5K tokens per GiB of KV against fp16's 13.3K, 3.1× rather than the
     4.7× slot ratio. The difference is the GDN state pages, which share the hybrid pool.
   - **But every Octave engine profiles 3.98 GiB of peak activation against 2.05 GiB on fp16.** It is the same for
     k3v3 and k3v4, and 3.86 GiB at chunk 2048.
   - **It is not the CUDA-graph split-KV workspace.** That is about 6 MB at capture size 8.
   - **It is not, on paper, the prefill partials.** In the v0.33.0 source of `csrc/attention/octave_prefill_rdna3.cu`
     they are sized `num_splits × total_q × hq × 258` fp32, with `num_splits = 2` at a 4,096-token profile batch.
     That comes to about 0.2 GB, and under 0.5 GB with its never-freed geometric growth.
   - **It is not chunk-proportional.** Halving the chunk barely changed it, which refutes my first guess that rotated
     q/k/v/o copies stay alive per layer.
   - **The cause is not identified.**

   Had it been as small as fp16's, the pool would be about (1.39 + 1.93) GiB × 41.5K/GiB ≈ 138K tokens (3.1×). That
   would clear P3 and fit one 128K request. **That is an estimate, not a measurement.**
3. **Octave's large hybrid blocks cost prefix reuse.** On a 7.6K repeat, fp16's 416-token blocks hit 7,488 tokens, with
   warm TTFT 0.24 s. Octave's 1,952-token blocks hit 5,856 tokens, with warm TTFT 1.70 s. Up to one block, about 2K
   tokens or about 2 s at this prefill rate, is recomputed on every hit. That matters for Mills' repeated prefixes even if
   the pool problem is solved. k3v4's smaller block (1,696) recomputes less (0.86 s).
4. **Arm 0 reproduced S3 arm B:** the same pool (44,650) and the same three greedy hashes. Decode was within 2.3 % at
   every size: 69.4 / 66.9 / 64.1 against 69.8 / 66.5 / 62.7.
   The card was in the same state as on the morning of 11-05.
5. **The cold-compile first boot fails the 32K KV check on fp16 only.** It needs 2.45 GiB and gets about 1.2–1.4 GiB.
   Octave needs far less KV for one 32K request, so its cold boots came straight up, with no crash and no recheck.

## Next step: G3 (fp8 KV on segmented attention for gfx11), written up, not run

The pre-registered table sends a failed Octave to G3, inventory item G3 in §5.2. `ROCM_SEGMENTED_ATTN` gates fp8 KV to
gfx12 (`rocm_segmented_attn.py:146-147, 182-185` at v0.32.0), even though the kernel converts fp8 to fp16 in Triton.

**Proposal**
- The text plugin registers a CUSTOM backend subclass that lifts the gate on gfx11.
- A canary on the XTX slot, built from this slice's single-Model pattern:
  - arm 0 = fp16 segmented;
  - arm 1 = `kvCacheDtype fp8_e4m3` + the CUSTOM backend;
  - the same harness with `--apc-repeat 7600`.

**Riskiest assumption:** software fp8 → fp16 conversion inside the segmented kernel keeps ≥ 95 % of decode. #56992 measured
software fp8 2.2–2.8× slower in another gfx1100 kernel.

**Pass bars (from §5.3):**
- decode ≥ 0.95× at 2K and 16K;
- TTFT ≤ 1.15×;
- pool ≥ 1.8×.

Add from S6:
- record `Available KV` and peak activation per arm, so a pool shortfall is attributed at once;
- report APC hit tokens. fp8 halves the page per token, so the hybrid block roughly doubles, to about 832 tokens.

**Effort:** S to try. Plugin files are `build/vllm-qwen35-text-plugin`, with one writer at a time. S5 (the pruned draft
head) touches the same plugin.

## Follow-ups (not done here)

- **Identify the ~1.9 GiB.**
  - Method: one canary boot with `torch.cuda.memory._record_memory_history` around vLLM's profile run (or
    `VLLM_LOGGING_LEVEL=DEBUG` plus a memory snapshot), on `octave_k3v3`.
  - If it is a profiler over-reservation, Octave re-qualifies: re-run P3 and phase L only. The speed and correctness
    rows above stand.
  - If it is a real Octave allocation, report it upstream on #59774's follow-ups, with the numbers in this file.
- **S7 (embeddings in pinned host memory) goes ahead.** The pool is still the bottleneck. S7 is lossless, frees about
  2.37 GiB, and stacks with G3 or a fixed Octave.
- **Octave on 0.34+.** Re-check the profile-time peak first: one boot and one log line.

## Harness and tooling (from !1293)

- `scripts/verify-vllm-mtp.py --apc-repeat N` sends one ~N-token prompt with a planted code, cold and then twice warm. It
  records `cached_tokens`, the warm/cold TTFT ratio and answer identity, and its correctness failures gate every phase.
  `--request-timeout` was added for long prefills.
- `scripts/context-needle-bench.py` no longer says "near the very beginning" for depth cells past the start. It was not
  exercised here, since phase L could not run. The bench's default 22 tokens per line underestimates Qwen3.8's tokenizer at
  4-digit line numbers (22.8 at 5,000 lines, measured against `/tokenize`); use `--tokens-per-line 22.9` for 128K points.

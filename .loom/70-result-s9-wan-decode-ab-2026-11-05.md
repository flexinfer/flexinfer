# Result: S9, the Wan decode A/B on the 0.32 runtime base (2026-11-05/06)

S9 is slice S9 of `10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` (§3.3, §6.1). It is the evidence owner Q7
asked for before D1's deferred in-pod creative lanes (Drafts !1237 T2V, !1238 VACE, !1239 Stable Audio) can move.

Five rows in `60-validation-matrix.md` hold the rules:

- **Written before any data and merged in !1307 (77855bdf9):**
  - `gfx1100-wan-decode-ab-v032-2026-11-05`, the A/B;
  - `gfx1100-wan-vace-vae-bypass-2026-11-05`, the VACE VAE kill-test;
  - `gfx1100-wan-denoise-cache-2026-11-05`.
- **Two addenda, each committed and pushed before the arms it governs ran:**
  - `gfx1100-wan-decode-ab-v032-fix-2026-11-06` (00:23Z);
  - `gfx1100-wan-decode-ab-v032-fix2-2026-11-06` (00:35Z, amended 00:38Z before any of its arms ran).

The procedure is `32-iteration-plan-s9-wan-decode-ab-2026-11-05.md`.

Raw evidence is local-only, under `/Users/cblevins/workspace/.loom/local/flexinfer/s9-wan-decode-ab-2026-11-05/`:
frames (`.npz`), MP4s, run records, logs, 5 Hz VRAM traces, kernel logs, `host.json`/`sims.json`/`verdicts.json`, and
`collect.txt`.

## Verdicts

| Row | Verdict | Why |
|---|---|---|
| A/B, **T2V** | **`fail`** | As shipped, B runs out of memory in the VAE decode (bar 8). Arm A is valid and matches 11-04. |
| A/B, **VACE** | **`fail`** | B renders NaN: 18.8 M values in `v0`, all 39.5 M in `v1` (12c). Speed and memory bars pass: warm 170 s against A's 265 s; 17,854 MiB. |
| A/B, **Stable Audio** | **`fail`** | B crashes in the first render: torchsde `RecursionError` (`tb=500.00006` > `t1=500.0`). A renders in 3.2 s warm. |
| Fix arm `t2v-B-tile` (`ENABLE_VAE_TILING=1`) | **`fail`** | Bars 2 (decode 16.6 s), 7 (RSS 42,522 MiB), 8, 9 and 10. Only the first VAE use per process is sound. |
| Fix2 diagnostics | **none resolves; `t2v-B-fix2` not run** | The two clean diagnostics decode A's latents at 34.2 dB / worst 31.8 dB / SSIM 0.979, against the pre-registered 35 / 32 / 0.98. |
| VACE VAE kill-test, V1 bf16 and V2 fp32 | **`fail`** (both) | K1 and K2 on speed: bf16 decode 15.9 s and fp32 60.2 s, against ≤ 6.6 s. The NaN in K4 is inherited from `vace-B`'s latents. |
| Denoise cache | **not run** | Its baseline would have been `t2v-B-tile`, whose `c1`/`c2` are NaN. The two `-fix` arms were made to fail fast; no cache data exists. |

**For the Drafts.** !1237, !1238 and !1239 stay deferred.

- The S9 evidence goes into each description.
- Draft !1310 drops the two VACE VAE flags, which failed.
- It keeps `WAN_DENOISE_CACHE` (default off, unjudged) for a rerun on a sound B configuration.

**The finding that matters most** is not a speed result. On the runtime image (ROCm 7.2.3, torch 2.13),
`expandable_segments:True` breaks two things in any diffusers lane that uses model CPU offload:

- it keeps ~19 GiB on the card outside torch's accounting;
- it corrupts every VAE use after the first in a process.

The lane env sets it in `PYTORCH_HIP_ALLOC_CONF`, and the XTX runtime pod sets it in `PYTORCH_ALLOC_CONF` (§4). With it
off in both variables, B renders clean and untiled, decoding at A's speed (~6 s), with clips of 61–70 s against A's
122 s. Only bar 9 (decode equivalence, missed by ~1 dB) and bar 6 (VRAM, which A misses too) stand in the way. That is
the owner's call, not this run's (§6).

## 1. Setup

| Item | Value |
|---|---|
| Card | cblevins-7900xtx, `card0` = 0000:03:00.0 (Navi 31, 24 GiB). The slot link is 8 GT/s x8 at upstream port 01:00.0. |
| Hold | The owner's GPULease `s9-wan-decode-ab` on `7900xtx-textgen` (4 h; expires 2026-11-06 03:09:34Z). Phases ran 00:00:19Z–01:13:27Z. The card read 28 MB before every arm; 0 amdgpu/SMU/AER kernel lines over the run. |
| Arm A | `diffusers-video@sha256:b1f98ad0…` (torch 2.6.0, HIP 6.4.1, MIOpen 1.0.60401, diffusers 0.36.0), `/app/server.py`, 8 CPUs, 48 GiB. Stable Audio A: `diffusers@sha256:d87f75f8…` (torch 2.3, ROCm 6.2), 6 CPUs, 16 GiB. |
| Arm B | `runtime@sha256:527e2714…` (torch 2.13.0, HIP 7.2.53211, MIOpen 1.0.70203, diffusers 0.42.0), `/opt/flexinfer/server-diffusers.py`, 48 GiB, no CPU quota, plus the runtime pod's env (`PYTORCH_ALLOC_CONF=garbage_collection_threshold:0.7,max_split_size_mb:256,expandable_segments:True`). The entrypoint sources `runtime.env`. |
| Server code | Both Wan images' servers are byte-identical to master `4da96d96`. The VACE variants ran `build/server-diffusers.py` from `feat/s9-wan-opt-in-flags` (sha256 `9b907f12…`). |
| Lane env | From each lane's rendered Deployment. Both T2V arms got `USE_CPU_OFFLOAD=1`, `COMPILE_MODE=max-autotune-no-cudagraphs`, `TORCH_BLAS_PREFER_HIPBLASLT=1` and `PYTORCH_HIP_ALLOC_CONF=garbage_collection_threshold:0.9,max_split_size_mb:512,expandable_segments:True`. Torch 2.6 ignores the last one ("expandable_segments not supported on this platform"). B also had `HSA_NO_SCRATCH_RECLAIM=1` and `HSA_ENABLE_IPC_MODE_LEGACY=1` from its image. |
| Harness | `scripts/wan-decode-ab.py run` (in-process, the server's own `load_pipeline`, the `/v1/videos/generations` call, the same `[video-timing]`), driven by `scripts/wan-decode-ab-host.sh` (`k3s ctr run` per arm, a fresh or named compile cache). `collect` applies the rules. |
| Requests | Plan §2: boat/42 and fox/7, 832x480, 33 frames, 20 steps, guidance 5.0. VACE keyframe = frame 0 of A's `c1`. Stable Audio: rain/42, 20 s. **Deviation:** both audio arms ran 20 steps (the harness's `--steps` default), not the 100 in plan §2. The audio A/B is like for like, but at 20 steps. |

## 2. T2V

| Arm | Clips | Warm total (c1, c2) | Warm decode | Cold `k` (warm cache) | First-ever cold `c0` | Card peak | Peak RSS | Outputs |
|---|---|---|---|---|---|---|---|---|
| A, `t2v-A` / `-cold` | c0 c1 c2 + own | **121.7, 121.4 s** | **6.2, 5.7 s** | 189.5 s (clip 136.8 s) | 429.8 s | **24,466 MiB** | 21,711 MiB | sound; decode-only of its own `c1` latents is bit-identical |
| B as shipped, `t2v-B` | c0 | — | — | — | — | 24,473 MiB | 42,603 MiB | **OOM** in `vae.decode` of `c0` after 20 denoise steps at 2.35 s/step |
| B, `t2v-B-noexp` (first addendum) | c0 | — | — | — | — | 24,473 MiB | 34,274 MiB | **OOM** as `t2v-B`; `PYTORCH_HIP_ALLOC_CONF` still had `expandable_segments` |
| B tiled, `t2v-B-tile` / `-cold` | c0 c1 c2 + xA | 70.3, 72.1 s | 16.6, 16.7 s | 108.0 s (clip 80.7 s) | 203.8 s | 12,522 MiB | 42,522 MiB | `c0` and `k` sound; **`c1` all NaN, `c2` 4.3 M NaN, xA flat 128 gray** |

Arm A's validity bars all pass:

- V1: warm total 121.6 s, against 105–143 s (11-04 live: 124 s);
- V2: warm decode 5.9 s;
- V3: decode-only identical to the pipeline frames.

`c0` → `c1` on A is bit-identical as well.

**The as-shipped OOM.** Torch asked for 7.71 GiB, the im2col buffer of a 96-channel 3×3×3 conv over 4×480×832 in bf16
(96 × 27 × 4 × 480 × 832 × 2 B). It had 740 MiB free of 23.98 GiB, while it counted 3.88 GiB allocated and 0.28 GiB
cached. The card stood at ~12.2 GB from the first text encoding through denoise, then hit 24.6 GB in the decode.

**Arm A is close to the same failure.** Its untiled decode reserves 23.7–24.0 GiB, and the card peaks at 24,466–24,492
MiB, ~80 MiB short of 24 GiB. The T2V lane's `vramEstimateMB` is 18,000.

`t2v-B-tile` bars: 1 ✓, 2 ✗ (16.6 s against ≤ 8.9 s and ≤ 15 s), 3 ✓, 4 ✓, 5 ✓, 6 ✓, 7 ✗, 8 ✗, 9 ✗ (12.2 dB),
10 ✗, 11 ✓.

## 3. Diagnostics: what breaks B

Each diagnostic ran on B's image server with `t2v-B`'s compile cache. Each rendered `c1` and `c2`, then decoded A's `c1`
latents (`xA`): three VAE uses in one process. The changes were applied in-process before torch loaded
(`--setenv`/`--unsetenv`), so `runtime.env` could not override them.

| Diagnostic | Change | Tiled | Completes | `c1`, `c2` | xA vs A `c1` (PSNR mean / min, SSIM) | Decode | Card peak |
|---|---|---|---|---|---|---|---|
| `dx-B-noexp2` (H1) | both allocator variables without `expandable_segments` | no | **yes** | **sound, 0 NaN** | 34.22 / 31.76 dB, 0.9789 | **6.1, 5.8 s** | 24,431 MiB |
| `dx-B-scratch` (H4) | `HSA_NO_SCRATCH_RECLAIM` unset | no | OOM (7.71 GiB) | — | — | — | 23,289 MiB |
| `dx-B-rocblas` (H5) | `TORCH_BLAS_PREFER_HIPBLASLT=0` | no | OOM (7.71 GiB) | — | — | — | 24,258 MiB |
| `dx-B-tile-nooffload` (H6) | `USE_CPU_OFFLOAD=0` | yes | **yes** | **sound, 0 NaN** | 34.11 / 31.79 dB, 0.9787 | 14.6, 14.6 s | 16,995 MiB |
| `dx-B-tile-nocompile` (H7) | `COMPILE_MODE=off` | yes | yes | no NaN, but `c2` washed out | 12.16 dB, 0.381 (flat gray) | 15.2, 15.3 s | 13,248 MiB |

**Reading.**

- Removing expandable segments fixes both the OOM and the corruption.
- Removing CPU offload fixes the corruption, because modules no longer move between host and card.
- The compiler, scratch reclaim and hipBLASLt are not the cause.

The two clean arms agree with each other at 36.1 dB / 0.983 on `c1`. Against A, end to end and same seed:

| Arm | `c1` | `c2` |
|---|---|---|
| `dx-B-noexp2` | 23.7 dB / 0.897 | 27.2 dB / 0.932 |
| `dx-B-tile-nooffload` | 23.9 dB / 0.905 | 27.3 dB / 0.937 |

Both pass bar 10 (≥ 22 dB, ≥ 0.80) on both clips.

The remaining gap, ~34.2 dB on the same latents, is the two stacks' bf16 conv numerics. Tiled and untiled B decodes
land within 0.1 dB of each other. No diagnostic meets the fix2 resolve rule (bar 9: ≥ 35 / ≥ 32 / ≥ 0.98), so the
judged arm `t2v-B-fix2` did not run.

## 4. VACE and the VACE VAE kill-test

| Arm | `v0` (cold) | `v1` warm total | `v1` decode | Card peak | Peak RSS | Outputs |
|---|---|---|---|---|---|---|
| A, `vace-A` | 307.9 s | **264.8 s** | 25.6 s | 20,871 MiB | 23,240 MiB | sound |
| B, `vace-B` | 178.9 s | **170.2 s** | 26.4 s | 17,854 MiB | 23,058 MiB | **`v0` 18.8 M NaN, `v1` all NaN** |
| V1 `vace-B-bf16` (branch, `WAN_VAE_DECODE_DTYPE=bfloat16`) | — | 166.0 s | 15.9 s | 16,348 MiB | — | `v1` sound; x0 NaN (input latents NaN) |
| V2 `vace-B-fp32` (branch, `WAN_VAE_MIOPEN_BYPASS=fp32`) | — | 249.5 s | 60.2 s | 18,868 MiB | — | `v1` sound; x0 NaN (input latents NaN) |

- **VACE's decode is not slow.** VACE's fp32 tiled decode with MIOpen takes ~26 s on both stacks, not the 370 s the
  bf16 T2V decode once took with MIOpen's naive Conv3d (11-03). The kill-test's premise was weak from the start.
- **Neither variant is a win.** V1 is 1.7× faster at the decode, but needed 4×; V2 is 2.3× slower.
- **`vace-B` corrupts even its first clip.** VACE encodes the keyframe through the same VAE before decoding, so `v0`'s
  decode is already the second use.
- **The variants' own `v1` clips are sound.** V1 decodes through a freshly built bf16 copy. V2 is the first clip in its
  process. Neither tests the allocator.

## 5. Stable Audio

- **Arm A works.** It renders `a0` in 5.3 s and `a1` in 3.2 s (20 steps), with a card peak of 14,423 MiB. That is
  twice the lane's 7,000 MiB estimate; SA5 is judged on B only, so A's peak is reported, not judged.
- **Arm B crashes** in `a0` at step ~9 of 20: torchsde's Brownian interval recurses until `RecursionError` after
  `tb=500.00006 > t1=500.0`. This is a float-precision issue in `CosineDPMSolverMultistepScheduler`'s SDE noise on the
  B stack, independent of the allocator. Card peak 3,259 MiB.
- **SA3 count fixed.** `collect` first counted 11 "dummy-object" lines, but they were torchsde traceback lines. It now
  counts diffusers' "requires the torchsde library" message, and SA3 passes: torchsde is installed, and the scheduler
  loads. SA1 and SA2 still miss, since no render completed.

## 6. Findings

1. **`expandable_segments` on ROCm 7.2 / torch 2.13 is unsafe for diffusers lanes that offload.** The lane env sets it
   (`PYTORCH_HIP_ALLOC_CONF`), and so does the XTX runtime pod (`PYTORCH_ALLOC_CONF`). With model CPU offload, B holds
   ~19 GiB outside torch's accounting and corrupts reused modules. Any in-pod diffusers lane on `527e2714` that renders
   twice in one process would produce NaN or black output after the first request. **This blocks !1237, !1238 and
   !1239 as written,** whatever the bars say.
   - qwen-image (`cpuOffload: false`, and it bypasses the entrypoint) is outside this pattern. It was not tested here.
   - vLLM lanes do not move modules. Whether the pod-level setting harms them is untested.
2. **A sound B configuration exists:** both allocator variables without `expandable_segments`.
   - It is untiled, decodes in ~6 s like A, and clips take 61–70 s against A's 122 s (denoise 2.35 s/step against
     5.7 s).
   - On the pre-registered bars it would still miss bar 9 by ~1 dB (34.2 dB / 0.979 decode equivalence) and bar 6 (card
     peak 24,431 MiB against 18,000; A peaks at 24,466 MiB too).
   - Whether a ~34 dB decode difference and A-equal VRAM are acceptable is the owner's judgment. A rerun would need a
     new row that says so before data. The MP4s are in the evidence folder for a side-by-side look.
3. **The live T2V lane runs ~80 MiB from OOM.** On `b1f98ad0` its untiled decode peaks the card at 24.47–24.49 GB for
   a 33-frame 832x480 request. A longer clip or a larger size would fail.
   - Cheapest mitigation: `ENABLE_VAE_TILING=1` (B's tiled decode needs ~12.5 GB); it costs ~10 s of decode.
   - Alternative: free the offloaded text encoder's cache before the decode.
   - Neither was measured on A. This is a follow-up, not a change made here.
4. **Stable Audio needs a torchsde fix on the B stack** before !1239 can move. Candidates: clamp the Brownian
   interval's query time to `t1`, or pin a torchsde release that clamps it.
5. **Runbook nit (phase D link check):** read the slot link at the upstream port, `sudo lspci -vv -s 01:00.0 | grep
   LnkSta:`. The GPU function 03:00.0 sits behind the card's own switch and always reads 16 GT/s x16.

## 7. Cleanup

- **Nothing changed on the cluster.** No Model, image or controller changes were made. The arms were `ctr run`
  containers, all exited.
- **Node files.** `~/wan-ab-2026-11-05/` on cblevins-7900xtx (its own NVMe) keeps the latents and compile caches;
  delete it when the evidence is no longer needed.
- **Hand-back.**
  - The owner deletes the lease (`kubectl delete gpulease -n flexinfer-system s9-wan-decode-ab`), or it expires at
    03:09:34Z.
  - One proxied warm-lane request reclaims the card.
  - "XTX free" was written to the shared notes at 01:20Z.

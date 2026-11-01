# D1 instant flex card: what shipped, what the window applies, KT-SWAP (2026-11-01)

Status: **live-neutral half merged; window half staged as Drafts.** Brief D1 of
`.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` (§6.1, §9).
Kill-test row: `gfx1100-xtx-inpod-swap-2026-11-01` in `.loom/60-validation-matrix.md`
(pass rule written, not yet run).

## 1. What is on master (live-neutral)

| MR | What | Live effect |
|---|---|---|
| !1224 | `ef28b7b0` on the 7900xtx prewarm list. Harbor already had `pinned-ef28b7b0d1cb` (2026-10-28 05:30Z, from the 5930k entry). Adds `controllers/runtime_pin_contract_test.go`: a certified gfx1100 runtime set. A repin fails CI unless the GPUProfile `runtime.image` and the live gfx1100 DaemonSet move together and every certified digest is prewarmed on the XTX. | XTX prewarm pod rolled 2026-11-01 16:07Z; `ef28b7b0` resident (container `image-5` ready). Runtime pod and warm lane untouched. |
| !1232 | `7ae8e92f` (`rocm-gfx1100-serving-v032-5ca58769`, §3.1) on the 7900xtx prewarm list and in the certified set. | Prewarm pod rolls. The prewarm reference makes `harbor-flexinfer-pin-tags` tag it `pinned-7ae8e92ff28a`; it had no pinned tag and no pull. |
| !1227 | `flexinfer-bench swap`, the swap-floor profiler (`docs/user/swap-profiler.md`). Also fixes `build_binaries`, which compiled `cmd/flexinfer-bench/main.go` alone. | None: a CLI mode. |

## 2. What the window applies (owner, ≥ 2026-11-06 00:15Z)

The window opens with the XTX soak verdict. Before any of these, !1203 must be
applied and KT-FLEX must have passed, on an earlier evening than the repin
(design §7). Each Draft carries its own paste sequence and rollback. Merge
`master` into each branch first and check `git diff origin/master --stat`.
Flux applies everything on merge, so no `kubectl apply` paste is needed:
`flexinfer-models` covers `deploy/models` and `deploy/gpuprofiles`, the
HelmRelease covers the runtime DaemonSet.

| Order | MR | Change | Gate |
|---|---|---|---|
| 0 | — | Record the warm lane's Deployment baseline: `flexinfer-bench swap --probe-url http://qwen38-27b-xtx-warm-canary.flexinfer-system.svc:8000 --cycles 3 --out baseline-warm.json` | before 2 |
| 1 | !1234 | XTX runtime profile and gfx1100 GPUProfile `cfefae18 → 7ae8e92f`, plus `sharedCompileCache: true`. One runtime pod roll; snac reloads at its next election. | after KT-FLEX |
| 2 | !1235 | Warm lane in-pod: drop `dedicatedDeployment`, compile cache `scope: Image` on the default hostPath. | after 1 is Running with 0 restarts |
| 3 | KT-SWAP | Hold the card with a GPULease on `7900xtx-textgen` and run the profiler (matrix row). Use the fallback arm (warm-lane reloads) unless a v032 image with bitsandbytes exists by then. | after 2 is Ready in-pod |
| 4 | !1236 Qwen-Image, !1237 Wan T2V, !1238 Wan VACE, !1239 Stable Audio | Creative lanes in-pod, one per MR, stacked in that order. | **Blocked** on the runtime image (§3.2) |

Rollbacks:

- !1234: revert. `cfefae18` stays prewarmed and pinned.
- !1235: `dedicatedDeployment: true` by `flexinfer_update_model`, or revert.
- !1236–!1239: revert.

## 3. Found while building it (not in the design)

### 3.1 `ef28b7b0` is a stale build of its persona

`ef28b7b0` (c444b5ff, 2026-10-26) predates the runtime half of the S6b
pre-work. At c444b5ff, `internal/runtime/load_progress.go` and
`internal/runtime/compile_cache.go` are absent, and `manager.go` carries no
`SpecHash`. So on that image:

- there are no in-pod load substages and no short-KV-pool recheck (R4);
- a spec edit does not reload the lane in-pod (R1);
- the image-scoped compile cache is not re-pointed (C5).

KT-SWAP's substage breakdown cannot be taken on it.

CI rebuilt the same persona four times since. The newest is `7ae8e92f` =
`rocm-gfx1100-serving-v032-5ca58769` (pipeline 36312, job 442027,
2026-10-30 22:08Z):

- the same `build/Dockerfile.runtime-serving-v032`;
- the same frozen stack: 265 packages unchanged, diffusers and snac added;
- the same build-time verify, now with 28 diffusers names;
- `2f1f6134` (R4), `04c7df40` (C5), `1b62ad00` and `ea8bbb8f` (Qwen-Image
  2.1) are all in its history.

The repin Draft targets it. `ef28b7b0` stays certified and prewarmed as a
one-line fallback.

### 3.2 The v032 persona cannot serve four of the five XTX lanes in-pod

Checked read-only inside `ef28b7b0` (`kubectl exec` into the 5930k prewarm
container, against `/opt/flexinfer/vllm-stack.txt` and the installed
diffusers 0.42). `7ae8e92f` has the same package set.

| Lane | Missing | Effect in-pod |
|---|---|---|
| warm lane (vllm) | nothing | serves; parity and decode are KT-SWAP's to prove |
| Qwen-Image 2.1 | bitsandbytes | NF4 transformer and text encoder cannot load (`ef28b7b0` also lacks the R1–R3 server code; `7ae8e92f` has it) |
| Wan T2V | imageio, imageio-ffmpeg (ftfy optional here) | `export_to_video` falls back to OpenCV `mp4v`, which browsers do not play |
| Wan VACE | ftfy (and imageio) | `WanVACEPipeline.basic_clean` calls `ftfy.fix_text` unguarded: every request fails |
| Stable Audio | torchsde | `CosineDPMSolverMultistepScheduler` is a dummy that raises at load |
| whisper-large-v3-turbo (runtime-eligible today) | librosa, soundfile | no audio decode after the repin. Idle since 2026-06-24; !1207 moves ASR to the Radeon VII |

The unblock is one v032 rebuild adding these to the overlay, under the
frozen-stack constraint, with the verifier extended. Then certify it,
prewarm it and repin in one line. bitsandbytes on ROCm 7.2 is the unproven
piece (R §7e S6e).

### 3.3 Other findings

- **Per-lane compile caches do not exist in-pod.** The runtime pod mounts only
  `/var/lib/flexinfer/compile-cache` (plus `/cache/compile` with
  `sharedCompileCache`). The warm lane's and Qwen-Image's custom
  compile-cache hostPaths are not mounted there. The lane Drafts move each lane
  to `scope: Image` on the default base. The new placement-layout test fails a
  lane that keeps a custom hostPath or `dedicatedDeployment`.
- **The runtime reports only the current substage.** There are no timestamps,
  so the profiler polls (250 ms) and the durations are accurate to one
  interval.
- **vLLM enters `Compiling` twice**, before and after the KV profile. The
  profiler sums both visits.
- **The diffusers and snac servers print none of vLLM's lines.** Their loads
  show only `Spawn` and `Ready`, so the diffusers floor has no breakdown
  without server-side substage lines.
- **The runtime does not evict in multi-model mode** (both gfx1100 profiles).
  A swap is DELETE then POST. The POST answers 503 until the runtime's VRAM
  accounting releases the outgoing lane, so the profiler records that
  admission wait as its own phase. The design's substage list had no slot
  for it.
- **The first request after Ready can carry first-use JIT.** On segmented
  attention it is +0.5–1.5 s (R §7d''). The profiler records it as
  `firstTokenS` and swap-to-first-token, apart from the load.
- **The warm lane runs vLLM prefix caching.** A repeated parity prompt would
  compare a warm cache with a cold one, so every probe sends a fresh vLLM
  `cache_salt`.
- **The profiler loads behind the controller.** Without a GPULease the
  controller reloads its lanes into the VRAM being measured.
- **The Wan lanes' dedicated memory limit (48 GiB) equals the whole runtime
  pod limit.** Qwen-Image's NF4-on-load would run inside it too. Watch host
  RAM when those lanes move.

## 4. Owner questions before the window

1. **Repin target.** `7ae8e92f` (recommended: substages, KV recheck, reload
   on edit, shared cache) or `ef28b7b0` as designed?
2. **Whisper on the XTX.** Accept that it cannot transcribe in-pod after the
   repin (recommended: idle since June, superseded by !1207), or add librosa
   and soundfile to the next rebuild first?
3. **Creative lanes.** Start the v032 rebuild with bitsandbytes (ROCm 7.2),
   torchsde, imageio, imageio-ffmpeg, ftfy, librosa and soundfile now, so
   !1236–!1239 can follow the warm lane? Or keep the creative lanes dedicated
   this round and run KT-SWAP's fallback arm only?
4. **Weights residency (§6.1 step 5).** It stays deferred until KT-SWAP says
   whether `LoadingWeights` dominates.

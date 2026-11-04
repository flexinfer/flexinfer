# Result: KT-FLEX, the 7900 XTX as the flex card (2026-11-01)

Kill-test KT-FLEX of `10-research-voice-stack-and-gpu-placement-2026-10-30.md` §9, run after
!1203 (the GPU placement layout) was applied in the owner's window. Status: **FAIL on the first
render. The XTX dropped off PCIe within about a minute of its first sustained diffusers compute.**
The layout itself applied exactly as its note predicted; the card did not survive the load.

| MR / branch | State | What |
|---|---|---|
| !1203 `feat/gpu-placement-layout` | merged 55a22375 at 22:59Z | 5930k workhorse primary 900 `minReplicas 1`; XTX warm lane 300 ondemand; four creative lanes on the XTX at 500; voice twins parked below 300 |
| !1247 `ops/wan21-first-byte-budget` | merged 4858de51 | `flexinfer.ai/lane-first-byte-budget: "600s"` on both Wan 2.1 lanes (the proxy would otherwise 504 a 480p clip at 120 s) |
| `revert/gpu-placement-layout` | prepared, **not pushed** | `git revert -m 1 55a22375`; applies cleanly, keeps !1247, matrix JSON consistent |

## Timeline (UTC)

| Time | Event |
|---|---|
| 22:59 | !1203 merged after one flaky-test retry (`TestLaneEnergy_RefreshLoopEndsWithRun`, a timing race in D7's agent test, 35/35 green locally) |
| 23:01 | HelmRelease and `flexinfer-models` reconciled. XTX prewarm DaemonSet grew to 13 images; the 5930k workhorse stayed on its 3-day-old pod; six 5930k members `ParkedBehindPrimary`; warm lane Ready at 300 |
| 23:16 | All four creative lanes `Cached=True` on the XTX, prewarm 13/13 Ready. Staging took 15 min, not the estimated 30 to 60 |
| 23:17:29 | Render 1: `POST /v1/images/generations` (`qwen-image-21-gfx1100`, 1024x1024) through `flexinfer-proxy`. The lane preempted the warm lane and loaded |
| 23:21:35 | Lane Ready; the render started |
| 23:22:30 | Kernel: `amdgpu 0000:03:00.0: device lost from bus!`, then an SMU `TransferTableSmu2Dram response:0xFFFFFFFF` flood and `snd_hda_intel 03:00.1 CORB reset timeout` |
| 23:22:36 | Agent sentinel: `flexinfer.ai/gpu.health=degraded`, `card0: pcie-link-down (current_link_speed=Unknown)`; every XTX lane Failed |
| 23:23:07 | Kernel: `MES failed to respond to msg=REMOVE_QUEUE`, `MES might be in unrecoverable state, issue a GPU reset` |
| 23:23:17 | Proxy returned 504 `upstream first byte budget exceeded` at 348 s; renders 2 and 3 got 503 `GPUDegraded` in 1 s |

Not run: the Wan T2V clip, the Stable Audio render, the warm-lane reclaim check, the 24 h health
window. Text serving was unaffected throughout (the 5930k workhorse answered a chat probe in 0.3 s).

## Pass rule vs outcome

| Criterion | Outcome |
|---|---|
| each creative first output within 5 min | **fail**: no output; the card died ~55 s into the first render |
| warm lane Ready within 5 min of the last creative idle-out | not reached |
| 0 SMU or `amdgpu` errors in the host kernel log | **fail**: `device lost from bus`, SMU bus errors, MES timeout |
| `flexinfer_gpu_healthy{node="cblevins-7900xtx"} == 1` for 24 h | **fail**: 0 since 23:22Z |

## What the dead card looks like

Config-space reads after the drop: the CPU root port `0000:00:01.1` answers; the card's own PCIe
switch **upstream port `01:00.0` answers and its link to the host is still up at 16 GT/s x16**;
the on-card downstream port `02:00.0` and the GPU functions `03:00.0/.1` read `0xffff`. The
slot link never dropped. The GPU core died beneath its own PCIe front end. The front end is
slot-powered; the core rails come from the 8-pin connectors through the card's VRM. That points
at core power delivery or SMU-managed power, not at slot signal integrity.

Two things the soak posture got wrong, found while reading the hardware:

- `card0/device/current_link_speed` is the card's *internal* switch-to-GPU link and always reads
  16 GT/s, also on the healthy 5930k. The host link is the upstream port's `LnkSta`. The
  `amdgpu.pcie_gen_cap=0x00070007` cmdline cap never applied: the root port's target link speed
  and trained link were both 16 GT/s (Gen4).
- journald keeps only the current boot on this host; rsyslog `kern.log*` is the record.

## The healthy twin

The same card model has run for 133 days on `cblevins-5930k` without a drop.

| | cblevins-7900xtx (fails) | cblevins-5930k (stable) |
|---|---|---|
| board / BIOS | ASUS ROG STRIX B650-A GAMING WIFI, 1811 (2023-10-07) | HUANANZHI X99-TF (Intel), 5.11 |
| host PCIe link | Gen4 16 GT/s x16 | Gen3 8 GT/s x16 ("downgraded" by the X99 root port) |
| kernel | Ubuntu 7.0.0-38-generic, Secure Boot, lockdown `integrity` | Fedora 42 6.14.9 |
| GPU firmware | linux-firmware 20240318 (Ubuntu): SMU 78.130.0, MES 128 | linux-firmware 20250509 (Fedora): SMU 78.128.0, MES 120 |
| power posture | DPM auto, cap 294 W (the card's minimum), stock OD | DPM auto, cap 303 W, stock OD |

## Actions taken (owner: "figure out a fix", then "Yes, apply both")

1. **Firmware pin, applied, effective at the next boot.** The 5930k's 14 Navi 31 blobs
   (`gc_11_0_0_*`, `smu_13_0_0`, `psp_13_0_0_*`, `sdma_6_0_0`, `vcn_4_0_0`, `dcn_3_2_0_dmcub`)
   are installed uncompressed in `/lib/firmware/updates/amdgpu/` on the XTX host, md5-identical
   to the originals; `update-initramfs -u` run. The loader searches `updates/` first and plain
   names before `.zst`. Expect `smu fw version ... (78.128.0)` in dmesg after the power cycle.
   Rollback: `sudo rm -r /lib/firmware/updates/amdgpu && sudo update-initramfs -u`.
2. **Gen3 cap at the root port, not applied.** `setpci` config-space writes are refused under
   Secure Boot lockdown. The path is the BIOS: PCIEX16_1 link speed = Gen3, set at the power
   cycle. Verify with `lspci -vv -s 00:01.1 | grep LnkSta` (want 8 GT/s) and `pp_dpm_pcie`.

## Owner decisions

1. **Layout.** Keep !1203 (text stays on the always-on 5930k workhorse; creative waits for the
   XTX) or merge the prepared revert (creative back to the 5930k at the old equal-priority tie,
   workhorse back to 500 ondemand). Recommendation: keep.
2. **Recover the card.** Hard power cycle (Tuya plug). etcd keeps quorum: voters are the 5930k,
   7900xtx, gtx980ti and k3s-cp-1. While at the console: BIOS PCIEX16_1 = Gen3; note the BIOS
   version for the 1811 → 38xx update.
3. **Power delivery.** One PSU cable per 8-pin, no pigtail; check PSU rating and age against a
   card with transients well above 400 W.
4. **Discriminator.** Swap the two cards between hosts. If the drop follows the card, it is the
   card; if it stays with the B650 host, it is the PSU, board or BIOS.

## Follow-ups

- Rerun KT-FLEX only after a cap-free burn of ≥ 10 min passes on the new firmware; add the
  host-link `LnkSta` (upstream port) and `pp_dpm_pcie` to the soak checks.
- Everything downstream on the XTX is blocked until then: D1 repin and in-pod lanes
  (!1234, !1235), KT-SWAP, the KT-JOBS flex-card arm, XTX power sampling, !1243.
- Make `TestLaneEnergy_RefreshLoopEndsWithRun` deterministic (synchronous stop or `>= 4`).

## Addendum 2026-11-02: the burn on the new posture also fails

The owner power-cycled the node through its Home Assistant plug (`switch.christmas_tree_socket_1`,
"cblevins-7900xtx Socket 1") at 14:16Z after k3s was stopped and disks synced; the host booted on
its own at 14:18Z. The pinned firmware loaded (SMU 78.128.0, MES 120) and, with it, the
`amdgpu.pcie_gen_cap` cap took effect for the first time: the host link trained at Gen3 at both
ends and `pp_dpm_pcie` topped out at the 8 GT/s level. The card re-enumerated, the node rejoined
as a voter, and the warm lane reloaded to Ready.

The 600 s fp16 matmul burn then ran at 14:23:23Z inside the runtime pod with the warm lane
resident: 71 TFLOPS steady, 282–287 W, junction 65→78 C, sclk ~2050 MHz, link 8 GT/s, zero kernel
errors for 357 s. At 14:29:14Z the kernel logged `device lost from bus!` with no preceding line;
the GPU core and on-card downstream port read `0xffff`, the upstream port stayed alive and the
host link retrained to Gen4 once the SMU that held the cap was gone. Matrix row
`xtx-pcie-soak-2026-11-02`, decision `fail`.

**Conclusion.** Every software axis is now exhausted: kernel 6.14 → 7.0, GPU firmware newer →
older (the stable twin's exact blobs), host link Gen4 → Gen3, ASPM off, power cap at the floor,
clock cap, card reseat, OS reinstall. The card dies under its first sustained compute on every
boot regardless. The fault is hardware: the card (VRM or die), the PSU or its 8-pin cables, or
the B650 board and its 2023 BIOS. The slot link is not involved. Next: swap the two XTX cards
between hosts (if the drop follows the card, it is the card; if it stays with the B650 host, it
is the PSU, board or BIOS), one PSU cable per 8-pin, BIOS 1811 → 38xx. Until then the XTX is not
a flex card: D1 repin and in-pod lanes, KT-SWAP, the KT-JOBS flex arm and XTX power sampling stay
blocked, and the layout (!1203) stays as is with text on the always-on 5930k workhorse.

## Addendum 2026-11-03: the burn passes after the hardware work

The owner did physical work on cblevins-7900xtx on 2026-11-02 (recorded separately); the host came
back at 19:48Z on the same XFX card with BIOS 1811, the pinned firmware and a clean boot, but the
host link trained at Gen3 **x8** where every earlier boot was x16. The same 600 s fp16 matmul burn
that had killed the card at 357 s that morning ran 01:01:51–01:11:53Z and passed: 71 TFLOPS steady,
277–289 W, junction 61→82 C, zero kernel errors, card alive afterwards. Row `xtx-pcie-soak-2026-11-03`,
decision `conditional`: KT-FLEX may rerun, and `promote` waits on a KT-FLEX pass plus 24 h of
`flexinfer_gpu_healthy == 1`. The x8 link is a loose end: half the lanes did not train after the
reseat, which argues for one more careful reseat before the slot is called good.

## Addendum 2026-11-03 (later): the KT-FLEX rerun

After the passing burn, KT-FLEX ran again through the proxy from 05:07Z to 05:56Z.

| Step | First output | Result |
|---|---|---|
| Qwen-Image 1024² (cold) | 173 s | pass |
| Qwen-Image 1024² ×2 | 72 s, 71 s | pass |
| Wan T2V 33 frames 832x480, cold | cut at 120 s by the proxy | proxy bug, fixed in !1264 |
| Wan T2V, retried on the Ready path | 506 s | 200, valid MP4; over the 5-minute rule |
| Stable Audio 40 s (cold) | 194 s | pass |
| Warm lane reclaim after the last creative hold | ~2 min | pass |

The card carried all of it with zero amdgpu, SMU, MES or AER kernel lines, peaking at 203 W and 66 C.

**The proxy bug.** The cold-start queue drained requests without loading the lane's
`flexinfer.ai/lane-first-byte-budget`, so after a proxy restart a lane whose first request was cold
ran under the 120 s default. Qwen-Image and Stable Audio passed only because they render inside two
minutes. !1264 loads the budget before queueing (with regression tests); !1266 rolled the proxy.

**Wan's time.** The diffusers server reports denoise 137 s and VAE decode plus postprocess 367 s,
and the July proving run of the same clip took 6m57s, so Wan was never a 5-minute lane on this card.
!1265 raises both Wan lanes' first-byte budget from 600 s to 900 s. The decode being three quarters
of the clip, at 60–110 W with the GPU fully busy, is a performance problem of its own.

Row `gfx1100-flex-card-layout-2026-11-03`, decision `conditional`: the riskiest assumption holds;
`promote` waits on 24 h of `flexinfer_gpu_healthy == 1` (to 2026-11-04 05:56Z) and on either a Wan
criterion that matches its decode-bound floor or a faster decode.

## Addendum 2026-11-03 (afternoon): Wan's decode is MIOpen's naive kernel, not a floor

The owner approved an A/B on the XTX to tell a per-pod first-use cost from the steady state. Three
clips of the KT-FLEX request (832x480, 33 frames, 20 steps, seed 42) went through `flexinfer-proxy`
from 15:22Z.

| Clip | Pod state | Result | Denoise | Decode + postprocess |
|---|---|---|---|---|
| 1 | cold | 502 `lane_unresponsive` at 81 s: both forwards `connection refused` | — | — |
| 2 | first render in the pod | 200, 505 s | 132 s | 370 s |
| 3 | warm, same pod | 200, 489 s, byte-identical MP4 | 116 s | 371 s |

The decode is steady state. During it the card read 100 % busy at about 2170 MHz sclk and 73 W on
average (36–115 W). Memory sat mostly idle: `mem_busy` at 0 %, mclk at 96 MHz in half the samples,
and fclk at 601 MHz in 58 %. That is a long, low-occupancy kernel.

Microbenchmarks inside the Wan pod (torch 2.6.0, HIP 6.4, MIOpen 3.4, diffusers 0.36.0):

| Case | MIOpen (`cudnn.enabled=True`) | PyTorch im2col + GEMM (`False`) |
|---|---|---|
| Conv3d 96→96 k3, (1,96,6,480,832), bf16 | 7,876 ms | 31 ms |
| same, fp32 | 9,645 ms | 219 ms |
| VAE decode, 2 latent frames → 5 frames, bf16 | 46.4 s | 0.79 s |
| VAE decode, 9 latent frames → 33 frames, bf16 | (370 s in the lane) | 5.3 s and 10.7 s, peak 11.6 GiB |
| VAE decode, 2 latent frames, fp32 | 60.8 s | OOM: one 15.4 GiB im2col allocation |

MIOpen logs `FW Chosen Algorithm: ConvDirectNaiveConvFwd` with `workspace = 0` under
`MIOPEN_FIND_MODE=2` (the lane's), under `MIOPEN_FIND_MODE=1` and with `cudnn.benchmark=True`
alike. PyTorch asks for a zero-workspace convolution, which leaves the naive kernel as the only
3D solver on gfx1100. The find mode is not a lever. The MIOpen kernel cache for the lane on the
host was last written 2026-07-14, so nothing new compiled. The July 14.6 s decode is still
unexplained: the image recipe matches, and the host posture changed since (kernel 7.0, pinned
firmware, Gen3 x8, 2100 MHz / 294 W).

**Fix.** `build/server-diffusers.py` runs a 16-bit Wan VAE's `encode`/`decode` with MIOpen off on
ROCm (`fix/wan-vae-bypass-miopen`). Expected clip time: about 120–135 s of denoise plus 5–11 s of
decode, inside the 5-minute rule. The fp32 VAE of the VACE lane keeps MIOpen because of the
im2col size, so VACE stays slow until its VAE moves to bf16 or the conv is chunked. Taking it live
needs the CI `diffusers-video` build, an XTX prewarm of the new digest and a repin of
`wan21-t2v-1p3b-gfx1100`.

**Second proxy race.** Clip 1 is a separate cold-start bug. `waitForReady` returns on
`Model.Status.Phase == Ready` (`internal/proxy/queue.go`). The Service had no ready endpoint 100 ms
later, so both forwards, 50 ms apart, hit a rejecting ClusterIP and the client got a 502.
Yesterday's cold Qwen-Image and Stable Audio requests won the same race by luck.

## Addendum 2026-11-04: Wan verified on the bypass build

Taking the fix live took three CI runs. The `diffusers-video` publish hit its 1 h timeout twice while
BuildKit re-extracted the PyTorch 2.6 base (jobs 458565 and 460030; job 426137 had failed the same
way on 10-27). !1273 raised the timeout to 2 h, and job 460942 (API pipeline 37384) published
`rocm-gfx1100-2c0cc642` = `sha256:b1f98ad0`. 58 of its 60 layers are `83cda206`'s; the other two
are the server scripts. !1275 repinned `wan21-t2v-1p3b-gfx1100` and added the digest to the XTX
prewarm. VACE stays on `83cda206`, which is also the rollback. The proxy had been rolled to
`bdd4bd25` in !1271, which carries the cold-start dial retry of !1270.

Two clips of the KT-FLEX request (832x480, 33 frames, 20 steps, seed 42) went through
`flexinfer-proxy` from 01:33Z. The warm lane held the card when the first one arrived.

| Clip | Pod state | Result (wall) | Denoise | Decode + postprocess |
|---|---|---|---|---|
| 1 | cold: preempt the warm lane, start, load, compile | 200, 252 s | 144 s | 7.3 s |
| 2 | warm, same pod | 200, 124 s | 115 s | 6.4 s |

The pod logged `Wan VAE encode/decode run with MIOpen off`. Decode went from 367–371 s to 6–7 s,
and the cold clip's first byte from 506 s to 252 s, inside the 5-minute rule. The cold request
took the proxy's cold-start path and got a 200; on 11-03 that path returned a 502. The two MP4s
are byte-identical, and frames 0, 16 and 32 show a coherent scene.

Row `gfx1100-flex-card-layout-2026-11-03`: criterion 1 now passes for all three creative lanes,
and criteria 2 and 3 passed on 11-03. The row waits only on criterion 4, 24 h of
`flexinfer_gpu_healthy == 1` until 2026-11-04 05:56Z; at 01:40Z it had held for 19h44m. VACE's
fp32 VAE is still on MIOpen's naive kernel and is not part of this row.

## Addendum 2026-11-04 (promotion): the 24 h window

The window ran from 2026-11-03 05:56Z to 2026-11-04 05:56Z. The owner approved the promotion on
2026-11-04, with the agent log standing in for the hours Prometheus missed.

| Source | Covers | Result |
|---|---|---|
| `flexinfer_gpu_healthy{node="cblevins-7900xtx",gpu="0"}` | 05:56Z to 03:50Z | never 0; a sample every minute up to 04:00Z (1,321 one-minute buckets in the 24 h) |
| agent log (`flexinfer-agent-5d5w6`) | 02:57:55Z to 14:57:55Z | 1,441 readings, all `gpu.health=ok`, no gap over 90 s |
| `journalctl -k -b` on the node | the whole boot (since 11-02 19:47 local) | 0 amdgpu/SMU/MES/AER errors; only the boot-time SMU init lines |
| PCIe link | read at 06:00Z | Gen3 x8 at the root port (`00:01.1`, `01:00.0`); x16 behind the card's own bridge |

Prometheus lost samples from 03:50Z for reasons that had nothing to do with the card. Writes to the
Longhorn device under its volume on `cblevins-radeonvii` failed with `Medium Error / Unrecovered
read error` at 03:50:36Z, while Longhorn was rebuilding replicas. ext4 aborted its journal and
remounted read-only, and every scrape from then on failed to write the WAL. Repair, approved by the
owner:
1. Suspend the HelmRelease and scale Prometheus to 0.
2. Attach the volume for maintenance on `cblevins-5930k` and run `e2fsck -f -y` (22 s). It
   corrected the free counts and optimized eight extent trees, and deleted no files.
3. Restart Prometheus. It cut the corrupt tail of WAL segment 24156 and has been ingesting since
   14:57Z.

Earlier, at 02:57Z, etcd lost its leader (term 421). k3s on the XTX, an etcd voter, exited and
systemd restarted it within 25 s. Every container on the node restarted once and Longhorn rebuilt
the node's replicas between 03:01Z and 03:04Z. The card reported healthy before, during and after.

Row `gfx1100-flex-card-layout-2026-11-03` is `promote`. The XTX flex-card layout of !1203 is
validated for the warm lane plus Qwen-Image, Wan T2V and Stable Audio. Not covered:
- VACE, whose fp32 VAE still uses MIOpen's naive kernel;
- in-pod switching (D1, Draft !1234), which has its own window.

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

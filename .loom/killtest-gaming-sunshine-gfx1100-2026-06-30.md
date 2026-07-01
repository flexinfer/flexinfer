# Kill-test verdict — Gaming mode v2 (Sunshine/Moonlight on gfx1100)

- **Plan**: `plan-gaming-mode-v2-sunshine-moonlight-on-gfx1100-7900xtx-declara-ced675` (Slice 1)
- **Date**: 2026-06-30
- **Node**: `cblevins-7900xtx` (192.168.50.125, Ubuntu 24.04.3, kernel 6.14.0-24, AMD Radeon RX 7900 XTX / RDNA3 / gfx1100 / NAVI31)
- **Verdict**: **PASS on the decisive substrate axis.** Remaining steps (live
  Moonlight stream) are low-risk engineering, not a viability bet.

## Load-bearing assumption
A privileged gfx1100 container with `/dev/dri` can run the **Mesa RADV** graphics
stack (Vulkan render) **and** hardware video **encode** (VA-API via `radeonsi`),
which Sunshine needs to GPU-render and HW-encode a game for Moonlight. The
flexinfer runtime image ships ROCm *compute*, not this graphics/encode userspace,
so it was unproven whether the substrate even supports it.

## What was tested (method)
Throwaway privileged containers on the node via `docker --context 7900xtx`, base
`ubuntu:24.04`, runtime-installed Mesa graphics + VA-API drivers. No flexinfer
inference workloads disturbed (probes use negligible VRAM; gemma4-26b primary +
kokoro-tts stayed Running throughout).

### Test 1 — RADV Vulkan render (criterion 1)
`docker --context 7900xtx run --rm --privileged --device /dev/dri ubuntu:24.04`
→ `apt install mesa-vulkan-drivers vulkan-tools` → `vulkaninfo --summary`:

```
GPU1:
    deviceType = PHYSICAL_DEVICE_TYPE_DISCRETE_GPU
    deviceName = AMD Radeon RX 7900 XTX (RADV NAVI31)
    driverName = radv
    driverInfo = Mesa 25.2.8-0ubuntu0.24.04.2
```
RADV is bound to the discrete 7900 XTX. (GPU0 = Raphael iGPU/RADV; GPU2 = llvmpipe
software fallback — present but we pin to the dGPU, so not disqualifying.)
**PASS.**

### Test 2 — Hardware VA-API encode (criterion 4), end-to-end
`vainfo` (radeonsi, navi31) reports HW encode entrypoints for H.264, HEVC
(Main/Main10), and AV1. Then actually encoded frames with ffmpeg VAAPI on the
NAVI31 render node (`/dev/dri/renderD128` = 7900 XTX; `renderD129` = iGPU):

```
ffmpeg -vaapi_device /dev/dri/renderD128 -f lavfi -i testsrc=1920x1080:rate=60:duration=3 \
       -vf format=nv12,hwupload -c:v <codec> -f null -
```
| Codec       | Frames | Speed         | Encoded output |
|-------------|--------|---------------|----------------|
| h264_vaapi  | 180    | 5.67× realtime| 125 kB         |
| hevc_vaapi  | 180    | 7.89× realtime| 96 kB          |
| av1_vaapi   | 180    | 8.11× realtime| 98 kB          |

Multi-× realtime + real bitstream output ⇒ VCN 4.0 hardware encode, not CPU x264.
All three codecs Moonlight supports are HW-accelerated **inside a container**.
**PASS.**

## Negative-evidence search (per spec-riskiest-assumption rule)
Searched the disconfirming case. Real failure modes found, and how this setup
avoids each:
- **Sunshine needs `CAP_SYS_ADMIN` for DRM/KMS framebuffer capture**
  (LizardByte/Sunshine #3391, #2776). → Avoided two ways: (a) we run privileged
  (grants it), and (b) the headless-compositor approach has Sunshine grab the
  Wayland compositor output, not raw KMS, so KMS-master isn't required.
- **VA-API picks the wrong GPU / `adapter_name` ignored** (Sunshine #2521, #4555).
  → Pin `adapter_name=/dev/dri/renderD128` (verified = NAVI31) explicitly.
- **`renderD128` inaccessible in containers even when privileged**
  (jellyfin #9229; cgroup device-rule / group GID). → Not observed here: privileged
  + hostPath `/dev/dri` gave full access (devices are `root:video` card0/1 and
  gid 992 renderD128/9; privileged bypasses the group requirement). The production
  DaemonSet already mounts `/dev/kfd`+`/dev/dri` privileged, so this carries over.
- **gamescope `--headless` removed; use `--backend headless` (v3.15+)**
  and RADV is the supported Vulkan driver. → Image must ship a recent gamescope.

Sources: Games-on-Whales/Wolf (reference headless-container AMD streaming),
LizardByte Sunshine docs/issues, joleuger/containerized-headless-sunshine-howto
(Ubuntu 24.04 + wlroots headless + vuinputd).

## Remaining (needs a human Moonlight client — NOT a viability risk)
- Criterion 2: GPU app under headless gamescope (`--backend headless`,
  `--prefer-vk-device` pinned to NAVI31).
- Criterion 3: Moonlight on a LAN client pairs (PIN) and shows live moving video.
- Criterion 5: graphics-engine activity (`radeontop`/`rocm-smi`) during stream.

These are standard integration (Games-on-Whales/Wolf does exactly this on AMD in
production) and are the build target of Slice 2, not a separate bet.

## Implications for the plan
- Proceed with the **pod-based Sunshine architecture** (Slices 2–5). The fallback
  branch in the spec (host-level non-container session) is NOT needed.
- Slice 2 image layer must add: `mesa-vulkan-drivers` (RADV), `mesa-va-drivers`
  + `libva2`/`vainfo` (radeonsi VAAPI), a recent `gamescope` (≥3.15), a headless
  wlroots compositor (e.g. labwc/sway) or gamescope's own headless backend,
  `sunshine`, PipeWire/Pulse for audio, `/dev/uinput`+`/dev/uhid` for virtual
  input. Pin encode to `/dev/dri/renderD128`.
- Device/inventory note: this host has TWO render nodes (dGPU renderD128, iGPU
  renderD129) and the iGPU also exposes VAAPI — always pin the dGPU explicitly.

## Slice 2 on-node validation (2026-06-30, through the real software)
Validated the productized recipe (sway headless + Sunshine + VA-API on
renderD128) in a privileged container on cblevins-7900xtx, via
`docker --context 7900xtx run -i --privileged --device /dev/dri ubuntu:24.04`
installing the exact gaming-layer packages the Dockerfile does, then Sunshine
v0.23.1 from the LizardByte .deb. Findings:
- **gamescope is NOT packaged for Ubuntu 24.04** (apt Candidate: none) →
  recipe switched to `sway` 1.9 (headless wlroots, packaged). gamescope is a
  future in-session ergonomics add (build-from-source), not a blocker.
- `sway -c … ` came up headless (Wayland socket present); WLR_RENDERER=vulkan,
  WLR_RENDER_DRM_DEVICE=/dev/dri/renderD128 (dGPU).
- **Sunshine itself selected all three HW encoders** with capture=wlr:
  `Found H.264 encoder: h264_vaapi [vaapi]`, `hevc_vaapi`, `av1_vaapi`. It found
  the Wayland display and negotiated `zwlr_export_dmabuf_manager_v1` (capture
  path). The interim "No usable entrypoint / retrying" lines are Sunshine's own
  probe noise ("you can safely ignore those errors").
- Only gap: `Failed to create client: Daemon not running` = no Avahi/mDNS in the
  container → Moonlight won't auto-discover (pair by IP, or bundle
  `avahi-daemon`, now added to the image + launch script).
- **Live Moonlight pairing CONFIRMED 2026-06-30**: operator paired Moonlight from
  a LAN client and saw live motion (weston-simple-egl triangle + glmark2 horse)
  streamed off the node → criteria 2/3/5 all pass end-to-end (RADV render →
  VA-API HW encode → Moonlight). GOTCHA: an empty headless sway session streams
  as a static gray desktop (looks "stalled") — needs on-screen content; and
  vkcube in vulkan-tools is XCB-only (no Wayland WSI), so use weston-simple-egl /
  glmark2-es2-wayland as headless test clients.

Recipe artifacts: `build/sunshine-headless.sh` (sway + Sunshine launch),
`build/Dockerfile.runtime` INCLUDE_GAMING layer, `backend/sunshine.go`
(SunshineBackend, default gaming backend).

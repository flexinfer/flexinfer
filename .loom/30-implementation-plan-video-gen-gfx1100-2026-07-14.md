# gfx1100 video-generation lane plan

Date: 2026-07-14
Target: one AMD RX 7900 XTX (`gfx1100`, 24 GiB) in `flexinfer-system`

## Riskiest assumption + kill-test

**Load-bearing assumption**: `Wan-AI/Wan2.1-T2V-1.3B-Diffusers` can load and
produce a decodable 480p MP4 through Diffusers 0.36 on FlexInfer's dedicated
ROCm 6.4.1 / PyTorch 2.6 runtime on an RX 7900 XTX.

**Kill test**: build and push the dedicated image, reconcile only the temporary
`Model` CR on one gfx1100 node, then call `/v1/videos/generations` with a
33-frame 832x480 prompt. Pass requires all of the following within 30 minutes
after the model is cached: the Model becomes Ready; logs identify the RX 7900
XTX and `WanPipeline`; the endpoint returns HTTP 200; the base64 payload decodes
to an MP4; `ffprobe` reports 832x480 H.264 video with at least 33 frames; and a
contact sheet shows visible motion rather than repeated/corrupt frames.

**Failure mode if the assumption is wrong**: the operator/API work would expose
a lane whose actual ROCm kernel, dtype, memory, or encoding path cannot complete
on the target hardware. Do not add the Model to the reconciled production
kustomization until this test passes.

**Status**: passed on gfx1100 (2026-07-14)

Live evidence:

- The dedicated image imported PyTorch 2.6.0 (HIP 6.4), Diffusers 0.36.0,
  Transformers 4.48.0, `WanPipeline`, and `export_to_video`; Harbor digest:
  `sha256:e0ce46364bf8768ece95955e0c88234ab19dd1214760d3324cc30e58dd651a6f`.
- The node-local cache staged 27.6 GB in 5m43s; the flash loader copied it to
  tmpfs in 1m14s at about 374 MB/s and passed its integrity check.
- The dedicated pod identified one RX 7900 XTX (`gfx1100`, 24 GiB), loaded the
  local Wan pipeline in BF16 with CPU offload, became Ready, and stayed at zero
  restarts throughout generation.
- A proxy-forwarded invalid-frame request reached the video backend and returned
  its expected HTTP 400 `4k+1` validation error, proving the serve-path route
  without paying for a second generation.
- A 20-step request for 33 frames at 832x480 and 16 fps returned HTTP 200 in
  6m57s. Observed GPU utilization reached 100% and VRAM utilization reached
  about 59%; no OOM or runtime restart occurred.
- The decoded 164,365-byte payload has SHA-256
  `5448ead1aec1b17c34f839a28c57efa3c4f4c9739423026a93ff6d03d7011312`.
  `ffprobe` reports H.264, 832x480, 16 fps, 2.0625 seconds, and exactly 33
  frames. Frames 0/10/20/32 show coherent fox motion rather than duplicated or
  corrupt output.

Local evidence (gitignored):

- `.loom/local/video-gen-gfx1100/wan21-gfx1100-kill-test.mp4`
- `.loom/local/video-gen-gfx1100/wan21-gfx1100-contact-sheet.png`

The cleanup check also caught an unsafe first-draft `gpu.forcePromotion` flag:
it kept the cold video Model elected after scale-to-zero and prevented the warm
text sibling from reclaiming the shared GPU. The production manifest omits the
flag; recent proxy demand plus priority 500 is sufficient to preempt on request,
while the warm-pinned sibling wins again after video demand clears.

## Decision

Use Wan 2.1 T2V 1.3B through the existing `diffusers` backend, with a dedicated
video runtime image and a synchronous `/v1/videos/generations` endpoint.

Why this slice:

- Wan's official Diffusers integration supports the 1.3B text-to-video model,
  and the model card recommends its trained 480p operating point.
- The 1.3B model is materially smaller than current 5B/14B alternatives and is
  the least risky first proof on a 24 GiB card.
- The existing FlexInfer Diffusers backend already supplies cache staging,
  serverless activation, gfx1100 device isolation, health checks, and proxy
  forwarding. A new backend would duplicate those contracts.
- Video stays on a separate `diffusers-video:rocm-gfx1100` image. Wan's model
  card requires PyTorch 2.4 or newer, while the proven image lane remains on
  PyTorch 2.3; the dedicated image uses the repo-proven ROCm 6.4.1 / PyTorch
  2.6 base without moving existing SD/FLUX workloads.

## Evidence and uncertainty

Positive evidence:

- Diffusers documents `WanPipeline`, MP4 export, and memory-saving offload for
  Wan. Its current Wan example reports about 13 GiB VRAM for a much larger 14B
  configuration with group offload.
- The official 1.3B model card states that T2V 1.3B supports 480p and recommends
  480p over its less stable 720p behavior.
- AMD lists RX 7900 XTX as `gfx1100` with 24 GiB and supports the card in ROCm's
  Radeon/PyTorch matrices.

Negative evidence:

- Wan's native repository still has an open request for explicit AMD/ROCm
  setup. This plan relies on the generic PyTorch/Diffusers path, not a vendor
  promise from the native Wan runtime.
- The 1.3B model card says PyTorch must be at least 2.4, ruling out FlexInfer's
  existing PyTorch 2.3 Diffusers image for this lane.
- No upstream source found documents this exact model/runtime combination on
  gfx1100. The live kill-test remains the release gate.

Sources:

- https://huggingface.co/docs/diffusers/main/api/pipelines/wan
- https://huggingface.co/Wan-AI/Wan2.1-T2V-1.3B-Diffusers
- https://huggingface.co/docs/diffusers/api/utilities
- https://github.com/Wan-Video/Wan2.1/issues/106
- https://rocm.docs.amd.com/en/docs-6.1.5/reference/gpu-arch-specs.html
- https://rocm.docs.amd.com/projects/radeon/en/latest/docs/install/native_linux/install-pytorch.html

## Delivery slices

### Slice 1: runtime contract and kill-test

- Add bounded video request/response types and `/v1/videos/generations`.
- Load `WanPipeline` only for `pipelineMode: text2video`.
- Enforce the proven first-lane envelope: at most 832x480, 81 frames, 24 fps,
  50 denoising steps, one result, base64 MP4 response.
- Add BF16, VAE tiling, frame/FPS/size controller env wiring.
- Build the isolated ROCm 6.4.1 / PyTorch 2.6 image and run the kill-test above.

Exit: **passed**. The live hardware evidence is recorded above.

### Slice 2: declarative cold lane

- Add a `Model` manifest pinned to a gfx1100 node and the dedicated image.
- Use node-local cache, CPU offload, BF16, VAE tiling, `minReplicas: 0`, and a
  video-only `flexinfer.ai/serve-paths` annotation.
- Add `video-gen` service labels and a stable alias.
- Reconcile only after Slice 1 passes.

Exit: a proxy request can activate, generate, return, and later scale the lane
back to zero without routing image/chat traffic to it.

### Slice 3: operational hardening

- Record cold-start, generation latency, peak VRAM/RAM, and output dimensions.
- Pin the validated image digest in the Model manifest.
- Document curl/decode/ffprobe usage and the parent-CR cleanup procedure.

Exit: local and CI gates pass, the MR is merged, and the live lane uses the
immutable tested image.

## Rollback

Remove the video Model from `deploy/models/kustomization.yaml` (or delete the
parent `Model` during the temporary kill-test). Do not delete its Deployment or
pod directly; the controller recreates children. Existing image-generation
Models and their runtime tags are unchanged.

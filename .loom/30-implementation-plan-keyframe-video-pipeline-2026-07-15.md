# GTX 980 Ti keyframe to RX 7900 XTX video plan

## Riskiest assumption

Wan VACE 1.3B can accept a 512x512 DreamShaper PNG as its first-frame
condition and generate a coherent 33-frame, 832x480 H.264 video on the RX 7900
XTX without exceeding 24 GiB VRAM or destabilizing shared-GPU leadership.

## Kill test

Deploy VACE as an on-demand `image2video` Model, generate one DreamShaper
keyframe on `cblevins-gtx980ti`, and submit that exact PNG to VACE with 33
frames, 20 steps, 832x480, and 16 FPS. Pass only if:

- the response is a decodable H.264 MP4 with 33 frames at 832x480;
- the first output frame visibly preserves the keyframe composition;
- the VACE pod reaches Ready and completes without OOM, restart, or node error;
- the warm Wan T2V lane reclaims the shared GPU after VACE demand expires; and
- the 5930k Qwen workhorse remains Ready throughout.

Any OOM, node reset, repeated pod restart, invalid video, or loss of first-frame
conditioning kills the assumption. The one-call proxy pipeline must not ship
until this test passes.

## Evidence

Positive evidence:

- Diffusers 0.36 exposes `WanVACEPipeline` and documents VACE 1.3B support.
  <https://huggingface.co/docs/diffusers/v0.36.0/api/pipelines/wan>
- The upstream integration includes an I2V example that constructs a first
  frame followed by neutral frames, with a black preserve mask on frame zero
  and white generate masks thereafter.
  <https://github.com/huggingface/diffusers/pull/11582>
- Wan's published 1.3B consumer-grade figure is 8.19 GiB VRAM, leaving useful
  headroom on a 24 GiB card when CPU offload is enabled.
  <https://huggingface.co/docs/diffusers/v0.36.0/api/pipelines/wan>

Disconfirming evidence:

- The already deployed `WanPipeline` is text-to-video only; its call signature
  has no image input. A separate VACE model and pipeline are required.
- VACE's input contract is more complex than the model-card shorthand: I2V
  requires a full video/mask pair, and mask polarity is load-bearing.
- Published memory figures are not ROCm gfx1100 measurements and do not prove
  this repository's offload/runtime combination is stable.

## Slices

1. Make the existing optimized Wan T2V Model the warm primary. Move the second
   7900 XTX Qwen replica to higher-priority on-demand service; the equivalent
   5930k Qwen replica remains warm and advertises the same 128K service labels.
2. Add the bounded VACE `image2video` server mode and publish a gfx1100 runtime.
3. Deploy VACE on demand, execute the kill test, and preserve response/video,
   pod, node, and leadership evidence.
4. Only after slice 3 passes, add the one-call proxy endpoint that sends a text
   prompt to DreamShaper and pipes its PNG into VACE.

## Rollback

Restore Qwen 7900 to `minReplicas: 1`, `warmPolicy: primary`, priority 425, and
`forcePromotion: true`; restore Wan T2V to `minReplicas: 0` and remove its
`warmPolicy`. Delete the parent VACE Model if deployed. Never delete generated
Deployments or pods directly.

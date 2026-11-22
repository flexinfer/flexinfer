`scripts/wan-decode-ab.py` and `scripts/wan-decode-ab-host.sh` are the S9 harness. They run the Wan decode
A/B that gates the deferred in-pod Wan and Stable Audio lanes (!1237–!1239). Each arm is a `ctr run` on the
XTX from an image already in containerd, with the lane's weights, env and server code, while a GPULease
holds the card. The harness records timings, VRAM and RSS peaks, latents, frames and MP4s. `collect` then
applies the pre-registered rows `gfx1100-wan-decode-ab-v032-2026-11-05`,
`gfx1100-wan-vace-vae-bypass-2026-11-05` and `gfx1100-wan-denoise-cache-2026-11-05`, using PSNR/SSIM and
ffprobe. Plan: `.loom/32-iteration-plan-s9-wan-decode-ab-2026-11-05.md`.

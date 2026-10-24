The certified llama.cpp feature surface accepts `spec.config.specType: draft-mtp` behind a new
GPUProfile capability certificate, `flexinfer.ai/backend-certificate-llamacpp-draft-mtp`
(plus `-since` / `-evidence`). It drives the MTP (NextN) head inside the target GGUF with no
`--model-draft`, at llama.cpp's default draft window: `draftMax` / `draftMin` are rejected for
this spec type because b11847 removed `--draft-max` / `--draft-min` and aborts at startup when
they are passed. The existing gates still apply (`dedicatedDeployment: true`, a digest-pinned
selected image equal to the certified one). The gfx1100 GPUProfile certifies
`library/llamacpp@sha256:95c1d390…` (b11847 / ROCm 7.2.3). On Qwen3.8-27B Q4_K_M on the 5930k
it decodes 1.33–2.30x faster than the same pod without speculation (44.3 vs 31.3 tok/s gauntlet
median, 39–97 % draft acceptance by prompt). Greedy output is not bit-identical to the
non-speculative path (one near-tie token flip on 2 of 3 prompts), and no lane opts in yet.
Evidence: matrix row `gfx1100-llamacpp-b11847-2026-10-24`.

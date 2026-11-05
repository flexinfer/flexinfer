Build profile `gfx1100-serving-v033` in `build/runtime.yaml`: the stacked gfx1100 serving persona on
`vllm/vllm-openai-rocm:v0.33.0-rocm72` (ROCm 7.2.3, base pinned by digest), built from the same
`build/Dockerfile.runtime-serving-v032` with only the base changed. Canary-only (no promotion target,
no CI publish job) until its parity canary passes. Both serving personas now install diffusers
0.42.1, the security fix for GHSA-4prv-5hj7-c2xr; the 0.32 persona picks it up on its next CI publish.

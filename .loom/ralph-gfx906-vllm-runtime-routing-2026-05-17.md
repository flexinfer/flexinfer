# RALPH: gfx906 vLLM Runtime Routing Contract

Date: 2026-05-17
Branch: `codex/gfx906-vllm-runtime-routing`

## Goal

Close the next blocker from the gfx906 vLLM canary: the persistent
`runtime:rocm-gfx906` image does not bundle the `vllm` Python module, while the
GPUProfile already declares a standalone `vllm:rocm-gfx906` backend image.

## Acceptance

- Runtime-managed load eligibility can distinguish backends bundled inside the
  persistent runtime image from backends that need their dedicated image.
- `backend: vllm` on gfx906 falls back to Deployment/image resolution instead
  of direct-loading into `runtime:rocm-gfx906`.
- Existing runtime-managed gfx906 llama.cpp/diffusers paths remain eligible.
- Focused controller/proxy/runtime tests pass.

## Slice

Add `GPUProfile.spec.runtime.bundledBackends` and consume it in both controller
and proxy direct-runtime fast paths. Missing metadata preserves legacy behavior
so older profiles keep working, but the tracked profiles now declare the live
runtime contract explicitly:

- `gfx1100`: `vllm`, `diffusers`
- `gfx906`: `llamacpp`, `ollama`, `diffusers`
- `sm_52`: `ollama`, `llamacpp`

The `qwen3-1p7b-vllm-radeonvii` manifest remains a canary, but its comments now
state that it must use the standalone gfx906 vLLM backend image rather than the
persistent runtime API.

## Evidence

- `make generate manifests`
- `go test ./pkg/runtime ./controllers ./internal/proxy`

## Next Slice

After merge and rollout, apply the updated GPUProfile CRD/profile and rerun the
`qwen3-1p7b-vllm-radeonvii` proxy smoke. If it reaches HTTP 200 with coherent
output, update `.loom/60-validation-matrix.md` from `block` to the appropriate
promotion decision. If it fails, the new failure should be inside the standalone
vLLM image rather than the persistent runtime packaging contract.

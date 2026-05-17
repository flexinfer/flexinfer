# RALPH: gfx906 vLLM Canary Smoke

Date: 2026-05-17
Branch: `codex/gfx906-vllm-smoke`

## Goal

Run the next feature-gap slice after the GPUProfile vLLM defaults work: prove
whether `qwen3-1.7b-vllm-radeonvii` can now smoke on the persistent gfx906
runtime, and leave the roadmap matrix with a clear promote/block decision.

## Acceptance

- The Radeon VII vLLM canary is safe to reconcile from `deploy/models`.
- Explicit canary traffic can exercise the model without stealing the bare
  `qwen3-1.7b` alias from the CPU fallback tool router.
- The validation matrix records the live outcome and the next blocker.
- The cluster is restored to the known-good `qwen3-1p7b-tools-radeonvii`
  fallback after the experiment.

## Slice

The existing canary resource name used a dot:
`qwen3-1.7b-vllm-radeonvii`. That is legal for the Model CR but unsafe for
child Services and runtime-managed Kubernetes resources. It also used
`SharedPVC`, so the runtime-managed load path did not get the same concrete
local cache layout used by the proven GGUF fallback.

This slice renames the resource to `qwen3-1p7b-vllm-radeonvii`, switches it to
`cache.strategy: Local`, and raises priority to `130`. The public served model
name and aliases stay dotted and canary-scoped: `qwen3-1.7b-vllm`,
`qwen3-1.7b-vllm-vii`, and `vllm-canary`.

The live smoke then exposed the next controller/runtime gap, so the slice also
patches the controller to send load/unload requests to a Running runtime pod
with a PodIP even when Kubernetes Ready is false. Pending runtime pods still
block load requests until they have an address.

## Evidence

- `kubectl kustomize deploy/models` rendered the renamed model with
  `strategy: Local` and `priority: 130`.
- `kubectl apply --dry-run=server -k deploy/models` accepted the manifest and
  would create `model.ai.flexinfer/qwen3-1p7b-vllm-radeonvii`.
- Live hot apply with Flux suspended staged the HF model into local cache:
  `artifact staged to local cache`, then `local cache previously staged`.
- A canary proxy request moved the canary into shared-group `Active` and
  preempted `qwen3-1p7b-tools-radeonvii`, proving priority/demand selection.

Evidence directory: `/tmp/flexinfer-gfx906-vllm-fixed-smoke-20260517T193341Z`.

## Result

The canary did not reach an HTTP 200 smoke. Once the CPU fallback was
preempted, the persistent runtime remained API-alive but pod-not-Ready. The
controller repeatedly logged:

```text
Runtime pod exists but not ready, waiting for it to start
```

The runtime `/healthz` endpoint returned OK, but `/readyz` hung after the
aborted vLLM load, so the controller never issued a fresh load request for the
active cached model. The code change in this MR makes a PodIP, not pod Ready,
the gate for using the management API.

## Recovery

The live test model was deleted, Flux `flexinfer-models` was resumed, and the
stuck `flexinfer-runtime-gfx906` pod was recycled. The fallback recovered:

```text
qwen3-1p7b-tools-radeonvii Ready ready=True
```

Direct service smoke returned:

```json
{"choices":[{"message":{"content":"Blue"}}]}
```

## Decision

Keep gfx906 vLLM at `experimental` and mark the `qwen3-1p7b-vllm-radeonvii`
smoke as `block`, not `pending`: the canary prerequisites and controller load
gate are fixed here, but support promotion still needs a post-merge live vLLM
HTTP 200.

## Next Slice

After merge and rollout, rerun the same canary command and only consider
`deploy/gpuprofiles/gfx906.yaml` `vllm.support` promotion after a real HTTP
200 vLLM response.

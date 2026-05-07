# Fast-Chat Resilience: 5930k Fallback Decision

Status: Accepted (2026-05-07)

## Context

`qwen3-8b-fast-7900xtx` (MLC-LLM, Qwen3-8B-abliterated) owns the warm
`fast-chat` / `gpt-3.5-turbo` / `copilot` aliases on `cblevins-7900xtx` with
`minReplicas: 1`, ~106 tok/s, AOT-compiled `lib_rocm_gfx1100.so` baked into the
local-path PVC `mlc-8b-abliterated-nvme-7900xtx`. Healthy.

The previous 5930k fallback (`deploy/models/fast-chat.yaml`,
`servedModelName=qwen3-8b-fast`, MLC backend) was removed from
`deploy/models/kustomization.yaml` on 2026-05-07 (commit `16a634d0`) because:

- The 5930k local-path PVC `mlc-8b-abliterated-nvme-5930k` lacks the model
  directory.
- The shared NFS PVC `mlc-models-nfs` lacks `mlc-chat-config.json`, so MLC
  exits before serving.
- Re-trying the empty manifests just generated noisy `Idle → Error` cycles in
  the controller.

Today there is no parallel cold-warm fast-chat lane on the 5930k node. If the
7900xtx primary is unavailable (image-pull, GPU contention, hardware fault),
`fast-chat` aliases have no fallback target.

## Decision

Restore the 5930k fallback via **llama.cpp + GGUF**, not MLC.

Cite as `qwen3-8b-fast-fallback-5930k`, source `HF://Qwen/Qwen3-8B-GGUF`,
file `Qwen3-8B-Q4_K_M.gguf`, backend `llamacpp`.

Carry the same alias surface the prior MLC manifest had:

- `fast-chat-5930k`
- `fast-chat-fallback`
- `fast-text-5930k`

Do NOT carry the primary aliases (`fast-chat`, `gpt-3.5-turbo`, `copilot`) —
those stay on the 7900xtx primary so LiteLLM's pool routing prefers
the warm route.

## Rationale

| Option | Pros | Cons |
|---|---|---|
| **A. Fix MLC artifact distribution** (rebuild on NFS or copy to `mlc-8b-abliterated-nvme-5930k`) | Same backend as primary, same throughput | MLC libs are AOT-compiled per ROCm/arch combo, fragile; requires CI step or manual node-pinned copy; ongoing maintenance burden |
| **B. Switch fallback backend to vLLM** | Existing GPTQ pipeline | Slower cold start than MLC/llama.cpp; defeats "fast" branding; no Qwen3-8B GPTQ artifact published yet |
| **C. llama.cpp + GGUF** (this decision) | HF-pullable single-file artifact, no PVC distribution problem; cold start ~5-15s (faster than MLC ~10-30s); template exists at `deploy/models/qwen3-8b-radeonvii.yaml` | Throughput ~80 tok/s vs MLC ~106 tok/s; acceptable for fallback role |
| **D. Accept no fallback** | Zero infra change | Single point of failure for `fast-chat`; the original removal was a regression to live with, not a deliberate posture |

C wins because it's the lowest-maintenance restoration of resilience and
matches the working pattern already in production on the radeonvii node.

## Implementation

New manifest: `deploy/models/fast-chat-5930k-llamacpp.yaml` (added in this
change). Mirrors `qwen3-8b-radeonvii.yaml` structurally (llama.cpp + GGUF, HF
source, SharedPVC cache), retargeted for:

- `nodeSelector: kubernetes.io/hostname: cblevins-5930k`
- `gpu.shared: 5930k-imagegen-textgen` (existing pool — text demand can
  preempt imagegen)
- `gpu.priority: 220` (matches the disabled MLC manifest's posture: above
  imagegen, below 26B/31B canaries when those re-enter the pool)
- `serverless.minReplicas: 0` (cold-warm; only spin up when 7900xtx is
  unavailable or saturated)
- `idleTimeout: 5m` (match prior fallback)
- aliases: `fast-chat-5930k`, `fast-chat-fallback`, `fast-text-5930k`

Kustomization: replace the commented-out `fast-chat.yaml` reference with the
new file. Leave `deploy/models/fast-chat.yaml` in place for one cycle (don't
delete in this MR) so we have a reversible exit if the llama.cpp lane has its
own pathology.

LiteLLM: pool routing already understands the alias hierarchy. Adding
`fast-chat-fallback` as a fresh `servedModelName` makes it eligible without
touching LiteLLM config.

## Acceptance Criteria

- ModelCache (none required — GGUF download via HF source on first activation
  populates the SharedPVC).
- Cold smoke from a port-forward to `flexinfer-proxy`:
  `curl ... -H "Content-Type: application/json" --data '{"model": "qwen3-8b-fast-fallback-5930k", "messages": [{"role": "user", "content": "Reply with one word: blue"}], "max_tokens": 8, "temperature": 0}'`
  returns HTTP 200 with a coherent single-word answer.
- Cold-load timing: scaling-up to first byte under 30s (target). Record in
  `.loom/60-validation-matrix.md` once captured.
- 24 h burn-in with `minReplicas=0`, `idleTimeout=5m`: no eviction-driven
  spin loops. Verify via `kubectl get events -n flexinfer-system | grep
  qwen3-8b-fast-fallback-5930k`.
- LiteLLM health check on `fast-chat-fallback` alias passes.

## Rollback

- Revert this MR. The 7900xtx primary continues to serve unaffected.
- If the GGUF GPU build is broken on 5930k specifically (gfx1100 should match
  the radeonvii pattern but we haven't run a 5930k canary on llama.cpp+ROCm in
  recent memory), drop the model to `minReplicas=0` and re-disable in
  kustomization.

## Follow-Ups

- Once stable, retire the dead `deploy/models/fast-chat.yaml` MLC manifest
  in a one-line cleanup MR.
- Consider whether to publish a Qwen3-8B-abliterated GGUF (matching the
  abliteration the 7900xtx MLC route uses) so the fallback returns the same
  refusal-suppressed shape as the primary. The current decision uses the
  unmodified `Qwen/Qwen3-8B-GGUF` for simplicity; the abliteration delta is
  orthogonal to fallback routing.

## Sources

- 7900xtx primary: `deploy/models/fast-chat-7900xtx.yaml:7-49`
- Disabled prior fallback: `deploy/models/fast-chat.yaml:1-47`
- Removal commit + reasoning: `git show 16a634d0`,
  `.loom/60-validation-matrix.md` 2026-05-07 fast-chat recovery posture
- Working llama.cpp+GGUF pattern on radeonvii: `deploy/models/qwen3-8b-radeonvii.yaml:1-58`
- Kustomization with current disabled state:
  `deploy/models/kustomization.yaml:46-50`
- Memory note (Cluster Model Fleet 2026-02-20):
  `qwen3-8b-fast` MLC at 108 tok/s; reference for throughput delta vs llama.cpp

# F5 72B 3-Way Validation Window Runbook

## Purpose

Serve Qwen2.5-72B-GPTQ across three heterogeneous GPUs — `cblevins-7900xtx`
(gfx1100, 24 GB) + `cblevins-radeonvii` (gfx906 Vega20, 16 GB) +
`cblevins-5930k` (gfx1100, 24 GB) — with vLLM 0.6.3 Ray PP=3 in graph mode on
the unified image, for validation and benchmarking.

Proven 2026-06-10: coherent HTTP 200 at ~14–17 tok/s single-stream.
Manifest: [deploy/debug/f5-3way-72b-window.yaml](../../deploy/debug/f5-3way-72b-window.yaml).
Evidence: `.loom/local/validation/f5-3way-29-22-29-2026-06-10/RELAUNCH-VERDICT.md`.

This is a **manual window**: it displaces the warm Gemma text lanes for its
duration (~45–90 min). Never auto-apply.

## Prerequisites

- All three GPU nodes `Ready` (`kubectl get nodes`).
- Model staged on `llm-models-nfs` at `/qwen25-72b-instruct-gptq-int4`.
- `registry.harbor.lan/flexinfer/vllm:rocm6.3.4-multiarch` ideally cached on
  all three nodes — image GC prunes it (canary is scale-to-zero); a cold pull
  is ~17 GB / ~25 min per node and the head's 10-minute wait-for-GPUs loop
  will expire mid-pull (recoverable, see Troubleshooting).
- No `qwen3-1p7b-vllm-radeonvii` canary pod active (it holds the radeonvii
  `amd.com/gpu` resource).

## Procedure

### 1. Open the window (suspend reconciliation, vacate gfx1100 GPUs)

```bash
export KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml
# snapshot state for the restore check
kubectl get models -n flexinfer-system -o wide > /tmp/pre-window-models.txt

flux suspend kustomization flexinfer-models -n flux-system
flux suspend helmrelease flexinfer -n flexinfer-system

# vacate Gemma lanes (warmPolicy primary ignores minReplicas alone under traffic)
for m in gemma4-26b-a4b-gptq gemma4-26b-a4b-gptq-5930k; do
  kubectl patch model $m -n flexinfer-system --type merge \
    -p '{"spec":{"config":{"warmPolicy":"ondemand"},"serverless":{"minReplicas":0}}}'
done
kubectl scale deploy flexinfer-controller -n flexinfer-system --replicas=0
# the primary lane usually needs a direct scale after the controller stops:
kubectl scale deploy gemma4-26b-a4b-gptq -n flexinfer-system --replicas=0
```

Verify no model pods remain on `cblevins-7900xtx` / `cblevins-5930k`.

### 2. Launch

```bash
kubectl apply -f deploy/debug/f5-3way-72b-window.yaml
kubectl logs -f f5-3way-head -n flexinfer-system
```

Expected sequence (timestamps from a warm-cache run):

| Phase | Marker line | Typical time |
|---|---|---|
| Ray formed | `3 GPUS READY` | ~2 min |
| RCCL init | `NCCL INFO Connected all rings` (×3 ranks) | ~1 min |
| Load | `Loading model weights took 10.0034 GB` (radeonvii) | ~11 min |
| KV | `# GPU blocks: 256` | seconds |
| Graphs | `Graph capturing finished` (×3) | 1–2 s each |
| Serving | `Uvicorn running on socket ('0.0.0.0', 8000)` | — |

Expected residency: head 17.87 GB (31 layers + embed), 5930k 15.55 GB
(31 + lm_head), radeonvii 10.00 GB (18 layers).

### 3. Smoke

```bash
kubectl exec f5-3way-head -n flexinfer-system -- \
  curl -s -X POST http://localhost:8000/v1/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen25-72b","prompt":"Q: What is the capital of France and what river runs through it?\nA:","max_tokens":64,"temperature":0}'
```

Expected: HTTP 200 with a coherent greedy completion ("Paris … Seine"),
~16 tok/s single-stream (64 tokens in ~4 s).

### 4. Restore

```bash
kubectl logs f5-3way-head -n flexinfer-system > <evidence-dir>/head.log
kubectl delete -f deploy/debug/f5-3way-72b-window.yaml

for m in gemma4-26b-a4b-gptq gemma4-26b-a4b-gptq-5930k; do
  kubectl patch model $m -n flexinfer-system --type merge \
    -p '{"spec":{"config":{"warmPolicy":"primary"},"serverless":{"minReplicas":1}}}'
done
kubectl scale deploy flexinfer-controller -n flexinfer-system --replicas=1
flux resume helmrelease flexinfer -n flexinfer-system
flux resume kustomization flexinfer-models -n flux-system
flux reconcile kustomization flexinfer-models -n flux-system
```

Verify against **master intent at restore time** (not the pre-window
snapshot — see the 06-09 canary-revival incident): both `gemma4-26b` lanes
`Ready` (cold load ~5–10 min), `bge-large`/`bge-reranker`/
`qwen3-1p7b-tools` `Ready`, `qwen3-1p7b-vllm-radeonvii` `Idle` with
`minReplicas: 0`, `whisper-large-v3-turbo` `Idle`.

## Rollback

The window is fully additive: deleting the three pods + ConfigMap and running
step 4 returns the cluster to GitOps-managed state. If a restore step was
missed, `flux reconcile kustomization flexinfer-models --with-source` enforces
master.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Head exits: `number of required GPUs exceeds the total number of available GPUs in the placement group` | Head's 10-min GPU wait expired before workers joined (usually cold image pulls) | Recreate just the head pod once workers are `Running` — workers' `ray start` retry loops rejoin the new head |
| Radeonvii rank loads 13.4144 GB (not 10.0) | Partition env not reaching workers — pod env missing/edited | `VLLM_PP_LAYER_PARTITION` must be pod env on **all three pods**; vLLM 0.6.3 does not propagate it from the driver |
| First request dies: `gptq_gemm … HIP error: invalid argument` (profile passed) | Vega20 contiguity wall: post-profile `empty_cache` + KV/graph pools fragment the map; gptq_gemm's ~928 MiB `temp_dq` can't re-alloc contiguously | Shrink the gfx906 shard (`…,18,…` is proven; never above 18/80 layers). Do not suppress `empty_cache` — that crosses the ~1.8 GB floor at KV/graph init instead |
| RCCL init hangs (channels half-connected, `HSA_AMD_AGENT_INFO_MEMORY_AVAIL query failed` in worker .err) | Intermittent radeonvii runtime wedge | Bounce all three pods; cleared on retry twice on 2026-06-10 |
| RCCL init hangs ~30 min, zero shards | `AMD_SERIALIZE_KERNEL`/`HIP_LAUNCH_BLOCKING` set on a rank | Never serialize multi-rank launches; use `AMD_LOG_LEVEL=2` on the suspect rank for attribution (`/tmp/ray/session_latest/logs/worker-*.err`) |
| worker1 `Pending: Insufficient memory` on 5930k | Co-tenants (CI runners, prometheus) hold node memory | Manifest carries explicit `requests.memory: 12Gi`; lower further if needed — rank host-RAM use is small |
| `TaintManagerEviction` on a window pod | Node `NotReady` blip (CI contention during pulls) | Eviction usually cancels on recovery; recreate the pod if it terminates |

For deeper attribution of gfx906 GPU failures, use the standalone probe:
[deploy/debug/gfx906-gptq-gemm-probe.yaml](../../deploy/debug/gfx906-gptq-gemm-probe.yaml)
(note its fresh-context caveat — it cannot see fragmentation-dependent walls).

## Contacts / References

- Validation history: `.loom/60-validation-matrix.md` (2026-06-09/10 entries)
- Kill-test plan + verdict: `.loom/32-iteration-plan-gfx906-gptq-gemm-killtest-2026-06-10.md`
- Vega20 constraints recap: alloc floor ~1.8 GB free (invalid-arg, not OOM);
  no VMM → contiguity-after-fragmentation is binding; single alloc cap ~2 GB.

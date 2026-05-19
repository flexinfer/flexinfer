# Slice 1 Kill-Test v2 — INCONCLUSIVE (2026-05-19, 14:33Z run)

**Plan**: `.loom/asr-diarization-7900xtx-plan-2026-05-18.md`
**Companion**: `.loom/asr-diarization-kill-test-inconclusive-2026-05-19.md` (v1 evidence + startup-sweep bug hypothesis)
**Outcome**: INCONCLUSIVE — vLLM was launched but crashed before loading Whisper weights because the cached model files did not land at the path the runtime DaemonSet sees. The Whisper-on-ROCm-gfx1100 question still unanswered.
**Production impact**: ~3 minutes of `gemma4-26b-a4b-gptq` preemption (14:33:03Z → ~14:36Z), then it reclaimed leadership automatically when the kill-test's Deployment failed. ICC chat traffic should have ridden the 5930k sister during the window.

## What v2 verified (in production, end-to-end)

| Step | Status | Evidence |
|------|--------|----------|
| Flux applied !436 | ✅ | `flexinfer-models` Kustomization `Applied revision: master@sha1:63c042c8...` at 14:28:25Z |
| Force-promotion chooser short-circuit | ✅ | `INFO Model is active in shared group {group: 7900xtx-textgen}` |
| Higher-priority preemption of warm-primary | ✅ | Event `Preempted by whisper-kill-test-v2 with priority 400` on `model/gemma4-26b-a4b-gptq` |
| `status.sharedGroup.state: Active` written | ✅ | `kubectl get model whisper-kill-test-v2 -o yaml` |
| Service `whisper-kill-test-v2` created | ✅ | ClusterIP `10.43.214.64:8000`, selector cleared for runtime management |
| Cache PVC + prefetch job | ✅ | `whisper-kill-test-v2-cache` PVC bound to `cblevins-7900xtx`, prefetch job downloaded 13 files in ~35s |
| `Cached` condition `True / PrefetchSucceeded` | ✅ | At 14:33:38Z |

**Slice 1-bis (`gpu.forcePromotion`, !433) is live-validated.** The chooser correctly returns the force-promoted candidate before the Ready-first branch fires; `handleSharedGPU` propagates Active/Preempted state; the proxy/scheduler stop routing to the preempted incumbent.

## What broke and why (NOT a Whisper/ROCm issue)

The vLLM subprocess inside `flexinfer-runtime-gfx1100-2pgnv` crashed during arg parsing, **before any weight load**:

```
python -m vllm.entrypoints.openai.api_server
  --model /models/whisper-kill-test-v2 --dtype half --max-model-len 448
  --gpu-memory-utilization 0.30 --enforce-eager
  --served-model-name whisper-large-v3-turbo --kv-cache-dtype auto
```

Traceback (vLLM 0.17.0):
```
arg_utils.py:1468 create_engine_config
  → transformers_utils/config.py:521 maybe_override_with_speculators
    → configuration_utils.py:737 _get_config_dict
      → utils/hub.py:278 cached_file
        → utils/hub.py:422 cached_files
          → hf_hub_download
            → _validators.py:132 validate_repo_id
              HFValidationError: Repo id must be in the form
              'repo_name' or 'namespace/repo_name': '/models/whisper-kill-test-v2'
```

The transformers fallback chain here means: vLLM passed the local path to `PretrainedConfig.get_config_dict`. transformers couldn't find `config.json` at that path, so it re-tried via `hf_hub_download`, which validated the input as a HF repo ID and rejected the leading slash.

### Root cause: cache strategy mismatch

The runtime DaemonSet pod mounts the **hostPath** `/var/lib/flexinfer/models` at `/models`:
```
$ kubectl exec flexinfer-runtime-gfx1100-2pgnv -- ls /models/
flexinfer-system/  ollama/  qwen35-27b-opus-distill/  qwen35-27b-opus-distill-gptq/
```

`whisper-kill-test-v2/` is absent. Files were downloaded to a separate local-path PVC at `/var/lib/rancher/k3s/storage/pvc-56a2a2a9-...`, which is on the same node but a different path.

Compare to `gemma4-26b-a4b-gptq` (working in the same group):

| PVC | Size | StorageClass | Purpose |
|-----|------|--------------|---------|
| `gemma4-26b-a4b-gptq` | 96Gi | `nvme-1r-gpu` | Main model PVC the runtime mounts |
| `gemma4-26b-a4b-gptq-cache` | 50Gi | `local-path` | Cache prefetch target |

Gemma4's cache pipeline is two-stage:
1. `cache-prefetch` job → downloads HF model to `<name>-cache` PVC
2. `cache-copy` job → copies from `<name>-cache` PVC to the main `<name>` PVC (which the runtime hostPath sees)

The kill-test Model CR declares only the cache PVC (`size: 5Gi, storageClass: local-path` in `cache:`). No main PVC is declared. So no `cache-copy` job is scheduled, and the runtime mount stays empty for this Model.

Verifiable via `kubectl get pod -n flexinfer-system | grep cache-copy`:
```
gemma4-26b-a4b-gptq-cache-copy-6d5gf       Completed
gemma4-26b-a4b-gptq-5930k-cache-copy-lrp5j Completed
# No whisper-kill-test-v2-cache-copy-* entry
```

## Side-finding (confirmed): controller startup-sweep does not pick up empty-Phase Models

The v1 evidence doc (line 146) hypothesized:
> The controller pod restarted with an empty cache and the startup-sweep (`model_controller.go:504-541`) only re-enqueues Models in `Phase ∈ {Loading, Pending}` — a brand-new Model with empty `Phase` is NOT swept.

**v2 reproduced this exactly.** Timeline:
- 14:14:41Z — Model created (resourceVersion 637836175, Phase empty)
- 14:23:05Z — flexinfer-controller pod rolled out (new image policy)
- 14:23–14:33Z — controller actively reconciled every other Model in flexinfer-system at ~1Hz; **zero log lines about whisper-kill-test-v2**; Model had no status, no events, no conditions
- 14:33:01Z — manual `kubectl annotate model whisper-kill-test-v2 flexinfer.ai/force-reconcile=...`
- 14:33:03Z — reconcile fires, chooser picks it, full flow runs in seconds

Until the manual annotation, the kill-test was **invisible to the controller's reconcile queue**. This is the same root cause as the v1 "8-hour resourceVersion stall" mystery. The startup-sweep filter at `controllers/model_controller.go:504-541` needs to include empty-Phase Models.

**Workaround for operators**: After any controller restart, sweep:
```bash
kubectl get model -A -o json | jq -r '.items[] | select(.status.phase == null or .status.phase == "") | "\(.metadata.namespace)/\(.metadata.name)"' \
  | xargs -I{} sh -c 'kubectl -n $(echo {} | cut -d/ -f1) annotate model $(echo {} | cut -d/ -f2) flexinfer.ai/force-reconcile=$(date -u +%FT%TZ) --overwrite'
```

## What Slice 1 still doesn't answer

- Does vLLM 0.17.0 actually support `--task transcription` on gfx1100?
- Does Whisper-large-v3-turbo load without CUDA-only kernel errors on RDNA3?
- Does `/v1/audio/transcriptions` produce coherent text against a ~10s LibriSpeech sample?
- What's the end-to-end latency vs the pass condition (`< 10s wall-clock`)?

## Recommended next moves

In priority order:

1. **Tear-down MR (small, can ship now)** — remove `deploy/models/whisper-kill-test-v2.yaml` and its line in `deploy/models/kustomization.yaml`. The chooser reverts to Ready-first preference; `gemma4-26b-a4b-gptq` reclaims leadership automatically.
2. **Rewrite kill-test Model CR with working cache layout** — add a main model PVC (e.g. `size: 5Gi, storageClass: nvme-1r-gpu` matching gemma4's pattern), keep the cache PVC. The controller will then schedule `cache-copy` and the runtime will see `/models/whisper-kill-test-v2/`. Alternative: switch `cache.strategy` to `Local` (no PVC dance — matches `qwen3-1p7b-tools-radeonvii`).
3. **File controller bug for startup-sweep** — the v1 hypothesis at `.loom/asr-diarization-kill-test-inconclusive-2026-05-19.md:146` is now confirmed. Fix: extend `model_controller.go:504-541` to re-enqueue Models with `Phase ∈ {Loading, Pending, ""}` on startup, or remove the Phase filter entirely.
4. **Slice 3a (production Model CR) is still gated on Slice 1 PASSED** — same recommendation as the v2 plan (`priority: 400`, `serviceLabels`, NO `forcePromotion`), but the cache-PVC fix from #2 must also apply.

The fallback paths in the original plan (whisper.cpp HIP sibling, omni image variant) do NOT activate from this evidence — those were for actual Whisper/ROCm-incompatibility outcomes. The current failure is a misconfigured Model CR, not a backend dead-end.

## Artifacts kept on cluster

- `model/whisper-kill-test-v2` still present in `flexinfer-system`, `Phase: Failed`
- `whisper-kill-test-v2-cache` PVC still bound on `cblevins-7900xtx`, 5GB used
- `whisper-kill-test-v2-cache-prefetch-2l9pm` pod kept (Completed) for log evidence
- `flexinfer-controller-846d6489d8-4ddmf` logs span 14:23:05Z+

A subsequent tear-down MR should remove all of these via Flux prune.

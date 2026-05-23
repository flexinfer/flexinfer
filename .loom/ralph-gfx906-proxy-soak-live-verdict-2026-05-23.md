# RALPH: gfx906 Proxy Soak Live Verdict

Date: 2026-05-23

## Review

- Roadmap milestone: unblock the `gfx906` llama.cpp fallback lane with a
  proxy-backed soak before any alias/default fallback promotion.
- Prior source slice: MR !480 added a controller guard so idle cross-group
  candidates do not unload an actively loading or recently demanded runtime
  peer.
- Live prerequisites confirmed:
  - Controller image:
    `registry.harbor.lan/flexinfer/flexinfer-controller@sha256:d4f10cc0fa0c8c288aff238345b1954ad31bc2a798eb682932d69edf7889618e`
  - Runtime image:
    `registry.harbor.lan/flexinfer/runtime@sha256:8797a08a209201dc7bcf6bce7f79b0697055a02824f5fe9947932ef91273c29e`

## Align

- Slice name: proxy-backed Qwen3 8B soak after controller guard rollout.
- Scope in:
  - Apply temporary `Model/qwen3-8b-radeonvii`.
  - Rerun `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` with a fresh evidence
    PVC.
  - Harvest live verdict and update the matrix.
- Scope out:
  - No alias promotion.
  - No default-chat fallback promotion.
  - No runtime image changes.

## Result

The live gate failed quickly enough that the 24 hour job was stopped after
harvest.

- Job `gfx906-llamacpp-proxy-soak-traffic` started at
  `2026-05-23T14:59:45Z`.
- The traffic log recorded `soak_start`.
- Warmup attempt 1 timed out after `900.122s`.
- Measured attempt 2 timed out after `900.109s`.
- `qwen3-8b-radeonvii` cache prefetch succeeded, but the Model stayed `Idle`.
- Status showed:
  - `sharedGroup.state=Queued`
  - `preemptedBy=qwen3-1p7b-tools-radeonvii`
  - `queuePosition=2`
- Runtime logs show `qwen3-8b-radeonvii` began loading at
  `2026-05-23T14:59:45Z`, then `qwen3-1p7b-tools-radeonvii` unloaded it at
  `2026-05-23T14:59:46Z` and returned to Ready.

This is not the same failure as the previous imagegen cross-group unload. The
controller guard fixed the source-level shape it targeted, but the live gate now
exposes same-group priority arbitration: the lower-priority Qwen3 8B soak target
cannot preempt the higher-priority Qwen3 1.7B fallback.

## Evidence

Harvested local evidence:

- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/proxy-soak.jsonl`
- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/proxy-soak-traffic.log`
- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/proxy-soak-job.yaml`
- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/proxy-soak-configmap.yaml`
- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/model-snapshot.yaml`
- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/controller.log`
- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/runtime.log`
- `.loom/local/validation/gfx906-llamacpp/2026-05-23-proxy-soak-guard-rerun-fail/events.txt`

## Cleanup

Deleted after harvest:

```bash
kubectl -n flexinfer-system delete \
  job/gfx906-llamacpp-proxy-soak-traffic \
  configmap/gfx906-llamacpp-proxy-soak-traffic \
  model/qwen3-8b-radeonvii \
  --ignore-not-found
```

`qwen3-1p7b-tools-radeonvii` remained the Ready fallback.

## Decision

- Proxy-backed Qwen3 8B soak after MR !480: FAIL.
- Alias/default fallback promotion remains blocked.
- Next slice should make the soak target eligible to become active without
  permanently promoting it above the fallback lane. Practical options are a
  temporary force-promotion annotation for the kill-test, a dedicated soak-only
  shared group, or a controller test-mode override that honors explicit
  route-scoped soak demand.

# Slice 1 Kill-Test — INCONCLUSIVE (2026-05-19)

**Plan**: `.loom/asr-diarization-7900xtx-plan-2026-05-18.md`
**Outcome**: INCONCLUSIVE — controller did not reconcile the kill-test Model CR. The riskiest assumption (vLLM serves Whisper via `/v1/audio/transcriptions` on ROCm gfx1100) **remains unproven**.
**Production impact**: zero. The 26B (`gemma4-26b-a4b-gptq`) was never evicted; ICC's quality-chat / mid-chat / project-mgmt routes remained available throughout.

## Timeline

| When | Event | Source |
|---|---|---|
| 2026-05-19 04:01:54Z | MR !429 merged — kill-test Model CR added to `deploy/models/` | https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/429 |
| 2026-05-19 04:02-04:03Z | Flux `flexinfer-models` Kustomization reconciled (manual force-reconcile triggered via `flux_reconcile`) | `flux_reconcile` tool result |
| 2026-05-19 04:03:00Z | `Model/whisper-kill-test` resource created in `flexinfer-system` namespace | `kubectl get model whisper-kill-test` creationTimestamp |
| 2026-05-19 04:03:00Z – 12:40Z+ | Model sat with `status: {}`, `events: <none>`, `resourceVersion: 637002597` (unchanged) for 8+ hours | `kubectl describe model whisper-kill-test` (twice, at 04:21Z and 12:39Z, identical Resource Version) |
| 2026-05-19 04:03:00Z – 12:40Z+ | No Deployment was created for the Model. No pod scheduled. `kubectl get pods -n flexinfer-system` never showed a `whisper-kill-test-*` pod. | `kubectl get pods` |
| 2026-05-19 04:03:00Z – 12:40Z+ | `gemma4-26b-a4b-gptq-55cc8657bc-86htz` continued Running on `cblevins-7900xtx` without restart. The shared-GPU swap that priority 400 > 350 was supposed to trigger never fired. | `kubectl get pods -n flexinfer-system | grep gemma4-26b` |

## Evaluation against the plan's pass/fail conditions

The plan specified three pass conditions, all-required:
1. HTTP 200 from `POST /v1/audio/transcriptions`
2. `text` field is a recognizable transcript of the input
3. Server log shows no fallback warnings about CUDA-only kernels / FlashInfer / unsupported task

**None of these could be evaluated** — the vLLM engine never started because the Model CR was never given a Deployment. The pass/fail criteria assume engine startup happens. It didn't.

## What this DOES tell us

- **The Model CR YAML is valid** — `kubectl get` returns it, Flux reconciled it cleanly, no admission webhook rejected it.
- **The flexinfer controller does NOT reconcile this Model CR shape**. Possible causes (in approximate priority):
  1. The controller's Model reconciler has a predicate that gates on something this Model lacks (e.g. `serviceLabels` or `litellm.enabled: true`). The plan deliberately left both off to keep the kill-test non-routable. That deliberate omission may also have made the Model invisible to the reconciler.
  2. The `7900xtx-textgen` shared-GPU controller logic in `controllers/model_shared_gpu.go` only acts when a Deployment for the new claimant already exists, not on Model-CR-only state. A chicken-and-egg if true.
  3. The controller has a separate watch on `7900xtx-textgen` group membership that doesn't fire on transient claimants without a corresponding cache job.
  4. A subtle defect in master since the new gfx906 commits (`5b134f28`, `9af52fc9`, `864a30ed`) that landed during the kill-test window — though the controller pod itself rolled successfully.
- **Production-side swap behavior was preserved** — the warm 26B is uninterruptible by adding a new high-priority Model CR alone. This is *conservative* behavior and arguably the right default (don't trash the warm primary for an unscheduled-by-traffic competitor), but it directly contradicts the kill-test design that assumed `priority: 400` was sufficient.

## What this does NOT tell us

- Whether vLLM serves Whisper transcription on ROCm gfx1100. **The riskiest assumption is still unverified.**
- Whether Slice 3a (production Whisper Model CR with `priority: 100` < 26B's `350`) would reconcile, given the same controller behavior. If the controller only reconciles Models that share a label-group with a yielding claimant, Slice 3a may have the same problem.

## What the cluster looks like at evidence-capture time

```
$ kubectl get model -n flexinfer-system
model.ai.flexinfer/gemma4-26b-a4b-gptq          # priority 350, Running
model.ai.flexinfer/gemma4-26b-a4b-gptq-5930k    # priority unchanged, Running on 5930k
model.ai.flexinfer/whisper-kill-test            # priority 400, NEVER GOT A POD
... (other unrelated models)
```

```
$ kubectl get pods -n flexinfer-system -l app=whisper-kill-test
No resources found.
$ kubectl get pods -n flexinfer-system | grep gemma4-26b
gemma4-26b-a4b-gptq-55cc8657bc-86htz         1/1     Running     0     79m
gemma4-26b-a4b-gptq-5930k-86b784c965-xdgr9   1/1     Running     0     80m
```

```
$ kubectl describe model whisper-kill-test -n flexinfer-system | tail -3
Status:        <none>
Events:        <none>
```

## Recommended next steps

1. **Roll back the kill-test Model CR** — open the cleanup MR to remove `deploy/models/whisper-kill-test.yaml` and its kustomization entry. The Model is stranded but harmless; remove it for cleanliness.
2. **Amend the plan** with the new finding: **the controller does not reconcile Model CRs without `serviceLabels` or `litellm.enabled: true`** (working hypothesis — needs verification). Add this as Open Question #14 in the plan.
3. **Before re-attempting the kill-test**: either
   - Add the Model to an existing shared-GPU group AND give it a `serviceLabels` entry (e.g. `whisper-kill-test`), accepting that the proxy will route to it (mitigation: ICC doesn't ask for `whisper-kill-test` by name)
   - Or read `controllers/model_controller.go` / `model_shared_gpu.go` to find the actual reconciler predicate and add whatever the kill-test lacks
   - Or pre-evict the 26B explicitly (e.g. annotate the gemma4-26b Model with `flexinfer.ai/pause: "true"`, run the kill-test, then un-pause)
4. **Slice 3a (production Whisper Model CR) is now blocked** on the same root cause — recommend NOT attempting Slice 3a until the controller-reconciliation question is answered. Add this to the plan.

## Evidence trail

- MR !429 (kill-test add): https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/429 — merged
- MR !425 (status follow-up to !423): https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/425 — merged
- MR !423 (schema + --task arg): https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/423 — merged
- Flux reconcile log: triggered via `mcp__loom__flux__flux_reconcile` for `kustomization/flexinfer-models` with `with_source: true` at ~04:02Z
- Two `kubectl describe model whisper-kill-test` snapshots (04:21Z, 12:39Z) showing identical `resourceVersion: 637002597` — definitive proof the controller never wrote to this resource.

## Status

**INCONCLUSIVE.** Slice 1 cannot be marked PASSED or FAILED — the engine validation never ran. The riskiest assumption (vLLM serves Whisper transcription on ROCm gfx1100) is unproven. Downstream slices (3a, 4, 5, 6) remain gated on Slice 1, and Slice 1 itself is now gated on a new sub-question about controller reconciliation predicates.

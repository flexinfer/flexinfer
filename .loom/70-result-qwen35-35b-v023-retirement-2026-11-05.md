# Result: retire the vLLM 0.23 Qwen3.5-35B-A3B MoE lanes (2026-11-05)

Matrix row: `gfx1100-qwen35-35b-moe-v023-retire-2026-11-05` (`.loom/60-validation-matrix.md`).
Context: B6 in `.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` §4. The dead plugin-compat
code may be deleted only once no lane pins a vLLM 0.23 or 0.26 image.

## Decision

The owner chose, on 2026-11-05, between migrating and retiring. The choices were:

- **Retire all three lanes.** The alternatives were to keep the 184K 128k lane on 0.33, or to migrate all three.
- **Full B6.** This retires the live OpenJEV gfx1100 cross-encoder (vLLM 0.26). It decouples the frozen gfx906 0.20.1
  OpenJEV image from the in-tree plugins. It drops the 0.23 ModelExperiment records. Then it deletes the dead
  compat code.
- **Merge now, not as a window Draft.** Only idle, zero-replica objects are deleted, and no GPU tenant changes.

## Usage evidence (30 days to 2026-11-05)

Sources and their reach:

- Prometheus holds data only from 2026-10-28 00:00Z (`prometheus_tsdb_lowest_timestamp_seconds` = 1793145600).
- Loki reaches 2026-10-29.
- For older days, the Model status is the record. A lane that serves updates `lastActiveTime`, and a parked lane
  carries a Ready=False reason.

| lane | node / priority | `flexinfer_proxy_requests_total` success (8 d) | proxy `request_start` (Loki) | last served | state on 2026-11-05 |
|---|---|---|---|---|---|
| `qwen35-35b-clean-gptq-workhorse` | 5930k / 300 | 0 (260 `error`, see below) | none | before 2026-08-26 | Preempted; Ready=False `ParkedBehindPrimary` since 2026-08-26T05:47Z, "not promotable on demand under current config" |
| `qwen35-35b-clean-gptq-workhorse-128k` | XTX / 290 | 0 | none | 2026-07-19T15:38Z | Idle |
| `qwen35-35b-clean-mtp-32k` | XTX / 90 | 0 | none | 2026-07-19T15:38Z | Idle; at 90 it cannot win the card |
| `openjev-0p8b-crossencoder-gfx1100` (manual, not Flux) | 5930k / 200 | 0 (256 `endpoint_unsupported`) | none | 2026-09-22T03:30Z | Preempted |

The 260 `error` counts on the workhorse are not demand:

- They fall at 2026-10-29 15:06Z–16:xxZ and 2026-10-30 00:0xZ.
- At 15:06:46Z the proxy created request queues for every parked lane at once. Each then logged
  `cold start failed … context canceled` every 30 s.
- muse-glimmer, qwen36-27b-autoround, qwen36-27b-fable, qwen36-35b-mtp and this lane each got exactly 138.8 errors in
  the same 3 h window.
- The lane's `lastActiveTime` of 2026-10-30T00:06:28Z is the same stamp seen on eight other parked Models.

The OpenJEV counts are the same kind of sweep: 256 on both twins.

LiteLLM saw no requests for the workhorse's only advertised aliases, `qwen35` and `qwen35-35b`, in 8 days. The other
two lanes had litellm disabled.

## Consumers

- **Live cluster.** No ConfigMap in any namespace, and no Deployment, StatefulSet or CronJob env, names a retired lane
  or alias (`qwen35`, `qwen35-35b`, `qwen35-128k`, `workhorse-128k`, `loom-workhorse-128k`, `mills-council-128k`,
  `qwen35-mtp`, `qwen35-mtp-32k`). The only exceptions are the flexinfer benchmark-result ConfigMaps.
- **Workspace.**
  - `labs/psyche-simulation/scripts/depth_recall/judge.py:27` has
    `DEFAULT_JUDGE_MODEL = "qwen35-35b-clean-gptq-workhorse"`. It is an offline bench script, last changed 2026-07-30.
    The lane has answered 503 "parked" since 08-26 anyway.
  - The rest are test fixtures: loom-core `pkg/mills/pipeline/error_class_test.go` (it records that 503), the HUD
    `routingBoard.ts` fixture, the flexdeck `public_metrics_test.go`, and psyche `tests/test_config.py`.
  - Nothing in platform/gitops, and no live config in loom-core or jobsearch-app.

## Why not migrate

The text plugin's `_patch_gptq_expert_loader` docstring states the gap: vLLM 0.26 removed `load_fused_expert_weights`,
so the MoE GPTQ expert-name contract is not provided on ≥ 0.26. MoE GPTQ lanes "must stay on a runtime where the hook
installs until the 0.26 expert loader is re-qualified". A 0.32/0.33 repin is therefore a qualification job, not a
digest bump. It would need:

- an expert-load test;
- a 184K needle gauntlet;
- a new MTP certificate for the 32K lane;
- the G8 tuned MoE JSON.

It would also wait for the !1289 controller roll and queue behind S4, S6 and KT-QOS/SO on the XTX. A 5930k canary is
impossible: the 900 workhorse holds that card, and its root disk is under the 200 GB pull rule.

The only unique capability lost is a 184K context tier; qwen38-27b serves 131K. Its routes were already dead: the
`*-128k` labels resolved to one parked lane and one lane idle since July.

## ModelExperiment records removed (verdicts preserved here)

Each ran once and kept only a verdict. The benchmark evidence lives in the benchmark ConfigMaps, which are untouched.
Deleting a record runs its finalizer. The finalizer deletes `<record>-candidate` and `<record>-gauntlet`, and neither
exists. No live Model has a `-candidate` name.

| record | Flux | candidate image | verdict | run 1 |
|---|---|---|---|---|
| `qwen35-35b-clean-gptq-128k-graph` | yes | `vllm@7bc680b43ab9` | Succeeded / GauntletPassed | 2026-07-14 20:19:52Z → 20:32:16Z |
| `qwen35-9b-context-ceiling-192k-canary` | yes | `runtime@2e9652edee30` | Succeeded / GauntletPassed | 2026-07-22 16:11:35Z → 16:18:51Z |
| `qwen35-9b-context-ceiling-224k-canary` | yes | `runtime@2e9652edee30` | Succeeded / GauntletPassed | 2026-07-22 16:41:37Z → 16:50:36Z |
| `qwen35-9b-default-nonthinking-canary` | yes | `runtime@2e9652edee30` | Succeeded / GauntletPassed | 2026-07-22 15:31:25Z → 15:36:20Z |
| `qwen35-9b-v023-rp-canary` | yes | `runtime@2e9652edee30` | Succeeded / GauntletPassed | 2026-07-21 19:28:05Z → 19:33:04Z |
| `qwen36-27b-fable-rp-canary` | yes | `runtime@2e9652edee30` | Succeeded / GauntletPassed | 2026-07-21 15:50:37Z → 15:56:12Z |
| `qwen35-35b-clean-gptq-64k-graph` | no (manual) | upstream `vllm-openai-rocm@3813e31cc3ab` | Succeeded / GauntletPassed | 2026-07-14 14:52:36Z → 14:57:22Z; its manifest is deleted, the live object stays until someone deletes it |

`qwen35-9b-ablit-rp` still carries `flexinfer.ai/canary-certificate: qwen35-9b-context-ceiling-224k-canary-generation-4`.
No code reads that annotation, and the verdict it names is in the table above.

## What changed

Removed:

- **Lanes:**
  - `deploy/models/qwen35-35b-clean-gptq-workhorse.yaml`, `-128k.yaml` and `qwen35-35b-clean-mtp-32k.yaml`. Flux prunes
    these Models.
  - `deploy/models/openjev-0p8b-crossencoder-gfx1100.yaml`. It was never in kustomization; the live Model is deleted
    separately.
- **Debug manifests:** `deploy/debug/qwen35-35b-{clean-gptq-canary,stretch-canary,workhorse-experiment}.yaml` and
  `deploy/debug/gfx1100-qwen35-mtp-kill-test.yaml` (the reproduction of the removed certificate).
- **ModelExperiment records:** all six records in `deploy/experiments/`. The kustomization stays, empty.
- **GPUProfile certificate:** `vllm-qwen35-moe-mtp1` (f467e202), removed from `deploy/gpuprofiles/gfx1100.yaml`.
- **Prewarm:** `vllm@7bc680b4` dropped from both gfx1100 `imagePrewarm` lists.
- **0.23 build:** `build/Dockerfile.vllm-rocm-v023-qwen35-text` and the manual CI job
  `publish_vllm_rocm_v023_qwen35_text`.
- **Tests and checks:** `scripts/check-runtime-patch-contracts.py` drops the 35B production contract. The gauntlet
  template check stays. `controllers/placement_layout_test.go` drops the three lanes. The render golden and
  `docs/certification-matrix.json` drop the retired entries; the golden diff is deletion only.

Kept on purpose:

- **ModelCache and NFS artifacts.** The ModelCache `qwen35-35b-a3b-clean-gptq` and both NFS artifacts stay:
  `gptq-w4-g128-gdn`, 22.8 GB, and `-mtp-experts-w4g128`, 21.6 GB. Reclaiming them is a separate owner call.
- **Prewarm rollback anchors.** `runtime@2e9652ed` and `runtime@ad93ae72` stay in the XTX prewarm list.
  - 2e9652ed is the 9B RP lane's only rollback until that lane first boots on b06b06fe, and Harbor no longer has it.
  - ad93ae72 is the documented rollback of the XTX warm lane and the workhorse.
  - B6 does not need either removed, because each image carries its own frozen plugin copy.
- **Other kept files:**
  - the suspended `qwen35-long-context-gauntlet` template;
  - the MTP-expert quantizer builder and `build/scripts/quantize_mtp_experts.py`;
  - the synthetic test fixtures that use the lane names as data.

Recovery: `git revert` of the MR restores every manifest. Both 0.23 images keep their Harbor tags
`rocm-v0.23.0-qwen35-text*`. They have no `pinned-*` tag, so retention can drop them 30 days after their last pull.

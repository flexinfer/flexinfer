# Product Spec: Runtime images built and promoted by CI (gfx1100, gfx906)

**Date**: 2026-09-13 · **Tracking issue**: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/74
**Origin**: operator request the same day ("get our runtime image fixed up and managed by CI so it
doesn't drift; gfx1100 and gfx906 first") after the utilization research found the XTX runtime
pod on vLLM 0.17 (`.loom/10-research-flexinfer-utilization-loom-mills-2026-09-13.md` §7).

## Summary

The per-architecture runtime images (`registry.harbor.lan/flexinfer/runtime:*`, plus the
standalone `flexinfer/vllm:*` family) are built by a mix of CI jobs, manual `allow_failure`
jobs, and off-CI `docker --context 7900xtx` scripts, and are pinned by digest in three places
that nothing reconciles: the runtime DaemonSet profiles in `deploy/system/values-k3s.yaml`, the
`GPUProfile` manifests, and individual `Model` manifests. The result is that what runs on the two
biggest cards is neither what CI builds nor what the build matrix says it should be. This spec
makes CI the only producer, an immutable tag the only thing a pin may reference, and a
`promote`-stage job the only way a pin moves.

## Current state (survey 2026-09-13, `build/`, `.gitlab/ci/runtime-publish.yml`, `deploy/`)

| Consumer | Pinned | vLLM | Built | By CI | Problem |
|---|---|---|---|---|---|
| gfx1100 DaemonSets ×2 (`values-k3s.yaml` runtime.profiles) + `gfx1100` GPUProfile `runtime.image` | `runtime@5d384ac4` = `rocm-gfx1100-57b56bb6` (unified persona) | 0.17.0 | 2026-08-24 | yes | `build/runtime.yaml` assigns the DaemonSet lanes to the **`gfx1100-serving`** persona, which CI builds on every qualifying master change (`rocm-gfx1100-serving-0d631512`, 2026-08-24) and nothing deploys |
| 15 Model / Experiment / debug manifests `spec.image` | `runtime@2e9652ed` = `rocm-gfx1100-v023-native-w4a16-037d0bc1` | 0.23 + RDNA3 W4A16 | 2026-07-21 | was | recipe repointed in place to v0.26 on 2026-08-01; not reproducible from HEAD; not prewarmed |
| `gfx1100` GPUProfile `backends.vllm.image` + prewarm | `vllm@a9b306af` | 0.17-era patched | 2026-05-20 | manual, `allow_failure` | 116 days; nothing rebuilds it |
| gfx906 DaemonSet + GPUProfile `runtime.image` | `runtime@89e8d0dc` = `rocm-gfx906-51987a76` | none | 2026-08-01 | yes | predates the 2026-08-23 Dockerfile change that bundles `server-snac.py`; the previous pin was GC'd from Harbor (4-day outage, `055843fe2`) |
| gfx906 GPUProfile `backends.vllm.image` | `vllm:rocm6.3.4-multiarch-serve@2f3fe306` | 0.6.3.post1 | 2026-06-09 | no (off-CI) | break-glass CI job documented as failing |
| `sm_52` GPUProfile `runtime.image`; two Models | `runtime:cuda-maxwell`, `vllm:rocm7.13-gfx110X-vllm0.19.1` | — | ? | no | mutable tags, no recipe |
| `pkg/quantization/gpu_job.go:114` | `runtime:unified-gfx906-v3` | — | — | CI publishes `-v4` | Go default one version behind |
| CI `promote` stage | — | — | — | — | contains no promotion job; promotion is a human running `scripts/promote-runtime-digest.sh` |
| gfx1100 publish jobs | — | — | — | — | do not rebuild on `cmd/flexinfer-runtime`, `internal/runtime`, `backend`, `server-snac.py` changes (the gfx906 job does) |
| Security | — | — | — | — | no Trivy job scans any GPU image; no Flux ImagePolicy covers them |

## Goals

1. Every runtime/vLLM image that production references is produced by a CI job with complete
   `changes:` triggers, pushed under an immutable `<tag>-<short-sha>` tag next to the floating
   tag, and prewarmed on its target nodes.
2. Every pin in git references a digest Harbor still holds under an immutable tag, and the
   DaemonSet/GPUProfile pins equal the latest *promoted* build of their profile.
3. Pins move only through a `promote`-stage path: drift report → canary (currency kill-test or
   ModelExperiment gauntlet) → validation-matrix row → pin-bump MR → Flux. Humans merge; CI
   proposes.
4. gfx1100 first, gfx906 second; both DaemonSets on a runtime that can host the current model
   families (Qwen3.5-family hybrid-GDN on gfx1100 needs vLLM ≥ 0.23 with the RDNA3 W4A16 op) with
   `FLEXINFER_RUNTIME_MULTI_MODEL=1`.

## Non-goals

- Rebuilding the off-CI torch/vLLM multiarch lane for gfx906 in CI (buildkitd-central cannot hold
  the from-source build; it stays a documented off-CI lane with an immutable tag).
- Changing the Model CRD or the promotion script's contract (validation row + rollback digest +
  prewarm stay mandatory).
- Fleet-wide default image flips in the same MR as a base-image build (issue #21 rule).
- ComfyUI/diffusers/mlc image lifecycles (reported by the drift job, not gated by it, until the
  runtime family is clean).

## Design

### Lanes

| Lane | Dockerfile | Floating tag | Immutable tag | Consumers after this program |
|---|---|---|---|---|
| gfx1100 serving (DaemonSets) | `build/Dockerfile.runtime-serving` | `runtime:rocm-gfx1100-serving` | `…-serving-<sha>` | both gfx1100 runtime DaemonSet profiles |
| gfx1100 unified (fallback surface) | `build/Dockerfile.runtime` | `runtime:rocm-gfx1100` | `…-gfx1100-<sha>` | `gfx1100` GPUProfile `runtime.image`, quantization runtimeImage |
| gfx1100 native W4A16 serving | `build/Dockerfile.runtime-serving-native-w4a16` | `runtime:rocm-gfx1100-v0NN-native-w4a16` (one lane per vLLM major, never repointed in place) | `…-<sha>` | Models via `promotion.model_manifests` |
| gfx1100 standalone vLLM | `build/Dockerfile.vllm-*` | `vllm:rocm-gfx1100-<variant>` | `…-<sha>` | `backends.vllm.image`, prewarm |
| gfx906 runtime | `build/Dockerfile.runtime-gfx906` | `runtime:rocm-gfx906` | `…-gfx906-<sha>` | gfx906 DaemonSet, `gfx906` GPUProfile |
| gfx906 unified | `build/Dockerfile.unified-gfx906` | `runtime:unified-gfx906` | `…-<sha>` | quantization jobs (Go default by digest) |

### Promote stage

1. `runtime_pin_drift` (S0, this MR): reads `build/runtime.yaml`, `deploy/**`, `charts/**`;
   asks Harbor whether each pinned digest exists and carries an immutable tag; compares each
   promotable profile's consumers with the profile's current floating-tag build; flags persona
   mismatches and mutable-tag pins. Advisory for one week, then blocking with `--strict`.
2. `runtime_canary_<profile>` (S1/S2): after a publish job pushes a new immutable tag, run the
   profile's canary against that digest on its target node — the currency kill-test recipe
   (`deploy/debug/qwen36-currency-*`) for vLLM lanes, the `model-eval-gauntlet` template through a
   `ModelExperiment` for serving lanes, `snac` decode + a `wan21` clip for the DaemonSet persona.
   Writes the validation-matrix row as an artifact.
3. `runtime_promote_mr` (S4): on canary pass, runs `scripts/promote-runtime-digest.sh <profile>
   --apply --validation-row <artifact> --rollback-digest <previous>` on a branch and opens the
   pin-bump MR through the GitLab API. Merge stays human; Flux rolls the DaemonSets.

### Pins

- Only `registry.harbor.lan/flexinfer/<repo>@sha256:…` where the digest is reachable through an
  immutable tag. `scripts/promote-runtime-digest.sh` resolves `<tag>-<short-sha>`, never the
  floating tag (gap #10).
- Every runtime digest referenced by a DaemonSet or Model is listed in `imagePrewarm.profiles`
  for its node, so Harbor GC and containerd GC cannot strand a lane.
- Go defaults (`pkg/quantization/gpu_job.go`) reference digests or the immutable tag family, not
  hand-bumped `-vN` tags.

## Slices

| Slice | Lane | Content | Acceptance |
|---|---|---|---|
| **S0** (this MR) | CI | `changes:` parity for both gfx1100 publish jobs; `scripts/check-runtime-pin-drift.py` + `runtime_pin_drift` job (advisory); `build/README.md` corrected; this spec | job green on master and MR pipelines; the report reproduces the survey's drift table |
| **S1** | gfx1100 | promote `rocm-gfx1100-serving-<sha>` onto both DaemonSet profiles + `FLEXINFER_RUNTIME_MULTI_MODEL=1`; one node first (5930k), then the XTX | kill-test below passes on the 5930k; drift report shows "current" for both DaemonSets |
| **S2** | gfx1100 | bump the serving persona off vLLM 0.17 (restore a `v023` native-W4A16 lane as its own profile, or re-qualify on v0.26/0.28 via the currency canary); move the 15 `2e9652ed` pins through `promotion.model_manifests` | runtime-managed Qwen3.8-27B loads in the XTX runtime pod beside snac (research §7b); currency canary 6/6 |
| **S3** | gfx906 | rebuild `rocm-gfx906`, repin DaemonSet + GPUProfile to the immutable-tag digest, prewarm runtime digests, fix the `-v3` Go default, document the off-CI vLLM lane | snac decode on radeonvii from the new image; drift report clean for gfx906 |
| **S4** | CI | Trivy for runtime/vllm (nightly, advisory → blocking); `runtime_promote_mr` | a pin-bump MR is opened by CI after a green canary |
| **S5** | gfx1100 | real master rules for `publish_vllm_rocm_gfx1100_qwen35_patched`; repin `backends.vllm.image` + prewarm; retire the two mutable-tag pins | no mutable-tag pins in the report |

### Status (2026-09-14)

| Slice | State | MRs |
|---|---|---|
| S0 | merged; gate advisory, 0 FAIL on master after the live-manifest filter | !1022, !1029, !1030 |
| repins + prewarm | merged | !1025, !1031 |
| S1 | merged, kill-test passed (XTX runtime pod on the serving persona, multi-model on) | !1027 |
| S3 | merged, kill-test passed (retrieval +30 s, voice +4 min); Go default fixed | !1031, !1028 |
| S4/S5 first half | **merged** — nightly advisory Trivy scans of the live GPU runtime/vLLM pins and master build rules for the standalone gfx1100 vLLM image. The grpc/govulncheck CI blockers were resolved before merge. | !1032 |
| S2 | **64K synthetic qualification passed; production promotion pending** — plugin 0.8.0 on `ad93ae72`, 0.96 utilization, two request slots: fresh compile 87.49s, 71,361 KV tokens; 3/3 keys at 65,365 tokens with explicit JSON mode (353.7214s TTFT, 25.3849 tok/s). Auto/required tool round trips and three cancellations passed. Five-minute soak: 82 requests, two active together, 39.35 tok/s median per request, zero restarts/faults. Final short smoke: 3/3, 41.4356 tok/s, 2.3575s TTFT at 2,337 tokens. Original 0.94 startup remains failed; unconstrained long JSON used Markdown fences. Target-node validation, longer workload soak, 128K and LoRA envelopes still gate production pins. | !1058 smoke; `.loom/iteration-runtime-s2-64k-2026-09-14.md`; matrix row `gfx1100-native-w4a16-2026-09-14-seqs2` |
| S4 promote-MR bot | **proposals job shipped** — `runtime_promotion_proposals` (`scripts/propose-runtime-promotions.py`, unit-tested with a stub promote script) consumes the drift JSON and writes one proposal per trailing profile (promote command, rollback digest, consumers, dry-run diff) as 30-day artifacts on schedule/master/MR runs; Draft-MR creation is wired behind `FLEXINFER_PROMOTE_MR_TOKEN` (not provisioned yet; prewarm-gated profiles stay proposal-only). The first live run reported every profile current. Remaining: provision the token; Trivy is shipped in !1032 | !1057 (merged 09-14) |
| S5 rebuild + repin, gate → `--strict` | **rebuild and repin merged** — pipeline 26956 / job 276644 produced `vllm@sha256:7ac257f3f04246b02c2cb05c610aeb56b19c2f57ef4ac4273c35d592a8ef1464`. !1060 moved `backends.vllm.image`, `gfx1100Image`, and both gfx1100 prewarm lists. Remaining: mutable debug pins and the advisory-to-strict gate after its observation window. | !1059, !1060 |
| S3' (incident #2 fix, gfx906) | **promoted** — `rocm-gfx906-882d81ba` (`171081de`) prewarmed via the new `radeonvii-gfx906` profile (!1046) and promoted onto the Radeon VII DaemonSet + GPUProfile (!1047); kill-test passed: entrypoint skip, 18 `config.json` mtimes unchanged, embeddings/rerank +2.5 min, voice +5 min. Publish jobs in `.gitlab-ci.yml` made non-interruptible (!1045) after two builds were auto-cancelled | !1045, !1046, !1047 |
| S1' (incident #2 fix) | **promoted** — !1037 entrypoint gate → `rocm-gfx1100-serving-e4ad4d10` (`f7d2e967`) prewarmed (!1042) and promoted onto both gfx1100 profiles (!1043); kill-test passed: entrypoint skips the node-wide pass, XTX `config.json` mtimes unchanged, Mills lane intact. S3' (gfx906) pending the rebuild (API pipeline 26760) | !1037, !1040, !1042, !1043 |
| eviction guard (incident, below) | merged; rolled 17:11Z (three runtime DaemonSets carry the class + request; gfx906 embeddings/rerank back within 5 min; both Mills lanes reloaded). Follow-up: the XTX lane then sat Pending on CPU behind priority-0 CI job pods → serving priority class on both Mills lanes | !1035, !1036, gitops !703 |

### Incident 2026-09-13 16:23Z — CI disk pressure evicted the workhorse lane

Three pipelines at once (!1032, !1033, loom-core !1945) put ~10 job pods on `cblevins-5930k`
(the main runner's job pods may land on every physical node; the overflow runner prefers
`radeonvii`/`7900xtx`; 34 job pods peaked on the Radeon VII's 127 G root today). Root usage
crossed the `nodefs.available<10%` line, image GC found 0.3 MB eligible, and kubelet's
eviction ranking put the serving pods **ahead of the CI pods**: the ranking's first key is
"usage exceeds the ephemeral-storage request", CI pods request 5Gi, and Model pods and the
runtime DaemonSets requested nothing — 50 MB of logs counts as exceeding. The workhorse
(Mills weaver primary) was evicted third, image GC then dropped its 11 GB runtime image, the
replacement pulled for 10 min and was Ready at 16:45Z. The `flexinfer-image-prewarm-5930k`
DaemonSet pod was denied admission under DiskPressure, so prewarm cannot cover this case.
Fix: 2Gi ephemeral requests on the two Mills lanes + all three runtime profiles, and
`flexinfer-serving-critical` on the runtime profiles (new chart key). Follow-up: a controller
default ephemeral request for every serving Deployment (one-time reload of every lane on the
controller roll, so it rides the next controller release). GitOps side: cap the runner
`ephemeral_storage_limit_overwrite_max_allowed` 50Gi → 25Gi on both lanes (gitops !703, merged;
25Gi is flexinfer-site's image-build opt-in, the largest in the fleet).

Second finding from the !1035 roll: the XTX Mills lane pod sat Pending on `Insufficient cpu`
for 8+ minutes — nine CI job pods at 1.5–2 CPU each held 23 of the node's 24 allocatable
cores, and the serving pod was priority 0 too, so "no preemption victims found". The
`flexinfer.ai/serving-priority-class` annotation (already used by the radeonvii retrieval
lanes) fixes that; !1036 sets it on both Mills lanes. Rule going forward: **every dedicated
lane carries both an ephemeral-storage request (eviction ranking) and the serving priority
class (scheduler preemption)** — the controller-default follow-up should set both.

### Incident 2026-09-13 16:22Z (found 17:35Z) — the S1 runtime pod rewrote the XTX lane's cache

`build/runtime-entrypoint.sh` normalizes Qwen3.5 / Gemma4 `config.json` files **in place** under
`/models` at every container start (architectures → the text-only class, text_config, M-RoPE
fields). In a dedicated pod `/models` is that pod's cache; in the per-node runtime DaemonSet it
is the node-wide hostPath holding every lane's cache. The S1 roll (pod `mf99r`, 16:22:15Z)
logged `[entrypoint] Fixed architectures -> ['Qwen3_5ForCausalLM'] in
/models/flexinfer-system/qwen38-27b-xtx-warm-canary/config.json`. The lane kept serving until
its next restart (the !1035 roll), then died on every first request: vLLM 0.23's text-only
class has no M-RoPE support (`AssertionError: M-RoPE support is not implemented` →
`EngineDeadError` → CrashLoopBackOff, 4 restarts). The 5930k workhorse was untouched only
because its DaemonSet is desired=0 — the same image would have done the same there. **The S1
kill-test could not see this**: it checked the runtime pod, not the neighbours' caches.
Stopgap 17:39Z: `config.json` restored to `Qwen3_5ForConditionalGeneration` in-pod (byte-identical
to the 5930k copy, backup `.config.json.bad-20260913-1622Z`) and the container restarted; lane
Ready at 17:43Z, answered in < 1 s. Fixes: !1037 gates the node-wide pass in manager mode
behind `FLEXINFER_RUNTIME_NORMALIZE_MODEL_CONFIGS=1` (dedicated pods unchanged; contract lint
asserts it) → master pipeline 26743 rebuilds serving / unified / gaming / gfx906 → promote the
serving and gfx906 digests (S1'/S3') with the kill-test **"a runtime pod start leaves every
`config.json` mtime under `/models` unchanged"**; and `hfOverrides` pins
`architectures=["Qwen3_5ForConditionalGeneration"]` on both Mills lanes (applied before
architecture resolution, immune to the on-disk rewrite). New rule for the matrix: every
runtime-image kill-test on a node with dedicated lanes includes a `config.json` mtime check.

## Riskiest assumption + kill-test

> Every spec must name its riskiest load-bearing assumption and a kill-test that proves it before
> downstream slices ship (`spec-riskiest-assumption`).

**Kill-test result (S1, 2026-09-13)**: passed on the only node with a runtime pod (the XTX).
The runtime pod came up Ready on the serving persona with vLLM, diffusers and the snac server
importable and both dedicated text lanes untouched. The 5930k half of the test could not run:
its DaemonSet is desired=0 behind the `quantization-pause` selector. The snac-decode path on the
serving persona remains unexercised until a voice window returns to that card.

**Assumption**: the `gfx1100-serving` persona is sufficient for the managed DaemonSet lanes — i.e.
nothing in production reaches the runtime pod for a layer only the unified image carries
(llama.cpp, Ollama, quantizer, Steam). If something does, S1 would break a live path the moment the
persona swaps, and the program has to keep two DaemonSet personas instead of one.

**Kill-test (S1, one node, ≤ 1 h, reversible)**: promote the current
`runtime:rocm-gfx1100-serving-<sha>` digest to the `gfx1100-5930k` DaemonSet profile only
(rollback digest = `5d384ac4`). Pass only if, on cblevins-5930k: the runtime pod is Ready with the
new image ID; `snac-decode-gfx1100`'s recipe (a runtime-managed backend) loads and decodes one
utterance when pointed at that node; a `wan21-t2v-1p3b` request completes through the runtime
(diffusers path); and a runtime-managed vLLM Model (e.g. a `qwen35-9b` debug lane without
`dedicatedDeployment`) reaches Ready and answers the 6-probe gauntlet. Any failure → revert the
one profile and record the missing layer; the DaemonSet persona decision is then made on evidence.

**Second-order risk** (S2): the `2e9652ed` recipe cannot be rebuilt from HEAD. Kill-test: build the
restored `v023` lane in CI and prove byte-for-byte irrelevance by running the promoted
`qwen38-27b-autoround-workhorse` gauntlet + needle set on the new digest before any Model pin moves.

## Rollback

Every slice changes pins by MR; rollback is the previous digest recorded in the validation-matrix
row (`.loom/60-validation-matrix.md`) applied through the same promotion script. The drift job is
advisory until S1 and S3 have rolled, so a red report never blocks an unrelated merge in the
meantime.

## Handoff

- Operator lane (needs GPU windows and the promotion contract): S1, S2, S3.
- Mills-routable (CI/YAML/Go only, verifiable in the sandbox): S0 follow-ups, S4, S5, the Go
  default fix. File them as `mills-eligible` issues referencing #74 once S0 has merged.

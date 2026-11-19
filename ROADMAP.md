# FlexInfer Roadmap

> Last Updated: 2026-11-04
> Tier: 1 (see workspace AGENTS.md "Portfolio Tiers")
> Tracking Issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/63

<!--
Convention (portfolio-refresh 2026-H2, see libs/STANDARDS.md "Roadmap & Backlog"):
- This file states CURRENT TRUTH, derived from git activity and deployed state —
  never re-date stale content. Each refresh MR must cite its evidence (git-log
  window inspected, deploy-state query used).
- Backlog lives in GitLab issues (P1/P2/P3 labels + milestones), NOT in this file.
  This file links the backlog; it does not duplicate it.
- If a live plan exists in the agent-context plan store, reference its plan_id
  here; the store is canonical and this file is a rendered summary.
- Staleness SLO: Tier 1/2 repos must have this file dated within 90 days.
  `bin/portfolio-inventory --roadmaps` reports conformance.
-->

## Current Status

FlexInfer is the production Kubernetes AI-inference operator for the homelab:
CRD-driven model lanes (`Model`, `GPUProfile`, `ModelCache`, `LoRAAdapter`,
`ModelCatalog`, `InferenceJob`), per-node runtime DaemonSets, and an
OpenAI-compatible proxy with scale-to-zero, lane breakers, context-overflow
reroute and failover. It serves LLM, embedding, rerank, speech, image, audio and
video lanes on two gfx1100 (RDNA3) cards, a Radeon VII (gfx906) and a GTX 980 Ti
(Maxwell). Each lane's card, image and measurements are in the
README's "What runs where" table; certification per GPU class is in
[docs/certification-matrix.json](docs/certification-matrix.json).

**Serving now** (read 2026-11-04 18:48Z, see Evidence below):

- **5930k (gfx1100), the always-on workhorse card.** `qwen38-27b-autoround-workhorse`
  (Qwen3.8-27B AutoRound, priority 900, `minReplicas: 1`) is the production text
  lane: vLLM 0.32 (`runtime@b06b06fe`) since 2026-10-25 (!1155), with
  `ROCM_SEGMENTED_ATTN`, fp16 KV and MTP K=2 at 32K since 2026-10-26 (!1178).
  Prompts over 32K overflow to the XTX lane. The Muse Glimmer 30B tools lane
  (llama.cpp b11847, priority 500) shares this card and stays Preempted behind
  the always-on workhorse.
- **7900 XTX (gfx1100), the flex card.** The warm Qwen3.8-27B lane
  `qwen38-27b-xtx-warm-canary` (priority 300, 64K, fp8 KV, `TRITON_ATTN`, same
  0.32 image) serves Mills and the overflow, and yields to the on-demand creative
  lanes at 500: Qwen-Image 2.1, Wan 2.1 T2V and VACE, Stable Audio Open. This
  placement layout (!1203) was promoted on 2026-11-04 (!1277): the card had
  dropped off PCIe under load until the owner's hardware work of 11-02, and
  the KT-FLEX rerun then held for 24 h.
  The XTX **runtime DaemonSet is still on `cfefae18`, the vLLM 0.17 serving
  persona**. Its move to the 0.32 build `527e2714` is pending: the GPUProfile
  prerequisite merged as !1278, the repin itself is Draft !1234.
- **Radeon VII (gfx906), retrieval, TTS and ASR.** llama.cpp serves bge-large,
  bge-reranker, gte-Qwen2-1.5B (with a CPU twin on the 5930k as failover),
  Qwen3-1.7B tools and Orpheus TTS; whisper.cpp serves
  `whisper-large-v3-turbo-radeonvii`, ICC's ASR since 2026-11-02 (!1207, !1252).
  Runtime `6fc6fcbd` = llama.cpp b11335 + whisper.cpp v1.9.5.
- **GTX 980 Ti (Maxwell).** Dreamshaper 8 image generation.

The Qwen3.5 9B RP pair, the Qwen3.5 35B lanes and the Qwen3.6 lanes are Idle or
Preempted; none of them is the production text lane any more.

**Recent activity** (`git log --first-parent origin/master`, 2026-08-01 to
2026-11-04, through `1d5ca03d1`, 306 merges):

- **August**: the Qwen3.6-27B, then Qwen3.8-27B AutoRound workhorse (!965, !974);
  the RDNA3 split-KV decode fix (`TRITON_ATTN`, !967, !972); Muse Glimmer 30B as
  the Mills tool and judge lane (!973); Orpheus TTS on the Radeon VII and SNAC
  decode as a bundled runtime backend (!982, !990); Stable Audio Open (!986);
  the secure gaming lease (!1005); MR validation pipelines that refuse to merge
  on a skipped pipeline (!991).
- **September**: the Python 3.14 model-tools image promoted (!1016, #73); the
  Mills utilisation program (!1019, !1021, !1024); the runtime-image CI program
  ([#74](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/74), below);
  serving PriorityClass and ephemeral-storage defaults for every serving pod
  (!1035, !1056); the OpenJEV cross-encoder as a pooling model (!1063, !1073).
- **October**: hybrid-GDN prefix caching (!1076–!1078); engine-stall detection
  and the per-lane circuit breaker (!1082, !1083); the GPU health sentinel that
  parks lanes on a dead card (!1104); vLLM 0.26 and then 0.32 on gfx1100
  (!1110, !1155, !1166, !1174) with segmented attention and MTP (!1178); the
  context-overflow reroute (!1177, !1182); the proxy request path off the API
  server (!1161); llama.cpp b11847 and its draft-mtp certificate (!1132, !1145);
  gfx906 hardware-queue admission (!1198); a shared compile cache (!1200); the
  whisper.cpp backend (!1206); Qwen-Image 2.1 (!1165); and the November design
  (!1208).
- **2026-11-01 to 11-04**: the code for features D1–D7 of that design, the
  placement layout, whisper.cpp ASR live, the Wan VAE MIOpen bypass (cold clip
  252 s, was over 5 min; !1268, !1275), two proxy cold-start fixes (!1264,
  !1270), the gfx1100 GPUProfile's vLLM defaults moved to the 0.32 stack
  (!1278) and the backend currency plan (!1279). Per-feature state:
  [docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md).

**Evidence for this refresh**: the git window above; read-only `kubectl` against
k3s on 2026-11-04 18:48Z: Flux Kustomizations `flexinfer-system` and
`flexinfer-models` Ready at `master@435ded362`, HelmRelease `flexinfer` Ready
(chart `1.0.16+435ded362204`), `kubectl get models.ai.flexinfer -A` for the
phases above, `kubectl -n flexinfer-system get ds,deploy` for the images above,
`flexinfer.ai/gpu.health=ok` on the three AMD GPU nodes; GitLab MR and issue
state the same day.

- **Plans**: [November design, D1–D7](.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md) ·
  [backend currency plan, S0–S10](.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md) ·
  [October gfx1100 program](.loom/10-research-gfx1100-next-feature-2026-10-23.md) ·
  [serving gaps and in-pod switching](.loom/10-research-serving-gaps-switching-caching-2026-10-27.md) ·
  [runtime-image CI spec](.loom/20-product-spec-runtime-image-ci-2026-09-13.md)
- **Verdicts**: [.loom/60-validation-matrix.md](.loom/60-validation-matrix.md), one dated row per kill-test
- **Plan store**: no live flexinfer plan; the only one, `plan-gaming-mode-v2-sunshine-moonlight-on-gfx1100-7900xtx-declara-ced675`, is parked (2026-10-11)
- **Deployed**: K3s GPU cluster via Flux (`flexinfer-system`, `flexinfer-models`)
- **CI**: custom (bespoke `.gitlab-ci.yml` + `.gitlab/ci/` includes: BuildKit image matrix, Go build/test, Trivy, Helm, runtime pin-drift and certification-matrix checks)
- **Climb** (2026-11-07): [.loom/climb.yaml](.loom/climb.yaml) declares this
  repository's Mills climb (problem, proxy-success / roadmap-gates / CI / cadence
  summit, constraints, tier 1) for flexinfer.ai/lab; from now on, Mills push
  bullets in this roadmap name their `bl-` backlog row so each merged step joins
  to its bullet.

## Now

- **Run the placement layout.** Keep the 5930k workhorse warm at 900 and the
  XTX as the flex card. The health sentinel parks the XTX lanes if the card
  drops again; KT-FLEX's rollback is `git revert -m 1 55a22375`.
- **Backend currency S0, in flight today** (owner override of the 11-06
  window): !1278 moved the gfx1100 GPUProfile's vLLM defaults off the
  0.17/0.19 stack (merged 18:48Z); Draft !1234 repins the XTX runtime
  DaemonSet and GPUProfile to `527e2714` (vLLM 0.32 with the creative-lane
  deps). Until !1234 merges and the XTX runtime pod is verified on 0.32, it
  stays on `cfefae18`.
- **Owner window, on or after 2026-11-06 00:15Z.** The Drafts held for it are
  listed in [docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md):
  D1 in-pod lanes (KT-SWAP), the `InferenceJob` API on (D2, KT-JOBS), the
  diarization lane (D5-a, KT-DIAR), gte twin headroom (D6, K2 re-run) and the
  agent digest pin (!1243). KT-QOS (D3) and KT-SO (D4) need only an
  owner-applied canary; the 11-04 plan recommends judging both on vLLM 0.33
  (decision P2), because 0.33 fixes upstream bugs either canary could hit.

## Next

- [ ] **Backend currency and gfx1100 vLLM gaps**
  ([plan](.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md) §6;
  owner decisions P1–P6 open):
  - S0: the XTX runtime onto 0.32 (Now, above).
  - S1: controller currency for vLLM 0.33 flags and keys; S2: plugin 0.10.0,
    0.33-ready. Both live-neutral, in progress since 2026-11-04.
  - S3: a `v0.33.0-rocm72` serving image and a parity canary on the XTX warm
    lane; the path to the upstream fixes KT-QOS and KT-SO depend on.
  - S4 to S7: measure the decode step, then the gfx1100 kernel candidates as
    canaries: a pruned-vocabulary MTP draft head (S5), Octave 3–4-bit KV on
    0.33 (S6, KT-OCTAVE), input embeddings in host memory (S7).
  - S8: gfx906 llama.cpp b11335 → b11847 and the whisper.cpp pin; S9: Wan
    creative work (VACE bf16 decode, denoise caching, decode A/B on the 0.32
    base); S10: drop two legacy ROCm env overrides on gfx1100.
- [ ] **Runtime images built and promoted by CI, no pin drift**
  ([#74](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/74), closed
  2026-09-15; [spec](.loom/20-product-spec-runtime-image-ci-2026-09-13.md)).
  Shipped: S0 drift gate, S1 serving persona, S3 gfx906 rebuild, S4 Trivy and
  promotion proposals, S5 standalone vLLM rebuild (all September), and S2, off
  vLLM 0.17, through the 0.32 promotions of 10-24 to 10-26. Left: the XTX
  runtime DaemonSet is the last 0.17 pin (S0 above); `runtime_pin_drift` is
  still advisory (`allow_failure: true`); CI-opened promotion MRs need the
  `FLEXINFER_PROMOTE_MR_TOKEN` project token.
- [x] **Put the idle gfx1100 cards to work for loom-core and Mills**
  ([research](.loom/10-research-flexinfer-utilization-loom-mills-2026-09-13.md)).
  Slices 1 and 2 shipped on 2026-09-13: the workhorse kept warm for Mills, the
  dead XTX gemma4 lanes retired, ModelBackfill re-armed, voice moved to the
  Radeon VII, and the XTX warm text lane promoted with Mills routed to the
  local lanes (!1019, !1021, !1024, gitops !696 and !699). Slice 3's runtime
  bump landed as vLLM 0.32. The !1203 layout replaced the 12 h idle-timeout
  posture with an always-on workhorse. Slice 2b (intake curation) and the
  overseer and shadow-judge follow-ups are in loom-core's backlog
  (services/loom-core#754, services/loom-core#755).
- [x] Python 3.14 model-tools promoted through a ModelCache publisher canary
  ([#73](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/73), 2026-09-04)

## Later

- **D5 wave 2**: a realtime voice loop on the Radeon VII (KT-VOICE-RT; protocol
  is owner decision O6 of the November design).
- **ROCm 10 and gfx906 PyTorch**: a ROCm 10 canary on the 5930k when a
  ROCm 10.2 MIOpen or a vLLM feature needs it (P4, deferred); the gfx906
  PyTorch ladder only when a Radeon VII PyTorch lane needs it. vLLM stays on the
  Radeon VII, frozen (owner decision 2026-11-04).
- Multi-tenancy: namespace isolation (no issue filed yet).
- Gaming-mode hardening: the Sunshine secure-image canary
  (docs/dev/gaming-image-canary.md) that unlocks `gaming.approvedImages`, idle
  auto-revert default-on, and the WirePlumber fix in open MR !757, which needs
  a gfx1100 runtime rebuild.
- Renovate-driven dependency hygiene ([#9](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/9)).
  The `ci/renovate` CronJob is suspended (last success 2026-07-11); re-arming
  needs a rotated token in platform/gitops.
- CNCF Sandbox submission (prep artifacts shipped; submit when adoption evidence justifies it)

## Backlog

Full backlog: [P1 issues](https://gitlab.flexinfer.ai/services/flexinfer/-/issues?label_name[]=P1) ·
[P2](https://gitlab.flexinfer.ai/services/flexinfer/-/issues?label_name[]=P2) ·
[P3](https://gitlab.flexinfer.ai/services/flexinfer/-/issues?label_name[]=P3) ·
[Milestones](https://gitlab.flexinfer.ai/services/flexinfer/-/milestones) ·
[all open issues](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/?state=opened)
(#66, #68, #69, #71 and #72 carry no priority label, so only this last link lists them)

# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research: `10-research.md`
- Research (2026-09-13): idle gfx1100 cards → loom-core/Mills utilization + Sept-2026 24 GB model survey: `10-research-flexinfer-utilization-loom-mills-2026-09-13.md`
- Product spec: `20-product-spec.md`
- Product spec (2026-09-13): runtime images built + promoted by CI, gfx1100/gfx906, issue #74: `20-product-spec-runtime-image-ci-2026-09-13.md`
- Implementation plan: `30-implementation-plan.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`
- Gemma4 26B/31B GPTQ + TurboQuant plan: `gemma4-26b-31b-gptq-turboquant-plan.md`
- Gemma4 31B TurboQuant closeout: `gemma4-31b-turboquant-closeout.md`
- Gemma4 31B TurboQuant memory fix plan: `gemma4-31b-turboquant-memory-fix-plan.md`
- gfx1100 quantization validation matrix: `60-validation-matrix.md`
- PR-2 readiness plan: `../docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`
- Roadmap reconciliation checklist: `../docs/planning/roadmap-reconciliation.md`
- gfx1100/gfx906 platform enhancement spec: `gfx1100-gfx906-platform-enhancements-spec.md`
- gfx1100/gfx906 platform enhancement plan: `gfx1100-gfx906-platform-enhancements-plan.md`
- gfx1100/gfx906 next-round parallel plan: `gfx1100-gfx906-next-round-plan.md`
- vLLM feature-parity brainstorm (AMD, 2026-05-15): `brainstorm-vllm-feature-parity-amd-2026-05-15.md`
- vLLM feature-parity Wave 1 spec (AMD, 2026-05-15): `21-product-spec-vllm-feature-parity-2026-05-15.md`
- vLLM feature-parity Slice 3 falsification (rms_norm ROCm bug, 2026-05-15): `slice3-v1-sandbox-rms-norm-falsified-2026-05-15.md`
- ASR + diarization infra plan for ICC meeting transcription (7900 XTX, 2026-05-18): `asr-diarization-7900xtx-plan-2026-05-18.md`
- ASR Slice 1 kill-test INCONCLUSIVE (controller did not reconcile, 2026-05-19): `asr-diarization-kill-test-inconclusive-2026-05-19.md`
- Roadmap unblock plan (blocked feature churn, 2026-05-21): `roadmap-unblock-plan-2026-05-21.md`
- Context-curve benchmark spec capsule: `../docs/planning/context-curve-benchmark.md`
- RALPH context-curve benchmark spec iteration (2026-05-21): `ralph-context-curve-benchmark-spec-2026-05-21.md`
- RALPH context-curve runner MVP (2026-05-22): `ralph-context-curve-runner-2026-05-22.md`
- RALPH context-curve live capture (2026-05-22): `ralph-context-curve-live-capture-2026-05-22.md`
- RALPH context-curve ConfigMap storage (2026-05-22): `ralph-context-curve-configmap-storage-2026-05-22.md`
- RALPH context-curve spec closeout (2026-05-22): `ralph-context-curve-spec-closeout-2026-05-22.md`
- F4 long-context agent brainstorm (2026-05-25): `brainstorm-f4-long-context-agent-2026-05-25.md`
- RALPH F4-prefix-cache-flip canary plan (2026-05-26): `ralph-f4-prefix-cache-flip-canary-2026-05-26.md`
- RALPH F4-tool-loop-as-prefix kill-test plan (2026-06-01): `ralph-f4-tool-loop-as-prefix-2026-06-01.md`
- RALPH F4 agent-loop ReAct client — slice 1/CLI (2026-06-01): `ralph-f4-agent-loop-client-2026-06-01.md`
- Hardware-utilization sprints brainstorm (2026-06-03): `brainstorm-hardware-utilization-sprints-2026-06-03.md`
- Hardware-utilization arc implementation plan (2026-06-03): `30-implementation-plan-hardware-utilization-2026-06-03.md`
- RALPH model-tools Python 3.14 candidate (2026-08-01): `45-iteration-plan-model-tools-python314-canary-2026-08-01.md`
- Research (2026-10-23): next gfx1100 feature — MTP on the Qwen3.8-27B lanes via a vLLM 0.32 native-W4A16 runtime, llama.cpp refresh, Sep–Oct 24 GB model census: `10-research-gfx1100-next-feature-2026-10-23.md`
- Research (2026-10-24): Timestral-209M (Mistral zero-shot time-series forecaster) — thin snac-style `timestral` backend in the gfx906 stacked runtime pod, gated on a news-analyzer backtest kill-test: `10-research-timestral-209m-2026-10-24.md`
- Research (2026-10-24): Qwen-Image 2.1 "Uncensored-Quantized" candidate + image-inference support audit — bring Qwen-Image 2.1 to the XTX stacked runtime pod (diffusers 0.40 → 0.41 pin, GGUF/NF4 loading, pipeline-aware call contract), derivative as the second arm behind official weights, riskiest assumption = sd.cpp-converted GGUF loads in diffusers: `10-research-qwen-image-21-uncensored-2026-10-24.md`

## Current Goal (2026-10-21) - gfx1100 recovery: XTX still drops under load, runtime pins unpullable

State found 2026-10-21 (branch `claude/flexinfer-gfx1100-improvements-bcdac9`):

- [x] `cblevins-5930k`: Ready, `qwen38-27b-autoround-workhorse` Ready and answering through the
      proxy in 0.95 s. The `FreeDiskSpaceFailed` events are the intentional 60/50 image-GC policy
      finding nothing unused (prewarm pins every serving image); not a fault.
- [x] `cblevins-7900xtx`: rebooted 2026-10-20 23:32Z (Ubuntu 24.04.3, 6.14.0-24, `amdgpu.runpm=0`);
      card enumerated, link x16 Gen4, 0 amdgpu errors for 24 h idle. **Load soak FAILED**: an in-pod
      fp16 matmul burn (70 TFLOPS, 283-308 W, 73 C) wedged the SMU at +2m41s — same signature as
      10-04..10-10 (matrix row `xtx-pcie-soak-2026-10-21`). Fault is hardware/power-side, not runtime.
      The warm-canary selector stays parked; the host needs a reboot (likely a hard one: ttm kworker +
      the agent's `rocminfo` are in state D).
      2026-10-22: the warm reboot hung for 12 h (hard power cycle needed). Second soak under a
      lower-draw posture (DPM auto, 294 W floor, OD max sclk 1700 MHz, ASPM off; row
      `xtx-pcie-soak-2026-10-22`) hung the SMU within ~1 min at ~230 W / 1659 MHz — draw is not
      the trigger. Software knobs exhausted; next is a card swap with the 5930k's XTX. Node left
      cordoned.
      2026-10-22 15:36Z: update audit found the kernel held at 6.14 HWE (board BIOS 1811 from
      2023 is the other gap; GPU firmware was not behind). Staged 7.0.0-38 HWE, power cycle, same
      posture and firmware: **soak PASSED** (600 s, 56.8 TFLOPS, 224-233 W, 0 SMU errors; row
      `xtx-pcie-soak-2026-10-22b`). Node uncordoned. Next: vLLM-lane soak on the XTX (selector
      restore + startable image), burn without the clock cap, BIOS 1811 → 3854/3881.
- [x] 2026-10-22 17:16Z: !1097 merged; !1100 moved the controller digest pin in `values-k3s.yaml`
      so it actually rolled (`IfNotPresent` for digest pins live; the workhorse restarted once from
      its resident image); !1099 restored the warm-canary selector. Lane Ready in 2m15s from the
      node-resident `2e9652ed` image, then **vLLM soak PASSED**: 120/120 completions, 56.2 tok/s,
      p95 15 s, 182-190 W, 0 SMU errors (row `xtx-vllm-soak-2026-10-22`). `mills-warm` /
      `mills-council` are back on the XTX. Remaining: burn without the clock cap, BIOS update,
      `2e9652ed` repin (#74 S2), kernel 7.0 sits above ROCm's listed 6.17 ceiling.
- [x] 2026-10-22 20:03Z cap-free burn PASSED with the lane resident (row `xtx-pcie-soak-2026-10-22c`):
      73 TFLOPS pinned at the 294 W floor, ~2100 MHz, 80 C, 0 SMU errors. The 1700 MHz cap does not
      matter on 7.0. Operator decision 21:45Z: the boot unit now sets DPM auto + the 294 W floor
      and resets overdrive to stock (no cap); verified live. Remaining: BIOS 1811 → 3854/3881,
      `2e9652ed` repin, kernel 7.0 vs ROCm's 6.17 ceiling.
- [x] Stale canary Deployment (rendered for the real hostname, recreated after the reboot, 24 h of
      ImagePullBackOff holding both `amd.com/gpu` slots) scaled to 0 by hand; controller now parks a
      Deployment whose rendered selector the Model no longer declares (this branch).
- [x] **Registry**: Harbor retention (`flexinfer` project: keep latest-30-pushed OR pulled-within-30d,
      weekly) deleted `runtime@2e9652ed` (17 refs: every dedicated gfx1100 vLLM lane incl. the live
      workhorse), `@f7d2e967` (both gfx1100 runtime DaemonSets) and `@522d0fe2` (prewarm). Nodes
      cannot re-push them: k3s containerd discards unpacked layer blobs, only snapshots remain. Any
      restart of a dedicated gfx1100 lane fails to pull (policy Always). Controller now uses
      IfNotPresent for digest pins so a resident image starts without the registry (this branch).
- [x] 2026-10-24 (branch `claude/gfx1100-flexinfer-support-904425`): **gfx1100 next-feature program,
      slice S1 PASS** — vLLM 0.26 → 0.32.0 on the native-W4A16 profile (research
      `10-research-gfx1100-next-feature-2026-10-23.md`, matrix row `gfx1100-native-w4a16-v032-2026-10-24`).
      Image `runtime@sha256:8ff8d14a…` (`pinned-8ff8d14aa9ec`): the official 0.32 ROCm base already
      carries the gfx1100 `_rocm_C` op (12 code objects), so the Dockerfile's new `VLLM_REBUILD=auto`
      probe skipped the hour-long source rebuild; plugins ported (0.32 registers the Qwen3.5 text
      classes itself; `FLEXINFER_QWEN35_MTP_HEAD=checkpoint` added for S2); backend stops emitting
      `--no-calculate-kv-scales` (removed in 0.28, set on 12 lanes). Canary on the 5930k at the
      workhorse envelope: V2 model runner, RDNA3W4A16LinearKernel, TRITON_ATTN, pool 140,753 tok /
      2.15x after the KVPoolRecheck relaunch, probes 3/3 identical, 41.5 tok/s, prefix cache
      10.7 s → 1.0 s, 0 restarts; one recovered HIPCachingAllocator OOM warning on a 7.5K cold prefill
      → promote at 0.95. Next: S2 MTP kill-test, S3 ROCM_SEGMENTED_ATTN A/B, S4 promotion.
- [x] 2026-10-24 (same branch): **slice S2, the MTP kill-test — PASS on the riskiest assumption**
      (research §7b/§7c, matrix row `gfx1100-native-w4a16-v032-mtp-2026-10-24`): on the S1 digest,
      MTP K=2 with `FLEXINFER_QWEN35_MTP_HEAD=checkpoint` reached **pos-0 acceptance 0.80**, acceptance
      length 2.35, **65 vs 41 tok/s single-stream (1.58x)** at short context but only 48 vs 40 (1.21x)
      at 2K, 1.40x per request under concurrency 2, pool 1.55x vs 2.15x; probes identical, long greedy
      answers diverge ~150–200 tokens in (the MTP-off engine is not bit-reproducible either); 0 faults.
      Next: S3 segmented-attention A/B on the verify cost, a K=1/2/3 A/B, then S4 promotion as the
      stacked runtime persona. S3 prepared 2026-10-25 (research §7d:
      five-arm A/B at 32K, sweep points from the 7-day prompt histogram — workhorse p50 466 / p90 4,260
      tokens, 76 % <= 2K). Preparation notes: Reading v0.32.0 showed the draft model is built from the target's `VllmConfig`,
      so the biMEMO INT4 MTP block loads through the same AutoGPTQ config as the target and the
      `config.json` patch the 0.23 attempt needed is gone; an offline probe in the S1 image confirmed
      the plugin's `mtp_head=checkpoint`, the per-layer GPTQ decisions (`mtp.fc` fp16, seven INT4
      linears) and the `Qwen3_5MTP` draft mapping. Manifest `deploy/debug/qwen38-27b-v032-mtp-canary.yaml`
      (S1 + `speculativeConfig` mtp K=2 with the draft pinned to TRITON_ATTN, env via the command
      wrapper), probe `scripts/verify-vllm-mtp.py` (counter-delta acceptance, parity hashes, soak).
      Blocked on the S5 llama.cpp window (`muse-glimmer-30b-canary` at 520); operator applies after.
- [x] 2026-10-26 (branch `feat/gfx1100-serving-v032`): **slice S6a, one stacked 0.32 runtime image for
      gfx1100 — PASS offline** (research §7e, issue #74 S2 serving half). The runtime DaemonSets still run the 0.17 serving
      persona and 0.32 ships only as a `vllm serve` image, so the 5930k runtime is parked and every lane
      there is its own Deployment. New `Dockerfile.runtime-serving-v032` / profile `gfx1100-serving-v032` /
      CI `publish_serving_rocm_gfx1100_v032`: the native-W4A16 stack plus the runtime supervisor, diffusers
      0.42.0 and snac, with the vLLM stack frozen by pip constraints and `verify_runtime_serving_stack.py`
      proving the spawn contract at build time (the offline kill-test). The first build caught diffusers
      0.41.0 needing `huggingface-hub<2` against the base's 2.2.0, so the pin is 0.42.0; the final build
      kept all 265 base packages and adds ~92 MB over the shared base. Next: S6b un-park the 5930k runtime
      on it and qualify the workhorse through the runtime (GPU window).
- [x] 2026-10-22 (this branch, `claude/gfx1100-inference-optimization-323ab7`): **GPU health sentinel**
      so the next drop parks itself. Agent: sysfs-first per-card check (`current_link_speed=Unknown`
      on an awake card, EIO/EREMOTEIO on SMU-backed reads, vanished card) plus bounded vendor-tool
      timeouts (`--probe-timeout` 15 s + `WaitDelay`; the agent hung in `rocminfo` during every drop)
      → node label `flexinfer.ai/gpu.health=ok|degraded` + reason/since annotations +
      `flexinfer_gpu_healthy`. Scheduler `Filter` rejects degraded nodes; controller `detectGPU`
      skips them and reports `Schedulable=False/GPUDegraded` (Deployment parked, event) with a Node
      watch for instant park/release; chart alert `FlexInferGPUDegraded`. Replaces the by-hand
      `cblevins-7900xtx-gpu-offline` selector trick. Runbook
      `docs/agents/operations.md#gpu-health-sentinel`. Live kill-test pending: needs the agent and
      controller image pins moved after CI publishes, then a sysfs fault injection is not possible
      without a real wedge — verify on the next drop (or by temporarily setting
      `--probe-timeout=1ms` on one node to force `probe-timeout`).
      **Rolled out 2026-10-23** (!1104 merged 22:37Z as eda760ba): agent DaemonSet + scheduler
      restarted onto build `20261022-225154` at 23:47Z (all three AMD nodes label
      `gpu.health=ok`; the 980 Ti gets no label because a healthy NVIDIA card yields no verdict —
      small follow-up); !1105 (Go 1.26.10, merged 14:14Z via the REST merge call, govulncheck
      green again); !1112 (14:38Z) pinned the controller to master b8637f35 (digest `39581256`,
      tag `20261023-142445`; pod `647ccb879c-2hpdq` Running 14:40:00Z, old pod gone 14:40:20Z, no
      lane restarted — workhorse pod `bgjll` untouched) and enabled chart alerting for the first
      time in this cluster (`release: kube-prometheus-stack` + prometheus/role labels; Prometheus
      loaded `flexinfer-gpu` incl. `FlexInferGPUDegraded`; `FlexInferGPUVRAMLow` disabled via
      `vramFreeRatio: -1` because vLLM holds 94-96 % by design). Controller reconcile rate is the
      same before/after (10.2 vs 10.7/s — a PRE-EXISTING ~10/s loop with status conflicts on
      `gte-qwen2-1p5b-radeonvii`, worth its own look). Master lint/vet now fail only at the
      runner-cache save ("helper exceeded its local ephemeral storage limit 4Gi") and the drift
      gate FAILs on `f7d2e967` (both gfx1100 runtime DaemonSets) + `522d0fe2` (prewarm) — both
      proposed as separate tasks.
- [x] Decide the repin: (a) rescue single-layer images from the 5930k snapshots (identical rootfs,
      new digests, immutable `-rescued` tags) as the rollback anchor, and/or (b) promote the
      qualified v0.26 native-W4A16 build (`ad93ae72` / `93c93733`, #74 S2) onto the 5930k workhorse
      with a canary. Until then: do not restart `qwen38-27b-autoround-workhorse`.
      2026-10-22 23:3xZ: (b) chosen, no rebuild needed — the plugin port landed 09-14 and `ad93ae72`
      was still in Harbor (`pinned-ad93ae726bbe`). !1106 prewarmed it on both gfx1100 nodes; !1107
      rolled the XTX warm lane onto it as the target-node canary: identical answers, 43.0 tok/s,
      pool 103,517 (1.58x at 0.94; 0.26 charges cudagraph memory to the budget → draft !1108 for
      0.96), 40/40 concurrent completions at 37 tok/s per stream — then **the card dropped off the
      bus 2m54s into the soak** (SMU bus error, `device lost from bus!`; first time on kernel 7.0
      and at uncapped 2.8 GHz / 307 W). Lane parked again (!1109), pin stays `ad93ae72`. !1110
      (merged 2026-10-23 02:35Z on the user's go) moved the workhorse at 0.96: the first 0.26 boot
      profiled 71,493 (1.09x), the controller's KVPoolRecheck relaunched it warm → 128,837 / 1.97x,
      Ready +8m12s, 3/3 identical in-pod and via the proxy, 40.6 tok/s clean single-stream, host
      clean. Evidence row `gfx1100-native-w4a16-2026-10-22`. `2e9652ed` stays in both prewarm lists
      as the rollback anchor (node snapshots only); (a) is no longer needed. Still pinned to
      `2e9652ed`: `qwen36-27b-autoround-workhorse`, `qwen36-27b-fable-gptq`, `qwen35-9b-ablit-rp`
      (parked / fallback lanes) and three debug manifests — same repin when they are next needed.
- [x] 2026-10-23 runtime DaemonSet repin (#74 S1''): the gfx1100 runtime DaemonSets (`gfx1100` on
      the XTX, `gfx1100-5930k` at desired=0) and the GPUProfile moved off the Harbor-deleted
      `f7d2e967` onto `cfefae18` (`rocm-gfx1100-serving-b8637f35`, master pipeline 34198, Go 1.26.10).
      !1114 prewarmed it on both nodes, !1116 rolled it: XTX runtime pod Ready in ~25 s, multi-model
      on, entrypoint skips config normalization, `config.json` mtimes unchanged, `snac-decode-gfx1100`
      Ready in ≤ 16 s and decoding on the GPU through the pod and the proxy. Then `f7d2e967` /
      `522d0fe2` left both prewarm lists (`f7d2e967` stays resident on the XTX as the comment-only
      rollback anchor). Row `gfx1100-serving-2026-10-23`. diffusers 0.40 / transformers 5.17 came
      along unpinned; no diffusers lane runs through the XTX runtime pod today.
      Drift gate still FAILs on `2e9652ed` (the 27B rollback anchor in both prewarm lists +
      `qwen36-27b-autoround-workhorse`, `qwen36-27b-fable-gptq`, `qwen35-9b-ablit-rp`,
      `qwen35-9b-ablit-rp-5930k`) and gfx906 `0e590ca2` (abliteration image, gone from Harbor and
      not resident on the Radeon VII).
- [x] 2026-10-23 17:49Z check-in: !1098 (open since 2026-10-22) merged — `flexinfer-scheduler`
      loses its hard `cblevins-7900xtx` pin (any amd64 control-plane node, XTX preferred at
      weight 100) and `kokoro-tts` widens to {7900xtx, 5930k}; Flux rolled both within 5 min and
      each landed back on the XTX (`flexinfer-scheduler-59475d6ddc-w8gqk`,
      `kokoro-tts-6d796c7546-wv6nc`). An XTX wedge no longer takes the control plane or
      `/v1/audio/speech` down with it.
- [x] 2026-10-23 gfx906 runtime repin (#74 S3''): the Radeon VII DaemonSet, the GPUProfile
      `runtime.image` and its abliteration pin moved off the off-CI Humanizer overlay `8181b690`
      (and the Harbor-deleted `0e590ca2`) onto the master CI build `90648c35`
      (`rocm-gfx906-b8637f35`, pipeline 34198). !1118 prewarmed it, !1119 rolled it: pod Ready
      13 s after start, retrieval plane back in 56–72 s, orpheus in 64 s, Humanizer serves on the
      GPU from the CI build (20 s cold load, coherent rewrite, evicts only orpheus for its 30 s
      idle window), `config.json` mtimes unchanged, probes identical to the pre-roll baseline.
      Drift gate FAILs only on `2e9652ed` now. Row `gfx906-serving-2026-10-23`. The retired overlay
      turned out to mix two llama.cpp library generations (its `llama-embedding` cannot start —
      `undefined symbol: llama_params_fit`). The `amdgpu: init_user_pages … -1` bursts seen in
      the kernel log during llama.cpp loads are pre-existing and harmless (identical 288-line
      counts from both images in an A/B); details in the row.
- [x] 2026-10-23 XTX lane restored (operator decision: 2100 MHz cap). Host power-cycled 02:35Z,
      16 h clean on kernel 7.0; the boot unit `amdgpu-power-pin.service` now sets DPM auto + the
      294 W floor + an overdrive max sclk of 2100 MHz (both kernel-7.0 passes were ≤2100 MHz, the
      one kernel-7.0 drop was at 2780-2837 MHz), applied live 18:54Z. !1121 flipped the selector
      back; the Deployment stayed at 0 until the `flexinfer.ai/engine-stall-recovery` latch from
      the 10-22 drop was removed by hand (`kubectl annotate model … flexinfer.ai/engine-stall-recovery-`,
      the runbook step in `docs/agents/vllm-engine-stalls.md`) — Ready +3m05s after that, pool
      103,517 / 1.58x at 0.94, probes 3/3 identical, 40.3 tok/s. **Soak PASSED**: 147/147
      concurrency-2 completions over 604 s, 0 errors, 201-223 W / 59-72 C / 2020-2141 MHz, 0 SMU
      or amdgpu errors, link x16 Gen4, lane 0 restarts. Then !1108 merged (0.96): pool 118,411 /
      1.81x, probes 3/3 identical, `mills-warm` / `mills-council` / `xtx-warm` back. Row
      `xtx-vllm-soak-2026-10-23`. Still open: BIOS 1811 → 3854/3881; a cap-free soak is a
      separate decision — the card has never survived sustained load above ~2140 MHz.
- [x] Retention guard (gitops): shipped 2026-10-22 as platform/gitops !971
      (`k3s/monitoring/harbor-flexinfer-pin-guard/`): CronJob `harbor-flexinfer-retention-ensure`
      asserts an `always` retain rule for `pinned-*` on the `flexinfer` policy and
      `harbor-flexinfer-pin-tags` (05:30 / 17:30Z) tags every `deploy/` + `charts/` digest pin
      `pinned-<12hex>`; today's drift report already shows `pinned-cfefae18061f`. Runbook
      `docs/runbooks/harbor-flexinfer-pinned-digests.md` (gitops).
- [x] Controller reconcile hot loop (pre-existing, measured again 2026-10-23): the `model`
      controller ran ~8.6–9.8 reconciles/s (`requeue_after`) — every shared-group member on a 3 s
      `requeueFast` poll — with periodic `the object has been modified` conflicts on
      `gte-qwen2-1p5b-radeonvii` from the Ready branch of `reconcileViaRuntime` writing status on
      every pass. **Fixed 2026-10-23** in !1122 (steady shared members re-elect every 30 s, every
      status writer skips unchanged status via `persistStatus`, sibling + GPULease watches keep
      swaps event-driven), !1125 + !1127 (the cache staging rebuilds no longer remove and restore
      `prefetchPolicy`/`lastPrefetchTime` on every pass — a latent two-writes-per-pass churn the
      first roll turned into a worker-starving loop on gte and then qwen36-35b; Ready-model
      `lastActiveTime` stamps no longer reconcile) and !1129 (parked Deployment lanes at 30 s, not
      10 s). Controller pinned to `65d99ead` (tag `20261023-210446`, !1130): verified over 10 min
      at 0.90 reconciles/s (was 8.6), 0 conflicts, 0 errors, queue depth 0, every runtime-served
      Model on its 30 s cadence, pod with 0 restarts, Model phases unchanged.
      Worklog entry "Controller reconcile hot loop — fixed in four MRs". Follow-up worth a look:
      controller-runtime's priority queue lets one self-looping Model starve every initial-list
      Model (single worker) — consider `MaxConcurrentReconciles` > 1.
- [x] 2026-10-24 vLLM currency 0.26 → 0.32 (#74 S2), canary stage. !1134 moved the
      `gfx1100-native-w4a16` profile onto the upstream `vllm/vllm-openai-rocm:v0.32.0` wheel and
      dropped the hour-long from-source rebuild (the 0.32 wheel ships gfx1100 code objects; the
      verify script asserts the ISA at build time); image `cd108c9e` built off-CI on the 7900xtx
      docker daemon because the BuildKit group was saturated, prewarmed by !1143, canaried on the
      XTX warm lane by !1144: Ready 5m19s after merge via one KVPoolRecheck relaunch, Model Runner
      V2, pool 129,582 / 1.98x at 0.96 (1.81x on 0.26), probes 3/3 identical, 40.5 tok/s under the
      cap, proxy `mills-warm` 0.48 s, 0 SMU errors. `calculateKvScales` left the manifest (flag
      removed upstream in 0.28; every other 0.26-era manifest must drop it when repinned). Row
      `xtx-vllm-v032-2026-10-24`.
      Side effects: the 5930k prewarm pull pushed its root from 80 % to 87 % → kubelet
      DiskPressure 17:36–17:47Z evicted DaemonSet pods (incl. the Longhorn instance-manager) and
      the prewarm pod, and the 60/50 image GC deleted `2e9652ed` there for good (!1149 dropped the
      dead entry; !1147 released the 18 GB `cfefae18` hold). My GPU-visible docker probe containers
      on the XTX host coincided with the 17:01Z host stall (kubelet NotReady, Mills lane
      rescheduled, a Longhorn restore killed) — never again on a serving host.
- [x] 2026-10-25 0.32 promoted on the 5930k workhorse (!1155, operator-approved; operator chose to
      soak the 5930k card rather than the XTX). Ready on `cd108c9e` at about 14:49Z after one
      KVPoolRecheck relaunch: pool 140,753 / 2.15x (0.26: 128,837), probes 3/3 identical, 41.5 tok/s
      (0.26: 40.6). 10-min concurrency-2 soak alongside live Mills traffic: 135/135 OK, p95 14.0 s,
      aggregate 44.8 tok/s, host 302 W / ≤ 90 °C with 0 SMU and 0 amdgpu errors, 0 restarts. Row
      `workhorse-vllm-v032-2026-10-25`. XTX canary stays on `cd108c9e` as the functional reference.
- [ ] Move the workhorse (and the XTX canary) from the off-CI `cd108c9e` to the CI-built 0.32
      digest `b06b06fe` (master pipeline 34797 job 414103, published 2026-10-25 22:38Z, prewarmed
      on the 5930k by !1166; same Dockerfile lineage plus the newer text plugin). The parked
      5930k lanes already run it. The other session's S3 uses its own off-CI `b5afdb20` (same
      inputs). Then drop `ad93ae72` from the 5930k prewarm list.
- [x] 2026-10-25 14:2xZ 5930k disk: operator-approved deletion of the orphaned probe caches
      (`qwen38-27b-v032-canary`, `-v032-mtp-canary`, `-gguf-spec-probe`, `-gguf-vulkan-probe`,
      `muse-glimmer-30b-canary` + `compile-cache-qwen38-27b-v032{,-mtp}-5930k`): re-confirmed no
      Model CR and zero open handles per directory (`lsof +D`), hostPath dirs so not reclaimable
      through `flexinfer_evict_cache`, removed over ssh → 84 GiB freed, root 79 % → 70 % (283 GB
      free; Longhorn had already fenced the disk via platform/gitops!989). Node Ready, no
      DiskPressure, workhorse/agent/prewarm pods untouched. Longer term the 5930k root still
      cannot hold Longhorn replicas, model caches and 300 GB of images together.
- [x] 2026-10-25 22:43–23:41Z the three parked 5930k lanes that lost `2e9652ed` run the
      CI-built 0.32 image `b06b06fe` (operator-approved; !1166, plus !1167 and !1168 to make two
      of them boot). Each was booted and probed in turn, then parked again with the card back on
      the workhorse: Qwen3.6 workhorse 73,068 / 1.11x at 0.96, 41.6 tok/s; Fable 46,811 / 5.71x
      eager, 7.6 tok/s (0.23: 8.7); 9B twin 694,559 / 3.03x, 103.5 tok/s base and 68.5 with the
      `nsfw-rp-twin` adapter. All probes coherent, 0 restarts after the fixes, host clean. Row
      `parked-5930k-lanes-v032-2026-10-25`.
- [x] 2026-10-26 5930k disk: operator-approved deletion of `compile-cache-qwen35-9b-v023-5930k`
      (3.4 GB of 0.23 artifacts, newest file 2026-09-20). Confirmed first: no Model spec, pod or
      non-comment line on master references it (only ten scaled-to-0 rollout-history
      ReplicaSets of the twin), 0 open handles, no mounts. 3,449 MiB freed, root 72 % → 71 %
      (270 GB free); node Ready, no DiskPressure, workhorse Ready.
- [ ] Fable decodes 13 % slower on 0.32 in its eager envelope (7.6 vs 8.7 tok/s). Graph mode
      (`enforceEager: false`) would likely put it near the Qwen3.6 lane's ~40 tok/s, but that is
      a separate qualification (VRAM at 0.90, coherence), not part of the repin.

## Previous Goal (2026-09-13) - Idle gfx1100 cards → loom-core / Mills utilization

Research: `10-research-flexinfer-utilization-loom-mills-2026-09-13.md`. The 7900 XTX is
0.0015% busy over 30d and the 5930k workhorse cold-starts (>300 s, 12/12) after every
30-minute quiet spell, so Mills keeps every inline stage on vendor models.

- [x] Slice 1 (this cycle's MR): workhorse `idleTimeout` 30m → 12h; retire the two Pending
      7900xtx gemma4 lanes (faulted cache PVC, data gone); re-arm ModelBackfill against the
      workhorse; prepared XTX warm-lane canary (blocked, see research §7).
- [ ] Slice 1 kill-test (48h post-merge): zero workhorse cold starts on Mills calls, video
      lanes still demand-swap (research §8).
- [x] Operator decision on the XTX (2026-09-13): voice to the VII tier only (7a). Orpheus XTX
      parked by MR; `deploy/debug/qwen38-27b-xtx-warm-canary.yaml` applied by hand.
- [x] XTX canary applied 2026-09-13 14:38Z, Ready at +6 min (research §7a): 3/3 probes coherent,
      ~34 tok/s, 2.3K-token prompt in 2.57 s, voice stack intact on the VII, cold-start count 1.
- [x] Soak waived by the operator ("move faster"). flexinfer !1024 promoted the XTX lane into
      Flux (labels `mills-warm` / `mills-council` / `xtx-warm`; `mills-council` off the 5930k
      workhorse). !1025 repinned the three dead quantization/abliteration digests and prewarms
      the serving digests on both gfx1100 nodes.
- [x] Slice 2 (gitops !696): council lenses ×3, audit pool ×2, spinning ring/open-end →
      the XTX lane; research/weaver → flexinfer backend on the 5930k workhorse with the XTX lane
      as fallback; judge unchanged. Operator rolled 2026-09-13. Correction !699: Mills must use
      the serving name `qwen38-27b-xtx-warm-canary` (labels 404 in the reviewer client).
- [x] Slice 2 verified on the 18:00Z council (research §7b): three local lenses + audit pool on the XTX
      lane, research weaver on the 5930k workhorse; $0.003 local vs $0.98 frontier for the run.
- [ ] Slice 2 remainder (loom-core code): overseer triage model knob (#754), shadow-judge calibration
      gauge (#755), reranker on (!1945, CI-flaky under the runner storm), HUD coordinator decision.
- [ ] Slice 3: vLLM 0.28+ currency canary → Smaug-Mini A/B, Glimmer on vLLM, Laguna XS 2.1
      requant, Granite 4.2-30B tools, Qwen3-Embedding upgrade.
- [x] Runtime-image CI program (issue #74, spec `20-product-spec-runtime-image-ci-2026-09-13.md`):
      S0 drift gate (0 FAIL on master), repins + prewarm, S1 gfx1100 serving persona (XTX runtime
      pod on `522d0fe2`, multi-model on), S3 gfx906 (`25fce977`, retrieval + voice re-verified)
      all landed 2026-09-13 (research §7c).
- [x] Incident 2026-09-13 16:23Z (spec "Incident" section): three concurrent pipelines filled the
      5930k root and kubelet evicted the Mills workhorse ahead of the CI job pods (no
      ephemeral-storage request = exceeded at the first byte); ~20 min lane outage. !1035 (2Gi
      requests on both Mills lanes + all runtime DaemonSets, `flexinfer-serving-critical` on the
      runtime profiles) merged and rolled; !1036 adds the serving priority class to both Mills
      lanes after the XTX pod sat Pending on CPU behind priority-0 CI pods; gitops !703 caps the
      runner ephemeral overwrite ceiling at 25Gi. govulncheck `@latest` → x/vuln v1.8.0 (Go 1.26)
      broke every pipeline; pinned to v1.7.0 in !1033.
- [x] Incident #2 (spec): the S1 runtime pod's entrypoint rewrote the XTX lane's cached
      `config.json` to the text-only class (node-wide `/models` pass); the lane crashed on every
      first request after its next restart (M-RoPE assertion). Restored in place 17:39Z; !1037
      gates the pass in manager mode (rebuild + re-promote pending); `hfOverrides` architectures
      pin on both Mills lanes.
- [x] #74 S4/S5 first half merged (!1032); S1' promoted (!1043): the entrypoint-fixed serving persona
      `f7d2e967` on both gfx1100 profiles, kill-test passed.
- [x] S2 canary FAILED and rolled back (!1039 → !1041): the 08-01 v0.26 native-W4A16 image's bundled
      plugin imports a v0.23 FLA module; matrix row `fail`.
- [x] S3' promoted (!1047): the entrypoint-fixed gfx906 runtime `171081de` on the Radeon VII, kill-test
      passed (retrieval +2.5 min, voice +5 min, cache mtimes untouched). Both priority GPU lanes now run
      images built by CI from master with the incident #2 fix.
- [ ] #74 remaining:
      S2 = rebuilt `ad93ae72` passes isolated 64K/two-request qualification at 0.96:
      3/3 keys at 65,365 tokens in JSON mode, tools, cancellation, and an 82-request soak.
      Evidence: `iteration-runtime-s2-64k-2026-09-14.md`; original smoke in !1058.
      Production pins await target-node validation, longer workload soak, and their actual
      context/LoRA envelopes (128K is not certified). S4 proposals and nightly Trivy shipped;
      Draft-MR creation waits on `FLEXINFER_PROMOTE_MR_TOKEN`. S5 standalone vLLM rebuilt/repinned
      in !1060. Remaining CI work: mutable debug pins and gate → `--strict` after observation.
- [x] Controller defaults (2026-09-14): every dedicated serving pod gets `flexinfer-serving-critical`
      and a 2Gi ephemeral-storage request unless the Model sets its own; `.gitlab-ci.yml` publish jobs
      non-interruptible (!1045). S2 plugin ported to vLLM 0.26 (register() verified at build time);
      rebuilt native-W4A16 digest smoke-tested on the 5930k; full lane qualification remains above.
- [ ] Video lane: LTX-2.5 surveyed (research §6b) — 22B only, zero RDNA3 evidence; canary needs
      operator license acceptance (gated repo), diffusers 0.40 / transformers 5.5 image, and an
      `ltx2` pipeline mode in server-diffusers.py. Kill-test defined in §6b.

## Previous Goal (2026-06-03) - Hardware-utilization arc (retrieval + throughput)

Brainstorm: `brainstorm-hardware-utilization-sprints-2026-06-03.md`.
Plan: `30-implementation-plan-hardware-utilization-2026-06-03.md`.

After the F4 compound + voice stack landed, the planning question stepped up a
level: which silicon is still under-utilized? An 8-framing brainstorm converged
(operator pick 2026-06-03) on the **utilization/throughput** arc — extract more
from the fleet we own rather than chase capability ceiling (70B/multimodal) or
product polish (daily driver). Four sprints, leverage-ordered:

- **Sprint 0 (thin) — fleet utilization instrument [F4].** Per-card util +
  KV-pressure metrics + Grafana dashboard + 24 h baseline snapshot. Closes #28.
  Hard prerequisite so every later claim is metered.
- **Sprint 1 — gfx906 HBM2 retrieval plane [F1], KILL-TEST GATED.** Finish the
  already-stubbed `bge-large-radeonvii` llamacpp lane (the predecessor brainstorm's
  never-built F2): kill-test ≥5× emb/s GPU-resident vs nomic/980 Ti baseline, then
  warm-promote + repoint the `embeddings` alias + add the missing `/v1/rerank` +
  migrate one consumer. HBM2's ~1 TB/s is the clearest wasted silicon.
- **Sprint 2 — idle-time batch appliance [F3].** Nightly re-embed/index + offline
  eval gauntlet + prefix prewarm on the now-cheap embedding plane (#26/#27/#34).
- **Sprint 3 — fleet throughput replication [F2].** Near-free: replicate the
  proven 2× n-gram SD to the 5930k twin + qwen35, tune params, find the
  `maxNumSeqs` batch-depth knee. Pull-forward if concurrent demand appears.

Riskiest assumption + kill-test live in the brainstorm doc. Grounding correction:
the current embeddings baseline is nomic on the **GTX 980 Ti (ollama)**, not CPU
as the 2026-05-25 brainstorm assumed.

## Previous Goal (2026-06-01) - F4 agent-loop ReAct client (slice 1: CLI)

Focused plan: `ralph-f4-agent-loop-client-2026-06-01.md`.

The kill-test PASS (below) cleared the spec-riskiest-assumption gate, so the
F4 ReAct **client** is unblocked. Operator picked client forms **(a) CLI +
(b) loom-core MCP tool**, scope **loop + real tool execution**. Because (a)
and (b) are separate modules/repos, RALPH ships them as two vertical slices.

**Slice 1 (this slice) — CLI `cmd/agent-loop/` on a reusable
`internal/agentloop/` engine.** The engine enforces the cache-paying
invariant in the type system: `Conversation` is append-only and
mutability-ordered (immutable system+tools prefix; history grows by `Append`
only — no Insert/Replace/Reorder). `ChatClient` pins
`X-Flexinfer-Cache-Key=session_id` for prefix-consistent routing; per-turn
`TurnMetrics` parse the proxy headers the kill-test used; a `Budget` guard
encodes the usable-context bound (`maxModelLen − system − output`) row 194
surfaced and stops the loop cleanly before HTTP 400 (the F4-413-as-feature
affordance, in-process). The CLI runs REAL read-only, path-jailed tools
(`read_file`, `list_dir`) in a ReAct loop, with an offline `--self-check`.
Offline-validated locally (build/vet/test/-race/gofmt/self-check green);
**live-validated 2026-06-01 (PASS)** via operator-authorized full canary
preemption: two `bin/agent-loop` sessions vs `gemma4-26b-a4b-gptq-apc-canary`
through `flexinfer-proxy`. The prefill-isolation probe showed `prompt_tokens`
growing **22.5×** (247→5565) while `upstream_ms` tracked the per-round token
*delta* (526–701ms for small-delta rounds; only large-file rounds rose) — the
prefix cache absorbing the immutable prefix, observed client-side, matching
row 194's engine-side finding. Caveat: gemma4 omits `cached_tokens`, so the
hit is inferred from delta-bound latency (follow-up: prefill-only/TTFT header).
Production restored cleanly (primary Ready+Active). **Matrix row 195 → `pass`.**

**Slice 2 (SHIPPED 2026-06-01 — loom-core MR !585):** the same engine shape
exposed as an MCP tool. `pkg/agentloop` (ported from `internal/agentloop` —
different Go modules, so layout mirrored not imported) + `cmd/mcp-agent-loop`
with two tools: `agent_loop_run` (bounded live ReAct session, per-turn metrics
report) and `agent_loop_self_check` (offline wiring gate, `always_allow`'d).
Wired into `MCP_SERVERS` + Makefile + `registry.yaml`. Verified gofmt/vet/
`go test ./pkg/agentloop` + a live stdio smoke (`agent_loop_self_check` →
passed, all 5 checks). **Both F4 client forms (a)+(b) now delivered.**

The F4 compound is complete: `F4-tool-loop-as-prefix` (kill-test PASS, row 194)
→ client slice 1 CLI (row 195 PASS) → client slice 2 MCP tool (loom-core !585).

**Row-195 prefix-hit follow-up — FULLY CLOSED end-to-end 2026-06-01 (3 MRs
merged):** the "client can't read the hit ratio directly when the engine omits
`cached_tokens`" caveat is gone. (1) Proxy header — flexinfer !535 (`a7740702`):
opt-in `X-Flexinfer-Prefix-Cache-Hit-Rate` (request sets
`X-Flexinfer-Want-Prefix-Hit: 1`), best-effort scrape of the engine `/metrics`,
1.5s-bounded, zero-cost when not opted in. (2) flexinfer CLI client — !536
(`bc65ba23` / `c2781e87`): `ChatClientConfig.WantPrefixHit` sends the opt-in,
`TurnMetrics.PrefixCacheHitRate` parses the response, `cmd/agent-loop
--want-prefix-hit` (default true). (3) loom-core MCP mirror — !597 (`8be7a6bc`):
same in `pkg/agentloop` + `cmd/mcp-agent-loop` (`agent_loop_run want_prefix_hit`
param, default true). Both client forms now report the engine prefix-cache hit
ratio directly even on gemma4. Library defaults stay opt-out (backward-compat);
only the CLI flag + MCP param default on. **F4-tool-loop-as-prefix milestone
(kill-test + both client forms + row-195 follow-up) is COMPLETE.**

Remaining F4 open item: `F4-prefix-cache-flip` canary promotion is still
`conditional` (row 193) — the genuine next candidate for an F4 RALPH loop.

## Previous Goal (2026-06-01) - F4-tool-loop-as-prefix kill-test

Focused plan: `ralph-f4-tool-loop-as-prefix-2026-06-01.md`.

Second leg of the F4 compound. The canary slice (below) returned a
**conditional** verdict on 2026-05-28: APC survives the *alternating
two-prefix* (multi-tenant) eviction pattern at `maxModelLen ≤ 20480`
(hit_rate 0.666, TTFT decay 17-24×) but is structurally infeasible at
32k FP8 KV on the 22 GB cap. This slice ships the kill-test for the
**distinct, unproven** *append-only growing* pattern
(`F4-tool-loop-as-prefix`): an immutable `system + tool-schema` prefix
followed by an append-only `(user → assistant → tool-result)` history,
re-sent in full each round. Runner `scripts/f4-tool-loop-as-prefix.py`
(schema `flexinfer.f4_tool_loop_as_prefix.v1`) with an offline
`--self-check`.

**RESOLVED — live kill-test PASSED 2026-06-01.** Ran on the canary
(`flux suspend` → force-promote at `maxModelLen 20480` → 16 tuned rounds →
restore + `flux resume`; production primary preempted ~7 min, reclaimed Ready).
Two independent signals confirmed the assumption: TTFT-flatness ratio **1.42**
(≤ 1.5) despite **2.94× prompt growth**, and engine `/metrics` prefix-cache
block hit rate **93.0%** over the run (the gemma4 engine omits `cached_tokens`,
so the fallback path was taken as designed). Matrix row 194 → `promote`.
Operational bound surfaced: at a 6k system prefix the append-only context
exceeds `maxModelLen 20480` by round 12 (HTTP 400) — usable budget =
`maxModelLen − system − output`, which is the `F4-413-as-feature` leg's domain.

**Next slice (unblocked by the PASS, per spec-riskiest-assumption rule): build
the ReAct client.** Open question from the brainstorm — pick the client form:
(a) CLI `cmd/agent-loop/`, (b) MCP tool loom-core hosts, (c) Open WebUI plugin;
recommendation leans (a). Append-only tool history is proven near-free per turn.

## Previous Goal (2026-05-26) - F4-prefix-cache-flip canary

Focused plan: `ralph-f4-prefix-cache-flip-canary-2026-05-26.md`.

After F4 decode-tail kill-test PASS on 2026-05-25 (decode flat 50-67 tok/s
across 2k→28k context — F4 "feels instant" structurally viable), the
recommended F4 first slice from
`brainstorm-f4-long-context-agent-2026-05-25.md` was unblocked. MR !519
landed the side-by-side `gemma4-26b-a4b-gptq-apc-canary` Model that
mirrors the warm primary except for `enablePrefixCaching: true` and
`gpuMemoryUtilization: "0.94"`. **Live verdict 2026-05-28: conditional**
— APC passes eviction-thrash at `maxModelLen ≤ 20480`, fails to load at
32k. See `60-validation-matrix.md` row 193.

## Previous Goal (2026-05-21) - Roadmap unblock plan

Focused plan: `roadmap-unblock-plan-2026-05-21.md`.

The current execution posture is to stop feature churn by ordering the open
work around explicit runtime kill-tests and validation-matrix evidence:

- Lane 0: link and freeze the roadmap-unblock context.
- Lane 1: unblock or retire gfx906 production fallback through the llama.cpp
  `hipMemGetInfo` kill-test before soak or alias promotion.
- Lane 2: close runtime-promotion evidence gaps for gfx1100/gfx906 canaries.
- Lane 3: run live deploy/swap observability validation for the proof-complete
  PR-2 surfaces.
- Lane 4: spec and implement a reporting-only context-curve benchmark MVP.
- Lane 5: resume major dependency/base-image rollout only as one image family
  per MR with rollback evidence.

Primary blocker: the gfx906 llama.cpp pre-soak probe currently fails
`hipMemGetInfo=1:invalid argument`, so downstream radeonvii alias promotion is
blocked until that image-level compatibility issue is fixed and recorded.

## Previous Goal (2026-05-14, late evening) - F1+F7-vectorize shipped: -24.9% cumulative, 5930k gap closed from 2.13x -> 1.60x

Brainstorm session (`.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md`) produced 8 framings for closing the 5930k decode-rate gap without hardware swap. User picked F1, then F2 (falsified), then F7 (profile first), then vectorize the MoE patch inner loop. Four slices attempted on 2026-05-14.

**Status:**

| Slice | Mean req time | Δ vs prev | Cum gain |
| --- | --- | --- | --- |
| Baseline (Flux-managed config) | 48.89 s | — | — |
| F1 (CPU governor `schedutil` → `performance`) | 47.01 s | −3.8% | −3.8% |
| F7 sync-hoist (MR !361, `runtime:...-moe-patched-fast`) | 45.87 s | −2.4% | −6.2% |
| F2 (revisit `enforce_eager`) | — | n/a | **falsified** (HIP stream-capture crash, manifest comment still binding) |
| F7 vectorize (MR !363, `runtime:...-moe-vectorized`) | **36.70 s** | **−20.0%** | **−24.9%** |

**5930k went from 2.13x slower than 7900xtx → 1.60x slower.** The gap closed by ~25% with no hardware change.

MR !363 (vectorize) replaced the top_k per-slot inner loop in the GEMMA4_MOE_ROCM_REFERENCE_PATCH with two `torch.bmm` calls per token: 16 small matmul launches per layer per token → 2. PCIe roundtrip savings dominate the gain on the older X99 platform. Coherence gauntlet (6 prompts at temperature=0, golden = 7900xtx output): **5/6 exact-match**. The 6th (haiku) diverges at line 3 due to expected FP16 reduction non-associativity (vectorized `sum(dim=0)` parallel reduce vs the original sequential accumulator); output is still a valid 5-7-5 haiku. Factual outputs are bit-identical. Image registry digest: `sha256:c2c89b330c3f414e23b75f468d94b1d80b512a8d539951645c6971446adf77a1`.

**What's left in the gap (1.60x → 1.0x = ~14 s remaining):** likely memory bandwidth + residual per-token Python overhead (cache lookups + stacking + remaining per-layer small launches we still do). Closing further requires either (a) pre-dequantizing all experts at startup (memory cost: 46 GB total, doesn't fit on 24 GB GPU), (b) writing a proper Triton/HIP kernel for INT4 + GELU MoE on ROCm (real upstream engineering, months), or (c) hardware. Returns diminish sharply from here.

**Remaining options, listed for explicit user direction:**

- [ ] **(Pin 7900xtx to vectorized image)** After 24 h of clean 5930k canary, pin the 7900xtx Model to the same `sha256:c2c89b330c3f...` digest. Same patch benefits it proportionally (likely 5-15% on the already-fast lane; the per-token launch overhead matters less but is still real). Trivial follow-up MR; one-line digest swap in `deploy/models/gemma4-26b-a4b-gptq.yaml`. No correctness exposure — patch already proven on sister.
- [ ] **(F5) Workload-shaped routing.** Route short-output requests to 5930k, long-output to 7900xtx. Proxy code change in `internal/proxy/proxy.go`. Routes around the residual gap; doesn't fix it.
- [ ] **(F6) llama.cpp on 5930k.** Highest potential lift; 12-24 h GGUF re-quant + feature divergence (different tool/reasoning parsers, separate operational paths).
- [ ] **(Demote 5930k)** Set `minReplicas: 0` + lower priority. Loses parallel capacity; eliminates the slow-path tax on routine traffic.
- [ ] **(Accept)** Status-quo + documented. The 1.60x gap is operationally usable for current workload volumes. F5 (cheap routing tweak) is the natural next step if user-visible latency becomes a complaint.

Detailed evidence in `.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md` (execution log), `.loom/60-validation-matrix.md`, and `.loom/50-worklog.md` 2026-05-14 entries.

## Previously queued follow-ups (lower priority)

- [ ] **(Optional) Increase per-upstream concurrency.** Both 26B Models run `maxNumSeqs: 1`. A 100-req `-P 20` probe saw 42% HTTP=000 timeouts — purely upstream queue saturation, not routing. Raising `maxNumSeqs` on both Model CRs would scale fleet throughput beyond 2 concurrent reqs but trade per-token latency. Worth re-measuring after a real workload shape is known. Likely not urgent until project-management (or another downstream caller) flips `ICC_LLM_ENABLED=1` and produces sustained concurrency.
- [x] **Document/wire service-side consumption pattern** — closed by services/project-management MR !73 (2026-05-14). `llm_qwen.py` defaults now point at `flexinfer-proxy` + the `project-mgmt` alias; new `FLEXINFER_QWEN_MODEL` env override mirrors the existing `FLEXINFER_QWEN_URL` pattern; rollback recipe documented in ICC's `.loom/40-decisions.md`. Cluster validation captured in flexinfer `.loom/50-worklog.md` 2026-05-14 entry — proxy round-tripped an ICC-shaped extraction request through `project-mgmt → gemma4-26b-a4b-gptq-5930k` and returned valid JSON matching ICC's `_validate_response` schema. ICC overlay still needs `ICC_LLM_ENABLED=1` to actually enable extraction, but that's a deployment-side toggle independent of code.
- [ ] **(Optional) Migrate `--log-level=debug` toggle to Helm values.** The 2026-05-14 debugging cycle required `kubectl patch` to add `--log-level=debug` (Flux didn't revert during the window, but the path is fragile). A first-class `proxy.logLevel` Helm value would make future debugging cycles a one-liner.

## Previous Goal (2026-05-14, late afternoon, closed)

Close the concurrent-load failure mode in shared-service-label routing. **Closed by MR !356.** The 26% failure rate observed at parallelism 10 was NOT a cache-refresh race in the picker — `slog.Debug("forwarding to upstream", ...)` logs (MR !355) with `--log-level=debug` revealed `getRoutingStrategy` was auto-defaulting to `StrategyLeastLoaded` for any label-group member, and `refreshEndpoints`' aggregation wrote the union of all members' pod endpoints into each member's router ring, cross-routing bodies (5930k body → 7900xtx pod → 404).

- [x] Reproduce: 50-req `xargs -P 10` probe → 13/50 vLLM 404s with both directions of mis-routing.
- [x] Root-cause from the new forwarding log: `target=10.42.0.7:8000` (7900xtx pod) for `model=gemma4-26b-a4b-gptq-5930k`.
- [x] Fix: removed the two `isModelInLabelGroup` auto-default branches in `internal/proxy/routing.go:getRoutingStrategy`. Picker (MR !354) now owns cross-model selection; router branch stays dormant unless an operator explicitly opts in via `flexinfer.ai/routing`. Aggregation in `refreshEndpoints` preserved for that explicit case.
- [x] Test: renamed `TestGetRoutingStrategy_LabelGroup_DefaultsToLeastLoaded` → `_StaysDefault` with inverted assertion and lock-in comment.
- [x] Prove: 20 reqs at parallelism 2 → 20/20 success, exact 10/10 split, 16/16 forwarding logs show model-name matching target (0 mismatches). At parallelism 10/20, HTTP=000 failures persist but are upstream queue saturation (`maxNumSeqs: 1`), not routing.
- [x] Validation matrix row added (MR !356 row in `60-validation-matrix.md`).

## Previous Goal (2026-05-13/14 afternoon, closed)

Make the two-instance 26B fleet load-balance across both Ready backends. Closed 2026-05-14 with MR !354 — `internal/proxy/proxy.go:409` now calls `ResolveServiceLabelGroup` + `pickReadyMember` (round-robin among Ready, alphabetical fallback when none Ready).

- [x] Pick a routing policy: round-robin among Ready members. Rationale captured in MR !354 description and `.loom/50-worklog.md` 2026-05-14 entry.
- [x] Implement `ResolveServiceLabelGroup` (`internal/proxy/model_resolver.go`) + `pickReadyMember` (`internal/proxy/resolver.go`). Per-label `atomic.Uint64` counter on `Proxy.labelRRCounters`. Sorted claimants for stable round-robin across cache refreshes. 5 unit tests in `internal/proxy/pick_member_test.go`.
- [x] Prove load-balancing: 20-request probe through `quality-chat` at 0.5 s spacing splits **exactly 10/10** across the two instances. Evidence in `60-validation-matrix.md` row "Proxy round-robin Ready-member routing across shared service-labels".
- [x] Drop "aspirational" caveat from both 26B Model CR comments.
- [x] Validation matrix row updated.

## Previous Goal (2026-05-13/14, closed)

Promote `gemma4-26b-a4b-gptq` to the warm quality lane on `cblevins-7900xtx` so downstream services consume capable reasoning + 16K context via stable aliases (`quality-chat`, `mid-chat`, `gpt-4`, `project-mgmt`). Pair-demote `qwen3-8b-fast-7900xtx` to scale-to-zero so the 24 GiB lane is not double-claimed. Extended on 2026-05-13 to a two-instance fleet (sister `gemma4-26b-a4b-gptq-5930k` on `cblevins-5930k` via OCI pull). Closed 2026-05-14 with MR !352.

- [x] Land the manifest swap (priority 350 / minReplicas 1 / warmPolicy primary on 26B; mirror demotion on 8B) — MR !343.
- [x] Pipeline green + Flux reconcile shows `gemma4-26b-a4b-gptq` Ready and `qwen3-8b-fast-7900xtx` Idle — 2026-05-13.
- [x] Validation matrix row for the warm 26B canary updated with first served request evidence — `60-validation-matrix.md` row 120.
- [x] Two-instance fleet shipped (MR !345 fleet-reshape) and OCI artifact seeded (MR !350) so the sister Model can pull without re-running the 12-24 h pipeline.
- [x] Sister instance source-path mismatch fixed — MR !352. Both instances Ready, direct smoke passes on each, shared `service_labels` identical, node-specific `litellm.aliases` set. Evidence in `60-validation-matrix.md` row "gemma4-26b-a4b-gptq-5930k sister instance via OCI pull".
- [ ] (Deferred) Document service-side consumption pattern (services point at `quality-chat` or `project-mgmt` alias) — out-of-scope for the fleet build; depends on the load-balancing slice above before downstream services can rely on shared-alias capacity.

## Previous Goal (2026-05-06, round 1 closed)

Decompose the remaining `gfx1100`/`gfx906` work into eight tracks (A-H) sized for parallel sub-agent execution.

Round 1 status (first wave: A, E, F, H):

- [x] **Track F** — runtime profile generation decision (consistency-test-only). MR !273 merged.
- [x] **Track E** — validation matrix schema rotation. MR !274 merged.
- [x] **Track A** — GPUProfile contract slice (`ResolveBackendImage` helper). MR (`feat/gpuprofile-contract-slice`) merged via `6ba66e06`.
- [x] **Track H** — qwen36-27b-gptq coherence triage. Investigation report at `.loom/local/qwen36-coherence-triage.md`; matrix pointer added; concrete one-line fix queued at `deploy/modelcaches/qwen36-27b-gptq-gfx1100.yaml:87`.

Held for round 2:

- [ ] **Track B** — gfx906 disk-pressure unblock (operator pairs).
- [ ] **Track C** — gfx906 vLLM revive-or-retire (coordinate with `backlog/31-vllm-gfx906-build`).
- [ ] **Track D** — gfx1100 capability push (qwen36 dynamic-exclusion fix from H, 26B-long KV ceiling, FLUX warmups).
- [ ] **Track G** — fast-chat resilience after 5930k MLC fallback removal.

## Previous Goal (2026-05-06)

Plan the next full-platform feature round for AMD ROCm `gfx1100` and `gfx906`, spanning GPUProfile contracts, runtime image promotion, backend capability gates, validation evidence, and operator workflows.

- [x] Refresh `.loom/00-workspace-snapshot.md`.
- [x] Re-check Loom resource inventory and codebase index readiness.
- [x] Capture source-backed facts for `gfx1100` and `gfx906` runtime/profile behavior.
- [x] Add focused spec and implementation-plan artifacts for the next round.
- [x] Convert accepted slices into tracking issues/MRs (Tracks A, E, F, H first-wave shipped).

## Previous Goal (2026-05-03)

Close the remaining Gemma4/Qwen/TurboQuant gfx1100 queue through spec-driven gates. Keep closure evidence in `60-validation-matrix.md`, with runtime posture summarized in `gemma4-26b-31b-gptq-turboquant-plan.md` and `docs/dev/gemma4-rocm-status.md`.

Spec-driven delivery contracts SD-1 through SD-5 are now complete in tracked
planning docs. Future planning changes should use
`../docs/planning/roadmap-reconciliation.md` after merge so `ROADMAP.md`,
`docs/planning/next-roadmap.md`, `.loom/00-index.md`, and GitLab issues remain
aligned.

- [x] Slice A - 26B guardrails: long-canary dGPU selector is safe; 16K FP8-KV is the validated fallback posture and 22K remains partial/non-primary.
- [ ] Slice B - `26B-dense-rerun-gate`: dense-validated rebuild reached only harmful prompt 80/128 before the 4h abliteration deadline and still needs a rerun with the longer timeout plus cosine/runtime evidence.
- [x] Slice C - 31B rebuild/recovery: immediate GPTQ lane is back on the clean `keqv` artifact from !193/!194, Ready/Active at `maxModelLen: 2048`, and direct smoke returned HTTP 200 with answer `4`.
- [x] Slice D - 31B production posture: 31B is the primary warm model at the validated 2048 ceiling with `minReplicas: 1`, `priority: 250`, `gpu.count: 2`, and `warmPolicy: primary`.
- [ ] Slice E - `E4B-turboquant-runtime-probe`: primitive sharing is implemented and patch-idempotence tested, but E4B/31B/26B runtime canaries are still pending a digest-pinned built runtime image.
- [ ] Slice F - `31B-long-turboquant-posture-gate`: keep 31B long/TurboQuant disabled until E4B passes and the 31B canary reaches KV sizing without the previous plugin allocation OOM.
- [ ] Slice G - `Qwen35-9B-gfx1100-validation-gate`: staged manifest remains disabled; run the full gfx1100 ModelCache/validator/smoke path before promotion or retiring the gfx906 artifact.

See `gemma4-26b-31b-gptq-turboquant-plan.md`, `60-validation-matrix.md`, and `../docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md` for slice detail, acceptance gates, and evidence targets.

Historical baseline goals (completed):

- [x] Refresh `.loom/` templates and regenerate workspace snapshot.
- [x] Capture current MCP/runtime inventory with tool counts and constraints.
- [x] Validate `codebase_memory` indexing/search readiness and document blockers.
- [x] Produce an evidence-backed research/spec/plan set for next execution.

## Current State (2026-02-20)

- FlexInfer architecture baseline remains six cooperating executables (`agent`, `bench`, `manager`, `sched`, `global-proxy`, metrics embedded) and is documented in `AGENTS.md`.
- Workspace is on `master` at `d01972d` after MR !40 merge (`fad43a7`). Cold-start reliability fixes and dependency batches are all on `master`.
- MCP inventory is available through `loom` CLI fallback (`42` servers, `445` tools) because direct MCP resource listing returned empty sets.
- `codebase_memory` indexing is operational via `loom tools call` after collection repair + binary rebuild (`total_chunks=1877`).

### Cluster Model Fleet (2026-02-20)

| Model | Backend | Phase | GPU Pool | Serverless | Notes |
|-------|---------|-------|----------|------------|-------|
| `nomic-embed-text` | ollama | Ready | — | idle 30m | Embedding model, always warm |
| `qwen3-30b-a3b-abliterated` | llamacpp | Idle | amd-gpu-pool | idle 30m, cold 25m | MoE 18.7GB GGUF, local-path NVMe cache, 108 tok/s gen |
| `qwen3-8b-fast` | mlc-llm | Idle | 5930k-models | idle 5m, cold 10m | Dense 8B, NFS cache |
| `sdxl-turbo-imagegen` | diffusers | Ready | 7900xtx-image | idle 10m | Image generation, ROCm |

- Controller and proxy images deployed with cold-start fixes (Loading phase guard, conflict retry, GPUGroup per-model timeout).
- Proxy timeouts: `queue=25m`, `coldStart=25m` (via Helm values).

## Open Questions

- What is the preferred fix for `codebase_memory` failures:
  - new Qdrant collection (`CODEBASE_QDRANT_COLLECTION`) with expected vector schema, or
  - server-side point-id generation update to UUID/numeric?
- Should `repo_id=flexinfer` remain canonical, or should this workspace use a namespaced id (for example `services-flexinfer`) to avoid collisions?
- Do we want a "minimum viable MCP set" for planning tasks in this repo to reduce tool selection overhead?

## Risks

- Direct MCP bridge instability may block tool calls even when daemon-side tools are healthy.
- MCP inventory can drift; if not refreshed, plans may rely on unavailable tools.
- Planning docs can drift quickly after merge trains unless reconciliation notes are refreshed alongside backlog updates.

## Sources

- [S1] `AGENTS.md:7`
- [S2] `AGENTS.md:12`
- [S3] `AGENTS.md:13`
- [S4] `AGENTS.md:14`
- [S5] `AGENTS.md:15`
- [S6] `AGENTS.md:16`
- [S7] `.loom/00-workspace-snapshot.md:11`
- [S8] `.loom/00-workspace-snapshot.md:12`
- [C1] `loom servers --json | jq '.servers | length'` -> `42`
- [C2] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'` -> `totalTools=445, totalPages=1`
- [C3] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json` -> `status: done, chunks_total: 1877`
- [C4] `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json` -> `total_chunks: 1877`

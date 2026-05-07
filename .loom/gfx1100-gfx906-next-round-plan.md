# gfx1100/gfx906 Next-Round Plan (Parallel Execution)

Date: 2026-05-06
Owner: planning umbrella for the next AMD platform pass after the 2026-05-06 RALPH cycle.
Predecessor: `gfx1100-gfx906-platform-enhancements-plan.md` (Slices 1, 3-partial, 6-lite landed; Slices 2/4/5 unstarted).

## Goal

Decompose the remaining `gfx1100`/`gfx906` work into **independent, PR-sized tracks that can run in parallel** without merge conflicts, then track each to evidence-backed promotion. The previous plan was a single sequential umbrella; this one is fan-out friendly and acknowledges the worklog deltas since 2026-05-07.

## Why a New Plan vs. Continuing the Old One

Three new events since the last plan invalidate the old "single sequential ladder":

1. **gfx906 runtime DaemonSet is paused** because the digest-pinned runtime fills `cblevins-radeonvii` containerd root to 100% and triggers `DiskPressure`. Mirrored in `deploy/system/values-k3s.yaml:354` (`flexinfer.ai/runtime-paused: "true"`). The RG-4 promotion is technically complete but operationally inert. [WL 2026-05-07]
2. **qwen36-27b-gptq is quarantined**: artifact loads, runtime serves HTTP 200, output is collapsed punctuation; quality lane held canary-only via commit `26de7c1b`. The Qwen3.5/3.6 patch stack needs deeper triage before any gfx1100 vLLM coherence claim is durable. [`60-validation-matrix.md:114`, WL 2026-05-05/06/07]
3. **5930k fast-chat fallback was removed** (`16a634d0`) because the MLC NFS path lacked `mlc-chat-config.json`. There is currently no parallel cold-warm fast-chat lane on the 5930k node. Affects all gfx1100 platform claims that say "predictable backend selection." [`60-validation-matrix.md:222`-`232`]

## Tracks (Run in Parallel)

Each track has a non-overlapping primary file footprint. "Conflicts with" lists tracks that touch the same manifest; merge those serially or use a shared base branch.

### Track A — GPUProfile Contract Hardening (controller-only)

Picks up the prior **Slice 2**. Bounded to Go code + tests + manifests reconciliation; no `build/runtime.yaml` edits.

- Goal: make GPUProfile the single source of truth for arch capability decisions; remove residual hardcoded `gfx1100`/`gfx906` branches in backends.
- Primary files:
  - `api/v1alpha2/gpuprofile_types.go` (lookup fields if needed)
  - `controllers/gpuprofile_controller.go`
  - `controllers/model_backend.go`, `controllers/model_runtime.go`
  - `pkg/quantization/*.go` (env injection precedence)
  - `backend/interface.go:138` (image selection fork)
- Decision points:
  - Should `support: canary` be a first-class enum value or a status annotation? Recommend status annotation (smaller schema delta).
  - Where do per-arch env defaults live: GPUProfile only, or fallback table in code? Recommend GPUProfile-first with fallback only for explicit `nil`.
- Validation: `make manifests`, `go test ./api/v1alpha2/... ./controllers/... ./pkg/quantization/... ./backend/...`, `git diff --check`.
- Rollback: status-only schema changes; revert controller consumers first if churn appears.
- Conflicts with: Track D (GPUProfile digest fields), Track F (if generator emits the API types).
- Sequencing: depends on nothing; can start immediately.

### Track B — gfx906 Disk-Pressure Unblock (infra/runtime-only)

New track. Reverses the operational pause that makes the `gfx906` lane inert.

- Goal: get the `flexinfer-runtime-gfx906` DaemonSet sustainable on `cblevins-radeonvii` so the `dd0a1936...` digest is actually serving.
- Primary files / surfaces:
  - `cblevins-radeonvii` containerd root path (existing fix per memory: symlink to `/mnt/nvme/longhorn/k3s-containerd/containerd`) — verify still in place after recent reboots.
  - `build/Dockerfile.unified-gfx906` (image slim-down candidates: drop unused python wheels, prune calibration data, sccache layer cleanup).
  - `deploy/system/values-k3s.yaml:354` (drop the `runtime-paused` annotation when ready).
  - `deploy/system/runtime-daemonset-overrides/gfx906.yaml` if a separate ephemeral-storage `requests/limits` block is needed.
- Acceptance:
  - Runtime image fits within node ephemeral budget (target: <30 GiB resident image after image-pull, GC-headroom verified).
  - 24h burn-in without `DiskPressure` taint on `cblevins-radeonvii`.
  - SDXL inpaint canary still passes after un-pausing (`60-validation-matrix.md:117`).
- Validation: `kubectl describe node cblevins-radeonvii | grep -A2 Conditions`, `crictl images --digests`, `du -sh /mnt/nvme/longhorn/k3s-containerd/containerd`.
- Rollback: re-apply `flexinfer.ai/runtime-paused: "true"`; revert image change.
- Conflicts with: nothing in code; touches operator-side state.
- Sequencing: independent; gates Track E rows for `gfx906 textgen` and Track C if it ever revives vLLM.

### Track C — gfx906 vLLM Decision (slice 5 closure)

Coordinates with the existing in-flight worktree `.worktrees/backlog-31-vllm-gfx906-build`. Do not double-spawn; pick one.

- Goal: answer "is gfx906 vLLM strategically worth reviving?" with a binary outcome and matching repo state.
- Path A (revive): rebuild `Dockerfile.unified-gfx906` on PyTorch 2.4+ (ROCm 6.4 community wheels exist), validate llama-style smoke, promote a digest, flip GPUProfile `vllm.support: experimental → full`, add a canary row.
- Path B (retire): formally remove vLLM from `build/runtime.yaml:223`-`227` (already disabled) and `deploy/gpuprofiles/gfx906.yaml:33`, delete `examples/v1alpha2/model-vllm-gfx906.yaml`, document the steer-to-llama.cpp/Ollama/diffusers stance in `build/README-gfx906.md`.
- Validation (Path A): `go test ./pkg/quantization/...`, cluster smoke with a small model (Qwen3-1.7B or smaller; Vega20 VRAM is 16 GiB).
- Validation (Path B): `rg -n "vllm" deploy/gpuprofiles/gfx906.yaml build/README-gfx906.md examples/v1alpha2/model-vllm-gfx906.yaml` returns expected non-results; `git diff --check`.
- Rollback: keep the example file under a `disabled/` subtree for one cycle if Path B.
- Conflicts with: Track B (image rebuild bloats disk); coordinate so the disk-pressure fix lands first.
- Sequencing: blocked by Track B if Path A; unblocked otherwise.

### Track D — gfx1100 Capability Push (slice 4)

Picks up the prior **Slice 4** with new evidence baked in.

- Goal: stabilize the RDNA3 lane for the three artifacts currently `pending` or `fail` in the validation matrix:
  - `gemma4-26b-a4b-gptq` long-context KV ceiling (currently fails 32K, observed cap ~8896).
  - `qwen36-27b-gptq` coherence (currently `fail` with flat-logits collapse).
  - FLUX Fill/edit + 1024px warmup canary maintenance.
- Primary files:
  - `build/scripts/vllm_qwen35_patches.py` (logits-collapse triage; suspect: GDN attention core or FLA chunk path).
  - `deploy/modelcaches/gemma4-26b-a4b-gptq-long.yaml` (test 16K before 32K on fp16 KV; record the smaller-context evidence row before promoting).
  - `examples/v1alpha2/diffusers-sdxl-gfx1100.yaml`, FLUX Fill manifest.
  - `.loom/60-validation-matrix.md` rows for the three artifacts above.
- Validation: `go test ./backend/... ./controllers/... ./internal/proxy/...`; cluster smoke with deterministic prompts; record decode/prompt TPS, init time, KV ceiling.
- Rollback: revert GPUProfile runtime/quantizer digest to the previous known-good per `docs/dev/runtime-digest-promotion.md`.
- Conflicts with: Track A (GPUProfile digest fields); coordinate the digest field with Track A's status field if both land in the same MR cycle.
- Sequencing: independent of Track A's API decisions if no `BackendProfile.support` enum changes; otherwise after Track A.

### Track E — Validation Matrix Schema Rotation (docs-only, parallel-safe)

Picks up the prior **Slice 6 tail**. Pure docs change.

- Goal: rotate `.loom/60-validation-matrix.md` from "mostly gfx1100 quantization" to the canonical runtime-promotion table for both arches with the audit columns the spec requires.
- Work:
  - Add columns `gpu_arch`, `runtime_digest`, `backend`, `support_level`, `canary_command`, `ready_seconds`, `cold_load_seconds`, `decode_tps`, `imagegen_seconds`, `gate`, `rollback_digest` (only those that are not redundant; collapse where the existing schema already covers it).
  - Backfill the four required canary rows: gfx1100 textgen, gfx1100 imagegen, gfx906 textgen/quantization, gfx906 imagegen/offload.
  - Link each row to its spec/roadmap item and runtime promotion command.
- Validation: `markdownlint` (if configured), `git diff --check`, manual table render.
- Rollback: revert the docs file.
- Conflicts with: nothing.
- Sequencing: can start immediately and run alongside any other track. Update rows as Tracks A-D produce evidence.

### Track F — `build/runtime.yaml` → GPUProfile Generation (slice 3 tail)

Resolves the open decision: "generate vs. consistency-test" for runtime-profile drift.

- Goal: pick one mechanism and ship it. RG-2 already added a consistency test (`scripts/check-runtime-profile-consistency.sh`); this track decides whether to keep it or replace with generation.
- Recommendation: stay consistency-test only for the next cycle. The cost of a generator (yaml templating, CRD round-trip parity) outweighs the drift risk while only two profiles exist. Reconsider if a third profile lands.
- If generation is selected: extend `scripts/promote-runtime-digest.sh` to render GPUProfile YAML from `build/runtime.yaml` instead of patching it. Add round-trip test.
- Validation: `scripts/test-promote-runtime-digest.sh`; if generator is added, a new round-trip test under `scripts/test-generate-gpuprofile.sh`.
- Rollback: drop the generator; keep the consistency test.
- Conflicts with: Track A if generator emits API types; coordinate the merge order.
- Sequencing: docs/decision first; only ship code if generation is selected.

### Track G — Fast-Chat Resilience on 5930k (operational follow-up)

New track derived from `60-validation-matrix.md:222`-`232`.

- Goal: re-establish a working fast-chat fallback on `cblevins-5930k` so `qwen3-8b-fast-7900xtx` is not single-pointed.
- Work:
  - Decide whether MLC, llama.cpp, or vLLM is the right fallback backend for an 8B-class model on the 5930k node.
  - Stage the model artifact correctly: ensure `mlc-chat-config.json` (or backend equivalent) is present in the chosen storage path before re-enabling the manifest.
  - Re-add the fallback to `deploy/models/kustomization.yaml` only after a direct smoke succeeds.
- Validation: cold-load timing, decode TPS, deterministic chat smoke; record under `.loom/60-validation-matrix.md`.
- Rollback: scale to zero; remove from kustomization.
- Conflicts with: nothing.
- Sequencing: independent.

### Track H — Qwen3.5/3.6 Coherence Triage (deep dive, optional in this cycle)

Investigation track; does not promise a fix.

- Goal: identify why `qwen36-27b-gptq` produces flat-logits punctuation despite passing dequant cosine checks (`60-validation-matrix.md:188`-`195`).
- Hypotheses to test (in order of likelihood):
  1. The Qwen3.5 `text_config.vocab_size` extraction in `pkg/quantization/gptq.go` (commit `5870a6b`) corrupts an embedding alignment for Qwen3.6.
  2. The GDN attention core patch in `build/scripts/vllm_qwen35_patches.py` mis-handles a Qwen3.6 hybrid-layer index.
  3. The abliteration-corrupts-lm_head bug from Qwen3.5 (memory entry "TRUE root cause 2026-04-01") regressed for Qwen3.6 even with `ablitateLmHead: false`.
- Validation: dump first-layer hidden states from a known-good FP16 source vs. the published GPTQ artifact; compute cosine per layer.
- Rollback: keep the model quarantined.
- Conflicts with: Track D (shared file `vllm_qwen35_patches.py`); coordinate.
- Sequencing: independent; can run anytime, but progress here unblocks a row in Track D's validation matrix.

## Track Dependency Graph

```
Track A (controller)  ──┐
Track B (gfx906 disk) ──┼─► Track D (gfx1100 push)  ──► Track E (matrix)
Track C (vllm906)     ──┘                              ▲
Track F (gen vs test) ─────────────────────────────────┤
Track G (fast-chat)   ─────────────────────────────────┤
Track H (qwen coherence) ──────────────────────────────┘
```

- A and B are leaf-independent and start immediately.
- C is gated by B if Path A.
- D shares a file with H; serialize that one merge or use a shared base.
- E updates as A/B/C/D produce evidence.

## Active Worktree Conflicts

`git worktree list` for `/Users/cblevins/workspace/services/flexinfer` shows multiple branches in flight. Coordinate before starting:

| Worktree | Branch | Likely Track Overlap |
|---|---|---|
| `.worktrees/backlog-31-vllm-gfx906-build` | `backlog/31-vllm-gfx906-build` | Track C Path A (vLLM revive) |
| `.worktrees/codex-abliteration-telemetry-heartbeats` | `codex/gfx1100-quantizer-v14-digest` | Track D (quantizer digest) |
| `.worktrees/codex-monitoring-dashboard-dedupe` | `codex/monitoring-dashboard-dedupe` | Track E if dashboards overlap |
| `epic-feistel-42e808` | `feat/quant-observability` | Track E rows |
| `nice-mclean-339861` | `feat/31b-gptq-serve-on-7900xtx` | Track D row for 31B |
| `agitated-moore-88b88e` | `tune/textgen-kv-context` | Track D row for KV ceiling |

Before spawning agents on Tracks C, D, or E, check the worktree branch heads to avoid duplicate work.

## Suggested First-Wave Spawn

Six parallel sub-agents on a single morning, isolated worktrees:

1. **Agent-A**: Track A (controller hardening) — fresh worktree.
2. **Agent-B**: Track B (gfx906 disk-pressure unblock) — touches infra; user pairs.
3. **Agent-E**: Track E (validation matrix schema) — docs-only, fast.
4. **Agent-F**: Track F (decision doc + small follow-up) — docs first.
5. **Agent-G**: Track G (fast-chat resilience) — investigation + small manifest.
6. **Agent-H**: Track H (qwen coherence triage) — investigation only this cycle.

Hold Tracks C and D until A/B converge enough to know whether to bundle GPUProfile schema changes with quantizer digest changes.

## Acceptance Bundle for the Round

The round closes when all of the following are true:

- Tracks A, B, E ship to master.
- Track C ships either Path A (with a passing canary) or Path B (with the cleanup commits).
- Track D delivers at least one of: (a) `qwen36-27b-gptq` flipped from `fail` → `conditional`, (b) `gemma4-26b-a4b-gptq-long` 16K row recorded as `conditional` or `promote`.
- Track F merges either the consistency-test-only decision doc or a generator with round-trip tests.
- Track G has a working fallback row in `.loom/60-validation-matrix.md` or a documented "intentionally no fallback" decision.
- Track H closes with one of: (a) cosine-matrix evidence pointing at a specific patch, (b) a documented "no further triage" decision and the model row stays `fail`.

## Validation Plan (Round-Level)

- Every merged track must add or update a row in `.loom/60-validation-matrix.md`.
- Every track that promotes a runtime digest must run `scripts/check-runtime-profile-consistency.sh` and `scripts/test-promote-runtime-digest.sh`.
- Every track that touches GPUProfile types or controllers must run `make manifests` and the relevant `go test` packages.
- Round-end: regenerate `.loom/00-workspace-snapshot.md` and update `.loom/00-index.md` "Current Goal" to point at the next round.

## Sources

- Predecessor plan: `.loom/gfx1100-gfx906-platform-enhancements-plan.md`
- Predecessor spec: `.loom/gfx1100-gfx906-platform-enhancements-spec.md`
- gfx906 disk-pressure pause: `deploy/system/values-k3s.yaml:354`, `.loom/50-worklog.md:5`-`30`, `.loom/60-validation-matrix.md:273`-`278`
- qwen36-27b-gptq quarantine: `git log --oneline -- deploy/models/`, commit `26de7c1b`, `.loom/60-validation-matrix.md:114`
- 5930k fast-chat removal: commit `16a634d0`, `.loom/60-validation-matrix.md:222`-`232`
- Existing worktrees: `git -C /Users/cblevins/workspace/services/flexinfer worktree list`
- GPUProfile API: `api/v1alpha2/gpuprofile_types.go:24`-`99`
- Backend image selection: `backend/interface.go:138`-`152`
- Runtime YAML profiles: `build/runtime.yaml:20`, `:215`, `:223`-`227`
- Runtime promotion script: `scripts/promote-runtime-digest.sh`
- Spec-driven delivery contract: `docs/planning/spec-driven-delivery.md:22`-`63`

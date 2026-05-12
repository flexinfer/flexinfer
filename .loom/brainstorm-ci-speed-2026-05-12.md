# Brainstorm: CI Speed

**Date**: 2026-05-12
**Triggered by**: The quant publish fast-path MR merged, but master CI stayed blocked on a long `publish_unified_rocm_gfx1100` runtime image build.
**Constraints noted**: Keep the homelab GitOps path working; keep runtime images reproducible; avoid weakening master publish confidence for live GPU nodes; prefer repo-local changes before runner/platform rebuilds.
**Observed failure**: Master pipeline `9006` failed after 7,237 seconds because job `publish_unified_rocm_gfx1100` exceeded its `2h` timeout while still compiling llama.cpp HIP objects (`89/567`) and building `causal-conv1d`.

## Phase 1 - Framings

### F1 - Change-Aware Runtime Publishing

The fastest CI job is the one that never starts. The unified runtime publish job already has `rules:changes`, but `.gitlab-ci.yml` edits trigger it even when the changed CI lines do not affect `build/Dockerfile.runtime`, runtime scripts, or runtime build args. This framing treats runtime publishing as a derived artifact with a narrow input contract: only runtime Dockerfile, runtime config, runtime scripts, and explicitly relevant CI snippets should trigger the huge ROCm build. General CI edits, chart edits, controller edits, and utility-image edits should not.

- **Bet**: Most painful master pipelines are caused by broad trigger surfaces rather than real runtime rebuild requirements.
- **Risk**: A too-narrow rule misses a runtime-affecting CI variable change and leaves GitOps pointed at a stale runtime image.

### F2 - Split the Kitchen-Sink Runtime

The `gfx1100` runtime currently bundles vLLM, llama.cpp, Ollama, diffusers, Steam, bitsandbytes, and quantizer dependencies into one image. That maximizes node convenience but makes any rebuild pay every toll: ROCm apt downloads, llama.cpp HIP compilation, Python package resolution, quantizer installs, and large push time. This framing says the image should be split by operational persona: serving text, image generation, quantization, and optional Steam/headless tooling. The controller can select the right image per backend or workload.

- **Bet**: Operational clarity and build speed improve when each runtime image has fewer backends and fewer invalidation paths.
- **Risk**: More image tags and values increase deployment matrix complexity unless selection is made boring and explicit.

### F3 - Prebuild Heavy Builder Artifacts

The trace shows large time sinks in repeatable substrate work: installing ROCm dev packages and compiling llama.cpp HIP objects. Instead of compiling those during every runtime publish, maintain a small set of prebuilt builder/base images: `llamacpp-builder:gfx1100-b8173`, `ollama-builder:gfx1100-v0.17.4`, and possibly a runtime base that already includes stable Python dependencies. Runtime publish then copies artifacts or layers from those bases and only rebuilds when their pinned versions move.

- **Bet**: The slowest layers change far less often than application code and can be promoted independently.
- **Risk**: Base-image freshness becomes another lifecycle; stale builders can hide CVE or ABI drift until a forced rebuild.

### F4 - Make BuildKit Cache External and Deliberate

Most runtime jobs use `--import-cache type=registry,ref=${REGISTRY}/runtime:...` with `--export-cache type=inline`. Inline cache helps, but it couples cache metadata to the pushed runtime image and can be brittle across tag updates, multi-output pushes, and failed builds. This framing creates explicit cache refs such as `${REGISTRY}/cache/runtime:rocm-gfx1100` using `mode=max`, and separates cache export from production tags. It also avoids rebuilding the same layers for each tag where possible.

- **Bet**: Better cache plumbing turns the huge runtime build from a near-full rebuild into a mostly metadata/push operation.
- **Risk**: Registry cache objects can grow large and need retention rules, or the cache becomes its own storage problem.

### F5 - Move Slow Publish to a Scheduled Promotion Lane

Master could validate code, Helm, CRDs, and small images immediately, while large ROCm runtime images publish in a scheduled or manually approved promotion pipeline. Normal master merges stay fast. Runtime-affecting changes create a visible "runtime publish required" job or artifact, but do not block every master pipeline unless the change actually intends to promote a runtime image.

- **Bet**: The team values fast master feedback more than synchronous runtime image promotion on every applicable merge.
- **Risk**: A runtime-affecting change can merge before the image exists, so deployment automation needs a guard that does not pick nonexistent tags.

### F6 - Build a CI Cost Ledger Before Optimizing

This framing treats the problem as an observability gap first. Add lightweight timing around BuildKit phases, record job duration by layer family, and publish a small CI duration report artifact or metric. The next change then targets the measured top two costs. The immediate candidates are known from logs, but a ledger prevents whack-a-mole work and helps verify that optimizations actually move wall clock.

- **Bet**: A day spent making CI costs visible prevents weeks of optimizing the wrong stage.
- **Risk**: Measurement without a committed follow-up can become a comforting dashboard instead of speed.

### F7 - Runner/BuildKit Capacity Lane

The `resource_group: buildkit-rocm-images` serializes ROCm image jobs, and the current job runs on `k3s-ci` against a central BuildKit. This framing keeps repo behavior mostly unchanged but adds infrastructure capacity: a dedicated heavy-image BuildKit worker with warm disks, larger cache volume, local Harbor adjacency, and maybe separate resource groups for independent architectures. The pipeline remains conceptually simple while the runner stops being the bottleneck.

- **Bet**: The build graph is acceptable, but the current builder and serialization policy are undersized for ROCm images.
- **Risk**: Infrastructure work can mask repo-level waste, and capacity gains disappear if every image remains a kitchen sink.

## Phase 2 - Cross-Pollinations & Tensions

### Combinations

- **F1 + F4**: Narrow runtime triggers first, then make the fewer runtime builds use explicit registry caches. This gives an immediate speed win without changing runtime architecture, and it creates better data for deeper work.
- **F2 + F3**: Split runtime personas and prebuild the shared heavy pieces. This is the structural fix: smaller final images plus stable base layers for the parts that are still expensive.
- **F5 + F6**: Put heavy publishes into a promotion lane, then report promotion duration as a first-class release signal. That keeps master fast while still making slow publish drift visible.

### Tensions

- **F1 vs. F5**: F1 keeps runtime publish synchronous but rarer; F5 moves it out of the master critical path. The real decision is whether master means "all deployable artifacts exist now" or "source is validated now; heavyweight artifacts promote separately."
- **F2 vs. deployment simplicity**: Splitting images speeds builds and reduces blast radius, but it pushes complexity into Helm values, GPUProfile defaults, and controller image selection.
- **F3 vs. supply-chain freshness**: Prebuilt builders are fast because they are stable; security posture wants regular rebuilds. This needs scheduled base refreshes, not ad hoc pinning forever.

## Phase 3 - Convergence

### Recommended: F1 + F4

Start with a narrow, low-risk loop: make `publish_unified_rocm_gfx1100` trigger only when runtime inputs change, and improve its BuildKit cache export/import to an explicit registry cache ref. This fits the current pain because the latest MR edited CI and utility-image behavior, yet master still paid for the giant runtime publish. It also preserves the existing deployment model while reducing accidental rebuilds and making intentional runtime rebuilds more cache-friendly.

Concrete next slice:
- Extract runtime build arguments into `build/runtime.yaml` or a generated CI include so `.gitlab-ci.yml` edits unrelated to runtime do not invalidate the runtime publish rules.
- Remove broad `.gitlab-ci.yml` from the runtime job `changes:` list, or replace it with a narrower include path if GitLab config is split.
- Add `--import-cache type=registry,ref=${REGISTRY}/cache/runtime:rocm-gfx1100` and `--export-cache type=registry,ref=${REGISTRY}/cache/runtime:rocm-gfx1100,mode=max` to the runtime publish job.
- Keep a manual web/API escape hatch for forced runtime rebuilds.
- Treat increasing the timeout as an emergency unblocker only. The trace shows the job timed out before the expensive build was close to done, so a larger timeout hides the problem unless paired with trigger/cache or image-split work.

### Runner-up: F2 + F3

If runtime publish still dominates after trigger and cache tightening, split the unified runtime. The first split should be "serving runtime" versus "utility/quantizer/runtime extras", because the repo has already started moving publish/validation into `model-tools`. A second pass can isolate llama.cpp and Steam from vLLM/diffusers if live workloads do not need them in the same image. This is higher leverage but touches controller defaults, Helm values, and GitOps rollout assumptions, so it is the better second phase.

### Open question

Should a master pipeline be considered incomplete until heavyweight runtime images are published, or is it acceptable for runtime images to promote on a separate scheduled/manual lane as long as GitOps pins existing digests until promotion succeeds?

## Handoff

- If chosen -> next step is: `small-change-loop` for trigger/cache tightening, then `feature-dev` for runtime image splitting.
- Linked spec/plan doc: `.loom/ci-runtime-publish-fast-iteration-2026-05-12.md`

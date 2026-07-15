# gfx1100 video-generation latency optimization

Date: 2026-07-14
Target: `Wan-AI/Wan2.1-T2V-1.3B-Diffusers` on one RX 7900 XTX (`gfx1100`)
Baseline: 832x480, 33 frames, 16 fps, 20 steps, seed 42 in 6m57s

## Riskiest assumption + kill-test

**Load-bearing assumption**: at least 70% of the baseline's roughly 296-second
post-denoise tail is spent in `AutoencoderKLWan.decode`, and a targeted decode
change can reduce matched end-to-end latency by at least 25% without changing
the seed/request envelope, corrupting the MP4, or causing an OOM/restart.

**Kill test**: add monotonic timings around the last denoising callback,
pipeline return, MP4 export, and base64 encoding; build and pin a probe image;
then repeat the exact 832x480/33-frame/20-step/seed-42 request on
`cblevins-7900xtx`. The assumption passes only if decode/postprocessing is at
least 70% of the measured post-denoise time. The optimization passes only if a
single targeted follow-up probe is at least 25% faster end-to-end, returns
HTTP 200, decodes to a 33-frame 832x480 H.264 MP4, shows coherent motion, and
the pod records no OOM or restart.

**Failure mode if the assumption is wrong**: optimizing or replacing the VAE
path would add complexity while the actual bottleneck remains denoising,
offload transfer, cold activation, or MP4 serialization. Stop and redirect the
next probe to the largest measured phase instead of shipping a VAE change.

**Status**: failed as stated. The matched warm baseline completed in 142.180s:
125.493s denoising (88.3%), 14.593s decode/postprocess, and 2.094s MP4
export. Decode is only 10.3% of generation time, so the optimization target
moved to the measured transformer denoising phase.

## Source-backed constraints

Positive evidence:

- Diffusers' Wan guide recommends `torch.compile` and caching for repeated
  denoiser calls, and documents group offloading as the memory/speed balance.
- Diffusers 0.36 exposes `callback_on_step_end` and `output_type="latent"` on
  `WanPipeline`, so phase isolation does not require a fork of the pipeline.
- The validated lane used only about 59% observed VRAM, leaving room to test a
  less conservative component placement if timing evidence points there.

Disconfirming evidence:

- Diffusers explicitly states that `AutoencoderKLWan` supports neither VAE
  slicing nor VAE tiling. The production `enableVaeTiling` setting is therefore
  not a valid Wan optimization and must not be credited for memory or speed.
- Upstream warns that CPU offloading can be extremely slow; model-level
  offloading is faster than sequential offloading but still introduces
  component transfers. The current lane uses model-level CPU offload.
- Upstream compile examples target the transformer, not the Wan VAE. A VAE
  compile probe remains experimental and must earn its place through the live
  A/B gate.

Sources:

- https://huggingface.co/docs/diffusers/main/api/pipelines/wan
- https://huggingface.co/docs/diffusers/main/optimization/memory
- https://github.com/huggingface/diffusers/blob/v0.36.0/src/diffusers/pipelines/wan/pipeline_wan.py
- https://github.com/huggingface/diffusers/blob/v0.36.0/src/diffusers/models/autoencoders/autoencoder_kl_wan.py

## Delivery slices

### Slice 1: isolate

- Add structured phase timing without changing inference semantics.
- Build a dedicated probe digest and prove the pod uses it.
- Run the matched baseline and record denoise, decode/postprocess, export, and
  encoding durations.

Exit: the riskiest-assumption kill test has a measured pass/fail result.

### Slice 2: one optimization

- Select exactly one patch point from Slice 1 evidence.
- Build a new immutable digest and run the same request, prompt, and seed.
- Compare latency, VRAM, restart count, ffprobe output, and contact sheets.

Exit: at least 25% end-to-end improvement with the correctness gates intact,
or a documented moved blocker and no production rollout.

### Slice 3: productionize

- Keep only the proven optimization and useful timing telemetry.
- Remove unsupported Wan VAE flags and pin the winning digest.
- Update the runbook, tests, and benchmark evidence; return the Model to zero
  and verify the warm text sibling is Ready/Active.

Exit: local/CI gates pass and the optimized lane is merged without leaving a
mutable probe or altered shared-GPU state.

## Iteration log

### Iteration 1: phase isolation

#### Scope

- Iteration goal: identify the dominant phase in the 6m57s matched baseline.
- Current blocker: progress-bar evidence combines VAE decode, postprocess, MP4
  export, and response encoding into one unexplained tail.
- Hypothesis: Wan VAE decode/postprocess is at least 70% of that tail.

#### Artifact pinning

- Branch: `codex/video-gen-gfx1100-optimize`
- Files touched: `build/server-diffusers.py`, this plan, and research notes
- Build profile: ROCm 6.4.1 / PyTorch 2.6 Diffusers video
- Image tag: `diffusers-video:rocm-gfx1100-profile-20260714`
- Image digest: `sha256:df05475ef044cd9b4963ac942d1cc63c914c5838618d9e48e4d5d1af690b3868`
- Upstream ref/fork: Diffusers 0.36.0, no fork
- Probe manifest: parent `Model` with a temporary pinned probe digest
- Target node: `cblevins-7900xtx`
- Cache/storage path: existing node-local Wan 2.1 cache + tmpfs flash loader
- Model: `Wan-AI/Wan2.1-T2V-1.3B-Diffusers`

#### Change

- Narrow patch point: `generate_video._run_inference` phase timers.
- Why this patch is the minimal test: it observes the existing pipeline call
  without changing scheduler, dtype, offload, request, output, or model weights.

#### Probe

- Commands: direct in-cluster POST to `/v1/videos/generations`; `ffprobe`
- Pod/job: `wan21-t2v-1p3b-gfx1100-57c596f44c-zlx7r`
- Confirmed image ID: `registry.harbor.lan/flexinfer/diffusers-video@sha256:df05475ef044cd9b4963ac942d1cc63c914c5838618d9e48e4d5d1af690b3868`
- Expected success condition: exact phase timings and a valid matched MP4.

#### Result

- Outcome: VAE assumption failed; transformer hypothesis selected.
- Exact evidence: HTTP 200 in 143 wall seconds; structured total 142.180s,
  denoise 125.493s, decode/postprocess 14.593s, export 2.094s, encode
  0.000s. `ffprobe` reports H.264, 832x480, 16 fps, 33 frames, 2.0625s.
- Evidence: `.loom/local/video-gen-gfx1100/wan21-gfx1100-profile-baseline-*`

#### Next

1. Compile the Wan transformer while retaining model CPU offload.
2. Pay the compilation cost once, then repeat the identical hardware request.
3. Compare the warmed compiled request with the 142.180s eager baseline.

### Iteration 2: transformer compilation

#### Scope

- Iteration goal: reduce the 125.493s transformer-dominated denoise phase.
- Hypothesis: `torch.compile(mode="reduce-overhead")` can cut matched warmed
  end-to-end latency by at least 25% while retaining model CPU offload.

#### Artifact pinning

- Image tag: `diffusers-video:rocm-gfx1100-compile-inplace-20260714`
- Image digest: `sha256:f03bfc2fbde17528aec2f95f656630c8a402385a70f591ff52931748a34ff599`
- Target: Wan transformer; fullgraph disabled around Accelerate offload hooks

#### Probe

- `fullgraph=true` failed safely in 6s because Dynamo attempted to capture an
  Accelerate `module.to("cpu")` device operation. The pod did not restart.
- `fullgraph=false` compiled, but `reduce-overhead` failed safely in 54s when
  CUDA-graph output from the first CFG transformer call was overwritten by the
  second call. The pod again did not restart.
- Final compile-mode probe uses `mode="default"` with `fullgraph=false`, which
  keeps graph compilation without reduce-overhead CUDA-graph reuse.
- The wrapped-module default probe completed all 20 steps (steady steps about
  4.18s versus about 6.27s eager) but failed during hook cleanup because the
  `OptimizedModule` wrapper replaced Accelerate's hooked transformer. The final
  implementation uses `nn.Module.compile()` in place to preserve hook identity.
- In-place default-mode runs completed at 107.651s and 107.659s (24.28% faster
  than 142.180s), consistently 1.02s short of the 25% gate. The runtime exposes
  `max-autotune-no-cudagraphs`; one final compiler-only probe will determine
  whether safe kernel autotuning closes the gap.

#### Result

- Outcome: passed. The autotune warm pass took 375.484s and populated the
  compilation cache. The immediately following matched request returned HTTP
  200 in 106.123s: 90.875s denoise, 14.210s decode/postprocess, 1.038s export,
  and 0.001s encode.
- Improvement: 25.36% end to end and 27.59% in denoising versus the measured
  142.180s / 125.493s eager baseline.
- Correctness: H.264, 832x480, 16 fps, 33 frames, 2.0625s; coherent fox motion;
  zero pod restarts and no OOM.
- Evidence: `.loom/local/video-gen-gfx1100/wan21-gfx1100-autotune-optimized-*`
- Operational finding: the flash loader recopies an already-complete 27.6GB
  tmpfs destination and then fails its headroom check. Track destination reuse
  as a separate cold-start optimization; it was not mixed into this A/B.

## Rollback

Reapply the previous eager production digest
`sha256:e0ce46364bf8768ece95955e0c88234ab19dd1214760d3324cc30e58dd651a6f`
on the parent `Model`, restore `minReplicas: 0`, and clear temporary demand so
the warm text sibling reclaims the shared group. Never delete or scale the
generated Deployment directly.

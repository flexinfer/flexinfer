# Plan — FlexInfer ASR + Diarization Infrastructure for ICC Meeting Transcription (7900 XTX)

**Date**: 2026-05-18
**Driver**: integration-command-center round-10 meeting-transcription plan (`.loom/round-10-meeting-transcription.md` in the icc repo)
**Target nodes**:
- **ASR**: `cblevins-7900xtx` (RX 7900 XTX dGPU, gfx1100, 24 GiB VRAM, 62 GiB RAM)
- **Diarization**: `cblevins-radeonvii` (Radeon VII, gfx906, 16 GiB VRAM) — co-resident with FLUX Fill inpainting
**Sharing group**: `7900xtx-textgen` on the gfx1100 node (current owner: `gemma4-26b-a4b-gptq`, priority=350, minReplicas=1, 16 GiB VRAM estimate)
**Estimated effort**: 4–6 days end-to-end (1 day kill-test, 1–2 days Whisper Model CR + serving, 1–2 days pyannote sibling Deployment, 1 day proxy/litellm wiring + smoke)
**Status**: Draft — blocked on Slice 1 kill-test before Slice 2+ proceed

## Goal

Stand up two on-cluster inference endpoints that the ICC service can call to turn an uploaded meeting recording into a diarized transcript:

1. **ASR endpoint**: OpenAI-compatible `/v1/audio/transcriptions` serving `openai/whisper-large-v3-turbo` via vLLM on gfx1100, sharing the 7900 XTX with the warm 26B textgen model under the existing `7900xtx-textgen` GPU sharing group.
2. **Diarization endpoint**: HTTP service serving `pyannote/speaker-diarization-3.1`, deployed as a sibling Kubernetes Deployment (not a Model CR), pinned to **`cblevins-radeonvii` (gfx906)** so it does not contend with the 26B for the 7900 XTX. Gated by a HF-token Secret for the gated pyannote weights. Co-resident with the existing FLUX Fill inpainting Deployment (~10 GiB VRAM at NF4 + cpuOffload); pyannote's ~500 MiB GPU residency during diarize fits in the headroom.

Both endpoints route through `flexinfer-proxy` so ICC sees a single base URL (`http://flexinfer-proxy.flexinfer-system.svc/`) with the same auth surface it already uses for chat/embeddings.

**Why split arches**: pyannote 3.1 is PyTorch-only with no custom CUDA/HIP kernel dependencies, so it runs unmodified on the gfx906 PyTorch stack (`mixa3607/pytorch-gfx906`). Moving it off the 7900 XTX eliminates a contention vector against the warm 26B and avoids stacking three workloads on one GPU. Whisper stays on gfx1100 because the gfx906 vLLM runtime is currently paused (Track B in `.loom/gfx1100-gfx906-next-round-plan.md`) and gfx906 has no FlashAttention kernel — the gfx1100 path is the higher-throughput, lower-friction option. Whisper.cpp HIP on gfx906 remains the documented fallback if the Slice 1 kill-test fails.

## Non-Goals

- **Not** real-time/streaming ASR. ICC's round-10 plan is post-call batch. Whisper is exposed as `/v1/audio/transcriptions` only; `/v1/audio/translations` and any websocket streaming are out of scope this cycle.
- **Not** a new flexinfer backend type for Whisper. Whisper rides the existing `backend: vllm` with `vendor: amd`, gfx1100 GPUProfile, and the `vllm-omni` *or* `vllm` image — whichever the kill-test in Slice 1 proves works for `/v1/audio/transcriptions`. No new `backend/whisper.go`.
- **Not** a new flexinfer backend type for pyannote either. Pyannote is a one-off FastAPI app that doesn't fit the OpenAI-style Backend interface (no `/v1/models`, no token streaming, no shared servedModelName routing). It ships as a hand-written Deployment + Service under `deploy/system/` and `platform/gitops/k3s/ai/flexinfer/`, registered in `flexinfer-proxy` only as a static upstream so ICC has one base URL.
- **Not** voice enrollment / speaker identification (phase 2 in the ICC plan). Anonymous "Speaker N" output only.
- **Not** changes to the ICC service. This plan is flexinfer-side infra only; ICC integration lives in its own round-10 plan.
- **Not** GPU group reshuffling. The 26B stays primary (priority 350); Whisper joins at lower priority and is serverless idle-to-zero by default.

## Users / Operators

- **ICC service** (Python, in-cluster) — calls `POST http://flexinfer-proxy.flexinfer-system.svc/v1/audio/transcriptions` (multipart `file=@recording.wav`, `model=whisper-large-v3-turbo`), then `POST http://flexinfer-proxy.flexinfer-system.svc/diarize` (multipart `file=@recording.wav`), then merges the two outputs server-side.
- **Homelab operator** — promotes via `kubectl apply -f deploy/models/whisper-large-v3-turbo.yaml` → Flux reconcile → smoke against the proxy from a debug pod. Watches `7900xtx-textgen` sharing group behavior under simultaneous 26B-decode + Whisper-transcribe load.
- **FlexInfer maintainer** — owns the GPUProfile `vllm.audioTranscription` capability flag (new) and the runtime image pin for whichever vLLM tag survives the kill-test.

## Riskiest assumption + kill-test

**Load-bearing assumption**: vLLM ≥ 0.19.x (matching the `gfx1100` GPUProfile pin) actually serves `openai/whisper-large-v3-turbo` via OpenAI-compatible `/v1/audio/transcriptions` on ROCm gfx1100 (RDNA3 / RX 7900 XTX), producing usable transcripts under the existing CK-FA-from-base attention backend, **without** requiring a CUDA-only kernel path (Marlin, FlashInfer, etc.) or a vLLM-Omni-only multimodal stack we have not yet validated for audio-in/text-out on gfx1100.

The entire ICC ASR path depends on this. If it's false, ICC either gets a fallback (whisper.cpp HIP build, separate runtime image, no vLLM sharing semantics) or has to ship the recording to a CPU-only Whisper service — both meaningfully different infra plans.

**Kill-test** (≤30 min, run before any other slice in this plan):

1. SSH to a fast-iteration debug pod or run locally against the cluster:
   ```
   kc-k3s
   kubectl run -n flexinfer-system whisper-kill-test --rm -it --restart=Never \
     --image=registry.harbor.lan/flexinfer/vllm@sha256:cb6d92c956ee150b4b8210e625586140e1b5da4c204caa422b1965e953de78e8 \
     --overrides='{"spec":{"nodeSelector":{"kubernetes.io/hostname":"cblevins-7900xtx"},"containers":[{"name":"vllm","image":"registry.harbor.lan/flexinfer/vllm@sha256:cb6d92c956ee150b4b8210e625586140e1b5da4c204caa422b1965e953de78e8","resources":{"limits":{"amd.com/gpu":2}},"env":[{"name":"HIP_VISIBLE_DEVICES","value":"0"},{"name":"HSA_OVERRIDE_GFX_VERSION","value":"11.0.0"}],"command":["bash","-lc"],"args":["vllm serve openai/whisper-large-v3-turbo --host 0.0.0.0 --port 8000 --gpu-memory-utilization 0.30 --max-model-len 448 --enforce-eager --task transcription"]}],"tolerations":[{"key":"dedicated","operator":"Equal","value":"gpu","effect":"NoSchedule"}]}}'
   ```
   - GPU memory pinned to 30% so the 26B serving pod survives (uses 0.98 of its own visible memory, but VRAM accounting is by *total* on this GPU — see Open Question #3).
   - `--task transcription` is the vLLM flag that exposes the audio endpoints.
2. From a second shell: `kubectl -n flexinfer-system port-forward whisper-kill-test 8000:8000`.
3. Grab a known-text wav (e.g. LibriSpeech sample, ~10s of clean speech) and POST it:
   ```
   curl -sS -X POST http://127.0.0.1:8000/v1/audio/transcriptions \
     -F file=@sample.wav \
     -F model=openai/whisper-large-v3-turbo \
     -F response_format=verbose_json
   ```

**Pass condition** (all three required):
- HTTP 200.
- `text` field is a recognizable transcript of the input (English words, not random tokens or empty string).
- Server log shows no fallback warnings like "CUDA-only kernel", "FlashInfer required", or "task=transcription not supported".

**Fail-fast condition** (any one ends the kill-test as FAILED):
- 4xx/5xx response.
- vLLM startup exits with `ValueError: model architecture WhisperForConditionalGeneration is not supported` or similar.
- Engine starts but the audio endpoint returns 501/404 (means transcription task isn't wired in this image).

**Failure mode if assumption is wrong**: ICC cannot use shared-GPU vLLM Whisper. Fallback paths in priority order:
1. **whisper.cpp HIP build** as a dedicated sibling Deployment (no GPU sharing semantics — pin to gfx1100 with `amd.com/gpu: 1`, accept that it preempts 26B during transcribe). Already in MEMORY.md as the explicit ICC-plan fallback.
2. **vLLM-Omni image** (`registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100`, already in the gfx1100 GPUProfile) — same kill-test against the omni image. Confirmed to handle multimodal endpoints; audio-transcription path on gfx1100 is unproven.
3. **CPU-only faster-whisper-server** on a non-GPU node — practical for ICC's batch-after-call cadence (5–10 min for a 30-min call is acceptable), but no GPU savings.

**Status**: not run.

## Current Evidence

- The proxy already accepts multipart bodies. `internal/proxy/resolver.go:158-165` and `:169-200` parse `multipart/form-data` and extract the `model` form field exactly the way OpenAI's `/v1/audio/transcriptions` and `/v1/images/edits` clients send it; the body is buffered and restored before forwarding. **No proxy code changes are required to route `/v1/audio/transcriptions`.**
- The proxy mux registers a catch-all: `mux.HandleFunc("/", p.handleRequest)` at `internal/proxy/proxy.go:343`, so any path the upstream understands is forwarded as-is. New endpoint paths require zero `mux.Handle` additions.
- vLLM is already the default backend for OpenAI-compatible endpoints. The gfx1100 GPUProfile (`deploy/gpuprofiles/gfx1100.yaml:33-48`) pins `vllm` to `registry.harbor.lan/flexinfer/vllm@sha256:cb6d92c956ee150b4b8210e625586140e1b5da4c204caa422b1965e953de78e8` and declares `vllm.v1Engine: supported`, `vllm.flashAttention: ck`. The capability matrix has no `audioTranscription` flag today — Slice 2 adds it.
- The `7900xtx-textgen` sharing group is observable on `gemma4-26b-a4b-gptq` (`deploy/models/gemma4-26b-a4b-gptq.yaml`, `spec.gpu.shared: 7900xtx-textgen`, `priority: 350`, `vramEstimateMB: 16000`). The group's swap-on-priority semantics are implemented in `controllers/model_shared_gpu.go` (52 KB; the canonical entry point for adding a second claimant).
- Multipart-aware `extractModelFromMultipart` is already in production for `/v1/images/edits` (Diffusers backend) — same parser handles Whisper's `model` form field. Verified at `internal/proxy/resolver.go:169-200`.
- `ModelSpec` (v1alpha2) has **no `Env` or `EnvFrom` field** today (`api/v1alpha2/model_types.go:277-358`). HF_TOKEN injection at *serving* time goes through `ModelCache.SecretRef` (`api/v1alpha1/modelcache_types.go:777-779`) which only injects into the *download* job (`controllers/modelcache_shared_pvc.go:693-706`). For pyannote we have to either (a) cache the gated weights to a PVC via ModelCache and load them offline at runtime (preferred — no runtime token surface), or (b) extend `ModelSpec` with `Env`/`EnvFrom` (out-of-scope for this plan; tracked as a follow-up debt item).
- Existing `qwen25-omni-3b.yaml` (`deploy/models/qwen25-omni-3b.yaml`) uses `backend: vllm-omni` for multimodal *input*. Image: `registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100` (vLLM 0.14.0rc0 + vllm-omni 0.14.0, ROCm 7.2, FA disabled). This is the fallback image candidate if standard vLLM 0.19.x fails the kill-test.
- vLLM upstream PR #36594 (merged Sep 2025) added Whisper to the V1 engine; the OpenAI-compatible `/v1/audio/transcriptions` route is registered when `--task transcription` is passed or auto-detected from the model architecture. Confirmed from vLLM docs: https://docs.vllm.ai/en/latest/serving/openai_compatible_server.html (audio endpoints section).
- pyannote/speaker-diarization-3.1 is a gated HuggingFace model; license acceptance is required against the user's HF account and `HF_TOKEN` is needed at first load. Once cached locally (e.g. via `huggingface-cli download pyannote/speaker-diarization-3.1` into a PVC), it can load offline. Source: https://huggingface.co/pyannote/speaker-diarization-3.1 (gated card text).
- The 7900 XTX has **24 GiB total VRAM**. The 26B at `gpuMemoryUtilization=0.98` reserves ~23.5 GiB of its visible slice. With `HIP_VISIBLE_DEVICES=0` on both pods, AMD does NOT do MPS-style hardware partitioning — both processes contend for the same physical VRAM. This is the load-bearing reason the existing 5930k sister model exists for capacity. Adding a Whisper pod here means either (a) capping Whisper at very low `gpu-memory-utilization` and trusting vLLM's allocator to coexist, or (b) swapping the 26B out on demand via the existing `7900xtx-textgen` priority swap. See Open Question #3.

## Requirements

1. **Whisper serves via vLLM on gfx1100 as a Model CR**, with `backend: vllm`, `gpu.shared: 7900xtx-textgen`, priority below the 26B (e.g. 100), `serverless.minReplicas: 0`, `idleTimeout: 5m`. ICC accepts cold-start latency on first transcribe after idle.

2. **GPUProfile gains an `audioTranscription` capability flag** under `vllm.*` (mirrors the existing `v1Engine`, `flashAttention`, `fusedMoETriton` pattern at `deploy/gpuprofiles/gfx1100.yaml:38-44`). Initial value on gfx1100: `experimental` until the kill-test passes; bumped to `supported` after Slice 1 evidence. gfx906: `unsupported` (Vega20 has no FA kernel — even if Whisper loads, perf will be unacceptable, and Whisper-on-gfx906 isn't a target this cycle).

3. **Whisper Model CR uses HF source** (`source: HF://openai/whisper-large-v3-turbo`), not a local PVC. Weights are ~1.5 GiB; HF download via the existing ModelCache shared-PVC pattern is fine, no special handling needed. Use `cache.strategy: SharedPVC`, `cache.storageClass: local-path`, `cache.hostPath: /var/lib/flexinfer/models`, `cache.size: 5Gi` to match the gemma4 SharedPVC pattern.

4. **Pyannote ships as a hand-written sibling Deployment on `cblevins-radeonvii`**, not a Model CR. Reasons:
   - It is not OpenAI-compatible (no `/v1/models`, no `servedModelName`, no token streaming).
   - It does not need the proxy's per-model routing, shared service-label groups, or cold-start activation machinery.
   - The flexinfer Backend interface (`backend/interface.go:191-232`) is shaped around `--host`/`--port`/probes/Image/Args for OpenAI-compatible servers; a one-off FastAPI wrapper for pyannote would force a synthetic Backend implementation that adds no leverage.
   - Sibling Deployment means it does NOT depend on the (currently paused) gfx906 flexinfer-runtime DaemonSet. It ships its own container image and is independently lifecycled.

   The Deployment lives under `deploy/system/pyannote-diarization/` (manifests) and is reconciled into the cluster via the same Flux Kustomization that owns the rest of `flexinfer-system`. The Service is `pyannote-diarization.flexinfer-system.svc`, port 8000. Coexists with the existing FLUX Fill inpainting Deployment on the same node.

5. **HF token for pyannote** is stored in a K8s Secret `flexinfer-hf-token` (key `HF_TOKEN`) — same Secret already used by ModelCache. A one-shot init container in the pyannote Deployment downloads the gated weights into an emptyDir or a small PVC; the main container then loads from the local path with no token in scope. Avoids long-lived runtime token exposure.

6. **flexinfer-proxy gets a static upstream route for pyannote**. The proxy already proxies arbitrary paths via `handleRequest` (`internal/proxy/proxy.go:343`), but its model resolver (`internal/proxy/resolver.go`) keys on the OpenAI `model` field. Pyannote requests have no such field. Options (decide in Slice 3):
   - **Option A (recommended)**: add a hardcoded path prefix `/diarize` that proxy.go matches *before* the catch-all, with the upstream URL pulled from an env var (`FLEXINFER_PYANNOTE_UPSTREAM=http://pyannote-diarization.flexinfer-system.svc:8000`). Minimal code: ~30 LOC in `proxy.go`, no resolver changes, no Backend abstraction needed.
   - **Option B**: ICC calls pyannote directly via its in-cluster Service DNS, bypassing the proxy. Saves the proxy code but splits ICC's "one base URL" property and means pyannote has no shared auth surface.
   - **Option C**: introduce a generic "static upstream" config block in proxy config keyed on path prefix. Better future-proofing, more code. Defer.

7. **ICC needs a single base URL**. Both endpoints reachable as:
   - `POST http://flexinfer-proxy.flexinfer-system.svc/v1/audio/transcriptions` → Whisper Model
   - `POST http://flexinfer-proxy.flexinfer-system.svc/diarize` → pyannote sibling Deployment

8. **Coexist with the 26B** under the `7900xtx-textgen` group. Whisper's priority (100) sits below the 26B's (350), so under the existing shared-GPU swap semantics, the 26B holds the GPU and Whisper is asked to release on conflict. Practical test in Slice 4: with the 26B serving a chat request, fire a Whisper transcribe and confirm either (a) Whisper waits and runs after the 26B yields, or (b) the swap policy unloads the 26B briefly. Tune `swapCooldown` if thrash is observed.

## Architectural Plan

### Slice 1: Kill-test (≤30 min, blocks everything else)

Run the procedure in the Riskiest Assumption section. Two outcomes:

- **PASSED**: capture evidence (curl output, vLLM startup log first 50 lines, GPU memory snapshot from `rocm-smi`) into `.loom/asr-diarization-kill-test-passed-2026-05-18.md`. Proceed to Slice 2.
- **FAILED**: capture evidence, re-run against `registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100`. If that also fails, switch the plan to fallback path: whisper.cpp HIP sibling Deployment. Delete Slices 2/3a from this plan, rewrite Slice 2 to build/pin a whisper.cpp HIP image, and re-plan downstream. **Do not start Slice 2 on a failed assumption.**

### Slice 2: GPUProfile capability flag (no-op rollout, parallel-safe)

Add `audioTranscription` to the typed `vllm` capability block in `deploy/gpuprofiles/gfx1100.yaml:38-44`:

```yaml
backends:
  vllm:
    support: full
    image: registry.harbor.lan/flexinfer/vllm@sha256:cb6d92c956ee150b4b8210e625586140e1b5da4c204caa422b1965e953de78e8
    vllm:
      v1Engine: supported
      piecewiseGraphs: experimental
      flashAttention: ck
      fusedMoETriton: experimental
      fp8KVEmulation: experimental
      marlinINT4: unsupported
      audioTranscription: supported  # NEW (gated on Slice 1 PASSED; otherwise unsupported)
      defaults:
        cudagraphMode: "NONE"
        enforceEager: true
        kvCacheDtype: auto
```

Also add to `deploy/gpuprofiles/gfx906.yaml` as `audioTranscription: unsupported` (Vega20 has no FA; even if it loads, perf is unacceptable and we don't want a Model CR accidentally targeting radeonvii).

Schema change in `api/v1alpha2/gpuprofile_types.go`: add `AudioTranscription string \`json:"audioTranscription,omitempty"\`` to the existing `VLLMCapabilities` struct alongside `V1Engine`, `PiecewiseGraphs`, etc. (Track A in the Wave-1 vLLM-parity spec is touching this struct — coordinate to avoid merge conflicts. The other Wave-1 fields already in flight at `api/v1alpha2/gpuprofile_types.go` are the model.)

`make manifests` + commit both CRD copies (`config/crd/ai.flexinfer_gpuprofiles.yaml` and `charts/flexinfer/crds/ai.flexinfer_gpuprofiles.yaml`).

**Acceptance**: `kubectl explain gpuprofile.spec.backends.vllm.vllm.audioTranscription` shows the new field with its description. No controller logic consumes the field yet; this is a no-op schema rollout.

### Slice 3a: Whisper Model CR (depends on Slice 1 PASSED)

New file `deploy/models/whisper-large-v3-turbo.yaml`:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: whisper-large-v3-turbo
  namespace: flexinfer-system
spec:
  backend: vllm
  # Image inherits from the gfx1100 GPUProfile vllm.image pin (or, if the
  # kill-test required vllm-omni, set image: registry.harbor.lan/flexinfer/vllm-omni:rocm-gfx1100
  # explicitly here and add a comment pointing to the kill-test evidence).
  source: HF://openai/whisper-large-v3-turbo
  cache:
    strategy: SharedPVC
    storageClass: local-path
    hostPath: /var/lib/flexinfer/models
    size: 5Gi
  gpu:
    vendor: amd
    # Same count=2 trick as gemma4-26b — claim both K8s plugin slots so the
    # iGPU can't co-tenant, then hipVisibleDevices=0 pins to the dGPU.
    count: 2
    vramEstimateMB: 4000  # ballpark; refine after Slice 1 captures actual
    shared: 7900xtx-textgen
    priority: 100  # well below the 26B's 350 — 26B holds the GPU on contention
  serverless:
    enabled: true
    minReplicas: 0
    idleTimeout: 5m
    coldStartTimeout: 5m
  config:
    dtype: "half"
    maxModelLen: 448  # Whisper-large-v3 hard ceiling
    gpuMemoryUtilization: "0.30"  # leave room for the 26B sister-process
    enforceEager: true  # gfx1100 default, V1 engine
    trustRemoteCode: false
    servedModelName: whisper-large-v3-turbo
    hipVisibleDevices: "0"
    # Tell vLLM to expose audio endpoints. The CLI flag is --task transcription;
    # the controller's vLLM Args path passes ConfigString-driven args, so add
    # this as a recognized config knob in backend/vllm.go (Slice 3b).
    task: transcription
  nodeSelector:
    kubernetes.io/hostname: cblevins-7900xtx
  resources:
    requests:
      cpu: "1"
      memory: 4Gi
    limits:
      cpu: "2"
      memory: 8Gi
  serviceLabels:
    - whisper
    - whisper-large-v3-turbo
    - asr  # generic alias so ICC can ask for model=asr
  litellm:
    enabled: true
    servedModelName: whisper-large-v3-turbo
    aliases:
      - whisper-large-v3-turbo-7900xtx
```

**Acceptance**: `kubectl apply -f deploy/models/whisper-large-v3-turbo.yaml` reaches `phase=Ready`. From a debug pod: `curl -X POST http://flexinfer-proxy.flexinfer-system.svc/v1/audio/transcriptions -F file=@sample.wav -F model=whisper-large-v3-turbo` returns the expected transcript text.

### Slice 3b: vLLM `--task transcription` wiring (controller code change)

`backend/vllm.go` `Args` (`backend/vllm.go:54+`) currently has no path for `--task`. Add (around line 90 alongside the other `ConfigString` knobs):

```go
// Task explicitly selects the vLLM serving task. "transcription" exposes
// the OpenAI /v1/audio/transcriptions endpoint for Whisper models.
if task := spec.ConfigString("task", ""); task != "" {
    args = append(args, "--task", task)
}
```

Plus unit-test addition in `backend/vllm_test.go` confirming `config: {task: transcription}` yields `--task transcription` in the rendered args. Pattern is identical to the existing `kvCacheDtype` and `attentionBackend` test cases.

**Acceptance**: `go test ./backend -run TestVLLMArgs` passes. The Slice-3a Model CR's rendered Deployment includes `--task transcription` in container args (verifiable with `kubectl get deploy whisper-large-v3-turbo -o yaml | grep -A1 task`).

### Slice 4: Pyannote sibling Deployment on `cblevins-radeonvii` (gfx906)

New directory `deploy/system/pyannote-diarization/` with three files:

- `pvc.yaml` — 5 GiB PVC `pyannote-cache`, storageClass `local-path`, hostPath restricted to `cblevins-radeonvii` (pyannote weights ~500 MiB plus dependencies; conservative size to leave headroom on radeonvii's 98 GiB root FS where FLUX Fill cache already lives at ~55 GiB per MEMORY.md).
- `deployment.yaml` — single replica, nodeSelector `cblevins-radeonvii`, init container that runs `huggingface-cli download pyannote/speaker-diarization-3.1 --revision <pinned-sha> --local-dir /cache/pyannote-3.1` with `HF_TOKEN` from the `flexinfer-hf-token` Secret, main container running a thin FastAPI wrapper. Env: `HSA_OVERRIDE_GFX_VERSION=9.0.6` (gfx906 reports as gfx900 without it — required for PyTorch HIP to dispatch correctly per MEMORY.md).
- `service.yaml` — ClusterIP `pyannote-diarization` port 8000.

Container image: new build `registry.harbor.lan/flexinfer/pyannote-diarization:rocm-gfx906` from `build/Dockerfile.pyannote-rocm-gfx906`. Base: `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` (the community PyTorch that restores gfx906 GPU compute — already validated for flexinfer's gfx906 abliteration/quantization images per MEMORY.md). Python deps: `pyannote.audio==3.1.*`, `fastapi`, `uvicorn[standard]`, `python-multipart`, `soundfile`, `numpy==1.26.*` (pyannote 3.1 pins to numpy 1.x; gfx906 stack already pins 1.26 for transformers compat). Add `pyannote-rocm-gfx906` build target to `build/Makefile` and a new publish lane in `.gitlab-ci.yml` (independent of the paused gfx906 runtime publish lane).

FastAPI surface (`build/scripts/pyannote_server.py`):

```python
@app.post("/diarize")
async def diarize(file: UploadFile, num_speakers: int | None = Form(None)):
    # write to temp wav, run pipeline, return {"segments": [{"start": s, "end": e, "speaker": "SPEAKER_00"}, ...]}
```

Resource pinning: `amd.com/gpu: 1` (radeonvii has a single GPU, no iGPU — the gfx1100 `count: 2` iGPU-blocking trick is unnecessary here). VRAM budget: pyannote loads to ~500 MiB GPU on demand; with FLUX Fill resident at ~10 GiB the 5 GiB headroom is comfortable. No tolerations beyond the existing `dedicated=gpu:NoSchedule` (added automatically by the controller for GPU-claiming workloads — confirm the manual Deployment also tolerates it; `flexinfer.ai/pause` annotation should NOT be set so this pod is not affected by the legacy pause mechanism used for runtime/quantize coordination).

**Co-tenancy with FLUX Fill**: ICC calls Whisper, then pyannote, serially after a recording finishes. FLUX Fill serves image-edit requests independently. Concurrent diarize + image-edit will share the GPU; pyannote's working set is small enough that the failure mode is latency (queue at HIP scheduler), not OOM. Document expected latency degradation in Slice 6 — if FLUX Fill p99 degrades >50% during a diarize, consider serializing via a node-local semaphore (defer to follow-up).

**Acceptance**: From a debug pod, `curl -X POST http://pyannote-diarization.flexinfer-system.svc:8000/diarize -F file=@sample.wav` returns a JSON segments array. Pod survives back-to-back transcribe+diarize cycles (transcribe hits the 7900 XTX, diarize hits the radeonvii — fully independent GPUs). Smoke loop: 10 iterations with FLUX Fill serving an inpaint request in parallel.

### Slice 5: Proxy `/diarize` route

`internal/proxy/proxy.go:340-344` — add before the catch-all `mux.HandleFunc("/", p.handleRequest)`:

```go
mux.HandleFunc("/diarize", p.handlePyannoteProxy)
```

New handler ~30 LOC in `internal/proxy/proxy.go` (or a new `pyannote.go` if separation feels right): single-shot httputil.ReverseProxy with `Director` setting `r.URL = pyannoteUpstream` from an env var `FLEXINFER_PYANNOTE_UPSTREAM` (default empty → returns 503 with `pyannote upstream not configured`). Pass through multipart body unchanged; add the same `X-Forwarded-For` and auth headers the existing handlers add.

The env var is set on the `flexinfer-proxy` Deployment in `deploy/system/proxy/deployment.yaml` (or wherever the proxy Deployment is owned — locate via `kubectl get deploy -n flexinfer-system flexinfer-proxy -o yaml | grep -A2 env`).

**Acceptance**: `curl -X POST http://flexinfer-proxy.flexinfer-system.svc/diarize -F file=@sample.wav` returns the same segments array as Slice-4's direct call.

### Slice 6: End-to-end smoke + behavior observation under load

From a debug pod, run a 5-minute loop:
- Every 20s: fire a chat request against `gemma4-26b-a4b-gptq` (mid-length prompt, expects ~70 tok/s decode).
- Every 60s: fire a Whisper transcribe of a 30s clip against `/v1/audio/transcriptions` (hits 7900 XTX).
- Every 60s: fire a diarize of the same clip against `/diarize` (hits radeonvii).
- Every 90s: fire a FLUX Fill inpaint against radeonvii (existing endpoint) to exercise pyannote co-tenancy.

Capture (per node):
- `rocm-smi` snapshot every 10s on **both** `cblevins-7900xtx` and `cblevins-radeonvii` (memory + utilization).
- Per-request latency histogram for each endpoint.
- Any controller event from `kubectl get events -n flexinfer-system --sort-by=.lastTimestamp` showing GPU swap activity on `7900xtx-textgen`.

**Acceptance** (split by node):
- **7900 XTX**: no GPU OOMs, no `7900xtx-textgen` swap thrash (≤2 swaps over 5 min), 26B decode latency degradation ≤30% during overlapping Whisper transcribe.
- **radeonvii**: no GPU OOMs, FLUX Fill p99 latency degradation ≤50% during overlapping diarize, pyannote returns segments within 2× the standalone (no-FLUX-Fill) baseline.

Document findings in `.loom/asr-diarization-load-test-2026-05-18.md`. If 7900 XTX thrash is observed, tune `swapCooldown` on the Whisper Model CR. If radeonvii contention is worse than tolerable, add a node-local semaphore between diarize and FLUX Fill (defer; flag as a Slice 7 follow-up).

## Open Questions

1. **GPU sharing semantics under `7900xtx-textgen`**: does the existing `controllers/model_shared_gpu.go` priority-swap logic correctly handle a *transient* low-priority claimant (Whisper, called for ~10s every few minutes), or will it cause the 26B to be unloaded and reloaded on each transcribe? If the latter, do we want a different policy (e.g. "low-priority is suspended, not unloaded")? Decide before Slice 6. (Pyannote is no longer in this group — moved to radeonvii — so the contention surface is Whisper-vs-26B only.)

2. **Which vLLM image survives Slice 1?** If standard `flexinfer/vllm@sha256:cb6d92c956...` works, use it (single image, single GPUProfile pin, no special-casing). If only `vllm-omni:rocm-gfx1100` works, the Whisper Model CR pins `image:` explicitly to the omni image — but then we accept that omni's `vllm 0.14.0rc0` is *older* than the textgen pin (0.19.x), which is fine because Whisper is a different model architecture and the textgen behavior is unaffected. **Do not** try to unify on a single image at this stage.

3. **Per-process `gpu-memory-utilization` accounting on AMD**: vLLM's `gpuMemoryUtilization: "0.98"` on the 26B and `"0.30"` on Whisper sum to >100%. On NVIDIA with MIG this would be unsafe; on AMD ROCm with both pods sharing one physical GPU, the allocator is process-local — each pod queries free VRAM at startup and reserves its fraction *of what's currently free*. In practice the 26B starts first, grabs ~23.5 GiB of the 24 GiB; if Whisper then starts, it queries free VRAM, sees ~0.5 GiB, and either OOMs or refuses to load. The `7900xtx-textgen` priority swap is what makes this work — Whisper's load triggers a 26B yield. Confirm in Slice 6. If it doesn't behave, drop Whisper's `gpuMemoryUtilization` to a fraction of *total* via a vLLM CLI flag we control rather than relying on swap, or move Whisper to its own time window via a separate Model CR with `serverless.minReplicas: 0` + cold-start-per-call.

4. **Cold-start latency budget**. Whisper-large-v3-turbo is ~1.5 GiB; from cold (model on PVC, gfx1100 GPUProfile flash-loader available per `flashLoader.enabled` pattern in `qwen25-omni-3b.yaml:36-40`), cold-start should be 30–60s. ICC's round-10 plan assumes batch (5–10 min budget per 30-min call). 60s cold-start on first call after idle is fine. Confirm. If ICC wants warm, change `serverless.minReplicas: 1` and accept the constant 4 GiB VRAM reservation (and the proportional 26B context-length squeeze).

5. **Should `audioTranscription` capability gate the controller**, or is it doc-only? Wave-1 vLLM-parity spec sets the precedent that capability flags are doc-only in the schema slice and consumed by controllers in a follow-up. Match that pattern: Slice 2 ships the schema, Slice 3a Model CR works regardless of the flag's value, controller-level enforcement (refuse a transcription Model CR if `audioTranscription: unsupported`) is a debt item.

6. **HF_TOKEN provisioning for pyannote**. The `flexinfer-hf-token` Secret is assumed to exist with `HF_TOKEN` key. Confirm by `kubectl get secret -n flexinfer-system flexinfer-hf-token -o jsonpath='{.data}'` — if missing, the operator must (a) accept the pyannote/speaker-diarization-3.1 license on huggingface.co with their HF account, (b) create the Secret with `kubectl create secret generic flexinfer-hf-token -n flexinfer-system --from-literal=HF_TOKEN=hf_xxx`. Document this in `docs/operations/pyannote-setup.md`.

7. **pyannote-as-Backend vs sibling Deployment (revisit)**. The plan picks sibling Deployment for the reasons above. If a second non-OpenAI-compatible model shows up (e.g. a TTS service) we should reconsider — at that point the cost of a generic "static upstream" abstraction in the proxy (Option C in Requirement 6) is paid back.

8. **Concurrency on the pyannote Deployment**. Single replica, single FastAPI process. Concurrent requests will queue inside the process. For ICC's call-rate (a few per day at most), this is fine. Add a `concurrencyLimit` middleware or move to `gunicorn -w 1 -k uvicorn.workers.UvicornWorker` if the load profile changes.

9. **Diarization model version**. `pyannote/speaker-diarization-3.1` is current as of the round-10 plan write date. Pin the revision SHA in the init-container download command (`huggingface-cli download pyannote/speaker-diarization-3.1 --revision <sha>`) so a HF push doesn't silently change behavior.

10. **Whisper model variant**: `openai/whisper-large-v3-turbo` is the recommended pick (~6x faster than large-v3 at near-equivalent WER for English-dominant audio). If ICC's recordings are heavily multilingual or contain medical-domain vocabulary, `openai/whisper-large-v3` (non-turbo) may be worth a parallel Model CR. Out of scope for this plan; tracked as a follow-up.

11. **Pyannote co-tenancy with FLUX Fill on radeonvii** (new with the gfx906 placement): both processes will hold GPU contexts on the same 16 GiB Radeon VII. FLUX Fill is `minReplicas=1` at ~10 GiB (NF4 + cpuOffload, per MEMORY.md). Pyannote loads ~500 MiB on demand. Headroom is real but not generous; if FLUX Fill's cpuOffload behavior or pyannote's GPU residency grows on a future model bump, this gets tight. Watch in Slice 6 and flag if either footprint moves.

12. **gfx906 PyTorch base image staleness**: the `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` community image is the validated gfx906 baseline today, but it's a community fork — upstream PyTorch dropped gfx906 in 2.4+, and this image's maintenance cadence is not Anthropic-controlled. If `mixa3607` stops publishing, we either pin to a SHA and stay there, or build our own gfx906 PyTorch (large undertaking, out of scope here). Pin the digest in `build/Dockerfile.pyannote-rocm-gfx906` and record the pin in MEMORY.md.

## Risk Register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| vLLM 0.19.x doesn't serve Whisper transcription on gfx1100 | Medium | High (blocks ASR path) | Slice 1 kill-test before anything else. Fallback to whisper.cpp HIP. |
| 26B + Whisper VRAM contention thrashes under `7900xtx-textgen` swap | Medium | Medium (degrades both endpoints) | Slice 6 load test. Tune `swapCooldown`. Fallback: move Whisper to non-shared GPU group. |
| pyannote 3.1 requires CUDA-only kernels at inference | Low | Medium (forces CPU diarization) | Validated in pyannote 3.x release notes (PyTorch-only, no custom CUDA kernels). Confirm in Slice 4 with `rocm-smi` on radeonvii showing GPU residency during diarize. |
| FLUX Fill + pyannote VRAM contention on radeonvii | Medium | Medium (degrades inpaint or diarize latency) | Slice 6 captures rocm-smi on both nodes. 16 GiB total − 10 GiB FLUX − 0.5 GiB pyannote leaves margin but not infinite headroom. Fallback: node-local semaphore serializing the two endpoints. |
| `mixa3607/pytorch-gfx906` base image becomes unavailable / unmaintained | Low | High (breaks pyannote build; no upstream gfx906 PyTorch) | Pin digest in the Dockerfile. If the upstream disappears, the pinned digest persists in Harbor. Long-term: track gfx906 deprecation in the Open Questions backlog. |
| HF Secret missing on cluster, init container loops on download | Medium | Low (one-time setup) | Operator runbook in `docs/operations/pyannote-setup.md`. Init container exits clean with clear error message. |
| ModelSpec lacks runtime Env field, can't inject HF_TOKEN to a hypothetical Whisper-needs-token scenario | Low | Low (Whisper is not gated; doesn't need token) | N/A — only matters if we add a gated ASR model. Tracked as `debt-modelspec-env-injection`. |
| `--task transcription` flag isn't accepted by the pinned vLLM image | Medium (kill-test surfaces) | High | Slice 1 evidence. If flag is unknown, vLLM exits at startup — clear signal. |
| Whisper cold-start exceeds 60s on first call after idle | Low | Low | ICC plan already accepts post-call batch budget. If observed, set `serverless.minReplicas: 1`. |

## Validation Matrix

| Slice | Verification | Owner | Evidence path |
|---|---|---|---|
| 1 | Kill-test passes against pinned vLLM image | flexinfer maintainer | `.loom/asr-diarization-kill-test-passed-2026-05-18.md` (or `-failed-`) |
| 2 | `kubectl explain` shows new field; `make manifests` green | flexinfer maintainer | CRD diff in MR |
| 3a | Whisper Model CR reaches Ready; transcribe via proxy returns expected text | operator | smoke log appended to validation matrix |
| 3b | `go test ./backend -run TestVLLMArgs` passes; deployed pod has `--task transcription` | maintainer | unit test green in CI |
| 4 | pyannote Deployment Ready on radeonvii (`kubectl get pod -n flexinfer-system -l app=pyannote-diarization -o wide` shows `cblevins-radeonvii`); `/diarize` returns segments JSON | operator | smoke log appended |
| 5 | Proxy `/diarize` route returns segments JSON same as direct pyannote call | maintainer | smoke log appended |
| 6 | 5-min load test: no OOM, ≤2 swaps, ≤30% 26B latency degradation | operator | `.loom/asr-diarization-load-test-2026-05-18.md` |

## Files this lands

| Path | Action | Slice |
|---|---|---|
| `.loom/asr-diarization-kill-test-passed-2026-05-18.md` (or `-failed-`) | new | 1 |
| `deploy/gpuprofiles/gfx1100.yaml` | add `audioTranscription: supported` | 2 |
| `deploy/gpuprofiles/gfx906.yaml` | add `audioTranscription: unsupported` | 2 |
| `api/v1alpha2/gpuprofile_types.go` | add `AudioTranscription string` field on `VLLMCapabilities` struct | 2 |
| `config/crd/ai.flexinfer_gpuprofiles.yaml`, `charts/flexinfer/crds/ai.flexinfer_gpuprofiles.yaml` | regenerate via `make manifests` | 2 |
| `deploy/models/whisper-large-v3-turbo.yaml` | new | 3a |
| `backend/vllm.go` | add `--task` arg passthrough (~6 LOC) | 3b |
| `backend/vllm_test.go` | add test for `--task` arg | 3b |
| `build/Dockerfile.pyannote-rocm-gfx906` | new (base: `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` pinned by digest) | 4 |
| `build/scripts/pyannote_server.py` | new | 4 |
| `build/Makefile` | add `pyannote-rocm-gfx906` build target | 4 |
| `.gitlab-ci.yml` | add publish lane for `pyannote-rocm-gfx906` (independent of the paused gfx906 runtime lane) | 4 |
| `deploy/system/pyannote-diarization/{pvc,deployment,service}.yaml` | new | 4 |
| `deploy/system/pyannote-diarization/kustomization.yaml` | new (wires into flexinfer-system) | 4 |
| `internal/proxy/proxy.go` | register `/diarize` handler before catch-all | 5 |
| `internal/proxy/pyannote.go` | new (~50 LOC ReverseProxy handler) | 5 |
| `internal/proxy/pyannote_test.go` | new (handler + env-var-not-set 503 test) | 5 |
| `deploy/system/proxy/deployment.yaml` (or wherever flexinfer-proxy env is set) | add `FLEXINFER_PYANNOTE_UPSTREAM` env | 5 |
| `docs/operations/pyannote-setup.md` | new (operator HF-token runbook) | 4 |
| `.loom/asr-diarization-load-test-2026-05-18.md` | new (Slice 6 evidence) | 6 |

## Dependencies on other in-flight work

- **Wave-1 vLLM feature-parity spec** (`.loom/21-product-spec-vllm-feature-parity-2026-05-15.md`) is also touching `api/v1alpha2/gpuprofile_types.go` `VLLMCapabilities`. Coordinate the `AudioTranscription` field addition on the same base branch to avoid merge conflicts. If Wave-1 lands first, rebase this plan's Slice 2 on top of it.
- **Track B (gfx906 disk-pressure unblock)** in `.loom/gfx1100-gfx906-next-round-plan.md` does NOT block this plan. Track B is about unpausing the gfx906 flexinfer-runtime DaemonSet (which hosts vLLM/diffusers). Pyannote ships as a *sibling Deployment* with its own image and does not consume the runtime DaemonSet — the runtime-paused state is irrelevant to it. Track B would only become a dependency if a future revision tries to put Whisper on gfx906 via vLLM (the fallback in this plan is whisper.cpp HIP, also sibling-Deployment, also Track-B-independent).
- **No** dependency on the `qwen35-layout-adapter-design` or `gemma4-pipeline-retro` work.

## What this plan does NOT include (deliberate)

- Voice enrollment / per-speaker identification (ICC round-10 phase 2).
- A summary/action-item LLM pass over the transcript (handled inside ICC by calling the existing 26B textgen endpoint — flexinfer doesn't need new infra).
- Streaming/incremental transcription.
- Multi-language model routing (one Whisper model serves all languages it knows).
- Per-tenant rate limiting on the audio endpoints (ICC is single-user; no multi-tenant surface).
- Audio file storage in flexinfer (ICC owns recording storage on its own PHI-marked disk).

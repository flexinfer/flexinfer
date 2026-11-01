# Swap profiler: `flexinfer-bench swap`

`flexinfer-bench swap` measures how long a runtime pod takes to swap one lane
for another, and where that time goes. It is the instrument behind KT-SWAP,
the D1 kill-test in
[.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md](../../.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md)
§6.1. Its output is the same for any runtime pod, image or later runtime change.

- Code: `cmd/flexinfer-bench/swap.go`, tested against a fake runtime in
  `cmd/flexinfer-bench/swap_test.go`.
- Runtime API it drives: `internal/runtime/api.go` (`POST
  /api/v1/models/{name}/load`, `DELETE /api/v1/models/{name}`, `GET
  /api/v1/models/{name}/health`, `GET /api/v1/status`, `GET /api/v1/models`).

## What it measures

A run first loads lane 1 as a warmup. Then each cycle swaps in every lane
once: `--cycles 5` with two lanes gives 5 swaps in each direction. A single
lane is unloaded and reloaded each cycle. Each swap-in records:

| Field | Meaning |
|---|---|
| `unloadS` | `DELETE` of the outgoing lane, including request drain (409 retries). |
| `vramUsedMBAfterUnload` | `vramUsedMB` from `/api/v1/status` right after the unload. |
| `admitWaitS`, `admitRetries` | The load answered 503 (VRAM or compute queues still counted as used) or 409 until the runtime admitted it. |
| `loadS` | From the accepted load to model state `Ready`. |
| `substages` | Seconds in each load substage the runtime reports (`internal/runtime/load_progress.go`). |
| `timeline` | Every observed state or substage change, in order. |
| `kvCacheTokens`, `kvMaxConcurrency` | The vLLM KV profile from model health. |
| `firstTokenS`, `swapToFirstTokenS` | TTFT of the first request after Ready, and from the unload to that first token. |
| `probes` | Text lanes only: three greedy parity generations and one decode probe per `--decode-contexts` size. |

The substages:

| Substage | Covers |
|---|---|
| `Spawn` | Process start until the backend prints its first classified line: interpreter and engine imports. The runtime reports no substage for this span. |
| `LoadingWeights` | vLLM `starting to load model` and the safetensors shard progress. |
| `Compiling` | torch.compile and graph capture. vLLM enters it before and after the KV profile; both visits are summed. |
| `ProfilingKV` | vLLM memory profiling and the KV cache size lines. |
| `Ready` | The backend HTTP server is up, until the runtime's readiness probe marks the model Ready. |

The runtime reports only the current substage, so each duration is accurate
to one `--poll-interval` (default 250 ms). The diffusers and snac servers print
none of vLLM's lines, so their loads show only `Spawn` and `Ready`.

Text lanes (`vllm`, `llamacpp`, `mlc-llm`, `ollama`) are probed with the three
fixed parity prompts of `scripts/verify-vllm-mtp.py` (`parity_code`,
`parity_explain`, `parity_list`: greedy, 256 tokens, streaming). The hash is
`sha256` of the stripped answer, first 16 hex digits, the same as that
script's, so hashes compare with the ones S1–S3 recorded. Decode is `(tokens -
1)` over the first-to-last token time. Every probe sends a fresh vLLM
`cache_salt` (`--cache-salt=false` turns it off). Every run is then a cold
prefill, so warm and cold prefix-cache paths never get compared. The sized
decode prompts also open with a unique line.

## Run it

The profiler loads and unloads models behind the controller's back. Hold the
card first: a `GPULease` on the lane's shared group parks every member
([gpu-sharing.md](gpu-sharing.md)), and the runtime pod stays up. Otherwise
use lane names that no Model manages. If you skip both, the controller
reloads its own lanes into the VRAM the profiler is measuring.

1. **Baseline.** While the lane still runs as its dedicated Deployment, run the
   probes against its Service. This mode makes no runtime calls.

   ```bash
   flexinfer-bench swap --probe-url http://qwen38-27b-xtx-warm-canary.flexinfer-system.svc:8000 \
     --cycles 3 --out baseline-warm.json
   ```

2. **Swaps.** Point it at the runtime pod. A lane is a load payload (`.json`, as
   `POST .../load` takes it) or a Model manifest (`.yaml`). A manifest is
   rendered with `pkg/runtime.NewLoadPayloadForModel`, the controller's
   builder, against `--gpuprofile`; LoRA adapters are not looked up. The
   report warns when the controller would keep that Model off the runtime path
   (`dedicatedDeployment`, a `command` wrapper, an unbundled backend).

   ```bash
   flexinfer-bench swap --runtime-url http://10.42.0.137:8080 \
     --gpuprofile deploy/gpuprofiles/gfx1100.yaml \
     --lane warm=deploy/models/qwen38-27b-xtx-warm-canary.yaml \
     --lane image=deploy/models/qwen-image-21-gfx1100.yaml \
     --cycles 5 --baseline baseline-warm.json \
     --runtime-image registry.harbor.lan/flexinfer/runtime@sha256:<digest> \
     --rollback-image registry.harbor.lan/flexinfer/runtime@sha256:<digest> \
     --out swap.json --row-out swap-row.md
   ```

   The inference URL defaults to the runtime URL's host on the port the
   runtime reports for the model. Use `--inference-url http://host:{port}` when
   only a Service reaches the pod.

## Verdict

The report's `bars` hold the KT-SWAP bars the profiler can measure, with the
pre-registered values as flag defaults:

| Bar | Default | Flag |
|---|---|---|
| Text lane load p50 / p95 | ≤ 120 s / ≤ 180 s | `--max-text-load-p50`, `--max-text-load-p95` |
| Other (diffusers) lane load p50 | ≤ 90 s | `--max-other-load-p50` |
| Greedy hash | one hash per parity prompt across all swaps, equal to the baseline's | `--baseline` |
| Decode | median per probe ≥ (1 − 0.03) × the baseline's | `--decode-tolerance` |

Percentiles are nearest-rank, so p95 of five loads is the slowest. Two bars
are outside the profiler and always come back `not_measured`: zero SMU or
amdgpu errors in the host log, and the warm lane returning within the swap
cooldown, which is controller-driven.

- `verdict` is `fail` on any failed bar or run error, `pass` when every bar
  passed, and `conditional` otherwise.
- The exit code is 1 on `fail`, 2 on a usage error, and 0 otherwise.
- The JSON report goes to `--out` (default stdout). A ready-to-paste
  `.loom/60-validation-matrix.md` row (13 columns, with the substage p50
  breakdown and every bar) goes to `--row-out` (default stderr).

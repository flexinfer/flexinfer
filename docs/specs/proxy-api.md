---
title: Proxy API
description: HTTP entrypoint for routing, activation, and discovery.
---

# Proxy API

Binary: `flexinfer-proxy` (`services/flexinfer/cmd/flexinfer-proxy`)

The proxy is intentionally small: it doesn’t define a new inference protocol. Instead, it:

- routes requests to backend services
- uses the `"model"` selection semantics common to OpenAI-compatible APIs
- queues during cold starts

Machine-readable OpenAPI spec:

- `services/flexinfer/specs/openapi/flexinfer-proxy.openapi.yaml`

## HTTP endpoints

### `GET /healthz`

Returns `200 ok` when the process is up.

### `GET /metrics`

Prometheus metrics for proxy queueing + activation behavior.

### `GET /v1/models`

OpenAI-compatible model listing. It aggregates:

- v1alpha1 `ModelDeployment` resources
- v1alpha2 `Model` resources

Response shape:

```json
{ "object": "list", "data": [ { "id": "llama3-8b", "object": "model", "created": 0, "owned_by": "flexinfer", "metadata": {} } ] }
```

### `/*` (reverse proxy)

All other paths are forwarded to the selected model backend (subject to scale-to-zero/activation).

## Model selection

The proxy extracts model name from (first match wins):

1. `X-Model-ID` header
2. `/model/<name>/...` path prefix (stripped before forwarding)
3. OpenAI JSON body `{ "model": "<name>" }` (POST + `application/json`)
4. Multipart form `"model"` field (POST + `multipart/form-data`, e.g., `/v1/images/edits`)

For multipart requests, the payload is forwarded intact to the backend — no JSON model rewriting occurs.

## Request flow

```mermaid
flowchart TD
    req(["Incoming request"]):::step --> edge{"Token, model name,<br/>rate limit OK?"}:::step
    edge -->|"401 · 400 · 429"| rej(["Rejected"]):::fail
    edge -->|yes| resolve["Resolve service label,<br/>alias, LoRA parent"]:::proxy
    resolve --> gates{"Model serves this path,<br/>prompt within budget?"}:::step
    gates -->|"404 · 413"| rej
    gates -->|yes| ready{"Model<br/>Ready?"}:::step
    ready -->|yes| breaker{"Lane breaker<br/>open?"}:::step
    breaker -->|"503"| rej
    breaker -->|no| forward["Route and forward"]:::proxy
    ready -->|no| cold["handleColdStart()<br/>queue · activate · drain"]:::proxy
    cold --> forward
    forward --> overflow{"400 context length,<br/>overflow lane set?"}:::step
    overflow -->|yes| replay["Replay once to<br/>the overflow lane"]:::proxy
    overflow -->|no| swap{"Idempotent path, 502/503/504<br/>during a lane swap?"}:::step
    swap -->|yes| swapreplay["Replay once on the lane<br/>or its equivalent"]:::proxy
    swap -->|no| resp(["Response"]):::ok
    replay --> resp
    swapreplay --> resp

    classDef step fill:#E0E7FF,stroke:#6366F1,color:#0F172A
    classDef proxy fill:#60A5FA,stroke:#1E40AF,color:#0F172A
    classDef fail fill:#F06F6B,stroke:#8B3A3A,color:#0F172A
    classDef ok fill:#34D399,stroke:#047857,color:#0F172A
```

Forwarding rewrites `"model"` in a JSON body to the backend's model name; a multipart body is forwarded intact. Status codes the proxy itself returns:

| Gate | Status | `error.code` |
|---|---|---|
| Bearer token invalid (auth enabled) | 401 | `invalid_api_key` |
| No model name in header, path, JSON or multipart | 400 | `missing_required_field` |
| Rate limit exceeded | 429 + `Retry-After` | `rate_limit_exceeded` |
| Request validation failed (when enabled) | 400 | `validation_error` |
| No such Model, or the Model does not serve this path (`flexinfer.ai/serve-paths`) | 404 | `model_not_found` |
| Prompt over the context budget (`flexinfer.ai/admission` opt-in) | 413 | `admission_rejected` |
| Lane breaker open | 503 | `lane_unresponsive` |
| Not Ready and parked: `spec.litellm.enabled: false` | 404 | `model_parked` |
| Not Ready and parked behind a higher-priority warm primary | 503 | `model_parked` |
| Background-class request to a lane that is not Ready | 503 | by lane state |
| Cold start: queue full | 503 | `queue_full` |
| Cold start: `Prefer: wait=N` elapsed | 503 | by lane state |
| Cold start: activation failed (scale-up error, Failed phase, readiness timeout in the queue processor) | 503 | by lane state, `model_failed` for a Failed Model |
| Cold start: weight load stalled | 503 | `stalled_load` |
| Cold start: readiness timeout | 504 | by lane state |
| No first byte from the lane within its budget (counts against the breaker) | 504 | `lane_unresponsive` |
| Every forward attempt failed at the transport | 502 | `lane_unresponsive` |
| Over the lane's context, overflow lane not Ready or its breaker open | 400 | `context_overflow` |

"By lane state" is `model_failed` (Phase Failed), `model_parked` (parked behind a primary),
`model_preempted` (Phase Preempted, or shared group Queued/Preempted), else `model_cold_starting`.

Code: `internal/proxy/proxy.go` (`handleRequest`), `errors.go`, `load_eta.go`, `lane_breaker.go`,
`context_overflow.go`, `swap_replay.go`, `queue.go`, `routing_retry.go`, `pkg/validation/errors.go`.

## Error envelope

Every error the proxy originates about a lane is an OpenAI error object with a `flexinfer` block
inside `error`:

```json
{"error": {"type": "service_unavailable_error", "code": "model_cold_starting",
  "message": "model \"qwen38-27b-autoround-workhorse\" is not ready after the preferred wait of 0s; activation continues",
  "flexinfer": {"phase": "Loading", "loading_substage": "Compiling",
    "eta_seconds": 95, "retry_after_seconds": 30, "reroute_to": null}}}
```

All five keys are always present; a value the proxy does not know is `null`. Errors that are not
about a lane (401, 400 missing model, 404 unknown model, 429) keep the plain OpenAI shape. A 413
from admission also keeps its top-level `admission` block.

| Code | Status | Meaning | `Retry-After` |
|---|---|---|---|
| `model_cold_starting` | 503 (`Prefer: wait`, failed activation), 504 (queue timeout) | Not Ready and nothing blocks it: scaled to zero, Pending or Loading. Retrying works. | ETA clamped to 5–30 s; 30 s when the ETA is unknown |
| `model_parked` | 503, or 404 for `spec.litellm.enabled: false` | Parked behind a warm primary it cannot outrank, or de-advertised | 60 s on the 503; none on the 404 |
| `model_preempted` | 503 / 504 | Lost its shared GPU to a peer; waits on the election | 30 s |
| `model_failed` | 503 / 504 | Phase Failed; retrying cannot help until it recovers | 60 s |
| `lane_unresponsive` | 503 (breaker open), 504 (first-byte budget), 502 (transport) | The lane is Ready but did not answer | breaker reopen on the 503; 5 s on the 502; none on the 504 |
| `context_overflow` | 400 | Over the lane's context; its `flexinfer.ai/context-overflow-to` lane cannot take the replay now. `reroute_to` names it and the block describes it | none |
| `admission_rejected` | 413 | Refused by the admission filter (was `context_window_exceeded`) | none |
| `queue_full` | 503 | The lane's cold-start queue (`PROXY_MAX_QUEUE_SIZE`) is full | as `model_cold_starting` |
| `stalled_load` | 503 | The weight load stopped making progress | 30 s |

Fields:

- `phase`, `loading_substage`: the Model's `status.phase` and `status.loadingSubstage` when the
  error was written (the target lane's for `context_overflow`).
- `eta_seconds`: seconds until Ready. The lane's cold-start p50 minus how long its current load has
  run (from the `ModelReady=False` transition), never below a floor for the current substage (60 s
  `ImagePulling`, 30 s `Initializing` and `LoadingWeights`, 15 s `Compiling`, 5 s
  `HealthCheckPending`). The p50 is `flexinfer_model_cold_start_duration_seconds` over 7 days for the
  lane's `serving_path` when `FLEXINFER_PROXY_PROMETHEUS_URL` is set, else the last load the
  controller wrote to `status.metrics.loadTimeSeconds`. `null` with no load history, and for
  `model_failed`, `model_parked` and `model_preempted`, which wait on something other than a clock.
  For `lane_unresponsive` from an open breaker it is the time to the breaker's probe; for a Ready
  lane otherwise 0.
- `retry_after_seconds`: equals the `Retry-After` header. Every 503 carries one.
- `reroute_to`: an equivalent lane (`flexinfer.ai/unavailable-reroute-to`) or the overflow lane.

The messages keep their earlier wording, so callers that still match text keep working; decide on
`code` instead.

### `X-FlexInfer-Activation`

Every response to a request that entered the cold-start queue carries
`X-FlexInfer-Activation: <phase>/<substage>` (or `<phase>` with no substage): the lane state the
request found on arrival, including on the eventual 200. Error responses from the queue carry the
state when the error was written.

### `Prefer: wait=N`

A caller that sends `Prefer: wait=N` ([RFC 7240](https://www.rfc-editor.org/rfc/rfc7240#section-4.3))
waits at most N seconds in the cold-start queue, then gets a 503 whose code names the lane state,
with `eta_seconds` and `Retry-After`, instead of holding the connection to the cold-start timeout.
The activation it started continues. The response carries `Preference-Applied: wait=N` when the
preference shortened the wait. Without the header the queue behaves as before.

## Swap-safe replay

A multi-model runtime drains a lane before it unloads or reloads it: the lane's ingress answers new requests with `503 model is draining` (`Retry-After: 1`) while accepted ones finish, then the backend stops and its port closes. For idempotent, non-streaming requests the proxy holds back the failure and replays the request **once** when the failure coincides with a swap of the lane.

| | |
|---|---|
| Paths | `POST /v1/embeddings`, `/v1/rerank`, `/rerank`, `/v1/score`, and `/v1/audio/transcriptions` without `stream=true`. Never chat or completions, never a streamed body, never a LoRA adapter. |
| Held back | 502, 503 and 504 (bodies up to 64 KiB). Every other status passes through as it arrives. |
| Swap signals | The lane is parked in its shared group (`status.sharedGroup.state` not `Active`); the body is the runtime's drain answer; the lane is not `Ready`; or it was preempted, or became `Ready`, in the last 60 s. Without a signal the failure is the lane's own and reaches the caller unchanged. |
| Where | A parked lane is replayed on its `flexinfer.ai/unavailable-reroute-to` equivalent at once, if that Model is `Ready`, serves the path and its breaker is closed; a parked lane without one fails fast with the original response. A draining or reloading lane is replayed on itself once it is `Ready` and `Active` again and `GET /health` on its target answers below 500, polled every 250 ms; when the wait runs out the equivalent is used if it can serve. The equivalent is never cold-started. |
| Bound | The wait ends at the earliest of `FLEXINFER_PROXY_SWAP_REPLAY_WAIT` (default `30s`), the lane's first-byte budget measured from the request start, and the client's own deadline or disconnect. Then the original failure is returned. |
| Caller sees | The replay's response, with `X-FlexInfer-Replayed: <lane>-><served lane>` (for example `gte-qwen2-1p5b-radeonvii->gte-qwen2-1p5b-cpu-5930k`). The held-back response's headers are discarded. A replay that fails is returned as is; it is never replayed again. |
| Metric | `flexinfer_proxy_replays_total{lane,outcome}`, outcome `same_lane`, `reroute`, `failed` or `no_ready_lane`. |
| Off switch | `FLEXINFER_PROXY_SWAP_REPLAY=false` on the proxy. |

This sits after the dial-class retry in `routing_retry.go`, which re-dials a refused or reset connection once through the Service before any status exists.

## Scale-to-zero contract

If a model has zero replicas (idle), the proxy may:

- enqueue the request (bounded by `PROXY_MAX_QUEUE_SIZE`)
- activate the model
- wait for readiness (bounded by `PROXY_QUEUE_TIMEOUT` / `PROXY_COLD_START_TIMEOUT`)

Configuration details live in `docs/CONFIGURATION.md`.

## Caller QoS classes

Every inference request resolves to one QoS class (`internal/proxy/qos.go`). First match wins:

1. `X-FlexInfer-QoS: interactive|background|batch` request header (case-insensitive; an unknown
   value is ignored).
2. The caller-identity map in ConfigMap `flexinfer-proxy-qos` (chart values `proxy.qos`): first
   the sha256 of the bearer key (`apiKeys`, stored as `sha256:<hex>`), then the caller name
   (`callers`). The caller name is the `X-FlexInfer-Caller` header, else the User-Agent's first
   product token (`loom-mills/1.4` → `loom-mills`), lower-cased.
3. A LiteLLM alias suffix on the requested model: `workhorse-background` routes to `workhorse` as
   `background`, `<name>-batch` as `batch`. The suffix is honoured only when the full name is not
   itself a Model, service label, alias or LoRA adapter.
4. `interactive`.

| Class | vLLM priority | Intended callers |
|---|---|---|
| `interactive` | 0 | people waiting on an answer: jobsearch, psyche, the HUD, fi-fhir |
| `background` | 10 | agent research, shadow judging, evaluation sweeps (Mills) |
| `batch` | 20 | asynchronous jobs |

The proxy strips `X-FlexInfer-QoS` and `X-FlexInfer-Caller` before forwarding. The class is
independent of the proxy-internal `X-FlexInfer-Internal-Workload-Class` header, which only decides
whether a request may create cold-start demand: a `background` request still activates a cold
lane.

The class labels the `request start` and `request usage` log lines (`qos_class`, `qos_source`,
`caller`) and three metrics: `flexinfer_proxy_qos_decisions_total{class,lane,outcome}`,
`flexinfer_proxy_qos_ttft_seconds{class,lane}` (streaming completions, time to the first upstream
byte) and `flexinfer_proxy_qos_request_duration_seconds{class,lane}`. A lane that does not enforce
classes serves every request as before and counts it as `outcome="unenforced"`.

The proxy re-reads the mounted map every 30 s, so a values change needs no pod roll.

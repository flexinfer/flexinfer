---
title: Request Routing
description: Configure routing strategies for multi-replica models.
---

# Request Routing

FlexInfer supports multiple routing strategies for distributing requests across replicas of a model. The default behavior uses Kubernetes Service load balancing (round-robin), but you can enable smarter routing for better performance.

## Why Custom Routing Matters

### KV-Cache Locality

LLM inference backends maintain a KV-cache for each conversation. When requests from the same conversation hit different pods:

- KV-cache must be recomputed from scratch
- Latency increases significantly (especially for long contexts)
- GPU memory is wasted on duplicate caches

Session affinity routing ensures requests with the same session ID always hit the same pod, maximizing KV-cache hits.

### System Prompt Sharing

Many applications use the same system prompt across different conversations. Prefix-based routing groups requests with the same system prompt to the same pod, enabling:

- Shared KV-cache for the system prompt portion
- Reduced memory usage
- Faster time-to-first-token for new conversations

## Routing Strategies

### Default (Kubernetes Service)

By default, requests are routed through Kubernetes Service load balancing, which typically uses round-robin selection. This is the recommended configuration for most workloads.

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
spec:
  # No routing annotation = Kubernetes Service load balancing
  backend: ollama
  source: ollama://llama3:8b
```

**When to use default routing:**
- Stateless inference (embeddings, single-shot completions)
- Development/testing environments
- Applications that handle their own routing

### Session Affinity

Routes requests with the same session to the same pod.

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
  annotations:
    flexinfer.ai/routing: session-affinity
```

Session ID is extracted from (in priority order):

1. `X-Session-ID` header
2. `X-Conversation-ID` header
3. `session_id` field in request body
4. Hash of first few messages (implicit session from conversation history)

**Example request:**

```bash
curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Session-ID: user-123-conversation-456" \
  -d '{
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

### Prefix-Based Routing

Routes requests with the same system prompt to the same pod.

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
  annotations:
    flexinfer.ai/routing: prefix
```

Prefix is extracted from:

1. `X-Flexinfer-Cache-Key` header (explicit override)
2. `cache_key` or `cacheKey` field in request body (explicit override)
3. `prefix` field in request body (legacy explicit field)
4. Canonicalized context hash from:
   - routed model identity (falls back to request body `model` when route model is unavailable)
   - all `role: "system"` messages (normalized). `content` may be plain text or structured text-part arrays.
   - optional document context (`document_context`, `documentContext`, `context`, or first `documents[]` text payload)

If no prefix key can be derived, prefix routing falls back to session-derived affinity (when available), then to Service DNS.

**Best for:**

- Applications with long, shared system prompts
- Multi-tenant scenarios where each tenant has a unique system prompt
- Workflows where many users share the same context

**Example:**

```bash
# Both requests will route to the same pod
curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "system", "content": "You are an expert Python developer..."},
      {"role": "user", "content": "How do I use decorators?"}
    ]
  }'

curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "messages": [
      {"role": "system", "content": "You are an expert Python developer..."},
      {"role": "user", "content": "Explain async/await"}
    ]
  }'
```

### Explicit Cache-Key Contract

Use explicit cache keys when your client already has a stable context identifier (for example, `tenant/doc-version`).

```bash
curl -X POST http://proxy:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "X-Flexinfer-Cache-Key: tenant-a/doc-42" \
  -d '{
    "messages": [
      {"role": "user", "content": "Summarize this document"}
    ]
  }'
```

Body-based equivalent:

```json
{
  "cache_key": "tenant-a/doc-42",
  "messages": [{"role": "user", "content": "Summarize this document"}]
}
```

Precedence for `flexinfer.ai/routing: prefix`:
1. `X-Flexinfer-Cache-Key`
2. `cache_key` / `cacheKey`
3. `prefix`
4. canonicalized model/system/document context
5. session-derived fallback (`X-Session-ID`, etc.)
6. Kubernetes Service routing fallback

Safety rules:
- explicit cache keys are normalized and validated
- max explicit key length is `128` characters
- allowed characters: `A-Z a-z 0-9 . _ : / = -`
- malformed keys are ignored (router falls through to the next source)
- key strictness bounds are operator-configurable through proxy env/Helm values

#### Operator Keying Knobs

Tune these values when you need stricter cardinality control or longer canonical segments:

```yaml
proxy:
  routing:
    explicitKeyMaxLength: 128
    systemSegmentMaxLength: 512
    documentSegmentMaxLength: 256
```

These map to:
- `PROXY_ROUTING_EXPLICIT_KEY_MAX_LENGTH`
- `PROXY_ROUTING_SYSTEM_SEGMENT_MAX_LENGTH`
- `PROXY_ROUTING_DOCUMENT_SEGMENT_MAX_LENGTH`

Invalid (non-positive) values safely fall back to defaults.

### Least-Loaded

Routes to the pod with the lowest current load (active connections).

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
  annotations:
    flexinfer.ai/routing: least-loaded
```

**How it works:**
- Proxy tracks active connections per pod
- Requests route to the pod with fewest active connections
- Falls back to first available pod if all have equal load

**Best for:**
- Workloads with variable request durations
- Models where some requests are much slower (e.g., long generations)
- Preventing hot spots on individual pods

## How It Works

### Consistent Hashing

Session affinity and prefix routing use consistent hashing to select pods:

1. Session ID or prefix is hashed to a point on a virtual ring
2. The hash ring contains multiple virtual nodes per real pod
3. Requests are routed to the pod whose virtual node is closest to the hash

**Benefits:**

- Same key always routes to the same pod
- When pods are added/removed, only ~1/N of keys are remapped
- Virtual nodes ensure even distribution across pods

### Endpoint Discovery

The proxy watches Kubernetes EndpointSlices to maintain the list of ready pods for each model. When endpoints change:

1. New pods are added to the hash ring
2. Removed pods are deleted from the ring
3. Minimal redistribution occurs (unlike round-robin restart)

## Monitoring

### Metrics

Monitor routing effectiveness with these metrics:

| Metric | Description |
|--------|-------------|
| `flexinfer_proxy_requests_total{model,status}` | Total requests per model (by status) |
| `flexinfer_proxy_active_connections{model}` | Current connections per model |
| `flexinfer_proxy_routing_decisions_total{model,strategy,key_source,outcome}` | Routing decisions by strategy and key source (`outcome`: `pod` or `service-fallback`) |
| `flexinfer_proxy_routing_target_hits_total{model,strategy,target}` | Route-hit distribution by selected target (`target` is pod IP:port or `service-dns`) |
| `flexinfer_proxy_routing_key_cardinality{model,strategy,key_source}` | Approximate unique routing-key count per source (bounded in-memory tracker) |
| `flexinfer_proxy_routing_key_cardinality_overflow_total{model,strategy,key_source}` | Number of times cardinality tracking reached its cap |

### Logs

Enable debug logging to see routing decisions:

```text
routing to pod model=my-model strategy=prefix target=10.0.0.5:8000 key_source=explicit-header
routing fallback to service model=my-model strategy=prefix key_source=none
```

## Chat-with-Doc Benchmark Scenario

Use this benchmark to validate route stability and key-signal health for
`flexinfer.ai/routing: prefix` workloads.

### Scenario Goals

- Verify deterministic routing under repeated document-context traffic
- Verify malformed/invalid keys degrade safely (no routing failure)
- Measure route-hit distribution and key cardinality behavior
- Compare latency against a default-routing baseline

### Test Profile

Run three traffic phases against the same model service:

1. `explicit-stable`: repeated requests with a small fixed set of explicit cache keys
2. `canonical-context`: no explicit key, but stable system + document context segments
3. `malformed-key`: malformed `X-Flexinfer-Cache-Key` values to exercise fallback

### Example Load Generator (in-cluster)

```bash
kubectl -n flexinfer-system run routing-bench --image=python:3.11-alpine --restart=Never -- sleep 900
kubectl -n flexinfer-system exec routing-bench -- python3 - <<'PY'
import json, time, urllib.request

model_service = "http://YOUR-MODEL.flexinfer-system.svc.cluster.local:8000/v1/chat/completions"
headers = {"Content-Type": "application/json"}
docs = [
    ("tenant-a/doc-1", "FlexInfer routing guide section A"),
    ("tenant-a/doc-2", "FlexInfer routing guide section B"),
    ("tenant-a/doc-3", "FlexInfer routing guide section C"),
]

def run_phase(name, make_request, iterations=60):
    lat = []
    ok = 0
    for i in range(iterations):
        req = make_request(i)
        start = time.time()
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                if 200 <= resp.status < 300:
                    ok += 1
                _ = resp.read()
        except Exception:
            pass
        lat.append(time.time() - start)
    lat_sorted = sorted(lat)
    p50 = lat_sorted[int(len(lat_sorted) * 0.50)]
    p95 = lat_sorted[int(len(lat_sorted) * 0.95)]
    print(f"{name}: ok={ok}/{iterations} p50={p50:.2f}s p95={p95:.2f}s")

def body(doc_text):
    return json.dumps({
        "model": "/models",
        "messages": [
            {"role": "system", "content": "You answer using provided document context only."},
            {"role": "user", "content": f"Summarize: {doc_text}"}
        ],
        "document_context": doc_text,
        "max_tokens": 120
    }).encode()

def explicit_stable(i):
    key, doc = docs[i % len(docs)]
    h = dict(headers)
    h["X-Flexinfer-Cache-Key"] = key
    return urllib.request.Request(model_service, data=body(doc), headers=h)

def canonical_context(i):
    _, doc = docs[i % len(docs)]
    return urllib.request.Request(model_service, data=body(doc), headers=headers)

def malformed_key(i):
    _, doc = docs[i % len(docs)]
    h = dict(headers)
    h["X-Flexinfer-Cache-Key"] = f"bad key with spaces {i}"
    return urllib.request.Request(model_service, data=body(doc), headers=h)

run_phase("explicit-stable", explicit_stable)
run_phase("canonical-context", canonical_context)
run_phase("malformed-key", malformed_key)
PY
kubectl -n flexinfer-system delete pod routing-bench
```

### PromQL Signals

Replace `$MODEL` with the ModelDeployment name.

```promql
# Key source mix
sum by (key_source) (
  rate(flexinfer_proxy_routing_decisions_total{model="$MODEL",strategy="prefix"}[5m])
)

# Service fallback ratio (should stay low in stable phases)
sum(rate(flexinfer_proxy_routing_decisions_total{model="$MODEL",strategy="prefix",outcome="service-fallback"}[5m]))
/
sum(rate(flexinfer_proxy_routing_decisions_total{model="$MODEL",strategy="prefix"}[5m]))

# Route-hit distribution by target
sum by (target) (
  rate(flexinfer_proxy_routing_target_hits_total{model="$MODEL",strategy="prefix"}[5m])
)

# Approximate key cardinality by source
max by (key_source) (
  flexinfer_proxy_routing_key_cardinality{model="$MODEL",strategy="prefix"}
)

# Cardinality tracker cap hits (should remain 0 in normal traffic)
increase(flexinfer_proxy_routing_key_cardinality_overflow_total{model="$MODEL",strategy="prefix"}[15m])

# p95 request latency for before/after routing comparison
histogram_quantile(0.95, sum by (le) (
  rate(flexinfer_proxy_request_duration_seconds_bucket{model="$MODEL"}[5m])
))
```

### Expected Signals

| Phase | Expected key source | Expected routing outcome | Cardinality expectation |
|------|----------------------|--------------------------|-------------------------|
| `explicit-stable` | `explicit-header` dominates | `pod` dominates, low fallback ratio | tracks fixed explicit key set (small, stable) |
| `canonical-context` | `canonical` dominates | `pod` dominates | tracks distinct normalized doc/system contexts |
| `malformed-key` | explicit source drops; canonical/session fallback increases | no routing failure; fallback may rise but remains bounded | no uncontrolled growth; overflow counter stays `0` |

Latency guidance:
- Compare against a short baseline run with default routing on the same model/config.
- Treat this benchmark as a pass when `prefix` routing is non-regressive (similar p95) and
  route stability improves for repeated context traffic.

## Recommendations

### When to Use Session Affinity

- Chat applications with conversation history
- Applications that maintain state across requests
- Workloads with variable context lengths

### When to Use Prefix Routing

- Applications with shared system prompts (e.g., coding assistants)
- Multi-tenant scenarios
- RAG applications with shared context documents

### When to Use Least-Loaded Routing

- Variable request durations (some fast, some slow)
- Batch processing with mixed workloads
- Preventing hot spots on specific pods

### When to Use Default Routing

- Stateless inference (embeddings, single-shot completions)
- Applications that already handle their own routing
- Development/testing environments

## Graceful Degradation

If the routing layer encounters issues, it falls back to Kubernetes Service routing:

- No ready endpoints → Service DNS
- Session ID not found → Random pod selection
- Hash ring empty → Service DNS

This ensures requests are never dropped due to routing configuration.

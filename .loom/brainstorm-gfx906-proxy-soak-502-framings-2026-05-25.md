# Brainstorm: gfx906 llamacpp proxy-soak — 502 root-cause framings

Date: 2026-05-25
Owner: cblevins / Claude (Lane 1B)
Status: planning → next slice decision

## TL;DR

Five MRs (!487–!491) made the proxy-soak self-describing enough that
the latest 900-second smoke gave us conclusive structural signal:

- The `flexinfer-proxy` returns every 502 **before forwarding** to the
  upstream `flexinfer-runtime-gfx906-njg9w` runtime DaemonSet on
  `cblevins-radeonvii`. Runtime logs show zero `qwen3-8b-radeonvii-soak`
  activity during the failure bursts.
- Only ~4 of 9 failures produced a `proxy error` log line. The other
  ~5 failed silently. We are flying with a covered instrument panel.
- One of the visible proxy errors dialed
  `10.43.5.133:8080` (Service VIP, **wrong port** — Service exposes
  only `8000`). That implies a code path or cache that resolves the
  per-model upstream to the wrong port for at least some requests.
- We have a **riskiest assumption** to kill before we commit to a code-
  archaeology slice on pkg/proxy: that the proxy is the bug, not the
  k8s network plumbing under it.

## What we observed (evidence)

From `/tmp/proxy-soak-491/soak.jsonl` (15-min smoke, 2026-05-25 10:24–10:40):

```
preflight  ok=1 fail=1
warmup     ok=1 fail=0
measured   ok=6 fail=8   (57% failure rate)
model_mismatch (ok, llama.cpp reports GGUF basename): preflight=1 warmup=1 measured=6
```

Failure pattern is **bursty**, not random:

```
10:24:15  preflight  1   502  (~30s timeout)
10:25:xx  warmup     1   ok
10:26:xx  measured   2   ok
10:27:16  measured   3   502
10:28:16  measured   4   502
10:29:16  measured   5   502   ← 6 consecutive 502s
10:30:16  measured   6   502
10:31:16  measured   7   502
10:32:16  measured   8   502
10:33:xx  measured   9   ok
10:34:xx  measured  10   ok
10:35:16  measured  11   502
10:36:16  measured  12   502   ← 2 consecutive
10:37:xx  measured  13   ok
10:38:xx  measured  14   ok
```

Each 502 took exactly ~30s (an upstream-dial timeout). `diag` probe
showed `flexinfer-proxy /healthz` was healthy on every failure.

### Proxy log signal (de-noised — see Tech Debt below)

Four real `proxy error` lines in 20 minutes of logs:

```
10:23:42  "http: proxy error: dial tcp 10.43.5.133:8000: i/o timeout"
10:24:13  "http: proxy error: context canceled"
10:24:45  "http: proxy error: dial tcp 10.43.5.133:8000: i/o timeout"
10:27:46  "http: proxy error: dial tcp 10.43.5.133:8080: i/o timeout"   ← wrong port
```

Service `qwen3-8b-radeonvii-soak` clusterIP `10.43.5.133`, ports
`[{name:http, port:8000, targetPort:8000}]`. There is **no port 8080**
on the Service, the Endpoints, or anywhere in the Model CR.

### Runtime upstream

`flexinfer-runtime-gfx906-njg9w` on `cblevins-radeonvii`:
- restarts=0, ready=true since 09:06:03Z
- No `qwen3-8b-radeonvii-soak` slot activity in stderr until 10:38:16
- When it does serve (post-burst), it's at 14ms/token, 945ms total per
  64-token completion. Healthy.

### Endpoints / EndpointSlice topology

- Service is **selectorless**.
- Two EndpointSlices coexist:
  - `qwen3-8b-radeonvii-soak-hjrng` — managed by
    `endpointslice-controller.k8s.io`, empty (`endpoints: null`,
    `ports: null`).
  - `qwen3-8b-radeonvii-soak-jxpdp` — managed by
    `endpointslicemirroring-controller.k8s.io`, populated with
    `10.42.8.90:8000`.
- Legacy `Endpoints` object is current and correct (single subset,
  `10.42.8.90:8000`).
- This dual-slice pattern is a known but tolerated artefact of
  selectorless Services; kube-proxy unions slices when building rules.

## Riskiest assumption + kill-test

**Load-bearing assumption**: The `flexinfer-proxy` is the source of
every 502; the runtime pod at `10.42.8.90:8000` was reachable and
serving correctly on every failed request. The bursty pattern, the
silent 502s, and the `:8080` dial address all originate in proxy
code or proxy state — not in kube-proxy iptables, CNI, or network
policy.

**Kill test**: Run a tight curl loop **from inside the
`flexinfer-proxy` pod** against the upstream pod IP **and** against
the Service ClusterIP, alongside the next 15-minute soak. Capture
HTTP status + time + timestamp per attempt to a JSONL on the proxy
pod's filesystem. After the soak, cross-reference the proxy-pod
curl results with the soak's `ok=false` minutes:

```
# Pod IP path (bypasses kube-proxy):
curl --max-time 5 -X POST http://10.42.8.90:8000/v1/chat/completions \
  -H 'Content-Type: application/json' -d '{...}'

# Service VIP path (uses kube-proxy):
curl --max-time 5 -X POST http://10.43.5.133:8000/v1/chat/completions \
  -H 'Content-Type: application/json' -d '{...}'
```

Run every 2 seconds for the full soak window.

- **If curl-via-pod-IP and curl-via-Service-VIP both succeed during
  every soak-failure minute** → the proxy code is broken (resolution,
  cache, circuit breaker, port mapping). Framings A and C are in play.
  Framing B is dead.
- **If curl-via-Service-VIP fails when the soak fails, but curl-via-
  pod-IP succeeds** → the kube-proxy/Service-VIP path is the broken
  one. Framing B is the answer. The proxy is faithfully reporting an
  infrastructure failure.
- **If both curl paths fail together with the soak** → upstream pod /
  CNI / radeonvii node networking. Pivot to k8s-debug skill.

**Failure mode if wrong**: Without the kill-test, we'd spend hours
grepping pkg/proxy for a port-mapping bug while the real issue is a
CNI flake on the radeonvii node — completely different fix, different
repo, different on-call rotation. The :8080 detail is a tempting
strange-attractor; we should not commit to it as the root cause
without disproving the infra hypothesis first.

**Status**: not run

**Pair with positive/negative search**: Before declaring the
kill-test "passed", search both ways:
- positive: does `pkg/proxy` ever reference `:8080`, hardcoded admin
  ports, or per-Model port mapping? (`grep -rn '8080' pkg/proxy/`)
- negative: does k3s have any known kube-proxy bug for selectorless
  Services with mirror EndpointSlices in 1.26.3?
  (`tavily: kube-proxy selectorless service double EndpointSlice
  iptables flap 1.26`)

## Three framings

### Framing A — "The proxy has a port-resolution bug specific to this model"

**Frame**: One code path inside `pkg/proxy` returns the Service VIP
with the wrong port (`:8080` instead of `:8000`) for some lookups
against `qwen3-8b-radeonvii-soak`. Other lookups against the same
model use `:8000` correctly.

**Bet**: There is a hardcoded `:8080` literal, an admin/sidecar port
constant, or a stale per-model cache entry from a previous
configuration. A few-line change fixes the dial address.

**Explains**:
- The `:8080` line at 10:27:46.
- Bursts could be triggered by per-instance cache rotation (e.g.,
  N requests served by a worker with the bad cache entry before the
  next refresh).

**Doesn't explain**:
- The 5 silent 502s (no proxy log line). Different code path?
- Why dial-tcp times out instead of immediately failing with
  "connection refused" — :8080 on the Service VIP should ICMP-rejet,
  not time out, unless there's no listener AND no RST path.

**First experiment**: Code-grep
`pkg/proxy` for `8080`, `Port`, `Endpoint`, and per-model resolution
functions. Read the path that goes from `model="qwen3-8b-radeonvii-soak"`
to a `host:port` URL. Diff against a known-working model's path.

### Framing B — "The proxy faithfully reports a network flake"

**Frame**: The kube-proxy iptables/IPVS rules for
`qwen3-8b-radeonvii-soak` are inconsistent or flapping. Sometimes
the dial to `10.43.5.133:8000` works; sometimes the rule is missing
or points at the wrong DNAT target. The `:8080` line is kube-proxy
DNATting to a wrong port from a stale or duplicate rule.

**Bet**: The double EndpointSlice (one empty from
`endpointslice-controller`, one populated from
`endpointslicemirroring-controller`) causes kube-proxy to flap iptables
rules in v1.26.3, dropping the rule transiently every N seconds.

**Explains**:
- Bursty timeouts (rule absent for a window, then reconciled).
- All 502s timing out at ~30s (kube-proxy black-holes packets — no
  TCP RST when iptables drops to OUTPUT-DROP).
- Why proxy logs show dial-tcp i/o timeout: legitimate kernel-level
  timeout.

**Doesn't explain**:
- `:8080` (kube-proxy doesn't typically remap to an undefined port —
  this would require a stale rule from a different Service or a
  Service that changed port mapping).
- Why only this Service is affected on this cluster — every other
  selectorless Service has the same dual-slice pattern.

**First experiment**: Run the kill-test (curl from proxy pod to
pod IP vs. Service VIP). If curl-via-pod-IP succeeds while
curl-via-Service-VIP fails, Framing B is the answer.

### Framing C — "The proxy has a silent-fail path (rate limit / circuit / queue)"

**Frame**: 5 of 9 failures had no `proxy error` log line. The proxy
has internal gating that returns 502 *before* attempting to dial
the upstream. Rate limit, circuit breaker, per-model queue overflow,
deadline exceeded in a middleware layer, etc.

**Bet**: A 30s response timeout in the upstream layer (line 10:23:42
shows ~30s) trips a circuit-breaker. Trip lasts N requests, then
closes. Pattern of 6 consecutive failures + 2 ok + 2 consecutive
failures matches classic half-open circuit behavior.

**Explains**:
- Bursty pattern (open/half-open/closed circuit transitions).
- Silent 502s (circuit-rejected requests don't dial, don't log
  "proxy error", just short-circuit to 502).
- Why exactly 5 of 9 don't log: those are the circuit-trip rejections.

**Doesn't explain**:
- `:8080` (circuit breaker shouldn't change the dial address).
- Why no `circuit open` / `request rejected` log line either —
  unless that path is also silent.

**First experiment**: Grep `pkg/proxy` for `502`, `BadGateway`,
`http.Error`, `circuit`, `breaker`, `limiter`, `503`. Map every
path that writes a 5xx response. Add WARN log at each path. Re-run
soak.

## Cross-pollination

- **A ∧ C**: A real port-resolution bug (`:8080`) causes a fast dial
  failure → trips a circuit breaker → next 5+ requests get silent
  rejection until cooldown. Matches every observation. Most likely
  combined hypothesis if the kill-test rules out infra.
- **A ∧ B**: Stale kube-proxy rule from a previous Service revision
  has `:8080` DNAT; mirror-controller race when re-creating the
  Endpoints triggers iptables to flip back to that stale rule
  periodically. Rare, but worth a kube-proxy log peek.
- **B ∧ C**: Endpoints flap → proxy temporarily has no endpoint →
  falls into "no upstream" silent-502 path → circuit trips on
  subsequent requests.

## Convergence

**The kill-test forks the work cleanly.** Until it runs, every code
slice on pkg/proxy is a coin flip.

**Recommended next slice tree:**

1. **Slice α (kill-test)** — 30 min: stand up an in-proxy-pod curl
   loop sidecar (or `kubectl exec` background process) that hits
   pod-IP and Service-VIP in parallel during a 15-min soak. Capture
   to JSONL. Cross-reference failures. Outcome: definitive A/B
   between Framing B vs. Framings A∧C.
2. **Slice β-fork-on-result** — depending on slice α:
   - **If A∧C (proxy code)**: read `pkg/proxy` end-to-end for
     resolution + 5xx-write paths. Likely 1-2 hour code dive
     followed by a targeted MR. Expect to find both a port bug
     **and** at least one silent-fail path.
   - **If B (infra)**: pivot to `k8s-debug` skill; inspect
     kube-proxy/iptables on `cblevins-radeonvii` during a soak;
     investigate the dual EndpointSlice rule precedence; possibly
     a node-level repair.
3. **Slice γ (parallel, always-valuable)** — 30 min:
   silence the two known noisy log lines in `pkg/proxy`:
   - `v1 Endpoints is deprecated…` (every 10s) → migrate to
     EndpointSlice watch, or rate-limit / drop to DEBUG.
   - `serviceLabel shared by multiple models` (every minute,
     10 labels) → log once at startup, not per reconcile.
   - This unblocks every future log-driven investigation.
   - Should run regardless of α/β outcome.

**If forced to pick one slice and freeze the rest**: **Slice α**.
It is the lowest-cost highest-information move in the entire tree,
and it eliminates the most expensive failure mode (chasing the
wrong layer).

## Tech debt called out

- **Proxy log noise** (Slice γ): ~40 noise lines/min crushed the
  ratio below 1:10. Without this fix, we will keep missing real
  signal under the deprecation and `serviceLabel` spam.
- **Proxy still watches legacy `v1 Endpoints`**: in k3s 1.26.3 it
  works but is deprecated in 1.33+. Migration to `discovery.k8s.io/v1
  EndpointSlice` is a separate, larger slice.
- **Silent 5xx paths**: Slice C investigation should add log lines
  whether or not C is the answer — it's an instrumentation
  improvement.
- **Two EndpointSlices for selectorless Services**: known artefact;
  worth a documented `decision-journal` entry rather than a fix.

## Open questions / leads we don't have evidence for

- Does the proxy use per-model worker goroutines? If so, do they
  share state (which would amplify a cache bug)?
- Is there a Helm value or Model CR field that sets `:8080`
  anywhere — e.g. a metrics or healthz port that the proxy might
  accidentally use as the dial target?
- Does the gemma4-26b-a4b-gptq cluster (referenced in the
  `serviceLabel shared` spam) share any code path with the soak
  target?

## Links

- !487, !488, !489, !490, !491 — five-slice arc that built the
  soak harness, diag probe, triage tool, and soft-preflight gate.
- `.loom/ralph-gfx906-proxy-soak-diag-probe-2026-05-25.md` —
  earlier RALPH doc that motivated the diag probe.
- `/tmp/proxy-soak-491/soak.jsonl` — raw evidence from the
  2026-05-25 10:24Z smoke (not durable; reproduce by re-running
  the manifest).
- `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` — the soak Job.
- `scripts/proxy-soak-triage.py` — the triage tool.

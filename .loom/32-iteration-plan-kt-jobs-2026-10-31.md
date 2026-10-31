# KT-JOBS run plan (D2, 2026-10-31)

Kill-test for feature D2 of `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md`
§6.2. Matrix row `proxy-async-jobs-2026-10-31` in `.loom/60-validation-matrix.md` holds the pass
rule, written before any run. Harness: `scripts/kt-jobs.py` (stdlib only; verdict tests in
`scripts/test_kt_jobs.py`).

## Question

Can async jobs take the 7900xtx flex card from the warm lane only while they run, keep it while
they run and give it back, with nothing but the existing demand signal? If not, D2 grows a
reservation primitive (a `GPULease`-like hold owned by the job) and this row re-runs.

## Code under test

| MR | What |
|---|---|
| !1210 | typed error envelope, `Retry-After`, `X-FlexInfer-Activation`, `Prefer: wait=N` |
| !1213 | cold capability descriptor, `GET /v1/models/{name}` (the harness reads the warm lane's phase from it) |
| !1216 | `InferenceJob` CRD and RBAC |
| job runner MR | `background: true`, `/v1/jobs*`, the runner, MinIO/PVC store, webhooks |
| GC MR | manager TTL GC, `flexinfer_jobs_*` metrics, `flexinfer jobs` CLI |

How a job holds the card: the runner sends the job's request through the proxy's own handler. On
a cold creative lane it sits in the cold-start queue exactly like a client request, whose
heartbeat stamps the lane's `lastActiveTime` every interval, so the election sees demand from the
moment the job runs until it finishes. Diffusers lanes sharing `7900xtx-textgen` share one runner
slot, so Qwen-Image and Wan jobs run back to back and never ask the election for the card at the
same time. Jobs waiting for that slot are not demand.

## Preconditions (owner)

1. The XTX soak verdict is in (≥ 2026-11-06 00:15Z), !1203 is applied and KT-FLEX passed: the
   creative lanes are on the flex card at 500, the warm lane at 300 with `minReplicas 1`.
2. Storage (decision O2; MinIO recommended):
   - create bucket `flexinfer-jobs` on `daemon/minio` (192.168.50.240; confirm the S3 port, 9000
     assumed) and an access key limited to it;
   - create Secret `flexinfer-jobs-minio` in `flexinfer-system` with keys `accessKey` and
     `secretKey` (SOPS in platform/gitops, as the other flexinfer secrets).
   PVC alternative: a Longhorn `bulk-*` claim in `flexinfer-system`, `proxy.jobs.store: pvc`,
   `proxy.jobs.pvc.claimName: <claim>`.
3. Images: a proxy build from master containing the runner and GC MRs, and a controller build
   with the GC MR; request `pinned-<12>` Harbor tags for both digests.
4. `deploy/system/values-k3s.yaml` (one MR, Flux applies it):

   ```yaml
   proxy:
     image:
       tag: "<new tag>"
       digest: "sha256:<new proxy digest>"
     prometheusURL: "http://kube-prometheus-stack-prometheus.monitoring.svc:9090"  # confirm the Service name
     jobs:
       enabled: true
       store: s3
       s3:
         endpoint: "http://192.168.50.240:9000"
         bucket: flexinfer-jobs
         credentialsSecret: flexinfer-jobs-minio
   ```

   The chart's CRD (`charts/flexinfer/crds/ai.flexinfer_inferencejobs.yaml`) is installed by the
   HelmRelease (`crds: CreateReplace`) and the proxy Role gains InferenceJob access in the same
   roll.

## Smoke (after the roll, before the window)

```bash
P=http://flexinfer-proxy.flexinfer-system.svc.cluster.local
curl -s $P/v1/jobs                                   # {"object":"list","data":[],...}
curl -s $P/v1/models/qwen-image-21-gfx1100 | jq .flexinfer   # phase, eta, limits.image
curl -s $P/v1/chat/completions -H 'Content-Type: application/json' \
  -d '{"model":"qwen38-27b-xtx-warm-canary","background":true,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}'
kubectl -n flexinfer-system get ijob -o wide
flexinfer jobs list
```

The chat job should go `queued` → `in_progress` → `completed` within seconds on the warm lane, and
`GET /v1/jobs/<id>/content` should return the chat completion.

## Run (in the window, owner present)

The harness must run where the proxy can reach its webhook receiver. In-cluster:

```bash
kubectl -n flexinfer-system run kt-jobs --image=python:3.12-slim --restart=Never -- sleep 7200
kubectl -n flexinfer-system wait --for=condition=Ready pod/kt-jobs
IP=$(kubectl -n flexinfer-system get pod kt-jobs -o jsonpath='{.status.podIP}')
kubectl -n flexinfer-system exec -i kt-jobs -- python3 - \
  --proxy http://flexinfer-proxy.flexinfer-system.svc.cluster.local \
  --warm-model qwen38-27b-xtx-warm-canary \
  --image-model qwen-image-21-gfx1100 --video-model wan21-t2v-1p3b-gfx1100 \
  --webhook-url http://$IP:8765/hook --webhook-port 8765 \
  --cooldown 300 --out /tmp/kt-jobs.json < scripts/kt-jobs.py
kubectl -n flexinfer-system cp kt-jobs:/tmp/kt-jobs.json kt-jobs-$(date -u +%Y%m%d).json
python3 scripts/kt-jobs.py --verdict kt-jobs-$(date -u +%Y%m%d).json
kubectl -n flexinfer-system delete pod kt-jobs
```

Expected wall time: 5 min baseline + up to the 300 s swap cooldown before the first render + 3
renders + 1 video back to back + the cancel round + 7 min tail, about 35–50 min.

Watch during the run:

```bash
kubectl -n flexinfer-system get ijob -w
kubectl -n flexinfer-system get model qwen38-27b-xtx-warm-canary qwen-image-21-gfx1100 wan21-t2v-1p3b-gfx1100 -w
```

Prometheus: `flexinfer_jobs_running`, `flexinfer_jobs_retries_total`,
`flexinfer_jobs_queue_wait_seconds`, `flexinfer_model_swap_duration_seconds`.

## Pass rule

As written in the matrix row (1)–(9). (9) needs the loom-core follow-up (Mills'
`isModelNotReadyResponse` reading `error.code`); run its unit test on the 503 body the harness
captured as `cold_probe`.

## Reading the result

- PASS: the existing demand signal holds the card; no reservation primitive. Record the verdict in
  the matrix row and as `flexinfer.ai/promotion-evidence` on nothing (jobs are a proxy feature,
  not a lane), and update §6.2's riskiest assumption to "held".
- FAIL on (4): the warm lane lost the card before the first job's flip (should not happen; the
  flip is the election's) or chat TTFT rose while the warm lane still served — look at proxy
  queueing (the job forwards share nothing with the warm lane) and the runtime pod's VRAM.
- FAIL on (5) or (7): the card did not come back. If the creative lane kept it with no job
  running, a job leaked demand (check `flexinfer_jobs_running`, the runner's heartbeat and
  in-flight count); if the warm lane could not reclaim it inside the cooldown, the election needs
  a release signal or a reservation the job owns.
- Flapping (warm ↔ creative more than once per job) means two demand sources competed: check
  that only one job ran on `gpu/7900xtx-textgen` at a time.

## Rollback

`proxy.jobs.enabled: false` (jobs stop; queued `InferenceJob`s stay until re-enabled or deleted)
and the previous proxy pin (`20261030-153940`,
`sha256:e345d649ce009ea75a6d5f5370e51d4f4e6f5b8176ce2a7f874e92950549cad6`). The synchronous paths
do not change either way.

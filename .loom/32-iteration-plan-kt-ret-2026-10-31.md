# KT-RET (K): retrieval plane failover, owner paste plan (D6)

Date: 2026-10-31. Feature D6 of `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md`
(§6.6, brief §9 D6). Matrix row: `gfx906-retrieval-swap-safety-2026-10-31` in
`.loom/60-validation-matrix.md`; its pass rule was written before any run and amended for the park
before (K). Harness: `scripts/kt-ret-retrieval-swap.py` (lands with !1215).

## Where D6 stands

| Part | State |
|---|---|
| (R) reproduction | **Done 2026-10-31 16:12–16:26Z.** 0 client errors in 1,596 gte requests across 6 forced Humanizer swaps. Humanizer (11,000 MB) evicts only Orpheus; the 16,384 MB budget keeps bge + reranker + gte resident beside it. The p99 rise (≤ 1.84×) is head-of-line blocking behind 64-input batches: singles with no batch in flight ran p95 85 ms during swaps. |
| (a) swap-safe replay | !1215: `internal/proxy/swap_replay.go`, `X-FlexInfer-Replayed`, `flexinfer_proxy_replays_total{lane,outcome}`. Live once the proxy rolls. |
| (b) eviction grace | **Dropped**: (R) reproduced no kills. |
| (c) twin by failover | This MR: `flexinfer.ai/unavailable-reroute-to: gte-qwen2-1p5b-cpu-5930k` on the VII gte lane, twin CPU request 2 → 4. Admission reroute needs !1204 (G1). |
| (d) dashboard | platform/gitops `feat/flexinfer-retrieval-dashboard`: Grafana "FlexInfer Retrieval Plane" (`services-flexinfer-retrieval`). |

CPU twin capacity, measured at its 6 threads (6-core limit): ~250 ms per production-shaped single
(~8 tokens), ~4 req/s ceiling closed loop (CFS-throttled at the limit); open loop through the proxy
2 req/s → p95 0.93 s on 3.0–3.7 cores, 3 req/s → p95 1.85 s on 4.6–5.1 cores. A 64-input batch
takes 30 s on the twin. Production gte traffic is ~1.33 req/s of single ~8-token inputs. More
threads were not measured (a throwaway `k3s ctr` probe on the node was refused to the agent
session); the 6-core limit is what the curve above was taken at.

## Preconditions for (K)

1. !1215 merged and the proxy rolled to an image that contains `internal/proxy/swap_replay.go`.
2. !1204 merged and the proxy rolled with it. Without G1 a parked gte lane fast-fails new requests
   with 503 `model_parked` (the park below stamps `primary/bge-large-radeonvii`), and (K2) fails.
3. This MR merged (Flux applies `./deploy` within 5 min), or step 1 below pasted.
4. No other window work on the Radeon VII at the same time (!1207 ASR lane).

## Paste sequence (owner)

Context: `export KUBECONFIG=platform/gitops/.kube/k3s.yaml`. The harness runs on `cblevins-5930k`
from `~/kt-ret-20261031`; the copy there is current.

1. Only if this MR is not merged yet, annotate the VII lane by hand (Flux keeps a field it does not
   own):

   ```bash
   kubectl -n flexinfer-system annotate model gte-qwen2-1p5b-radeonvii flexinfer.ai/unavailable-reroute-to=gte-qwen2-1p5b-cpu-5930k --overwrite
   ```

2. Check both lanes are Ready:

   ```bash
   kubectl -n flexinfer-system get model gte-qwen2-1p5b-radeonvii gte-qwen2-1p5b-cpu-5930k
   ```

3. (K1) swaps with the replay build, about 14 min:

   ```bash
   ssh cblevins-5930k 'cd ~/kt-ret-20261031 && python3 -I ~/kt-ret-retrieval-swap.py run --proxy http://10.43.49.107 --lane http://10.43.153.107:8000 --runtime http://10.42.8.233:8080 --out k1 > k1.log 2>&1'
   ```

4. (K2) park, about 8 min. Start the harness in one terminal; it prints
   `PARK gte-qwen2-1p5b-radeonvii NOW` 2 min in and `UNPARK ... NOW` 5 min later:

   ```bash
   ssh cblevins-5930k 'cd ~/kt-ret-20261031 && python3 -I ~/kt-ret-retrieval-swap.py run --proxy http://10.43.49.107 --lane http://10.43.153.107:8000 --runtime http://10.42.8.233:8080 --out k2 --baseline-min 2 --swap-min 0 --swaps 0 --park-minutes 5 --batch-period 0 2>&1 | tee k2.log'
   ```

   At PARK, suspend Flux for the models (it would revert the patch within 5 min), then make gte's
   VRAM estimate too large for the group budget so the election parks it and the runtime unloads it:

   ```bash
   flux -n flux-system suspend kustomization flexinfer-models
   ```

   ```bash
   kubectl -n flexinfer-system patch model gte-qwen2-1p5b-radeonvii --type merge -p '{"spec":{"gpu":{"vramEstimateMB":20000}}}'
   ```

   At UNPARK, restore it and hand the Model back to Flux:

   ```bash
   kubectl -n flexinfer-system patch model gte-qwen2-1p5b-radeonvii --type merge -p '{"spec":{"gpu":{"vramEstimateMB":2600}}}'
   ```

   ```bash
   flux -n flux-system resume kustomization flexinfer-models
   ```

5. Tell the session the runs are done; it reads `~/kt-ret-20261031/k1` and `k2` (`summary.json`,
   `requests.jsonl`, `runtime.jsonl`) and writes the verdict.

## Pass rule (from the row)

- 0 client-visible 5xx through the proxy in K1 and K2 (replays allowed, counted).
- K1: proxy p99 during swaps ≤ 2× the baseline p99, per stream.
- K2: the twin serves the full interactive rate (2 req/s) for 5 min with p95 ≤ 2 s; every answered
  park-phase request carries `X-FlexInfer-Unavailable-Reroute` or `X-FlexInfer-Replayed`. The
  Service stream measures the VII engine and fails by design while parked; it is not part of the
  park pass.

Expected: K1 replays nothing (nothing fails, as in (R)); K2 replays the requests in flight at the
park (drain 503 → `gte->twin`) and reroutes the rest at admission; the unpark reloads gte in about
4 s while new requests wait in the cold-start queue.

## Rollback

- Twin failover: revert this MR, or
  `kubectl -n flexinfer-system annotate model gte-qwen2-1p5b-radeonvii flexinfer.ai/unavailable-reroute-to-`.
- Replay: `FLEXINFER_PROXY_SWAP_REPLAY=false` on the proxy Deployment, or revert !1215.
- A park left in place by mistake: restore `vramEstimateMB: 2600`, resume `flexinfer-models`, then
  `flux -n flux-system reconcile kustomization flexinfer-models --with-source`.

## After (K)

- Record the verdict in the row (`promotion_decision`) and the lane's `promotion-evidence`
  annotation, and add a line to `.loom/00-index.md`.
- jobsearch-app can move `VECTOR_EMBEDDING_MODEL` back to `gte-qwen2-1p5b-radeonvii`
  (services/jobsearch-app!232, `docs/embeddings-gpu-lane-2026-10-31.md`). Its daemon embeds one text
  per request today. `generate_embeddings_batch` has no callers; a backfill that adopts it should
  keep `VECTOR_EMBEDDING_BATCH_SIZE` ≤ 16. Each input costs ~23.5 ms of the VII lane, so the
  default 100 would block every other gte caller for ~2.35 s, and 64 inputs cost 30 s on the twin
  during a failover.

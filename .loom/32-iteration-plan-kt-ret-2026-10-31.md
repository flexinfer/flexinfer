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
   VRAM estimate too large for the group budget so the election parks it and the runtime unloads it.
   *As run on 2026-11-02 (K2).* 20000 is above 95 % of the card, so before !1256 the controller
   refused the Model and left the engine loaded. Use § K2 re-run below.

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

## K2 re-run (after K2, 2026-11-02)

K2 passed availability (635/635, 0 5xx) and failed latency (park p95 8,484 ms) on one 60 s stall.
The two follow-ups:

- **The park did not unload the engine: a controller bug, fixed in !1256.** `vramEstimateMB 20000`
  is above 95 % of the 16,384 MB card. `validateVRAMFit` therefore marked gte Failed and returned
  before `reconcileViaRuntime`, the only path that unloads an in-pod lane, while the election had
  already dropped gte from the card's VRAM accounting. A refused Model now leaves the runtime. A
  budget park (an estimate that fits the card but not the room beside the leader) already unloaded,
  and is the park a real burst causes. The re-run uses that park: 15000, since bge 1500 + reranker
  700 + 15000 > 16384 and 15000 ≤ 95 % of the card.
- **The stall was head-of-line blocking at the twin.** One rerouted production input of 956 tokens
  held the twin's single llama.cpp decode for 12.4 s (16:45:36.98–49.37Z, 82.5 tokens/s at 6
  threads). Every slot waited behind it, three production clients cancelled, and 41 park requests
  ran over 2 s against the 31 the p95 allows; without them the park p95 is 845 ms. The twin carried
  ~5.6 req/s, not 2: rerouted production added ~3.6 req/s (daytime gte runs 2.4–4.5 req/s). The
  host is a 14-core / 28-thread Xeon E5-2680 v4 with p50 2.7 / p99 11.4 busy CPUs, so Draft !1257
  gives the twin 12 threads and a 14-core limit (request unchanged). Thread scaling is not measured
  yet; step 2 measures it before the park.

Preconditions: !1256 in the running controller (for the unload), !1257 merged in the window.
Context: `export KUBECONFIG=platform/gitops/.kube/k3s.yaml`.

1. Mark !1257 ready and merge it, then check that the twin rolled with `"--threads","12"` and a CPU
   limit of `14`:

   ```bash
   flux -n flux-system reconcile kustomization flexinfer-models --with-source
   ```

   ```bash
   kubectl -n flexinfer-system rollout status deploy/gte-qwen2-1p5b-cpu-5930k --timeout=5m
   ```

   ```bash
   kubectl -n flexinfer-system get deploy gte-qwen2-1p5b-cpu-5930k -o jsonpath='{.spec.template.spec.containers[0].args}{"\n"}{.spec.template.spec.containers[0].resources.limits.cpu}{"\n"}'
   ```

2. Twin-only check, about 3 min, with nothing touched on the VII. Run short singles at concurrency
   1 and 4, then long singles of 550 words (about 560–950 tokens, inside the 1024 context). Then read the twin's prompt
   throughput:

   ```bash
   ssh cblevins-5930k 'cd ~/kt-ret-20261031 && python3 -I ~/kt-ret-retrieval-swap.py capacity --url http://10.43.94.177:8080 --concurrency 1 4 --seconds 30 --out cap-t12 && python3 -I ~/kt-ret-retrieval-swap.py capacity --url http://10.43.94.177:8080 --concurrency 1 --seconds 30 --words 550 --out cap-t12-long'
   ```

   ```bash
   kubectl -n flexinfer-system logs deploy/gte-qwen2-1p5b-cpu-5930k --since=3m | grep -E "prompt processing|stop processing: n_tokens = [5-9][0-9][0-9]" | tail -4
   ```

   Go on at ≥ 140 tokens/s (K2 at 6 threads: 82.5). At ≤ 100, revert !1257 and stop.

3. The K2 twin stream with the park, about 8 min. The gfx906 runtime pod has moved since K2
   (10.42.8.233 then), so read its IP and put it in `RUNTIME_POD_IP`:

   ```bash
   kubectl -n flexinfer-system get pod -o wide | grep flexinfer-runtime-gfx906
   ```

   ```bash
   ssh cblevins-5930k 'cd ~/kt-ret-20261031 && python3 -I ~/kt-ret-retrieval-swap.py run --proxy http://10.43.49.107 --lane http://10.43.153.107:8000 --runtime http://RUNTIME_POD_IP:8080 --out k2b --baseline-min 2 --swap-min 0 --swaps 0 --park-minutes 5 --batch-period 0 2>&1 | tee k2b.log'
   ```

   At `PARK gte-qwen2-1p5b-radeonvii NOW`:

   ```bash
   flux -n flux-system suspend kustomization flexinfer-models
   ```

   ```bash
   kubectl -n flexinfer-system patch model gte-qwen2-1p5b-radeonvii --type merge -p '{"spec":{"gpu":{"vramEstimateMB":15000}}}'
   ```

   gte goes `Preempted` (`primary/bge-large-radeonvii`) and the runtime unloads it. The Service
   stream fails from then on, by design.

   At `UNPARK gte-qwen2-1p5b-radeonvii NOW`:

   ```bash
   kubectl -n flexinfer-system patch model gte-qwen2-1p5b-radeonvii --type merge -p '{"spec":{"gpu":{"vramEstimateMB":2600}}}'
   ```

   ```bash
   flux -n flux-system resume kustomization flexinfer-models
   ```

4. Tell the session the run is done. It reads `~/kt-ret-20261031/k2b` and the twin's CFS throttle
   ratio, and records the verdict in the row. The pass rule is unchanged. The twin's park load
   includes rerouted production, so a park with several long production inputs can still miss p95.
   In that case the next lever is a smaller `ubatchSize` on the twin, which bounds each decode step.

## Rollback

- Twin headroom: revert !1257 (Flux restores 6 threads and the 6-core limit).
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

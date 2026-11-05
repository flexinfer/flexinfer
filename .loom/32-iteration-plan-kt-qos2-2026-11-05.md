# KT-QOS2: a smaller vLLM per-step prefill budget behind the seat reserve (2026-11-05)

Status: **pre-registered 2026-11-05, before any run.**
- Pass rule: matrix row `workhorse-qos-prefill-budget-2026-11-05`.
- Re-plan of KT-QOS: row `workhorse-qos-priority-2026-10-31`, result `.loom/70-result-kt-qos-v033-2026-11-05.md`
  (!1302).
- The owner chose this engine-only test on 2026-11-05 over a proxy admission gate and over proxy "prefill
  preemption". §1 says why the gate was dropped.

The run happens after the XTX queue clears:
1. the S7 pool canary;
2. the 2026-11-06 owner window's XTX phase (`.loom/32-iteration-plan-owner-window-2026-11-06.md`, D0–D4).

No proxy code and no proxy roll: both arms use the live proxy's existing seat reserve.

## 1. Why the proxy admission gate was dropped

The re-plan first proposed a gate in flexinfer-proxy: while an interactive request on a lane is in prefill (no first
response byte yet), hold the admission of new background requests to that lane. On this posture the gate would never
hold anything.

**From the code.** `internal/proxy/qos_gate.go` (unchanged from the live proxy 72628e0e through master) counts every
admitted request in `inflight`, interactive ones included. It admits a background request only while
`inflight < seats − reserve`. With `maxNumSeqs 2` and reserve 1 that limit is 1, so a background request is admitted
only when nothing at all is in flight. An interactive request in prefill is in flight. The reserve therefore already
refuses every background admission the gate would hold.

**From KT-QOS's own proxy logs.** The proxy's `request_usage` line is written at the first body byte (lane breaker on).
Its `duration_ms` is TTFT from the forward, and the request id's numeric prefix is the proxy arrival in ns. The saved
exports are `.loom/kt-qos-v033-2026-11-05/proxy-usage-C.json` and `-D.json`. Reproduce with
`python3 scripts/kt-qos-priority.py --collisions <export>`.

| Arm | bg requests | bg TTFT p50 / max | bg forwarded while an interactive was before its first byte | interactive > 0.9 s | … of them forwarded while a bg request was before its first byte |
|---|---:|---|---:|---:|---:|
| C (fcfs, reserve 1) | 67 | 1.754 / 1.779 s | **0** | 9 | **9** |
| D (priority, reserve 1) | 66 | 1.766 / 2.092 s | **0** | 10 | **10** |

**Every slow turn arrived during a background prefill that was already running.** The interactive was forwarded
Δ = 0.0–1.7 s after the background, while that background's ~1.9K-token prompt ran as one ~1.75 s engine step
(`max_num_batched_tokens 4096` fits it whole). The interactive then waits out that step:

| Δ (s) | TTFT (s) |
|---:|---:|
| 0.14 | 2.14 |
| 0.41 | 1.89 |
| 0.71 | 1.59 |
| 1.15 | 1.16 |
| 1.45 | 0.83 |
| 1.74 | 0.61 |

That is TTFT ≈ 2.3 s − Δ. The background was admitted before the interactive existed, so an admission gate cannot
prevent it. The skew toward ~2 s (small Δ) is phase-locking between the harness's 10 s interactive clock and the
background cycle. It is not same-step batching.

## 2. What the 0.33 scheduler allows

Read from `vllm/v1/core/sched/scheduler.py` and `vllm/config/scheduler.py` at v0.33.0.

- **Running requests are scheduled first.** An in-progress background prefill takes `min(remaining, token budget,
  threshold)`, and a waiting interactive gets only what is left. A smaller `maxNumBatchedTokens` alone therefore leaves
  the interactive behind every remaining background chunk.
  - There is an extra catch in align mode. The leftover (512 − 416 = 96 tokens) is below the 416-token block. The
    mamba-aligned split then rounds the interactive's chunk to 0 (`_mamba_block_aligned_split`: an end that is not the
    prompt's last chunk must be block-aligned when `block_size ≤` the chunk limit).
- **`long_prefill_token_threshold` caps a request's chunk only when more than one request is eligible.** Eligible means
  running + waiting. A lone prefill uses the whole budget. `long_prefill_token_threshold_adaptive` defaults to false.
  With T = 256 < block 416, a block-aligned start runs a 256-token sub-block chunk, and the next chunk stops at the
  block boundary (160).
- **What the budget arm should do.**
  - A lone background prefill runs in 416-token chunks (512 floored to the block), ~0.35–0.45 s each.
  - An interactive arriving mid-prefill then shares two short steps with it:
    - background 256 + interactive 256;
    - background 160 + interactive 164 (the prompt's last chunk is exempt from alignment).
  - The ~420-token idle interactive prompt fits one 512 chunk, so idle TTFT should not move.
- **Validation.** `max_num_batched_tokens ≥ max_num_seqs` and threshold ≤ `max_model_len` hold. Spec decode only warns
  below 8192 scheduled tokens. Align mode adds no budget constraint. The new compile range (upper bound
  `max_num_batched_tokens`) misses the warm compile cache, so the budget arm compiles cold.

## 3. Arms

Both arms run on one canary, one boot apart, in one window. Both are fcfs, through the live proxy, with
`flexinfer.ai/qos: proxy` (seat reserve 1).

| Arm | Label | `maxNumBatchedTokens` | `longPrefillTokenThreshold` | How |
|---|---|---:|---:|---|
| Control | `control` | 4096 | — | the canary file as written |
| Budget | `budget` | 512 | 256 | `flexinfer_update_model config={"maxNumBatchedTokens":512,"longPrefillTokenThreshold":256}` (restarts the pod) |

**Harness.** `scripts/kt-qos-priority.py`, the KT-QOS harness, with four additions that are off by default. Each arm
runs, in order:
1. parity: three greedy 256-token prompts;
2. `--idle-warmup 2` discarded interactive requests;
3. the idle baseline: 10 interactive requests;
4. load: two background workers (1,024 tokens, `ignore_eos`, ~1.9K-token nonce prompts), plus one interactive request
   (64 tokens, ~420-token nonce prompt) every 10 s;
   - `--warm-sweep 120` s of this load is unmeasured;
   - then the 20 min measured window, over which engine counters, background gaps and interactive turns count;
5. long-prompt probes, after the window: `--long-probe-fillers 110,440,1540` (~2K, ~8K, ~28K tokens, 16 output tokens).
   Each size runs twice:
   - idle;
   - loaded, sent one second after a fresh background request's first token. That request is closed once the probe
     returns.

   Each size and condition gets one discarded warm rep (segmented attention compiles each new shape on first use),
   then 3 timed reps.

The warm-up and warm-sweep keep first-use compiles out of the idle p95 and the measured window. KT-QOS arm A's idle p95
was one such outlier.

## 4. Pass rule

The pass rule is the matrix row's, copied here:

- **(1)** budget arm interactive TTFT p95 under load ≤ 1.3 × its own idle p95;
- **(2)** budget idle p95 ≤ 1.10 × the control's, so a slower idle cannot loosen (1);
- **(3)** no background gap > 60 s;
- **(4)** aggregate decode ≥ 0.95 × the control's;
- **(5)** greedy parity hashes equal the control's;
- **(6)** MTP pos-0 ≥ 0.75 and within 0.02 of the control's;
- **(7)** long-prompt cost cap: at every probed size, idle and loaded, the budget arm's median TTFT ≤ 1.25 × the
  control's.
  - A loaded rep that ran without a decoding background request fails the cap and is rerun.

`--verdict-qos2 control.json budget.json` applies all seven.

**Validity.** The control repeats KT-QOS arm C (same image, card and harness, plus the warm-ups). If its aggregate
decode is outside ±5 % of C's 61.20 tok/s, or its idle p95 outside ±10 % of C's 0.575 s, the window is reported and not
judged. An arm is rerun if it is interrupted by a controller or proxy roll, or by a creative-lane request: those lanes
sit at 500, above the canary's 480, and can preempt it. The result records the proxy and controller digests per arm.

**Prediction (from §2, written before the run).**

- Bar 1, the tail:
  - collisions stay ~10 % of turns;
  - each costs one lone 416-chunk residual (0–0.45 s) plus two shared steps (~0.45 + ~0.3 s);
  - so a collided turn lands at ~0.85–1.25 s against ~0.55 s idle;
  - p95 should fall from 1.67 s to ~0.9–1.1 s and the max from ~2.2 s to ≤ ~1.3 s;
  - **bar (1) is still expected to fail, at ~1.5–2.0 × idle**.
- Bars (2), (4), (5) and (6) pass.
- Long prompts, bar (7):
  - idle within the cap (+3–15 %: 68 chunks of 416 instead of 8 of 3,744, compute-bound either way);
  - loaded, the probe prefills ≤ 256 tokens per step beside the decoding background request, so +15–45 % at 8K and
    28K. **The cost cap is expected to fail on the loaded 28K probe.**

**Decision rule.**
- **Every bar passes:** a Draft MR sets `flexinfer.ai/qos: proxy` with `maxNumBatchedTokens 512` and
  `longPrefillTokenThreshold 256` on the 5930k workhorse. It is held for an owner window.
- **Bar (1) fails and the rest pass:** no enable MR. The result reports the tail against the control. Adopting the
  reserve and budget on a re-worded bar (an absolute TTFT, or a p95 multiple) is a new row written before its run, not
  a re-reading of this one.
- **The cost cap (7) fails:** no enable MR, whatever (1) says.

## 5. Window procedure

**Pre-checks, all must hold before the owner applies the canary.**

```bash
export KUBECONFIG=/Users/cblevins/workspace/platform/gitops/.kube/k3s.yaml
# 1. No other debug canary on the group, and nothing at >= 480 holding it. The on-demand creative
#    lanes sit at 500 and are expected while Idle (the S7 pool canary is gone; no D-step lane is loaded).
kubectl get models.ai.flexinfer -n flexinfer-system -o json | jq -r '.items[] | select(.spec.gpu.shared=="7900xtx-textgen" and .spec.gpu.priority >= 480) | select(((.metadata.annotations["flexinfer.ai/promotion-state"] // "") | startswith("debug-canary")) or (.status.phase != "Idle")) | "\(.metadata.name) \(.spec.gpu.priority) \(.status.phase)"'   # prints nothing
# 2. No GPULease left from KT-SWAP
kubectl get gpuleases.ai.flexinfer -A   # none on cblevins-7900xtx
# 3. The controller roll !1289 (A1) has merged and soaked: this canary declares vllmVersion
glab mr view 1289 -R services/flexinfer | grep -i '^state'   # merged (otherwise wait, or plan to delete the canary before A1)
# 4. Record the proxy and controller digests the arms will run on
kubectl -n flexinfer-system get pods -o json | jq -r '.items[] | select(.metadata.name | test("^flexinfer-(proxy|controller)")) | "\(.metadata.name) \(.status.containerStatuses[0].imageID)"'
```

Also check the shared notes for "XTX free" from the S7 session and the window runbook.

If D2/D3 (!1235/!1236) have moved the XTX warm lane and Qwen-Image in-pod, check two things once the canary is applied:
- the canary pod schedules and goes Running;
- `flexinfer_get_model qwen38-27b-xtx-warm-canary` reports it unloaded.

If the canary sits Pending on `amd.com/gpu`, stop and ask. The in-pod layout may hold the device-plugin slots.

**Owner, one paste:**

```bash
kubectl apply -f deploy/debug/qwen38-27b-qos-canary.yaml
```

**Agent, per arm.**

Wait for Ready. Expect the known cold-compile pattern: one 32K KV-check crash, then the KVPoolRecheck relaunch, then 0
restarts. Confirm `max_num_batched_tokens` (and, for the budget arm, `long_prefill_token_threshold`) on the boot line.
Then:

```bash
POD=deploy/qwen38-27b-qos-canary; NS=flexinfer-system; ARM=control   # then ARM=budget
kubectl -n $NS exec -i $POD -- sh -c 'cat > /tmp/kt-qos.py' < scripts/kt-qos-priority.py
kubectl -n $NS exec $POD -- sh -c "nohup python3 /tmp/kt-qos.py \
  --endpoint http://flexinfer-proxy.flexinfer-system.svc.cluster.local \
  --metrics http://localhost:8000/metrics --model qwen38-27b-qos-canary --arm proxy --label $ARM \
  --idle-warmup 2 --warm-sweep 120 --long-probe-fillers 110,440,1540 \
  --out /tmp/kt-qos2-$ARM.json > /tmp/kt-qos2-$ARM.log 2>&1 &"
# ~30 min later
kubectl -n $NS exec $POD -- cat /tmp/kt-qos2-$ARM.json > .loom/kt-qos2-2026-11-05/kt-qos2-$ARM.json
```

Between arms:
1. Copy the control JSON out first, since the patch restarts the pod.
2. Patch the budget arm: `flexinfer_update_model qwen38-27b-qos-canary config={"maxNumBatchedTokens":512,"longPrefillTokenThreshold":256}`.
3. Wait for Ready again.

**Proxy-log check per arm.** Export the arm window's `request_usage` lines for the canary from Loki, with the MCP
`loki_query_range` tool (limit 400, forward), using this query:

```logql
{namespace="flexinfer-system", container="proxy"} |= "qwen38-27b-qos-canary" |= "request_usage" | json | line_format "{{.qos_class}} {{.duration_ms}} {{.request_id}}"
```

Then run `python3 scripts/kt-qos-priority.py --collisions <export>`. Under the budget arm the slow turns should still
overlap a background prefill, but with a smaller penalty.

**Verdict and cleanup.**

```bash
python3 scripts/kt-qos-priority.py --verdict-qos2 .loom/kt-qos2-2026-11-05/kt-qos2-control.json .loom/kt-qos2-2026-11-05/kt-qos2-budget.json
```

Then `flexinfer_delete_model qwen38-27b-qos-canary`, then one proxied `xtx-warm` request, which hands the card back to
the warm lane. Write "XTX free" in the shared notes.

**Time.** Each arm is ~27 min: parity and idle ~1 min, 2 min warm sweep, 20 min window, ~5 min of long probes. Add the
first Local-cache stage and boot, and the budget arm's cold compile. Budget about 1.5 h of XTX time.

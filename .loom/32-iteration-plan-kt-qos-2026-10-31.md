# KT-QOS — vLLM priority scheduling vs the proxy seat reserve on the workhorse posture

Feature D3 (caller QoS classes), `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md`
§6.3 and brief D3. Matrix row `workhorse-qos-priority-2026-10-31` in `60-validation-matrix.md`
(written before the run). Canary `deploy/debug/qwen38-27b-qos-canary.yaml`; harness
`scripts/kt-qos-priority.py` (tests: `scripts/test_kt_qos_priority.py`).

## 2026-11-05 re-target: vLLM 0.33.0 on the XTX slot (supersedes the placement below)

Owner decision P2 (`.loom/10-research-backend-currency-gfx1100-gfx906-2026-11-04.md` §6.2): judge
KT-QOS and KT-SO on vLLM 0.33.0-rocm72, which passed parity against 0.32 on the XTX (row
`xtx-vllm-v033-parity-2026-11-05`, `.loom/70-result-s3-vllm-v033-parity-2026-11-05.md`).
0.33 fixes priority self-preemption (#61976), so a 0.32 failure could have been upstream's bug
rather than this design.

- **Image**: `registry.harbor.lan/flexinfer/runtime@sha256:29378fe072741a9be5c23333762e37759c027dde5e445e9bc4a1b37bd730cf69` with `spec.config.vllmVersion: "0.33.0"`. The live controller (1286a2f1)
  predates !1281 and ignores the key: the pod renders two dead `VLLM_*` names and
  `--mamba-cache-mode` as warnings (`failOnEnvironValidation: false`), as S3 arm B did. If the
  controller rolls to a !1281 build mid-window, the pod template changes and the arm in progress
  is rerun.
- **Placement**: the XTX warm lane's slot, as S3 ran: `cblevins-7900xtx`, `7900xtx-textgen`,
  count 2 / `hipVisibleDevices 0`, `vramEstimateMB 23200`, priority 480. The image is prewarmed only
  there; the 5930k root has 194 GB free (under the 200 GB pull rule) and **its workhorse is never
  preempted**. The warm lane (mills-warm, mills-council, weaver) is off the XTX for the window.
- **Caches**: a new Local cache PVC per Model (19 GB, ~11 min to stage); the compile cache is S3
  arm B's XTX path `compile-cache-qwen38-27b-v033-parity-b-xtx` (vLLM loads `/models` for every
  Model, so the hash matches and the boot is warm). A first boot that still fails the 32K KV check
  once and recovers through the warm restart and KVPoolRecheck is this card's known cold-compile
  pattern, not a failure.
- **This card's pool** at the posture is ~44.6K tokens (S3 arm B), not 52.9K. Absolute decode is
  ~3 % below the 5930k (2100 MHz sclk cap); every bar is relative, so none moves.
- **One window, one paste for both kill-tests** (after S4 step 0 and S6 KT-OCTAVE have released the
  XTX, and only once `kubectl get models.ai.flexinfer -n flexinfer-system` shows no other canary on
  `7900xtx-textgen` at priority ≥ 480):

  ```bash
  kubectl apply -f deploy/debug/qwen38-27b-so-canary.yaml -f deploy/debug/qwen38-27b-qos-canary.yaml
  ```

  KT-SO runs first (`minReplicas 1`); the KT-QOS canary waits at `minReplicas 0` while its Local
  cache stages. The agent deletes the KT-SO canary (`flexinfer_delete_model`), raises the KT-QOS
  canary with `flexinfer_update_model qwen38-27b-qos-canary min_replicas=1`, runs its arms, deletes
  it, and confirms `qwen38-27b-xtx-warm-canary` is Ready again (one proxied `xtx-warm` request if it
  stays Idle). Window ≈ 2.5 h.
- **Proxy arms C and D run in this window**: the live proxy (72628e0e, tag 20261103-172712)
  carries D3 MR 2 (!1212) and MR 3 (`feat/qos-vllm-priority`).
- **`--verdict` applies ±5 % decode to every arm**; for C and D the bar is ≥ 0.60 × arm A, judged by
  hand from their `decode_vs_fcfs`.
- **KV pair on this pool**: `--background-filler 1050 --interactive-filler 400` (two ~19K-prompt
  background sequences fit 44.6K alone; adding the ~7K interactive prompt overruns it).
- **Prediction**: the source read below is v0.32.0; re-read `vllm/v1/core/sched/scheduler.py` in the
  0.33 image (or the v0.33.0 tag) before the run and record any change with the result.


## What the code says before the run

The design assumed vLLM's `--scheduling-policy priority` "preempts lower-priority running
sequences". Read from the vLLM v0.32.0 source (`vllm/v1/core/sched/scheduler.py`,
`request_queue.py`):

- **No seat preemption.** The waiting loop stops admitting when
  `len(running) >= max_num_active_reqs` (`max_num_seqs` by default). Nothing in the scheduler
  evicts a running request to make room for a higher-priority waiting one.
- **Priority orders the waiting queue**: `PriorityRequestQueue` pops by `(priority, arrival_time)`.
- **Priority picks the KV victim**: when `allocate_slots` fails for a running request, `priority`
  preempts `max(running, key=(priority, arrival_time))` (the lowest-priority, newest request);
  `fcfs` preempts `running[-1]` (the newest, which on a mixed lane is often the interactive turn).
- The per-request field is `priority: int` on `ChatCompletionRequest` and `CompletionRequest`,
  default 0; any non-zero value is rejected unless the engine runs priority scheduling.

So on a two-seat lane with both seats running background work, an interactive turn waits for one of
them to finish under either policy. Priority helps only (a) when interactive requests are already
waiting in vLLM's queue alongside background ones and (b) when the KV pool runs out (the workhorse's
52,932-token fp16 pool holds less than two full 32K sequences, so two long requests do collide).

That is why D3 MR 2 adds a proxy seat gate: background and batch work may hold at most
`maxNumSeqs - reserve` seats (`flexinfer.ai/qos-interactive-reserve`, default 1), so an interactive
turn always finds a seat. KT-QOS measures both mechanisms.

## Arms

All arms run on the canary (the workhorse config, `deploy/debug/qwen38-27b-qos-canary.yaml`), one
harness run each: parity (3 greedy prompts), idle baseline (10 interactive requests alone), then
20 minutes of two background workers streaming 1,024-token generations back to back (`ignore_eos`,
~2K-token nonce prompts) plus one interactive request (64 tokens, ~400-token nonce prompt) every
10 s.

| Arm | Engine policy | Path | What it isolates |
|---|---|---|---|
| A | fcfs (boot) | direct to the pod, no `priority` | baseline, today's behaviour |
| C | fcfs | through the proxy, `X-FlexInfer-QoS` | proxy seat reserve alone (`flexinfer.ai/qos: proxy`) |
| B | priority (patched) | direct, body `priority` 0/10 | vLLM priority alone |
| D | priority | through the proxy | reserve + vLLM priority (the full D3 design) |

Optional KV-pressure pair (10 min each, only if the window allows): A-kv and B-kv with
`--background-filler 1300` (~23K-token background prompts; filler lines are ~18 tokens) and
`--interactive-filler 300` (~5K), so two background sequences (2 × ~24K with their output) plus an
interactive prompt overrun the 52.9K-token pool and vLLM must preempt. This is
the case where priority should pay: fcfs evicts the newest (interactive) request, priority the
background one.

C and D need a proxy image that contains D3 MR 2 and MR 3. Without it, run A and B (the riskiest
assumption) and leave C/D for the next window.

## Pass rule (from §6.3; also in the matrix row)

Per arm, against the same run's idle baseline and arm A:

1. interactive TTFT p95 ≤ 1.3 × the idle-lane TTFT p95;
2. background progress: no gap > 60 s on either worker;
3. aggregate decode (engine `vllm:generation_tokens` over the window) within 5 % of arm A
   (arms B, B-kv); arms C and D hold one seat back by design, so their bar is ≥ 0.60 × arm A;
4. greedy parity: the three parity hashes equal arm A's;
5. MTP position-0 acceptance ≥ 0.75 over the load window.

**Prediction (from the source read):** B fails bar 1 exactly like A (interactive TTFT p95 bound by
the remaining background generation, ~10–15 s at 1,024 tokens and ~70 tok/s; `preemptions` 0
because 2 × ~3K tokens never fills the pool) and passes 2–5. C and D pass bar 1; D ≈ C. In the KV
pair, B-kv beats A-kv on interactive TTFT.

**Decision rule for MR 4:**

- C passes → the workhorse gets the reserve. If D also passes and is within noise of C, MR 4 sets
  `schedulingPolicy: priority` (the KV-victim choice is free insurance); if D regresses on decode,
  parity or pos-0, MR 4 uses `flexinfer.ai/qos: proxy` instead.
- C fails bar 1 → the reserve does not protect interactive turns on this posture; do not ship MR 4,
  record why, and re-plan (e.g. a third seat at a smaller context).
- C fails bar 3 → the owner decides whether one background seat is acceptable for Mills (its
  research calls are 5-minute jobs) or the reserve should only apply during interactive bursts.

## Window procedure (owner present; quiet hour)

The canary takes the 5930k from the production workhorse (priority 510 > 500) for the whole window,
as S3/S3b did. Requests to the workhorse go to its overflow route or wait. Budget: boot ~3 min warm,
each arm ~25 min (parity + baseline + 20 min load), one relaunch ~3 min: **A+B ≈ 55 min, A+C+B+D ≈
110 min**, KV pair +25 min. `--duration 600` halves the load phase if the owner wants a shorter
window (the row records the duration used).

Owner pastes (agent sessions cannot apply Models):

```bash
kubectl apply -f deploy/debug/qwen38-27b-qos-canary.yaml
```

Then the agent (or the owner) activates and runs the arms. Each harness run goes in through
`kubectl exec` so it reaches the pod on localhost:

```bash
# Arm A (fcfs boot, direct)
kubectl -n flexinfer-system exec -i deploy/qwen38-27b-qos-canary -- \
  python3 - --endpoint http://localhost:8000 --model qwen38-27b-qos-canary \
  --arm fcfs --out /tmp/kt-qos-A.json < scripts/kt-qos-priority.py
# Arm C (fcfs boot, through the proxy; needs the D3 MR 2/3 proxy image)
kubectl -n flexinfer-system exec -i deploy/qwen38-27b-qos-canary -- \
  python3 - --endpoint http://flexinfer-proxy.flexinfer-system.svc.cluster.local \
  --metrics http://localhost:8000/metrics --model qwen38-27b-qos-canary \
  --arm proxy --out /tmp/kt-qos-C.json < scripts/kt-qos-priority.py
```

Arm B/D relaunch: `flexinfer_update_model` (MCP) on `qwen38-27b-qos-canary` with
`config={"schedulingPolicy":"priority"}`; wait for Ready, confirm `--scheduling-policy priority` in
the pod args, then:

```bash
kubectl -n flexinfer-system exec -i deploy/qwen38-27b-qos-canary -- \
  python3 - --endpoint http://localhost:8000 --model qwen38-27b-qos-canary \
  --arm priority --out /tmp/kt-qos-B.json < scripts/kt-qos-priority.py
kubectl -n flexinfer-system exec -i deploy/qwen38-27b-qos-canary -- \
  python3 - --endpoint http://flexinfer-proxy.flexinfer-system.svc.cluster.local \
  --metrics http://localhost:8000/metrics --model qwen38-27b-qos-canary \
  --arm proxy --out /tmp/kt-qos-D.json < scripts/kt-qos-priority.py
```

Copy the four JSON files out (`kubectl exec ... cat /tmp/kt-qos-*.json`), then judge them locally,
arm A first:

```bash
python3 scripts/kt-qos-priority.py --verdict kt-qos-A.json kt-qos-B.json kt-qos-C.json kt-qos-D.json
```

Close the window:

```bash
kubectl -n flexinfer-system delete model qwen38-27b-qos-canary
```

and activate the workhorse (`flexinfer_activate_model qwen38-27b-autoround-workhorse`). Operator
gates during the run: no HIP/HSA/OOM markers in the canary log, no restarts, KV pool line at boot
(`GPU KV cache size`) ≈ 52.9K tokens.

## MR 4 (after the verdict, window-gated)

- `deploy/models/qwen38-27b-autoround-workhorse.yaml`: `schedulingPolicy: priority` (or
  `flexinfer.ai/qos: proxy`, per the decision rule), with a comment citing the row. The change
  relaunches the dedicated Deployment (~3 min warm); schedule with the owner.
- `deploy/system/values-k3s.yaml`: `proxy.qos.callers: {loom-mills: background}`. Mills sends Go's
  default User-Agent and no caller header today, so it must identify itself first: set
  `User-Agent: loom-mills/<version>` (or `X-FlexInfer-Caller: loom-mills`) in
  `services/loom-core/pkg/mills/clients/flexinfer.go` (loom-core change, separate MR).
- gitops note for the LiteLLM alias in `platform/gitops/k3s/ai/litellm/`:
  `model_name: workhorse-background`, `litellm_params.model: openai/workhorse-background`, same
  `api_base` and timeout as `workhorse`. The proxy strips the suffix and routes to `workhorse` as
  `background`.
- Proxy image bump in `deploy/system/values-k3s.yaml` to a master build containing MR 2 and MR 3
  (prerequisite for arms C/D as well).

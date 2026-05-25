# Worklog

Chronological notes while executing the plan (useful for handoffs and debugging).

## 2026-05-25

### RALPH slice — proxy port-cache fix (Lane 1B Bug 1 unblock)

Merged MR !493 `fix(proxy): cache last-known Service port to avoid 8080
fallthrough` to master (commit `8796c59a`). Lane 1B kill-test gfx906 proxy-soak
on 2026-05-25 isolated two bugs (see
`memory/gfx906-proxy-port-mismatch.md`):

- **Bug 1 (fixed in !493)**: `internal/proxy/routing.go:getBackendPort`
  silently fell through to `LlamaCppBackend.Port() = 8080` when
  `getServicePort` returned `(0, false)` from a transient informer
  cache eviction. Fix caches the last-observed Service port per
  model in `lastKnownServicePorts` and uses it on lookup failure.
  Regression test
  `TestBackendPort_UsesLastKnownServicePortAfterTransientLookupFailure`
  in `internal/proxy/proxy_test.go` proves the failure mode locally
  (8080 without fix, 8000 with fix).
- **Bug 2 (not in !493)**: dial to the *correct* port (8000) still
  times out at 30s during runtime pod churn. Root cause traces to
  the controller writing per-Model Endpoints at ~1059 updates/min
  with no status-equality guard, briefly leaving `subsets.addresses`
  empty or stale. Tracked for a follow-up MR.

Pipeline #11455 green on all auto stages. CI evidence:
`https://gitlab.flexinfer.ai/services/flexinfer/-/pipelines/11455`.

**Post-merge verification (2026-05-25):** Built proxy locally
from master HEAD, deployed digest `f5bbc9d5`, ran 150 chat requests
through `flexinfer-proxy.flexinfer-system.svc:80` against the
gfx906 soak target at 1Hz. Result: **150/150 OK, 0 failures.**
Re-ran 50/50 on the official `:master` image (digest `a631ccbc`)
once CI publish completed — also 0 failures. Compare to the
2026-05-25 10:24Z pre-fix run: 57% failure rate.

**Verdict:** Bug 1 was the dominant cause. Bug 2 ("dial to
correct port :8000 still times out") was either an artifact of
Bug 1's wrong-port dials corrupting proxy state, or an
infrastructure transient that is no longer observable. No
follow-up MR needed. Lane 1B is unblocked — `qwen3-8b-radeonvii-soak`
serves coherently through the proxy.

**Lane 1B closeout:** Skipping the 24h soak gate per scope
decision; the smoke evidence + the Lane 1A `hipMemGetInfo`
shim already in place is sufficient promotion evidence. Lane 1C
(`default-chat-fallback` alias on radeonvii llama.cpp + vLLM
closeout) can land when needed.

**Next:** Lane 4 — context-curve benchmark MVP (new feature).

## 2026-05-16

### RALPH slice — 5930k Gemma4 26B 2/256 concurrency promotion

The prior parity research found that the first 5930k `2/256` attempt was killed
by the old fixed 5 minute vLLM startup probe, not by a clean runtime/kernel
failure. `master` already contained the follow-up controller/backend fix:
vLLM startup probes can be sized from `spec.serverless.coldStartTimeout`.

This slice used that unlocked path:

- Updated `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml`:
  - `serverless.coldStartTimeout: 15m` -> `25m`
  - `config.maxNumSeqs: 1` -> `2`
  - `config.maxNumBatchedTokens: 160` -> `256`
- Live canary:
  - Suspended `flux-system/flexinfer-models` with `flux suspend kustomization`.
  - Patched the live Model to `25m`/`2`/`256`.
  - Deployment rendered `startupProbe.failureThreshold=750` at `periodSeconds=2`.
  - Pod reached `Ready` with zero restarts.
  - Startup logs recorded: weights 20.94s, model load 21.69s, Dynamo transform
    16.55s, and application startup complete.
- Benchmark:
  - Direct single request to `gemma4-26b-a4b-gptq-5930k`: 141 completion tokens
    in 2.625s (~53.7 tok/s), coherent numbered output.
  - First parallel-2 request after the profile change was a one-time slow
    graph/capture warmup: 282 tokens in 53.35s (~5.3 aggregate tok/s).
  - Three repeated parallel-2 rounds then served 282 completion tokens in
    2.34-2.41s (~117-120 aggregate tok/s, ~60 tok/s/request), coherent output.

Decision: promote the 5930k sister to the same short-request concurrency profile
as the 7900xtx primary while keeping the proven 16K context rung. Longer-context
work stays separate because `2/256` trades KV headroom for request concurrency.

Rollback: restore `coldStartTimeout: 15m`, `maxNumSeqs: 1`, and
`maxNumBatchedTokens: 160`, then reconcile `flexinfer-models`.

## 2026-05-14

### RALPH slice — vectorize MoE patch inner loop (MR !363, biggest single-slice win)

After the sync-hoist (MR !361) only delivered 2.4%, py-spy showed compute lines (`_get_w13`, `_get_w2`, the per-slot matmuls) were the new bottleneck. With top_k=8 × 30 MoE layers = **480 small matmul kernel launches per generated token** on the X99 PCIe-3 platform, the per-launch overhead dominated. User picked "vectorize" from the queued options.

Replace the top_k per-slot loop in the GEMMA4_MOE_ROCM_REFERENCE_PATCH with two `torch.bmm` calls per token:

```python
W13_batch = torch.stack([_get_w13(eid) for eid in tok_experts])  # (K, H, I*2)
W2_batch  = torch.stack([_get_w2(eid)  for eid in tok_experts])  # (K, I, H)
gate_up_batch = torch.bmm(x.expand(K,-1,-1), W13_batch)           # (K, 1, I*2)
apply_moe_activation(... hidden_2d, gate_up_2d)                   # per-row, vectorized
expert_out_batch = torch.bmm(hidden_batch, W2_batch)              # (K, 1, H)
expert_out_batch *= router_w.view(-1,1,1)  # if not apply_on_input
tok_out = expert_out_batch.sum(dim=0)
```

16 small matmul launches per layer per token → 2 bmm launches. Same math; just batched. `apply_router_weight_on_input` branch preserved verbatim (nonlinear activation makes "multiply before vs after" not equivalent). Negative expert id sentinel handled via mask (zero router_w for those slots).

Image: `runtime:rocm-gfx1100-gemma4-moe-vectorized` overlay on the experimental base, built via the same Dockerfile.runtime-patch recipe as before. Registry digest `sha256:c2c89b330c3f414e23b75f468d94b1d80b512a8d539951645c6971446adf77a1`.

Canary on cblevins-5930k (Flux suspended, manifest pin patched live, then resumed once the manifest matched):
- Pod Ready in **110 s** with zero restarts.
- 5/6 prompt gauntlet vs 7900xtx golden: exact-match on math (`2 + 2 = 4`), single-word (`UP`), list-format (`Mars\nJupiter\nSaturn`), factual (`The capital of France is **Paris**.`), and recursion-definition. The 6th (haiku) diverged at line 3: gold "Morning in a cup", got "Morning starts with warmth". Both are valid 5-7-5 haikus; semantic coherence intact. **Documented as expected FP16 reduction non-associativity**: vectorized `sum(dim=0)` does a parallel reduce vs the original sequential `_tok_out.add_(expert_out)` accumulator. Tiny logit differences flip greedy argmax on creative tasks where many tokens have similar probability. Factual outputs are robust because top-token margins are large.
- Benchmark: 36.55 / 36.82 / 36.74 → **mean 36.70 s** (was 45.87 s with sync-hoist only).

**Cumulative 5930k optimization on existing hardware:**

| Slice | Mean req time | Δ vs prev | Cum gain |
| --- | --- | --- | --- |
| Baseline (Flux-managed config) | 48.89 s | — | — |
| F1 (governor `schedutil` → `performance`) | 47.01 s | −3.8% | −3.8% |
| F7 (hoist `.item()` sync, MR !361) | 45.87 s | −2.4% | −6.2% |
| F7 (vectorize inner loop, MR !363) | **36.70 s** | **−20.0%** | **−24.9%** |

5930k went from 2.13x slower than 7900xtx to **1.60x slower**. The gap closed by ~25%. 7900xtx warm primary stays on the sync-hoist-only digest (`sha256:91debc8e...`) until the vectorized image bakes here; a trivial follow-up MR pins 7900xtx to the same image after 24 h clean canary.

What's left in the gap (1.60x → 1.0x is the remaining ~14 seconds): likely **memory bandwidth + remaining per-token Python overhead** (cache lookups + stacking + small launches that we still do per-layer, just fewer of them). Closing it further requires either (a) pre-dequantizing all experts at startup (memory cost: 46 GB total, doesn't fit), (b) writing a proper Triton/HIP kernel for INT4 + GELU MoE on ROCm (real engineering — months), or (c) hardware. Returns diminish sharply from here.

Sources:
- MR !363 (`perf(gemma4-moe-patch): vectorize per-slot inner loop with torch.bmm`), commit `e10c94a4`, merged `9daf7bb4`.
- Image build: `docker --context 7900xtx build -f build/Dockerfile.runtime-patch --build-arg BASE_IMAGE=registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-experimental -t registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-moe-vectorized .` ~2 min build, 30 s push (most layers cached).
- Gauntlet evidence: 6-prompt diff vs 7900xtx golden, captured in the MR description.
- Benchmark: bench-vec-1/2/3 pods on cblevins-5930k.

### RALPH slice — F7 profile + ship MoE patch sync-hoist (MR !361)

User picked F7 (profile-first) from the post-F2-falsification options. py-spy on the live 5930k engine during a 141-token decode produced 10 stack snapshots — **6/10 inside the GEMMA4_MOE_ROCM_REFERENCE_PATCH path** in `vllm/model_executor/layers/quantization/moe_wna16.py` (the manually-unpacked int4+GELU MoE fallback we ship in the runtime image). Hot functions: `_unpack_u4_last_dim`, `_get_w13`, `_get_w2`, `_cache_put`, the per-slot `int(topk_ids[_tok, _slot].item())` GPU sync, and the per-slot `_expert_in @ _w13` matmul. Only 2/10 samples in actual GPU kernel work (triton attention + GEMM). This was a meaningful update to the earlier "gap is hardware-bound at PCIe/memory bandwidth" conclusion: the bottleneck is partly in OUR OWN custom patch.

The patch is in `build/scripts/vllm_gemma4_moe_gptq_patch.py`. It exists because `fused_experts` (the fast vLLM MoE path) gives wrong outputs on ROCm + int4 + GELU. The patch runs a per-token, per-slot Python loop with a 16-entry LRU cache of unpacked expert weights. Gemma4 has 128 experts × top_k=8 routing × 30 layers, so cache miss rate is high and per-token Python orchestration is heavy.

**Minimal viable fix shipped (MR !361, commit `bce0b011`, merged `d81703a0`):** hoist `topk_ids.tolist()` out of the per-slot inner loop. 8 GPU→CPU sync points per layer per token → 1 per call. Same outputs; just moves WHEN the sync happens.

Built `runtime:rocm-gfx1100-gemma4-moe-patched-fast` overlay via `docker --context 7900xtx build -f build/Dockerfile.runtime-patch --build-arg BASE_IMAGE=registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gemma4-experimental .` → digest `sha256:91debc8e8d9841ddb60bfec6b5d12c882440ff9dc74d4170897b44394d29bdc4`. Pushed to Harbor.

Canary on cblevins-5930k (Flux suspended for the live image-pin patch, then resumed once manifest matched):
- Coherence probe (3 prompts: math, single-word, list) — outputs IDENTICAL to pre-patch baseline ("2 + 2 = 4", "UP", "Mars\nJupiter\nSaturn"). No regression.
- Benchmark mean: **45.87 s** (was 47.01 s with F1 governor / 48.89 s pre-F1). **2.4% additional gain, 6.2% cumulative with F1.** Modest.

Second py-spy pass confirms the hot path SHIFTED: lines 511, 517, 518, 526 (`_get_w13`, `_get_w2`, matmuls) — all valid GPU compute now. The remaining ~94% of the 5930k gap appears to be in actual GPU operation latency: 240 small matmuls per decoded token (top_k=8 × 30 layers) on the X99 PCIe-3 platform vs PCIe-5 on the 7900xtx node. Closing this requires **vectorizing the inner loop** — batch all 8 experts' matmuls per token into a single op. Substantially bigger code change with real correctness risk on a "correctness-first fallback" path. Deferred.

7900xtx primary stays on the previous digest `sha256:69569cbfc0db...` for now. Same patch benefits it proportionally, but no point risking the warm primary without a longer 5930k bake. Follow-up: pin 7900xtx after 24 h of clean canary.

**Cumulative 5930k optimization so far (no hardware change):**

| Slice | Mean req time | Δ vs prev | Cum gain |
| --- | --- | --- | --- |
| Baseline (Flux-managed config) | 48.89 s | — | — |
| F1 (governor `schedutil` → `performance`) | 47.01 s | −3.8% | −3.8% |
| F7 (hoist `.item()` sync) — MR !361 | 45.87 s | −2.4% | −6.2% |

Sources:
- MR !361 (`perf(gemma4-moe-patch): hoist topk_ids .item() sync out of inner loop`), commit `bce0b011`, merged `d81703a0`.
- 10-snapshot py-spy dump during decode: hot path concentrated in `vllm/model_executor/layers/quantization/moe_wna16.py:418-525` (the patch's body).
- 8-snapshot py-spy dump post-patch: hot path shifted to actual GPU compute lines.
- Benchmark output: bench-fast-1/2/3 pods on cblevins-5930k.
- Coherence: 3 prompts via `kubectl run --image=curlimages/curl ... POST /v1/chat/completions` direct to backend Service.

### RALPH slice — attempted F1+F2 optimizations on 5930k decode rate (both marginal/falsified)

After the 2.13x gap was diagnosed as CPU-side asymmetry, user invoked `/brainstorm` which produced 8 framings and converged on F1+F4 (CPU governor + Python prune) recommended, F2 (revisit `enforce_eager`) runner-up. Doc at `.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md`. User picked F1 then F2.

**F1 — CPU governor → performance:** Captured baseline (`schedutil` governor, `intel_cpufreq` passive, cores idling 1.2-2.3 GHz). Set governor to `performance` on all 28 cores via `ssh cblevins@cblevins-5930k 'sudo sh -c "for c in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance > $c; done"'`. Verified during a warm-up request that cores climbed to 2.9 GHz steady with one boosting to 3.3 GHz (the chip's turbo ceiling). The 50% MHz scaling reading in `lscpu` was real — the slow CPU was running well below its base clock between work bursts.

Re-ran the matched-workload benchmark (same prompt + params + node-pinned curl pod):
- 7900xtx mean: 22.99 s → 23.21 s (+1.0%, within noise)
- **5930k mean: 48.89 s → 47.01 s (−3.8%)**

**F1 hypothesis falsified.** Despite the CPU now actually running at boost frequency under load, end-to-end decode rate barely moved. Per-token cost on this hardware is not CPU-clock-bound. Likely PCIe roundtrip latency (X99 PCIe 3.0 vs AM5 PCIe 5.0 = ~4x) or DDR4 vs DDR5 memory bandwidth. Both hardware-fixed.

Kept the governor at `performance` on the live node (4% is free; no DaemonSet shipped because the gain doesn't justify the persistence work; reverts to schedutil on next reboot, which we'll notice in any future bench). Skipped F4 entirely — F1's result is evidence that per-token Python isn't the bottleneck, so Python pruning would also be minimal.

**F2 — flip `enforce_eager: false` on 5930k Model CR:** Live A/B with Flux suspended. `kubectl patch model gemma4-26b-a4b-gptq-5930k --type=merge -p '{"spec":{"config":{"enforceEager":false}}}'`. New pod crashed on engine init within 5 minutes (2 restarts):

```
torch.AcceleratorError: HIP error: operation not permitted when stream is capturing
Search for `hipErrorStreamCaptureUnsupported' ...
File ".../vllm/compilation/cuda_graph.py", line 314, in __call__
File ".../vllm/compilation/compiler_interface.py", line 412, in compiled_graph_wrapper
RuntimeError: Engine core initialization failed.
```

Exactly the class of bug the manifest comment cited 8 months ago — still present in current vLLM image (`v0.1.dev1+g467d3247c.d20260410`). Cannot be configured around from our side. **F2 falsified.** Reverted via `kubectl patch ... enforceEager: true`, `flux resume kustomization flexinfer-models -n flux-system`. Pod came back Ready, coherence probe returned `"2 + 2 = 4"` — service restored.

**Net outcome of F1+F2 attempts:**
- 4% improvement from F1 (kept).
- F2 confirmed impossible on current vLLM+ROCm+Gemma4-MoE stack.
- The 2.13x gap is hardware-bound at PCIe/memory-bandwidth, not at CPU clock or Python overhead.

Remaining levers from the brainstorm (none auto-actioned, surfaced for future direction):
- F3 spec decoding: probably blocked by the same CUDA-graph issue.
- **F5 workload-shaped routing**: cleanest mitigation. Route `max_tokens ≤ 256` requests to 5930k, long-output requests to 7900xtx. Proxy code change in `internal/proxy/proxy.go`.
- **F6 llama.cpp on 5930k**: biggest potential lift but 12-24h re-quantization pipeline + feature divergence.
- F7 `py-spy` flamegraph: now more valuable post-F1/F2 falsification, not less. Would tell us whether the bottleneck is HIP kernel launch overhead specifically (would point at F3 or F6) or something else.

Honest position: with the current stack locked at `enforce_eager: true`, the per-token CPU↔GPU dance on X99 is bound by PCIe 3.0 + DDR4. Mitigations exist (F5, F6) but each costs real engineering work for a node that may be retired before they pay off. Recommended next step is to **document, don't optimize further**, until a real workload makes the latency-vs-capacity tradeoff bite users.

Sources:
- Brainstorm doc: `.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md` (Phase 1-3 + execution log).
- F1 benchmark output: `/private/tmp/.../tasks/bt8i5sqlo.output`.
- F1 CPU freq verification: ssh probe during warm request → cores at 2900-3300 MHz.
- F2 crash trace: `kubectl logs gemma4-26b-a4b-gptq-5930k-8c6f59d84-hlgrx --previous` (`hipErrorStreamCaptureUnsupported` in cuda_graph.py line 314).
- Recovery: `flux resume kustomization flexinfer-models -n flux-system` + sanity probe `"What is 2+2?"` → `"2 + 2 = 4"`.

### RALPH slice — investigate 5930k vs 7900xtx decode-rate asymmetry (operational finding, documented)

- Why this slice: a previous worklog observed the 5930k engine showing 0.6-1.1 tok/s vs 7900xtx 6-8 tok/s in mid-load samples. That looked like a real anomaly worth investigating before bumping `maxNumSeqs` or doing other capacity work.
- Methodology:
  1. Probe each upstream directly (bypass proxy) with an identical workload — same prompt ("Count from 1 to 50…", 32 prompt tokens), same params (`temperature: 0`, `max_tokens: 200`), 3 sequential requests per backend.
  2. Time the curl from the host shell (Python `time.time()` boundaries around `kubectl run … curl`) since BusyBox `date +%s%N` in the proxy pod returns 0.
  3. Each curl-pod pinned to the matching GPU node via `nodeSelector` + `dedicated:gpu` toleration so network path is symmetric.
- Result (host-timed end-to-end, ~141 completion tokens each):
  | seq | 7900xtx elapsed | 5930k elapsed |
  | --- | --- | --- |
  | 1 | 24.55 s | 50.67 s |
  | 2 | 22.16 s | 47.44 s |
  | 3 | 22.25 s | 48.57 s |
  | **avg** | **22.99 s** | **48.89 s** |
  
  **2.13x decode-rate gap.**
- Root cause: **CPU hardware asymmetry** between the two nodes. `lscpu` from each:
  - `cblevins-7900xtx`: AMD Ryzen 9 7900X3D (Zen 4, 12c/24t, 5.6 GHz boost, 2023).
  - `cblevins-5930k`: Intel Xeon E5-2680 v4 (Broadwell-EP, 14c, 2.4 GHz base / 3.3 GHz boost, **2016**). The hostname `cblevins-5930k` is legacy — the original i7-5930K was replaced; current CPU is the Xeon.
  
  Engine init logs corroborate the same ratio applies to all CPU-bound work:
  - aiter JIT compile: 12.2 s (7900xtx) vs 22.9 s (5930k) — 1.88x.
  - Model weight load: 21.5 s vs 40.1 s — 1.87x.
  
  With `enforce_eager: true` (correctness lock per the manifest comment — `torch.compile` falls back to non-tanh GELU on ROCm and the gptq_gemm kernel is "buggy" with CUDA graphs) every decoded token bears Python-side CPU overhead (sampler, KV scheduler, structured-output validation), and `maxNumSeqs: 1` removes any batching that would amortize that overhead. The gap is hardware-bound; cannot be fixed serving-side.
- Why this didn't show up earlier:
  - During the round-robin proof on 2026-05-14 the probe used short prompts (`"UP"` / `"What is 2+2?"`) with `max_tokens=3-4` — so the per-token CPU overhead barely registered. The 7900xtx and 5930k both responded in <2 s, masking the gap.
  - The earlier engine-log throughput numbers (`Avg generation throughput: 0.6 tok/s` on 5930k) were sampled during `Running: 0, Waiting: 9` snapshots (the proxy queue burst). Those represent engine bubbles between iterations, not steady-state decode.
- Operational implication: the round-robin 1:1 split means mean request latency = `(22.99 + 48.89) / 2 = 35.9 s` for the ~141-token workload, ~1.6x worse than an all-7900xtx config. Fleet still serves correctly; just slower on average.
- **NOT shipped today — the fix is one of these slices, surfaced for explicit user direction:**
  1. **Weighted routing on `pickReadyMember`.** Add a `flexinfer.ai/routing-weight: "2"` annotation (or similar) on the Model CR and have the round-robin picker honor weights when distributing. `7900xtx weight=2, 5930k weight=1` would route ~67% of traffic to the faster node. Real change to `internal/proxy/resolver.go` + new resolver field + tests + image rebuild + rollout. Mean latency improves to ~30 s; tail latency on 5930k unchanged.
  2. **Demote 5930k to failover.** Set `gemma4-26b-a4b-gptq-5930k` `minReplicas: 0` + lower priority. Only spins up if the 7900xtx instance is unavailable. Loses parallel capacity but eliminates the slow-path tax on routine traffic.
  3. **Hardware swap.** Replace the Xeon E5-2680 v4 with something post-2020. Real cost, not a code change.
  4. **Accept the gap.** Status-quo. Document it so future operators don't re-investigate. Reasonable if expected request volume stays modest.
- Sources:
  - Benchmark output: `/private/tmp/.../tasks/bxme6bnuf.output` (host-timed, 3+3 sequential reqs).
  - CPU info: `ssh cblevins-7900xtx lscpu` + `ssh cblevins-5930k lscpu`.
  - Engine init logs: `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq{,-5930k}-* | grep -iE 'weights took|aiter'`.
  - 60-validation-matrix.md row "26B fleet asymmetric decode rate: 5930k node is 2.2x slower".

### RALPH slice — wire project-management to consume the warm 26B lane (services/project-management MR !73)

- What was open:
  - The `00-index.md` "Current Goal" closeout left three optional follow-ups queued. User picked #2: "Switch project-management to consume the warm-lane alias." Up until now, `services/project-management/src/integration_command_center/extractors/llm_qwen.py` hardcoded `LLM_MODEL = "qwen3-8b-fast-7900xtx"` and `DEFAULT_FLEXINFER_URL = "http://qwen3-8b-fast-7900xtx.flexinfer-system.svc:8000"`. URL was env-overridable via `FLEXINFER_QWEN_URL`; model name was not.
- What changed (services/project-management MR !73, commit `bd7df51`, auto-merge queued):
  - `llm_qwen.py`:
    - `LLM_MODEL = os.environ.get("FLEXINFER_QWEN_MODEL", "project-mgmt")` — new default is the proxy service-label alias.
    - `DEFAULT_FLEXINFER_URL = "http://flexinfer-proxy.flexinfer-system.svc"` — new default targets the proxy, not a specific Model Service.
    - Docstring rewritten to cover both env vars + rollback recipe.
  - `tests/test_runner.py`: 3 metric-label asserts rewritten from hardcoded `'icc_llm_extractions_total{model="qwen3-8b-fast-7900xtx",result="…"}'` to `f'icc_llm_extractions_total{{model="{llm_qwen.LLM_MODEL}",result="…"}}'` so the suite follows the default automatically.
  - `.loom/40-decisions.md`: 2026-05-14 entry capturing the rationale, alternatives considered, prompt_hash continuity note, and the env-rollback recipe.
- Why this alias (`project-mgmt`):
  - The proxy's round-robin policy is what makes the two-instance fleet useful; pinning to a single Model resource name forgoes the second instance's capacity.
  - `project-mgmt` is the project-management-specific alias (vs the shared `quality-chat`). Easier to grep in flexinfer-proxy access logs and a clear handle for future shaped-traffic experiments.
- Cluster validation (run BEFORE the MR even merges, against the live proxy):
  - `kubectl run … curlimages/curl -- curl -X POST http://flexinfer-proxy.flexinfer-system.svc/v1/chat/completions -d '{"model":"project-mgmt","messages":[{"role":"user","content":"<ICC extraction prompt>"}],"temperature":0,"response_format":{"type":"json_object"}}'` returned a valid extraction envelope: `model: gemma4-26b-a4b-gptq-5930k` (sister instance via round-robin), parsed content `{candidates: [{kind:"action_item",text:"confirm vendor list with Acme Health by Friday"}, {kind:"decision",text:"ship MVP on the 20th"}]}`. Both `kind` values in `CANDIDATE_KINDS`, both `text` values are strings. Schema matches ICC's `_validate_response`.
- Test coverage (ICC repo): 1095/1095 green (test_llm_extractor 32/32 + test_runner 13/13 + sweep).
- Out-of-scope follow-up (left in ICC's queue, not blocking this slice):
  - ICC's k8s overlays do not currently set `ICC_LLM_ENABLED=1`. The consumption path is now correct, but extraction stays opt-in via that env flag — flipping it is a deployment-side operator decision, not a code change.
- Sources:
  - services/project-management MR !73 (`feat/llm-warm-lane-26b`, commit `bd7df51`).
  - `services/project-management/src/integration_command_center/extractors/llm_qwen.py:46-50` (new defaults).
  - `services/project-management/.loom/40-decisions.md` 2026-05-14 entry.
  - Live cluster validation: curl above with body `{"model":"project-mgmt", …}` round-tripped through flexinfer-proxy and the `gemma4-26b-a4b-gptq-5930k` upstream.

### RALPH slice — fix concurrent-load cross-routing on shared service-labels (MR !356)

- What was broken (carry-over from the previous slice):
  - With MR !354's round-robin picker live, a 20-req `quality-chat` probe at 0.5 s spacing split 10/10 cleanly. But a 50-req `xargs -P 10` concurrent probe failed 13/50 (~26%) with vLLM 404s like `The model 'gemma4-26b-a4b-gptq-5930k' does not exist.` from the 7900xtx upstream.
  - Earlier worklog framed the cause as a "cache-refresh race" inside `pickReadyMember`. The new `slog.Debug("forwarding to upstream", ...)` log (MR !355) plus `--log-level=debug` patched into the live deployment proved that was wrong.
- True root cause (captured from the forwarding log):
  - `internal/proxy/routing.go:getRoutingStrategy` auto-defaulted to `StrategyLeastLoaded` whenever a model was in a label group (≥2 service-label claimants). Paired with `refreshEndpoints`' label-group aggregation pass — which writes the **union** of all members' pod endpoints into each member's router ring — that combination cross-routed requests. The picker chose `gemma4-26b-a4b-gptq-5930k`, the body was rewritten to that served-model-name, but the router then picked any pod from the aggregated ring (10.42.0.7:8000 — the 7900xtx pod). vLLM on 7900xtx serves `gemma4-26b-a4b-gptq`, not `-5930k`, and returns 404. Log evidence: entries like `{"model":"gemma4-26b-a4b-gptq-5930k","resolved_model":"gemma4-26b-a4b-gptq-5930k","target":"http://10.42.0.7:8000","target_pod":"10.42.0.7:8000"}`.
- What changed (MR !356, commit `de21f5cc`, merged in `0e2805f1`):
  - `internal/proxy/routing.go`: removed the two `isModelInLabelGroup` auto-default branches (v1alpha2 and v1alpha1 paths). With my MR !354 picker handling cross-model selection on its own, the router branch now stays dormant unless an operator explicitly opts in via the `flexinfer.ai/routing` annotation. Added a long block comment explaining the 2026-05-14 behavior change so a future reader doesn't re-add the auto-default by accident.
  - `internal/proxy/label_group_test.go`: renamed `TestGetRoutingStrategy_LabelGroup_DefaultsToLeastLoaded` → `_StaysDefault`, inverted the assertion, added a long comment locking in the new behavior + linking to the root-cause analysis.
  - Aggregation in `refreshEndpoints` is **preserved** for the explicit-opt-in case (`TestRefreshEndpoints_LabelGroupAggregation` still passes). Operators who annotate a label-group model with `flexinfer.ai/routing=least-loaded` still get the aggregated cross-model routing as before.
- CI: pipeline #9473 (master after MR !356 merge) — proxy_test 132 s, promotion_gate 80 s, publish 214 s; total ~10 min from merge to `flexinfer-proxy:master` digest `c5c4497cc6a102df1328d65022e4685dc9c7d6c0c3137b6ba62904260a23af90`.
- Rollout: `kubectl rollout restart deployment/flexinfer-proxy -n flexinfer-system` picked up the new digest; debug-log arg (`--log-level=debug`) preserved across rollout, then removed via `kubectl patch ... remove` after validation.
- Validation:
  - **20 reqs at parallelism 2: 20/20 success, exact 10/10 split** between `gemma4-26b-a4b-gptq` and `gemma4-26b-a4b-gptq-5930k`.
  - **16 forwarding log entries, 0 cross-routing mismatches** — every `target` matched the picker's resolved model.
- Capacity note (not a routing bug):
  - At parallelism 10 and 20, HTTP=000 connection failures appear because both 26B upstreams run `maxNumSeqs: 1` and queue-saturate. The 5930k pod log showed `Running: 0 reqs, Waiting: 13 reqs` during the probe. 50 reqs / `-P 10`: 41/50 success. 100 reqs / `-P 20`: 58/100 success. Successful responses still split correctly; nothing is mis-routed. Scaling beyond 2 concurrent reqs would need higher `maxNumSeqs` per upstream — a separate config-tuning slice if more throughput is needed.
- Follow-up captured (probably out-of-scope for this fleet build):
  - If the fleet ever needs >2 concurrent reqs (current limit = 2 instances × `maxNumSeqs: 1`), raise `maxNumSeqs` on both Model CRs. Tradeoff: more concurrency increases per-token latency. Worth re-measuring after a real workload shape is known.
- Sources:
  - MR !356 (`fix(proxy): stop auto-defaulting to LeastLoaded for label-group members`); commit `de21f5cc`; merged at `0e2805f1`.
  - Pipeline 9473 (master) jobs: build_binaries success 198 s, proxy_test 132 s, promotion_gate_test 80 s, publish 214 s.
  - `internal/proxy/routing.go:230-268` (`getRoutingStrategy` post-fix); `internal/proxy/routing.go:269-291` (block comment locking in the 2026-05-14 decision); `internal/proxy/label_group_test.go:TestGetRoutingStrategy_LabelGroup_StaysDefault`.
  - Forwarding-log evidence pre-fix: `model=gemma4-26b-a4b-gptq-5930k target=http://10.42.0.7:8000 target_pod=10.42.0.7:8000` (cross-routed to 7900xtx pod). Post-fix: 16/16 logs matched correctly.

### RALPH slice — proxy round-robin across shared service-labels (load-balancing the 26B fleet)

- What was broken (carry-over from previous slice):
  - With both 26B instances Ready and declaring identical `service_labels`, a 10-request probe through `quality-chat` routed 10/10 to `gemma4-26b-a4b-gptq` (the 7900xtx primary). The sister instance was healthy but received zero traffic. Root cause was `internal/proxy/proxy.go:409` calling `ResolveServiceLabel` which returns `serviceLabelCache.Load(label).(string)` — first claimant only.
- What changed (MR !354, commit `f3cc1046`, merged in `5aa483e`):
  - `internal/proxy/model_resolver.go`: new `ResolveServiceLabelGroup(ctx, label) ([]string, bool)` returns the full claimant slice; `refreshServiceLabelCache` now `sort.Strings(claimants)` before storing so the round-robin ring is stable across refreshes.
  - `internal/proxy/resolver.go`: new `pickReadyMember(ctx, label, members) string`. Policy: prefer Ready members, round-robin via per-label `atomic.Uint64`, alphabetical fallback when no member is Ready so the cold-start path engages on a deterministic target. Single-member groups short-circuit (zero counter touch, zero Model fetch).
  - `internal/proxy/proxy.go`: `Proxy.labelRRCounters TypedSyncMap[string, *atomic.Uint64]` added; the `proxy.go:409` call site swapped from `ResolveServiceLabel` to `ResolveServiceLabelGroup` + `pickReadyMember`.
  - `internal/proxy/pick_member_test.go`: 5 tests — single-member short-circuit, AllReady_RoundRobin exact 10/10 split over 20 picks, PrefersReady avoids Idle members, NoneReady_FallsBackToFirst hits alphabetical-first, PerLabelCounters proves two overlapping groups don't share state.
  - `internal/proxy/routing.go`: added a `slog.Debug("forwarding to upstream", ...)` log line at the bottom of `serveProxy` (per-request, debug-level so it's silent unless `LOG_LEVEL=DEBUG`). Useful for diagnosing shared-label routing mishaps.
  - `deploy/models/gemma4-26b-a4b-gptq.yaml` + `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml`: dropped the "aspirational" caveat from the serviceLabels comment and documented the routing policy. Primary points at `internal/proxy/resolver.go:pickReadyMember`.
- Policy decision (round-robin among Ready, not least-busy or weighted):
  - Round-robin is the smallest correct change; least-busy needs in-flight tracking on the routing path that doesn't exist yet.
  - "Among Ready" matters under partial outages (one node draining, one image pulling). Today both 26B instances run `minReplicas=1` so they're both Ready steady-state, but the filter is what keeps the policy correct when only one is Ready.
  - Alphabetical fallback when none Ready preserves the deterministic cold-start contract (operator predicts which instance warms up).
- CI: pipeline #9457 had `proxy_test` evicted mid-run (`Eviction API: evicting` — runner pod disruption, not a logic failure). Retried as job 100740, succeeded in 226 s. `promotion_gate_test` ~10 s, `publish` ~165 s. Total cycle ~10 min from merge to `flexinfer-proxy:master` digest landing.
- Rollout: `kubectl rollout restart deployment/flexinfer-proxy -n flexinfer-system` picked up `flexinfer-proxy@sha256:ad1f7bd13c7bbe9164dd7df3b047c7bd23b5ce605b1801be7be76417d6c771f0`; old pod terminated, new one Ready in seconds.
- Validation:
  - Direct-name proxy probes (`model: "gemma4-26b-a4b-gptq"` and `model: "gemma4-26b-a4b-gptq-5930k"`) — both 200, model-name passthrough confirmed (single-claimant short-circuit path).
  - 6-request `quality-chat` probe (no spacing): 6/6 success, 3/3 split.
  - 20-request `quality-chat` probe (0.5 s spacing): **20/20 success, exact 10/10 split** between `gemma4-26b-a4b-gptq` and `gemma4-26b-a4b-gptq-5930k`. Evidence captured in `60-validation-matrix.md` row "Proxy round-robin Ready-member routing across shared service-labels".
- Follow-up surfaced (next RALPH slice):
  - Concurrent-load race: an earlier 20-req probe **without** spacing returned 16/20 success and 4/20 vLLM 404s from the 7900xtx upstream (`The model 'gemma4-26b-a4b-gptq-5930k' does not exist.`). The picker is firing correctly, but during the 5-second `serviceLabelCacheTTL` refresh window concurrent reqs can race: one read of the cache pins `chosen = gemma4-26b-a4b-gptq-5930k` for the body-rewrite, while a downstream read of the cache (e.g. for `targetURL` via `k8surl.ServiceURL`) snaps to a stale-but-different mapping and forwards to the 7900xtx Service. Net effect: 5930k-labeled body lands on the 7900xtx upstream, which 404s. With 0.5 s spacing it's 20/20 clean — the race window is narrow. Likely fix: a single atomic snapshot of `chosen` consumed by both rewrite and targetURL resolution, OR a fast-path that re-checks `getModel(chosen).Phase` immediately before forwarding. Not a regression of the previous "all → 7900xtx" baseline since failures resolve to a clear 4xx instead of silent mis-routing.
- Sources:
  - MR !354 (`feat(proxy): round-robin Ready members for shared service labels`); pipeline 9457 (`proxy_test` retry 100740, publish success at 165 s).
  - `internal/proxy/model_resolver.go:71-87` (`ResolveServiceLabelGroup`); `internal/proxy/resolver.go:43-90` (`pickReadyMember`); `internal/proxy/proxy.go:417-431` (call site); `internal/proxy/pick_member_test.go`.
  - Probe scripts captured in `60-validation-matrix.md` row.

### RALPH slice — fix 26B 5930k source-path mismatch (instance #2 stuck Pending)

- What was broken:
  - Instance #2 (`gemma4-26b-a4b-gptq-5930k`) had been un-paused 9 h earlier via MR !350 (`626185b1`) after the OCI artifact was seeded into Harbor (`sha256:ef26e6c7b614e187b37a78f362d7afe176137fdf815c003cecc9b1be1fb6c932`, 42 files, ~17 GiB).
  - The OCI `ModelCache` was Ready, but the sister `Model` was sitting in `Pending` / `Cached: False / CacheNotReady` because the controller-generated cache-copy job kept failing with `Missing source path: /src/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` on every retry until `BackoffLimit` was exhausted.
  - Root cause: `oras push` on the 7900xtx had been executed from INSIDE the `gptq-w4-g128-attnfp16-clean/` directory, so the 42-file artifact landed FLAT under the `modelPath` (`/data/gemma4-26b-a4b-gptq/<file>`) without the `gptq-w4-g128-attnfp16-clean/` parent — but the Model `spec.source` still ended in `/gptq-w4-g128-attnfp16-clean`. The cache-copy `SRC` derives from `spec.source`'s subpath, so the script looked for a directory that didn't exist.

- What changed:
  - MR !352 (`fix/26b-5930k-source-path-flat`, commit `45a54175`, merged `d81e5e4a`): dropped the trailing `/gptq-w4-g128-attnfp16-clean` segment from `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml` `spec.source`, so the new source is `pvc://gemma4-26b-a4b-gptq-oci-5930k/gemma4-26b-a4b-gptq`. Both cache-copy `SRC`/`DST` and the vLLM `--model` flag derive from this, so the one-line change re-aligns the whole chain. `servedModelName: gemma4-26b-a4b-gptq-5930k` is set explicitly, so `/v1/models` is unaffected. The primary 7900xtx instance reads from a different (non-OCI) PVC where the subdir IS preserved by the quantization writer, so its source path is unchanged.
  - Updated `.loom/60-validation-matrix.md` with a `pass` row for the sister instance (cluster transitions, smoke evidence, rollback path, follow-up).

- Cluster transitions observed end-to-end:
  - Flux reconciled `master@d81e5e4a` after `flux reconcile kustomization flexinfer-models -n flux-system --with-source`. Controller's source-hash drift detection (`controllers/model_cache.go:138-146`) compared the existing job's `flexinfer.ai/source` annotation against the new `spec.source`, detected drift, deleted the stale failed job, and created a fresh one with the new annotation.
  - Cache-copy job succeeded in 72 s (vs the ~4 min on the 7900xtx primary) because the OCI source PVC and the cache PVC are both `local-path` on the same NVMe — no cross-PVC traffic.
  - vLLM pod (`gemma4-26b-a4b-gptq-5930k-7ffb9bc79c-h85t8`) on `cblevins-5930k` reached `1/1 Ready` ~3 min after cache-copy completed; Model phase `Loading → Ready`.

- Smoke evidence:
  - Direct backend: `POST http://gemma4-26b-a4b-gptq-5930k.flexinfer-system.svc:8000/v1/chat/completions` with greedy `"What is 2+2?"` → `"content":"4"` (26 / 2 tokens, `finish_reason=stop`, `stop_reason=106`).
  - `/v1/models` exposes both 26B instances Ready with matching `service_labels`: `["gemma4-26b-a4b-gptq","gemma4-26b-a4b","gemma4-26b","quality-chat","mid-chat","gpt-4","project-mgmt"]`. Node-specific `litellm.aliases` on instance #2: `gemma4-26b-5930k`, `gemma4-26b-a4b-5930k`.

- Follow-up surfaced (next RALPH slice):
  - 10-request load probe through the shared `quality-chat` alias routed 10/10 to the 7900xtx instance (`served_by=gemma4-26b-a4b-gptq`). Root cause is in `internal/proxy/proxy.go:409` → `internal/proxy/model_resolver.go:47`: `ResolveServiceLabel` returns `claimants[0]` (`r.serviceLabelCache.Load` — first-by-priority) per label. The infrastructure to load-balance already exists — `refreshServiceLabelCache` builds a `labelGroupCache` of ALL claimants per label (line 130) and a `labelGroupModels` reverse index (line 145–152) — but no caller uses them on the routing path. Fixing this is the next slice: pick a routing policy (round-robin / least-busy / weighted-priority), implement it on top of `labelGroupCache`, and prove load-balancing with a 20+ request probe. The manifest comment on both Model CRs is aspirational until that lands.

- Sources:
  - `kubectl get model gemma4-26b-a4b-gptq-5930k -n flexinfer-system -o yaml` (phase, status.cache, status.conditions)
  - `kubectl describe job gemma4-26b-a4b-gptq-5930k-cache-copy -n flexinfer-system` (BackoffLimit / SRC + DST)
  - Inspect pod that mounted the OCI PVC: `find /src -name '*.safetensors' | wc -l` → 42; `du -sh /src/gemma4-26b-a4b-gptq` → 15.6G; no `gptq-w4-g128-attnfp16-clean` subdir.
  - `controllers/model_cache.go:138-146` (source-drift detection), `controllers/model_cache_jobs.go:289-340` (cache-copy job script construction), `controllers/model_backend.go:88-101` (serving `ModelPath` derived from `pvc://` subpath).
  - `internal/proxy/proxy.go:409`, `internal/proxy/model_resolver.go:47,127,130` (proxy first-claimant routing).

## 2026-05-13

### RALPH slice — promote gemma4-26b-a4b-gptq to warm quality lane

- What changed:
  - `deploy/models/gemma4-26b-a4b-gptq.yaml`: `gpu.priority` 200→350, `serverless.minReplicas` 0→1, `config.warmPolicy` ondemand→primary, added `quality-chat`/`mid-chat`/`gpt-4`/`project-mgmt` aliases + serviceLabels, refreshed manifest preamble.
  - `deploy/models/fast-chat-7900xtx.yaml`: `serverless.minReplicas` 1→0, added explicit `config.warmPolicy: ondemand`.
  - `deploy/models/kustomization.yaml`: uncommented `- gemma4-26b-a4b-gptq.yaml`, updated the 7900 XTX section preamble to record the swap.
- Why:
  - The flexinfer fleet only had `qwen3-8b-fast-7900xtx` Ready as a text-gen lane on the discrete 7900 XTX. Downstream services (project-management and similar) need capable reasoning + 16K context, and the validated `gemma4-26b-a4b-gptq` artifact (gfx1100 hybrid GPTQ INT4, FP8 KV @ 16K) was sitting on disk with no `Model` CR reconciling it.
  - VRAM math (~17.7 GiB for 26B + 12 GiB est for 8B > 24 GiB) means only one of the two can be warm at a time on the shared `7900xtx-textgen` group. Per the user's selection, the 26B takes the warm slot; the 8B remains on disk for explicit `qwen3-default` / `qwen3-8b` traffic (cold-start ≤10m from `local-path` NVMe).
- Validation:
  - `kustomize build deploy/models` renders 9 `Model` resources cleanly; `quality-chat` / `project-mgmt` aliases land on the 26B; `warmPolicy: primary` (26B) and `warmPolicy: ondemand` (8B) appear in the built output.
  - `scripts/check-runtime-profile-consistency.sh` passed (runtime/profile contract unchanged).
  - `go test ./api/v1alpha2/... ./controllers/...` passed.
- What's next:
  - After Flux reconcile, watch `kubectl get models gemma4-26b-a4b-gptq -n flexinfer-system` transition to `Ready`; record cold-load + first-token TPS as a new row in `60-validation-matrix.md`.
  - Loom-core / project-management service configs need to point at the new aliases to actually consume the warm lane (follow-up).

### Post-merge live validation (same day)

- What changed (cluster side, no code/manifest edits):
  - Flux applied the new `Model` CR `gemma4-26b-a4b-gptq` at 17:55:54Z. The Model controller did not immediately pick it up because the reconcile worker was busy on a separate Model in the `default` namespace; forced a reconcile via `kubectl annotate model gemma4-26b-a4b-gptq -n flexinfer-system flexinfer.ai/force-reconcile=<ts>`.
  - Controller created cache PVC `gemma4-26b-a4b-gptq-cache` (50Gi, `local-path`, pinned to `cblevins-7900xtx`). Cache-copy job `gemma4-26b-a4b-gptq-cache-copy` succeeded in ~4 min (copied ~17 GiB across `attention-fp16-layer-*.safetensors`, `model-0000{1..4}-of-00004.safetensors`, tokenizer/config files from source PVC `gemma4-26b-a4b-gptq` on `nvme-1r-gpu`).
  - vLLM pod `gemma4-26b-a4b-gptq-6d798b8665-s859w` reached `1/1 Ready` ~18:11Z; API server initialized at 18:09:03Z. `qwen3-8b-fast-7900xtx` correctly transitioned `Ready -> Idle` and pod was removed when the 26B claimed the `7900xtx-textgen` warm lane.
- Smoke evidence:
  - Direct backend: `POST http://gemma4-26b-a4b-gptq.flexinfer-system.svc:8000/v1/chat/completions` with greedy `"What is 2+2?"` -> `"content":"4"` (27 prompt / 2 completion tokens, finish_reason `stop`).
  - Proxy via `project-mgmt` alias: `POST http://flexinfer-proxy.flexinfer-system.svc/v1/chat/completions` with a 3-task triage prompt -> coherent prioritization ("You should deploy the hotfix first because it addresses an immediate production issue that likely impacts users and system stability.", 55 / 23 tokens).
  - `/v1/models` exposes the gemma4-26b-a4b-gptq entry with `aliases: [gemma4-26b, gemma4-26b-a4b, quality-chat, mid-chat, gpt-4, project-mgmt]`, `phase: Ready`, `context_window: 16384`.
- Validation matrix:
  - Added a new row to `.loom/60-validation-matrix.md` (`promotion_decision: pass`) capturing runtime digest, cache PVC migration timing, direct + proxy smoke, and the rollback path (revert MR !343).
- Open follow-ups (out of scope for this slice):
  - `services/project-management/src/integration_command_center/extractors/llm_qwen.py` hardcodes `LLM_MODEL = "qwen3-8b-fast-7900xtx"` and `DEFAULT_FLEXINFER_URL = "http://qwen3-8b-fast-7900xtx.flexinfer-system.svc:8000"`. URL is env-overridable via `FLEXINFER_QWEN_URL`; model name is not. To consume the new warm lane, project-management needs both env-driven (URL + model name) or a switch to `flexinfer-proxy` with the `project-mgmt` / `quality-chat` alias. `prompt_hash` is salted with `LLM_MODEL`, so any switch breaks `extraction_runs.prompt_hash` continuity by design.
  - Loom-core resolver consumers on `qwen3-default` / `qwen3-8b` aliases now cold-start; needs a real-load measurement of the 8B cold-start budget before declaring this acceptable for Weaver/Mills/Coordinator/Autofix.
- Sources:
  - `kubectl get models -n flexinfer-system`, `kubectl describe model gemma4-26b-a4b-gptq -n flexinfer-system`
  - `kubectl logs gemma4-26b-a4b-gptq-cache-copy-95ch8 -n flexinfer-system`
  - `kubectl logs gemma4-26b-a4b-gptq-6d798b8665-s859w -n flexinfer-system`
  - Direct + proxy `/v1/chat/completions` smoke commands above.

## 2026-05-06 (round 1 closeout)

### Next-round parallel plan + first-wave shipping

- What changed:
  - Added `.loom/gfx1100-gfx906-next-round-plan.md` decomposing remaining gfx1100/gfx906 work into eight tracks (A-H) for parallel sub-agent execution.
  - Spawned four first-wave sub-agents on Tracks A, E, F, H in isolated worktrees.
  - Tracks F (MR !273), E (MR !274), A (MR !275) merged. Track H produced a local-only investigation report at `.loom/local/qwen36-coherence-triage.md` and added a matrix pointer.
  - Track A also produced `docs/planning/gpuprofile-contract-followups.md` with five prioritized next slices.
- Why:
  - The 2026-05-07 worklog deltas (gfx906 runtime paused for disk-pressure, qwen36-27b-gptq quarantined, 5930k fast-chat fallback removed) invalidated the prior single-ladder sequencing and surfaced new tracks.
  - User asked for parallel-execution shaping of the next gfx1100/gfx906 round.
- First-wave outcomes (concrete next moves):
  - Track A → next slice is `ROCmEnvVars` GPUProfile-first push at `backend/interface.go:299-353`.
  - Track E → matrix is now the canonical runtime-promotion table; four required canary rows labeled.
  - Track F → consistency-test-only locked in; revisit when a third profile lands.
  - Track H → one-line CRD fix queued at `deploy/modelcaches/qwen36-27b-gptq-gfx1100.yaml:87` (`dynamicExclusion: "gdn"`); confirming experiment is a `model.safetensors.index.json` grep on PVC `qwen36-27b-oci`.
- Held for round 2:
  - Track B (gfx906 disk-pressure unblock) — needs operator pairing on `cblevins-radeonvii`.
  - Track C (gfx906 vLLM revive-or-retire) — coordinate with active `backlog/31-vllm-gfx906-build` worktree.
  - Track D (gfx1100 capability push) — first concrete unlock is the qwen36 dynamic-exclusion fix from H.
  - Track G (fast-chat resilience after 5930k MLC fallback removal).
- Validation:
  - `git log --oneline origin/master | head -8` shows merges 273/274/275 in sequence.
  - Round-end `git diff --check` clean.
- Next:
  - Spawn round 2: Track D (qwen36 one-line fix + re-quant smoke) is the cheapest concrete win; Track A slice 2 (env-vars) extends round 1.
  - Pair on Track B and decide Track C path before touching disk-pressure.

## 2026-05-07

### RALPH Slice 3/RG-4 bridge — gfx906 runtime digest promotion

- What changed:
  - Built and pushed `registry.harbor.lan/flexinfer/runtime:rocm-gfx906` from
    `master@d8c75658`.
  - Resolved the new image digest:
    `sha256:dd0a1936f350ec117da1ab6a589618a571074d6828c2ccb5e273f2f6eb195b97`.
  - Promoted that digest into `deploy/gpuprofiles/gfx906.yaml` and
    `deploy/system/values-k3s.yaml` with `scripts/promote-runtime-digest.sh`.
  - Updated `.loom/60-validation-matrix.md` so the Radeon VII SDXL row points
    at the promoted runtime digest.
- Why:
  - MR !263 deployed Helm env for runtime profile/image metadata, but live pods
    still used an older runtime binary that could not emit the new
    `runtime_profile` and `runtime_digest` metric labels.
  - The promotion also removed drift between GPUProfile and Helm runtime
    consumers for the `gfx906` lane.
- Validation:
  - `crane digest registry.harbor.lan/flexinfer/runtime:rocm-gfx906` returned
    the promoted digest.
  - `docker --context 7900xtx run --rm --entrypoint /usr/local/bin/flexinfer-runtime registry.harbor.lan/flexinfer/runtime:rocm-gfx906 --help` showed the runtime binary defaulting to `gpu-vendor=amd` and `gpu-arch=gfx906`.
  - `scripts/check-runtime-profile-consistency.sh` passed.
  - `scripts/test-promote-runtime-digest.sh` passed.
  - Pending after merge: Flux reconcile and live metric scrape.

## 2026-05-06

### RALPH Slice 6-lite — runtime digest/profile observability

- What changed:
  - Added `runtime_profile` and `runtime_digest` labels to
    `flexinfer_runtime_info`.
  - Wired Helm runtime DaemonSets to pass `RUNTIME_PROFILE` and `RUNTIME_IMAGE`
    into `flexinfer-runtime`.
  - Updated the runtime Grafana dashboard table and metrics spec so digest-pinned
    runtime promotions are visible without shelling into pods.
- Why:
  - Runtime promotion decisions now depend on digest-pinned evidence across
    `gfx1100` and `gfx906`. The existing runtime info metric had node/vendor/arch
    but not the exact promoted profile or digest.
- Validation:
  - `go test ./cmd/flexinfer-runtime ./internal/runtime` passed.
  - `helm template flexinfer charts/flexinfer -f deploy/system/values-k3s.yaml --set grafanaDashboard.enabled=true --set grafanaDashboard.runtimeEnabled=true` rendered the runtime env and dashboard labels.
  - `git diff --check` passed.

### RALPH Slice RG-2 — runtime profile consistency check

- What changed:
  - Added `scripts/check-runtime-profile-consistency.sh`.
  - Wired the check into `scripts/test-promote-runtime-digest.sh`.
  - Updated `docs/planning/rocm-gfx1100-gfx906-platform-slice.md` with RG-2 scope, acceptance criteria, and validation commands.
  - Marked the runtime-build/promotion plan as partially complete in `.loom/gfx1100-gfx906-platform-enhancements-plan.md`.
- Why:
  - Slice 1 found support-truth drift. RG-2 adds a cheap local guard for the stable contract before touching CRDs or runtime images.
  - The current repo has digest drift between GPUProfile manifests and Helm runtime profiles, so this check intentionally validates digest pinning and arch/vendor identity rather than forcing all digests to match.
- Validation:
  - `scripts/check-runtime-profile-consistency.sh` passed.
  - `scripts/test-promote-runtime-digest.sh` passed.
  - `git diff --check` passed.

### RALPH Slice 1 — gfx1100/gfx906 capability matrix reconciliation

- What changed:
  - Ran the roadmap/spec RALPH loop against the new `gfx1100/gfx906` platform spec and selected Slice 1 as the smallest reversible increment.
  - Added `docs/planning/rocm-gfx1100-gfx906-platform-slice.md` with the iteration plan, support matrix, acceptance criteria, validation plan, and rollback path.
  - Demoted `gfx906` vLLM support in `deploy/gpuprofiles/gfx906.yaml` from `full` to `experimental`.
  - Updated `build/README-gfx906.md` so vLLM and MLC-LLM are canary/experimental, not full default lanes, and corrected the Vega20 env guidance to include `HSA_OVERRIDE_GFX_VERSION=9.0.6`, `HSA_ENABLE_SDMA=0`, `HSA_USE_SVM=0`, and `PYTORCH_ROCM_ARCH=gfx906`.
  - Added a canary warning to `examples/v1alpha2/model-vllm-gfx906.yaml`.
  - Linked the new platform lane from `docs/planning/next-roadmap.md` and marked RG-1 complete.
- Why:
  - `build/runtime.yaml` disables vLLM in the current unified `gfx906` runtime, while the GPUProfile and README called it full support. The first slice removes that contradictory truth before API or runtime-image work.
- Validation:
  - `git diff --check -- deploy/gpuprofiles/gfx906.yaml build/README-gfx906.md examples/v1alpha2/model-vllm-gfx906.yaml docs/planning/rocm-gfx1100-gfx906-platform-slice.md docs/planning/next-roadmap.md .loom/gfx1100-gfx906-platform-enhancements-plan.md .loom/40-decisions.md .loom/50-worklog.md` passed.
  - `rg -n "gfx906|vLLM|runtime:rocm-gfx906|support:|HSA_OVERRIDE_GFX_VERSION|HSA_ENABLE_SDMA|HSA_USE_SVM" ...` confirmed the reconciled support/env statements are present.
  - `yq e '.' deploy/gpuprofiles/gfx906.yaml` and `yq e '.' examples/v1alpha2/model-vllm-gfx906.yaml` passed.
- Blockers:
  - `agent_context__agent_session_start` failed with `Transport closed`; handoff is captured in `.loom` docs for this pass.
- Next:
  - Add a consistency check for `build/runtime.yaml` vs GPUProfiles/Helm runtime profiles.
  - Expand `.loom/60-validation-matrix.md` for runtime digest canary rows.

## 2026-05-02

- Refreshed `.loom/00-workspace-snapshot.md` with the plan-loom-core snapshot script.
- Confirmed Loom resource mode is available through `loom://config`, `loom://servers`, `loom://tools/index`, and `loom://health`.
- Confirmed `codebase_memory` health via `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json` with `total_chunks: 2831`.
- Added `docs/planning/spec-driven-delivery.md`.
- Updated `ROADMAP.md`, `docs/planning/next-roadmap.md`, and `docs/planning/README.md` to expose the spec-driven delivery lane.
- Updated `.loom/00-index.md`, `.loom/00-mcp-inventory.md`, `.loom/20-product-spec.md`, `.loom/30-implementation-plan.md`, and `.loom/40-decisions.md` with current planning context.

## 2026-04-25

### Planned 31B TurboQuant memory fix

- What changed:
  - Added `.loom/gemma4-31b-turboquant-memory-fix-plan.md`.
  - Updated `.loom/30-implementation-plan.md` with the preferred fix:
    patch TurboQuant to share/lazily materialize immutable codec primitives
    across attention layers.
- Key finding:
  - Pinned upstream `turboquant-vllm@9d19b87c` constructs `TurboQuantMSE` and
    moves rotation/codebook tensors onto the target device for every
    `TQ4AttentionImpl`.
  - Those primitives depend on head size, bit width, and seed, not layer
    identity, so sharing them is the lowest-risk memory fix before trying
    weight re-quantization or hardware changes.
- Sources:
  - `git clone --depth 1 https://github.com/Alberto-Codes/turboquant-vllm.git /tmp/turboquant-vllm-plan`
  - `git fetch --depth 1 origin 9d19b87cef462cf0abd5643f6d052ac5a3bc99b6`
  - `/tmp/turboquant-vllm-plan/src/turboquant_vllm/vllm/tq4_backend.py:347-392`
  - `/tmp/turboquant-vllm-plan/src/turboquant_vllm/quantizer.py:93-110`
  - `build/scripts/patch_turboquant_quantizer_gpu_qr.py`

### MR !192 closeout and durable knowledge capture

- What changed:
  - Refreshed the Loom context templates and regenerated
    `.loom/00-workspace-snapshot.md`.
  - Confirmed current MCP runtime is loom-resource mode rather than the old
    CLI-only fallback path:
    - `functions.list_mcp_resources({})` exposed `loom://config`,
      `loom://servers`, `loom://tools`, `loom://tools/index`, and
      `loom://health`.
    - `functions.read_mcp_resource(server="loom", uri="loom://config")`
      reported profile `full`, `serverCount=47`, `toolCount=504`.
  - Confirmed `codebase_memory` is healthy through the CLI fallback:
    `repo_id=flexinfer`, `total_chunks=2831`.
  - Fetched GitLab remotes and verified MR !192 state with `glab mr view 192`:
    `state: merged`.
  - Added `.loom/gemma4-31b-turboquant-closeout.md` and the decision entry above
    so future agents can recover the 31B TurboQuant ceiling without reading the
    whole MR thread.
- Production state after MR !192:
  - Historical closeout notes referenced `maxModelLen: 2048`, but the current
    manifest now caps `gemma4-31b-gptq` at `maxModelLen: 1920` because the
    loaded `keqv` artifact is semantically corrupt.
  - `gemma4-31b-gptq-long.yaml` is preserved but removed from Flux
    reconciliation.
  - The 31B TurboQuant lane is closed on 24 GiB gfx1100.
  - The next long-context path is the separate
    `gemma4-26b-a4b-gptq-long` canary.
- Sources:
  - `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/init_loom_context.py --root .`
  - `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
  - `functions.list_mcp_resources({})`
  - `functions.list_mcp_resource_templates({})`
  - `functions.read_mcp_resource(server="loom", uri="loom://config")`
  - `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json`
  - `git fetch --all --prune`
  - `glab mr view 192`
  - `git show b64f0502:docs/dev/gemma4-31b-turboquant-24gb-oom.md | nl -ba`

### Gemma4 26B/31B GPTQ + TurboQuant planning pass

- What changed:
  - Added `.loom/gemma4-26b-31b-gptq-turboquant-plan.md`.
  - Updated `10-research.md`, `20-product-spec.md`, `30-implementation-plan.md`, and `40-decisions.md` with the current direction.
  - Refreshed `00-workspace-snapshot.md` with the plan-loom-core snapshot script.
- Key conclusion:
  - The correct direction is clean GPTQ artifacts first, TurboQuant second. The current 31B `keqv` artifact is semantically corrupt and must be re-quantized before TurboQuant memory work can prove anything useful.
- Remaining issues accounted for:
  - 26B hybrid is coherent but large; 32K requires canary validation.
  - 26B long canary has a selector risk because it currently allows `gpu.count: 1`.
  - 31B `keqv` loads at 1920 but collapses to `<pad>` because late-layer tensors repeat.
  - 31B TurboQuant previously OOMed before KV because plugin state consumed about 3.57 GiB on top of about 20.02 GiB of weights.
  - TurboQuant is KV/vector compression, not GPTQ weight quantization.
- Evidence reviewed:
  - Tavily searches/extracts for Gemma4 official docs, TurboQuant paper/blog, vLLM ROCm docs, vLLM quantized KV docs, ROCm quantization docs, GPTQModel support, and community TurboQuant/Gemma4 reports.
  - Git history for Gemma4/GPTQ/TurboQuant/gfx1100 work, including commits `bde445f0`, `96d091e3`, `3b6f52b9`, `0fb31ecd`, `078914ae`, and `b64f0502`.
  - Current manifests under `deploy/models/` and `deploy/modelcaches/`.
- Tool note:
  - `codebase_memory` stats reported `total_chunks: 2831`, but semantic search failed with Morph HTTP 521 `origin_down`, so the plan relied on `rg`, direct file reads, git history, and Tavily.

### Rapid iteration 1 — 26B long canary dGPU selector

- Isolate:
  - The 26B long-context canary still rendered and lived with `spec.gpu.count: 1`, while the working primary 26B profile requires `count: 2` on `cblevins-7900xtx` to avoid the Raphael iGPU slot.
- Hypothesis:
  - Matching the primary's `count: 2` selector retires the iGPU-placement failure mode without starting the canary, because the canary remains `minReplicas: 0`.
- Patch:
  - Updated `deploy/models/gemma4-26b-a4b-gptq-long.yaml` to set `gpu.count: 2` and document why.
  - Updated `deploy/models/kustomization.yaml` stale comments from "TurboQuant/priority 50" to "32K long-context/priority 200".
- Build/prove:
  - `kubectl kustomize deploy/models` rendered `gemma4-26b-a4b-gptq-long` with `gpu.count: 2`, `config.hipVisibleDevices: "0"`, `minReplicas: 0`, and `shared: 7900xtx-textgen`.
  - `kubectl diff -f deploy/models/gemma4-26b-a4b-gptq-long.yaml` showed a single intended live spec delta: `gpu.count: 1` -> `2`.
- Observe:
  - Live cluster before reconcile: `kubectl -n flexinfer-system get model gemma4-26b-a4b-gptq-long -o jsonpath='{.spec.gpu.count}{"\n"}{.spec.config.hipVisibleDevices}{"\n"}{.spec.serverless.minReplicas}{"\n"}{.status.phase}{"\n"}'` returned `1`, `0`, `0`, `Idle`.
  - `kubectl diff -k deploy/models` also showed unrelated drift on the primary 26B and 31B objects, so no broad apply was performed.
- Next:
  - Land/reconcile this scoped GitOps change when ready.
  - Next technical blocker after selector safety: add repeated-tensor integrity checks so a corrupt 31B GPTQ artifact cannot advance into `k_eq_v`.

### Rapid iteration 2 — 31B repeated qweight integrity guard

- Isolate:
  - The current 31B `gptq-w4-g128-keqv` artifact loads but emits `<pad>` because the source GPTQ artifact has repeated projection tensors on late layers. The existing `k_eq_v` task could make a complete-looking artifact from that corrupt source.
- Hypothesis:
  - Hashing projection `qweight` tensors by module family and failing when the same qweight appears on different layers catches this corruption class before serving promotion.
- Patch:
  - Added a Gemma4 31B repeated-qweight guard to `build/scripts/validate_quantized_artifact.py`.
  - Added unit tests for duplicate-vs-distinct 31B qweights in `build/scripts/test_validate_quantized_artifact.py`.
  - Added the same source-integrity guard to `deploy/tasks/gemma4-31b-keqv/postprocess.py` before any `v_proj` duplication.
  - Re-check source integrity even when `DST_DIR` already contains a complete-looking `keqv` output, so an old bad artifact cannot keep no-oping forever.
  - Bumped the Flux Job names to `gemma4-31b-keqv-postprocess-v4` and `gemma4-31b-keqv-cache-copy-reset-v4`; updated the reset helper to wait for the v4 postprocess Job.
- Build/prove:
  - `python3 -m py_compile build/scripts/validate_quantized_artifact.py build/scripts/test_validate_quantized_artifact.py deploy/tasks/gemma4-31b-keqv/postprocess.py`
  - `python3 build/scripts/test_validate_quantized_artifact.py` -> 11 tests OK.
  - Synthetic duplicate source with an already-complete destination returned `dup_existing_dst_rc=4` and logged `source integrity failed: repeated qweight tensors across layers: self_attn.q_proj layers=[0, 1]`.
  - Synthetic distinct source with an already-complete destination returned `ok_existing_dst_rc=0` and logged `DST already complete... GitOps-safe no-op`.
  - `kubectl kustomize deploy/tasks/gemma4-31b-keqv` renders `gemma4-31b-keqv-postprocess-v4`, `gemma4-31b-keqv-cache-copy-reset-v4`, and the new source-integrity code.
- Observe:
  - Live cluster still has only `gemma4-31b-keqv-postprocess-v3` complete; no Flux reconcile or hot apply was performed in this iteration.
  - `kubectl diff -k deploy/tasks/gemma4-31b-keqv` shows v4 Job/ConfigMap additions, as expected.
- Next:
  - Land the GitOps changes, reconcile the task kustomization, and expect the v4 postprocess Job to fail on the current corrupt source. That failure is useful evidence confirming the guard caught the known bad artifact.
  - After the guard lands, the next technical blocker is a clean 31B re-quant with lower corruption risk and the same guard in front of `k_eq_v`.

## 2026-02-19

- What changed:
  - Ran loom-context initialization and snapshot scripts.
  - Refreshed `.loom` docs (`00`, `10`, `20`, `30`, `40`) to current session evidence.
  - Captured MCP inventory through `loom` CLI fallback.
  - Attempted `codebase_memory` re-index twice; both failed with compatibility errors.
  - Diagnosed `codebase_memory_v1` vector schema mismatch (`size=1`) and recreated collection with `size=1536`.
  - Rebuilt `/Users/cblevins/workspace/services/loom-core/bin/mcp-codebase-memory` from source and restarted loom daemon.
  - Re-ran index successfully (`job_id=1869e8aca6a0ab14`, `chunks_total=1877`, `errors=0`).
- Why:
  - Establish a trustworthy planning baseline before further implementation work.
- What’s next:
  - Resolve direct MCP bridge `Transport closed` instability for this chat; continue using `loom tools call` fallback meanwhile.
- Sources:
  - [S1] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/init_loom_context.py --root .`
  - [S2] `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
  - [S3] `loom servers --json | jq '.servers | length'`
  - [S4] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'`
  - [S5] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"5380e4246b4b7cf1\"})`
  - [S6] `functions.mcp__loom__codebase_memory__codebase_index_poll({job_id:\"237b41f443376c18\"})`
  - [S7] `loom tools call qdrant__qdrant_create_collection --args '{"collection":"codebase_memory_v1","vector_size":1536,"distance":"Cosine"}' --json`
  - [S8] `go build -o /Users/cblevins/workspace/services/loom-core/bin/mcp-codebase-memory /Users/cblevins/workspace/services/loom-core/cmd/mcp-codebase-memory`
  - [S9] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json`

## 2026-02-20

### Qwen3-30B-A3B abliterated — cold start bug hunt and resolution

- What changed:
  - Found and fixed **three bugs** blocking serverless cold start for large models:
    1. **Controller idle timeout kills Loading models** (`controllers/model_controller.go`):
       `desiredReplicas()` checked only `time.Since(LastActiveTime) > idleTimeout` without considering Loading phase. Added `if model.Status.Phase == ModelPhaseLoading { return 1 }` guard. Commit `4fecee3`.
    2. **Proxy triggerScaleUp swallows conflict silently** (`internal/proxy/queue.go`):
       `triggerScaleUp()` returned `nil` on `errors.IsConflict`, making `processQueue` think scale-up succeeded while `LastActiveTime` remained stale. Added 3-retry loop with re-fetch. Commit `d9fc215`.
    3. **GPUGroup cold start timeout not per-model** (`internal/proxy/gpugroup.go`, prior session):
       GPUGroup queue path used only the global `queueTimeout`, ignoring per-model `ColdStartTimeoutSeconds`. Added `max(queueTimeout, getColdStartTimeout)` pattern. Commit `bc60e05`.
  - Switched model cache from **Longhorn** (`nvme-cache-1r`) to **local NVMe** (`local-path` storageClass):
    - 18.7GB GGUF loads in ~3min from local NVMe vs 15-20min through Longhorn's block layer (mmap page fault overhead).
    - Updated `examples/v1alpha2/qwen3-30b-a3b-abliterated-llamacpp-amd.yaml`.
  - Increased proxy timeouts to **25m** in `platform/gitops/k3s/ai/flexinfer/values.yaml` and pushed to gitops main.
  - Set model `idleTimeout: 20m` and `coldStartTimeout: 15m` in the example manifest.
  - All three commits pushed to `codex/issue-9-prometheus-deps-batch1` branch (later renamed `codex/issue-8-car5-fallback-proof`).

- Results:
  - Qwen3-30B-A3B abliterated responded successfully: **HTTP 200**, 108 tok/s generation, 72.5 tok/s prompt processing.
  - Total cold start time: ~598s (includes container image pull + 18.7GB GGUF mmap from local NVMe).
  - Model served via llama.cpp ROCm on AMD gfx1100 (RX 7900 XTX), Q4_K_M quantization, flash attention enabled.

- Why:
  - Qwen3-30B-A3B is the first large MoE model tested on the serverless cold start path. The 18.7GB GGUF and 10+ minute load time exposed race conditions between proxy and controller that smaller models never triggered.

- What's next:
  - Rebuild proxy and controller images to deploy the fixes to the cluster (currently running old code; manual patches were used for testing).
  - Create PR for the three fix commits.
  - Test cold start end-to-end without manual patches to validate the fixes work in production.

- Sources:
  - `controllers/model_controller.go:195-199` — Loading phase guard
  - `controllers/model_controller_test.go` — TestDesiredReplicasServerless loading case
  - `internal/proxy/queue.go:296-323` — triggerScaleUp conflict retry
  - `internal/proxy/gpugroup.go` — per-model cold start timeout
  - `examples/v1alpha2/qwen3-30b-a3b-abliterated-llamacpp-amd.yaml` — local-path cache, timeout config
  - `platform/gitops/k3s/ai/flexinfer/values.yaml` — proxy timeouts 25m
  - Cluster test: `kubectl exec curl-30b-local -- curl -s proxy:80/v1/chat/completions` → HTTP 200, 108 tok/s

## 2026-04-07

### Gemma4 GPTQ monitoring, cache cleanup, and next-round planning refresh

- What changed:
  - Refreshed `.loom/00-workspace-snapshot.md` with `plan-loom-core` scripts.
  - Re-validated that MCP resource/template discovery is still empty in this session and fell back to repo-local evidence for planning.
  - Confirmed `gemma4-31b-gptq` is not actually stalled at low displayed progress; it is resuming from `perplexity_validated` and spending time in baseline perplexity validation.
  - Confirmed `gemma4-26b-a4b-gptq` was genuinely wedged on `gfx1100`:
    - full model load completed,
    - progress stopped at `harmful activations 0/128`,
    - Python process was in Linux `D` state.
  - Observed `cblevins-7900xtx` transition `NotReady`, lose SSH reachability, and destabilize cluster API/etcd leadership during reboot/failover.
  - Applied live mitigation:
    - patched `gfx1100` `GPUProfile` to set `ABLITERATION_ACTIVATION_CAPTURE_MODE=hidden_states`,
    - patched `gemma4-26b-a4b-gptq` selectors to `cblevins-5930k`,
    - deleted stale 26B jobs/pods and repeated stale `VolumeAttachment` cleanup.
  - Updated local repo manifests/tests to reflect the safer `gfx1100` activation-capture override.
  - Updated `.loom/10-research.md` and `.loom/30-implementation-plan.md` with the next improvement round focused on runtime stability, placement, storage, recovery, and observability.

- Why:
  - Convert the current live incident into a durable improvement plan while jobs remain in flight, instead of treating each failure as an isolated retry problem.

- Current live state when writing:
  - `gemma4-31b-gptq` still running on `cblevins-radeonvii`.
  - `gemma4-26b-a4b-gptq` rerouted to `cblevins-5930k`, with the warmup pod still pulling the runtime image.
  - `cblevins-7900xtx` still `NotReady`.

- What’s next:
  - Validate that the rerouted `26B` run on `5930k` actually starts with `hidden_states` and progresses past `harmful activations 0/128`.
  - If successful, commit/push the `gfx1100` profile change and follow with placement hardening.
  - If unsuccessful, stop scheduling Gemma4 abliteration onto `gfx1100` and treat `gfx906` as the only safe architecture until the ROCm/amdgpu failure is root-caused.

- Sources:
  - `python /Users/cblevins/.codex/skills/plan-loom-core/scripts/workspace_snapshot.py --root .`
  - `functions.list_mcp_resources({})`
  - `functions.list_mcp_resource_templates({})`
  - `kubectl -n flexinfer-system logs gemma4-26b-a4b-gptq-abliterate-zwvpv --tail=160`
  - `kubectl -n flexinfer-system exec gemma4-26b-a4b-gptq-abliterate-zwvpv -- sh -lc 'ps -o pid,ppid,stat,%cpu,%mem,etime,cmd -C python3'`
  - `kubectl -n flexinfer-system logs gemma4-31b-gptq-abliterate-sxxwv --tail=160`
  - `kubectl get nodes -o wide | rg 'cblevins-7900xtx|cblevins-5930k|cblevins-radeonvii'`
  - `ssh -o BatchMode=yes -o ConnectTimeout=5 cblevins-7900xtx 'hostname && uptime && systemctl is-active k3s && uname -a'`
  - `ssh cblevins-5930k 'kubectl get nodes -o wide | grep -E "cblevins-(5930k|7900xtx|radeonvii)"'`
  - `deploy/gpuprofiles/gfx1100.yaml`
  - `pkg/quantization/abliteration_test.go`

### Reconciliation + Backlog Tracking Refresh

- What changed:
  - Fast-forwarded `master` to `origin/master`, confirming `fix/cold-start-reliability` was already merged (`fad43a7`).
  - Merged `origin/codex/issue-9-prometheus-deps-batch1` into `master` with clean merge commit `a16b2d1`.
  - Verified merge delta with local tests + lint before commit, then pushed `master`.
  - Updated roadmap planning docs to reflect:
    - dependency rollout progress (`prometheus`, `golang-x` complete),
    - recent feature/tech-debt closure state.
- Why:
  - Ensure local and branch-level deltas are reconciled on default branch and backlog tracking artifacts match repository state.
- Sources:
  - `git log --oneline -n 8`
  - `git merge --no-ff --no-commit origin/codex/issue-9-prometheus-deps-batch1`
  - `go test ./...`
  - `golangci-lint run -c .golangci.v2.yml`

## 2026-04-09

### Gemma4 research / plan reset

- What changed:
  - Refreshed `.loom/00-workspace-snapshot.md`.
  - Re-checked MCP/runtime inventory:
    - MCP resource/template discovery is still empty,
    - direct loom MCP bridge calls still return `Transport closed`,
    - `loom` CLI fallback works and reports `502` tools.
  - Converted the Gemma4 incident work into a source-backed failure taxonomy.
  - Confirmed the two durable bug classes from live cluster evidence:
    1. missing-source retry loops after downstream phases removed weights,
    2. partial sharded-cache reuse caused by marker-only validation.
  - Landed and deployed two code fixes during the incident:
    - `af22ecc0`: reset to download when abliteration has lost source weights
    - `fdadc03d`: require complete shard sets before cache reuse / abliteration start
  - Updated `.loom/10-research.md` and `.loom/30-implementation-plan.md` with a phased permanent-fix program centered on integrity gates, recovery semantics, runtime image determinism, and status/monitoring.

- Why:
  - The current problem is no longer “why did the last job fail”; it is “how do we prevent Gemma4 pipelines from re-entering the same integrity and runtime traps next week”.

- What’s next:
  - Finish the `26B` full-redownload validation after `fdadc03d`.
  - Convert duplicated shard-integrity shell logic into one shared helper.
  - Build a Gemma4-capable runtime image so abliteration does not install Transformers from Git during job startup.

- Sources:
  - `python "$CODEX_HOME/skills/plan-loom-core/scripts/workspace_snapshot.py" --root .`
  - `loom tools list --json | sed -n '1,220p'`
  - `loom tools list --json | jq -r '.tools[].name' | awk -F'__' '{print $1}' | sort | uniq -c | sort -nr | sed -n '1,20p'`
  - `kubectl describe modelcache gemma4-26b-a4b-gptq -n flexinfer-system`
  - `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq-abliterate-hh4wn --tail=80`
  - `kubectl logs -n flexinfer-system gemma4-26b-a4b-gptq-downloader-85mm9 --tail=80`
  - `kubectl logs -n flexinfer-system gemma4-31b-gptq-abliterate-28pj6 --tail=120`
  - `git rev-parse HEAD`

## 2026-04-18

### Incident triage — 26B cold-start stall + observability gap

- What happened:
  - FlexDeck showed `gemma4-26b-a4b-gptq` stuck in `Loading` with a growing proxy queue. Verified against cluster: `kubectl get model … -o jsonpath='{.status.phase}'` → `Loading`. FlexDeck was accurate, not stale.
  - Events showed a preemption at 13:26:48Z by `gemma4-26b-a4b-gptq-long` (priority 200 vs 150) in the `7900xtx-textgen` shared group.
  - After re-activation, a fresh pod (`…-xtqb6`) pulled the runtime image in 749 ms, but vLLM weight load wedged for 8m47s on safetensors shard 31/34 (shards 1–30 loaded at ~2 s/it each).
  - Serving PVC is `pvc-ec945ced-172d-439a-b386-abe6a439dc71` (`gemma4-26b-a4b-gptq-cache`, 50Gi, storage class `longhorn`, 3 replicas across k3s-w-4 / cblevins-radeonvii / cblevins-7900xtx). Longhorn volume state was `attached/healthy` throughout; the stall was a replica-read slowdown, not a volume failure.
  - Pod eventually reached `Ready`; `/health` returns 200 and serves now.
  - Separately spotted vLLM validation error in logs: `VLLMValidationError: ... your prompt contains 79 characters (more than 0 characters, which is the upper bound for 0 input tokens)` — looks like `max_tokens=8192` + `max_model_len=8192` leaves 0 prompt budget. Tracked as a separate small issue below.
- Why it mattered:
  - Operator UX on FlexDeck gave no indication of "transient Longhorn stall" vs "actually wedged". Queue built up. Only `kubectl logs` into the pod could distinguish the two.
- Two decisions landed in `40-decisions.md`:
  - Add `Model.status.loadingSubstage` enum + `status.message` to surface shard progress and distinguish `ImagePulling`/`Initializing`/`LoadingWeights`/`Compiling`/`HealthCheckPending`/`Preempted`.
  - Migrate the 26B cache PVC off default 3-replica Longhorn to `local-path` or `nvme-1r-gpu` (1 replica, GPU-local NVMe), matching the 2026-02-20 Qwen3-30B GGUF precedent.
- What's next:
  - [services/flexinfer#53](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/53) tracks the CRD + controller + proxy + FlexDeck changes and the PVC migration.
  - Separate follow-up for the `max_tokens == max_model_len` validation error if it turns out to be a manifest bug vs a vLLM upstream quirk.
- Sources:
  - `kubectl get model gemma4-26b-a4b-gptq -n flexinfer-system -o jsonpath='{.status.phase}'`
  - `kubectl get events -n flexinfer-system --sort-by=.lastTimestamp | grep gemma4-26b`
  - `kubectl logs gemma4-26b-a4b-gptq-87c45466d-xtqb6 -n flexinfer-system | grep Loading`
  - `kubectl -n longhorn-system get volume pvc-ec945ced-172d-439a-b386-abe6a439dc71 -o yaml`

### Slice A1-lite — gemma4-26b-a4b-gptq validator evidence

- What changed:
  - Ran `build/scripts/validate_quantized_artifact.py` via `kubectl exec` into the live runtime pod `gemma4-26b-a4b-gptq-87c45466d-wpkg6` on `cblevins-7900xtx`.
  - Validated both on-PVC artifacts against `--layout vllm-gptq --family gemma4-26b-a4b`. Both returned `ok: true` with one flat-shape warning each. Raw JSON in `.loom/local/validation/gemma4-26b-a4b-gptq/20260418-085841/`.
  - Confirmed the **active** serving artifact is `/models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` (extracted from `/proc/1/cmdline`).
  - Populated `.loom/60-validation-matrix.md` with the first two rows (clean + hybrid-v10) and added a findings block noting two validator follow-ups (family auto-detect gap, flat-warning noise).
  - Corrected the 2026-04-18 plan-refresh entry earlier today: the validator is **metadata/layout**, not cosine — see `30-implementation-plan.md` "Slice A1 execution path" for the A1-lite / A1-full split.
- Why:
  - Slice A1 acceptance asked for validator evidence. A1-lite (validate existing artifact) unblocks the matrix at ~10 min of cluster cost instead of committing to a 12–24 h A1-full re-quant on cblevins-7900xtx.
  - The `detected_family: null` finding is itself a tractable fix for the next slice and proves the validator is safe to rely on once families are registered.
- What's next:
  - Add `gemma4_text` / architecture-string markers to `FAMILY_PROFILES` so `--family` auto-detects (small PR, 1 file, adds a test).
  - Run `--run-generation` probe as a lightweight coherence gate (needs `torch` + a tokenizer import — confirm runtime pod has transformers before trying).
  - Move on to Slice B (Qwen3.5-9B port to gfx1100) or Slice C (OmniCoder-9B end-to-end) — user's call on priority.
- Sources:
  - `kubectl exec -n flexinfer-system gemma4-26b-a4b-gptq-87c45466d-wpkg6 -- python3 /tmp/validate_quantized_artifact.py --artifact-path /models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean --layout vllm-gptq --family gemma4-26b-a4b --json`
  - `kubectl exec -n flexinfer-system gemma4-26b-a4b-gptq-87c45466d-wpkg6 -- sh -c 'cat /proc/1/cmdline'`
  - `kubectl get pvc -n flexinfer-system` → `gemma4-26b-a4b-gptq` (96Gi, nvme-1r-gpu), `gemma4-26b-a4b-gptq-cache` (50Gi, longhorn).
  - `build/scripts/validate_quantized_artifact.py:393-554` (validator entry point).
  - `cmd/flexinfer/commands/quantize.go:117-169` (CLI spec).

### gfx1100 quant pipeline multi-family plan refresh

- What changed:
  - Updated `00-index.md` Current Goal to target gfx1100 quant pipeline multi-family rollout (Gemma4 → Qwen3.5 → OmniCoder → Qwen3-14B regression → validation matrix).
  - Appended 2026-04-18 Execution Slice to `30-implementation-plan.md` with six priority targets, five delivery slices (A–E), acceptance gates, and open questions for `/feature-dev` to resolve before branching.
- Why:
  - Recent merge train (2026-04-13..18) landed dense GPTQ validation, artifact recovery, and gfx1100 vLLM env pins. Pipeline is stable enough to stop firefighting and start systematic family coverage with artifact-validator evidence.
- What's next:
  - `/feature-dev` should pick Slice A1 (drive `gemma4-26b-a4b-gptq` through full pipeline under `denseModulePolicy: validate`), answer the three open questions in the slice, then proceed family-by-family.
- Sources:
  - `git log --oneline --since="2026-04-13"`
  - `deploy/gpuprofiles/gfx1100.yaml`
  - `deploy/modelcaches/{gemma4-26b-a4b-gptq,omnicoder-9b-gptq,qwen35-9b-gptq,gemma4-31b-gptq}.yaml`
  - Commits: `551f6763`, `0378749e`, `0e8ec72a`, `f3b6c164`, `3e77d9da`, `b8ab9cf4`, `d5355aec`.

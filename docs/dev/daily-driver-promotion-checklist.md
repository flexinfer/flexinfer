# Daily-driver promotion checklist

Gate to run **before** promoting any model to a warm-pinned / default daily-driver
lane (`serverless.minReplicas >= 1`, or a `serviceLabel` / litellm alias that real
traffic hits by default). Promotion = pointing live traffic at a model, so it
gets the same provenance + behavior scrutiny as any production change.

## 1. Base-model provenance (DO THIS FIRST)

Confirm and **record** what the served artifact was actually quantized/built from,
and that its alignment posture matches the intended daily-driver policy.

- Trace the source: `ModelCache.spec.source` (the HF repo) → the quant artifact
  path → the `Model.spec.source` PVC path. They must chain to the base you mean.
- Classify the base: **abliterated / uncensored / aligned(stock) / distilled**.
  This workspace's daily-drivers are uncensored by policy (e.g. gemma4 uncensored;
  the 70B is `huihui-ai/Llama-3.3-70B-Instruct-abliterated`). An aligned/stock
  base promoted as a daily-driver is a defect, not a default.
- Record it on the Model CR as an annotation so it survives the next edit:

  ```yaml
  metadata:
    annotations:
      flexinfer.ai/base-model-source: "llmfan46/Qwen3.6-35B-A3B-uncensored-heretic-Native-MTP-Preserved"
      flexinfer.ai/base-model-posture: "uncensored"   # abliterated | uncensored | aligned | distilled
  ```

> **Why this gate exists.** On 2026-06-17 the Qwen3.6-35B-A3B reasoning lane was
> promoted from `hesamation/…Claude-4.6-Opus-Reasoning-Distilled` — an *aligned*
> base — without anyone flagging that it wasn't an uncensored daily-driver source.
> The promotion writeup covered serve-coherence, throughput, and context tuning,
> but not provenance. Nobody "swapped" the source (git blame shows it was the
> declared source from 2026-06-13); the miss was that promotion didn't *check* it.

## 2. Serve-coherence on the real image

- The artifact loads and returns coherent output on the exact serving image/recipe
  it will run under — not a different one. New arch/quant variants (e.g.
  MTP-preserved checkpoints, new expert layouts) can break loaders that an older
  artifact passed. Validate via the canary recipe
  (`deploy/debug/qwen36-currency-canary-model.yaml`) before re-pointing prod.

## 3. Performance within threshold

- Decode tok/s, TTFT, and per-answer latency are recorded and acceptable for the
  lane's role (`eval/model-compare/` for quality+perf; `cmd/flexinfer-bench` for
  throughput). Note any spec-decoding / graph-mode caveats.

## 4. Capacity + GPU handoff

- The target node/GPU can hold it at the chosen context (KV dtype, maxModelLen,
  maxNumSeqs). If it preempts another lane, that's intentional and recorded.

## 5. Promotion provenance annotations

Record the decision trail on the Model CR (existing convention):

```yaml
metadata:
  annotations:
    flexinfer.ai/promotion-state: "<what-was-validated>-<date>"
    flexinfer.ai/promotion-evidence: "<path-or-MR>"
    flexinfer.ai/base-model-source: "<hf-repo>"
    flexinfer.ai/base-model-posture: "<abliterated|uncensored|aligned|distilled>"
```

## 6. GitOps

- The change goes through git (commit the Model CR + its ModelCache). If
  `flexinfer-models` Flux is suspended, apply live **and** commit the durable
  copy — an untracked live-applied manifest is config drift.

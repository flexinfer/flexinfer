# model-compare — quality + performance eval for served LLMs

A focused, **deterministically-scored** comparison harness for two (or more)
OpenAI-compatible models served by flexinfer. It fills the gap left by
`cmd/flexinfer-bench` (throughput + substring coherence only): it measures
**answer quality** across categories AND **performance** (TTFT, decode tok/s)
in one side-by-side run — with **no LLM judge** (every item is checkable).

## What it measures

- **Quality** — `prompts.json` is a set of objective items across
  `arithmetic, factual, reasoning, logic, instruction, code`. Each item declares
  `expect` + a `mode` (`contains` / `all` / `exact`). The model's **final answer
  is scored after stripping reasoning** (`reasoning_content` is taken from the
  stream when the backend separates it, and any residual `<think>…</think>` is
  removed), so reasoning and non-reasoning models are judged on the same ground.
- **Performance** — a fixed-length streamed generation gives p50 **TTFT** and
  **decode tok/s** (over `--perf-iters`), plus the p50 per-item quality latency.

Output: `<out-prefix>.json` (full per-item detail) + `<out-prefix>.md`
(summary + per-category + per-item ✓/✗ tables).

## Running it (in-cluster, zero-dep)

Runs with only the Python standard library, so it works inside any vLLM pod.
Targets the flexinfer proxy, which routes `/model/{name}/…` to each backend:

```bash
python3 compare.py \
  --base-url http://flexinfer-proxy.flexinfer-system.svc:80 \
  --url-template '{base}/model/{model}/v1/chat/completions' \
  --models gemma4-26b-a4b-gptq,qwen35-moe-reasoning-5930k \
  --dataset prompts.json --out-prefix results --perf-iters 3
```

The model name is used both for proxy routing and as the OpenAI `model` field.
To hit a model's Service directly instead of the proxy, pass e.g.
`--base-url http://qwen35-moe-reasoning-5930k.flexinfer-system.svc:8000`
`--url-template '{base}/v1/chat/completions'`.

## Datasets / tiers

- `prompts.json` — **easy/objective** tier (basic arithmetic, factual, simple
  reasoning). Run with the default `--max-tokens 1024`.
- `prompts-hard.json` — **hard** tier (multi-step word problems, combinatorics,
  number theory, counting, logic traps). Run with `--max-tokens 2048` (hard
  items legitimately need more reasoning):
  `python3 compare.py ... --dataset prompts-hard.json --out-prefix hard --max-tokens 2048`

See `results/` for the 2026-06-17 runs and `FINDINGS-*.md` for the two-tier
takeaway (short version: the fast lane wins easy/objective work outright; on hard
work the two lanes tie overall but are *complementary* — reasoning wins logic
traps + multi-step, the fast lane wins direct-recall/enumeration — so **route by
task** rather than picking one).

## Task-router (`router.py`) + offline validation (`route_analysis.py`)

The "route by task" finding is actualized as a zero-dependency, **content-only**
classifier — no model call, ~no added latency:

```bash
python3 router.py "How many seconds are there in 2.5 hours?"   # -> fast
python3 router.py "If 4 hens lay 4 eggs in 4 days, how many do 8 hens lay in 8?"  # -> reasoning
python3 router.py --selftest          # pins intended routings (10/10)
python3 router.py --dataset prompts-hard.json   # routing table for a dataset
```

Rules (derived from the observed failure modes, fast→reasoning escalation only
where it pays): counting/enumeration & unit-conversion → **fast** (reasoning
over-enumerates / misreads units); abstract symbolic syllogisms → **fast**
(reasoning over-thinks them); concrete multi-step / trap word problems →
**reasoning**; everything else → **fast** (default).

`route_analysis.py` measures the router **offline** against the real per-item
correctness (`results/per-item-2026-06-17.json`) — no model calls:

```bash
python3 route_analysis.py --out results/FINDINGS-router-2026-06-17.md
```

On the 2026-06-17 set the two lanes' errors are not just disjoint but
**content-separable**, so the router recovers the full oracle:

| strategy | accuracy (56 items) | mean answer latency |
|---|---|---|
| fast-only | 94.6% | 0.14s |
| reasoning-only | 92.9% | 4.92s |
| **routed** | **100.0%** | **1.46s** (25% → reasoning) |
| oracle | 100.0% | — |

That table is an *in-sample* fit (the rules encode failure modes seen on that
set), so it was validated on a held-out tier:

### Held-out validation (the generalization kill-test) — PASSED 2026-06-17

`prompts-heldout.json` is **24 fresh items written after `router.py` was frozen**
(MR !635) and never used to tune it. Run live on both lanes, then analyzed
out-of-sample:

```bash
python3 route_analysis.py --peritem results/per-item-heldout-2026-06-17.json \
  --out results/FINDINGS-router-heldout-2026-06-17.md
```

| strategy | accuracy (24 unseen items) | mean answer latency |
|---|---|---|
| fast-only | 95.8% | 0.13s |
| reasoning-only | 91.7% | 4.44s |
| **routed** | **100.0%** | **1.57s** (33% → reasoning) |
| oracle | 100.0% | — |

The frozen router routed **every** unseen item to a correct lane — including the
three discriminators: `wp-7` (overtime, fast slipped) → reasoning ✓; `cnt-1`
(digit count, reasoning over-enumerated 40.9s) → fast ✓; `cmb-1` (LEVEL
arrangements, reasoning answered 60) → fast ✓. So the content-separability of the
two lanes' errors **generalizes out of sample** — it is not an artifact of
fitting the tuning set. See `results/FINDINGS-router-heldout-2026-06-17.md`.

**Remaining caveat:** still a small set written by the same author who knows the
rules. A larger third-party / adversarial held-out set is the next confidence
step before wiring the router into the proxy for production traffic.

## Notes & caveats

- This is an **as-deployed** comparison: each model runs at its production config
  (context length, batching, spec-decoding, quant), which is the meaningful
  real-world signal — not an isolated apples-to-apples kernel benchmark.
- Reasoning models spend tokens "thinking", so their **per-answer latency is
  higher** even at equal decode tok/s; the harness reports both so the trade-off
  is visible (`reasoning_tokens_approx` per item in the JSON).
- The item set is intentionally small and objective (fast, reproducible, no
  judge). Extend `prompts.json` to add categories; scoring stays deterministic.
- `mode: exact` is strict (the normalized answer must equal the expected token) —
  it doubles as an instruction-following test.

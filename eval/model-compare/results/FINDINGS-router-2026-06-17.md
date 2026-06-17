# Offline routing analysis — does a heuristic router beat either lane alone?

Fast lane: `gemma4-26b-a4b-gptq` · Reasoning lane: `qwen35-moe-reasoning-5930k`
Source: real per-item correctness from the 2026-06-17 gfx1100 runs (`results/per-item-2026-06-17.json`); routing by `router.py`; no model calls. Latency is the per-tier per-lane p50 used as a per-item estimate.

## easy tier (28 items, dataset `prompts.json`)

| strategy | accuracy | mean answer latency |
|---|---|---|
| fast-only (`gemma4-26b-a4b-gptq`) | 28/28 (100.0%) | 0.12s |
| reasoning-only (`qwen35-moe-reasoning-5930k`) | 27/28 (96.4%) | 3.72s |
| **routed (router.py)** | **28/28 (100.0%)** | 0.63s |
| oracle (best per item) | 28/28 (100.0%) | — |

Router sent **4/28 (14%)** to the reasoning lane.

## hard tier (28 items, dataset `prompts-hard.json`)

| strategy | accuracy | mean answer latency |
|---|---|---|
| fast-only (`gemma4-26b-a4b-gptq`) | 25/28 (89.3%) | 0.16s |
| reasoning-only (`qwen35-moe-reasoning-5930k`) | 25/28 (89.3%) | 6.11s |
| **routed (router.py)** | **28/28 (100.0%)** | 2.28s |
| oracle (best per item) | 28/28 (100.0%) | — |

Router sent **10/28 (36%)** to the reasoning lane.

## Combined (both tiers)

| strategy | accuracy | mean answer latency |
|---|---|---|
| fast-only | 53/56 (94.6%) | 0.14s |
| reasoning-only | 52/56 (92.9%) | 4.92s |
| **routed** | **56/56 (100.0%)** | **1.46s** |
| oracle | 56/56 (100.0%) | — |

Router sent 14/56 (25%) to the reasoning lane overall.

No residual misroutes: on this set the router picks a correct lane for every item (it recovers the full oracle).

## Caveats

- **In-sample upper bound.** `router.py`'s rules encode failure modes observed on *this* item set (reasoning over-enumerates counting, over-thinks abstract syllogisms). A clean recovery here proves the two lanes' errors are *content-separable*, not that the rules generalize. The real test is a held-out tier — build it before trusting these numbers in production.
- **Latency is an estimate.** Per-item latency was not persisted; this uses the per-tier per-lane p50. The item set is also reasoning-heavy by construction, so the 'fraction to reasoning lane' overstates the cost on real traffic (mostly direct recall, which routes to the fast lane).
- 28 items/tier — directional. Grow each tier for tighter confidence.


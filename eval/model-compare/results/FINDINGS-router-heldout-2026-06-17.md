# Offline routing analysis — does a heuristic router beat either lane alone?

Fast lane: `gemma4-26b-a4b-gptq` · Reasoning lane: `qwen35-moe-reasoning-5930k`
Source: real per-item correctness from the 2026-06-17 gfx1100 runs (`results/per-item-2026-06-17.json`); routing by `router.py`; no model calls. Latency is the per-tier per-lane p50 used as a per-item estimate.

## heldout tier (24 items, dataset `prompts-heldout.json`)

| strategy | accuracy | mean answer latency |
|---|---|---|
| fast-only (`gemma4-26b-a4b-gptq`) | 23/24 (95.8%) | 0.13s |
| reasoning-only (`qwen35-moe-reasoning-5930k`) | 22/24 (91.7%) | 4.44s |
| **routed (router.py)** | **24/24 (100.0%)** | 1.57s |
| oracle (best per item) | 24/24 (100.0%) | — |

Router sent **8/24 (33%)** to the reasoning lane.

## Combined (both tiers)

| strategy | accuracy | mean answer latency |
|---|---|---|
| fast-only | 23/24 (95.8%) | 0.13s |
| reasoning-only | 22/24 (91.7%) | 4.44s |
| **routed** | **24/24 (100.0%)** | **1.57s** |
| oracle | 24/24 (100.0%) | — |

Router sent 8/24 (33%) to the reasoning lane overall.

No residual misroutes: on this set the router picks a correct lane for every item (it recovers the full oracle).

## Caveats

- **This IS the held-out test.** These items were written after `router.py` was frozen (MR !635) and were never used to tune its rules. The router still routed every item to a correct lane, so the content-separability of the two lanes' errors **generalizes out of sample** — it is not an artifact of fitting the tuning set.
- **Still directional.** Small set, written by the same author who knows the rules; a larger third-party / adversarial set is the next confidence step. Note the fast lane alone is already strong (95.8%), so the routed gain is concentrated in a few discriminating items.
- **Latency is an estimate** (per-lane p50 as the per-item value); the set is reasoning-heavy by construction, so the fraction routed to the reasoning lane overstates the cost on real traffic.


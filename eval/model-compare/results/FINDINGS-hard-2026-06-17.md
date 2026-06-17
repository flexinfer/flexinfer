# Findings — hard tier, and the two-tier picture (2026-06-17)

Second run of the harness, this time on `prompts-hard.json` (28 multi-step
objective items, `--max-tokens 2048`). Read alongside the easy-tier
`FINDINGS-2026-06-17.md`. Both lanes are on gfx1100, hit via the flexinfer proxy.

## The result that matters

On hard, objective problems the two lanes **tie at 89.3% (25/28)** — but they
are **complementary, not interchangeable**: they fail *different* items, along a
clean fault line.

| | gemma4-26b-a4b-gptq (fast) | qwen35-moe-reasoning (reasoning) |
|---|---|---|
| logic (traps) | 60% | **80%** |
| word-problems | 87.5% | **100%** |
| counting / units | **100%** | 50% |
| latency / answer | **0.16 s** | 6.11 s (~38×) |

- **The reasoning lane earns its keep on traps + multi-step word problems.**
  qwen35 catches the two-coin trap and the hens-and-eggs trap that gemma4 falls
  for, and never slips on the multi-step pay/age/area problems.
- **The fast lane wins where reasoning over-thinks.** qwen35 tried to *enumerate*
  "how many 7s from 1–100" (41 s, blew the 2048-token budget, truncated to
  garbage), misread "seconds" as "minutes", and even reasoned a clean syllogism
  into the wrong answer. gemma4 answered all three instantly and correctly.
- **Reasoning is not free and not infallible.** ~38× the latency for, here, *no*
  net accuracy gain — the win is in *which* questions, not *how many*.

## The two-tier picture (easy + hard)

| | gemma4 (fast) | qwen35 (reasoning) |
|---|---|---|
| Easy set accuracy | **100%** | 96.4% |
| Hard set accuracy | 89.3% | 89.3% |
| Latency / answer | **~0.1–0.2 s** | ~3.7–6.1 s |

- On **easy/objective** work the fast lane is strictly better (equal-or-higher
  accuracy, ~30× faster, never over-thinks).
- On **hard** work they tie overall, with the reasoning lane decisively better
  on the *trap/logic* slice and the fast lane better on the *direct-recall /
  enumeration / unit* slice.

## Recommendation: route by task, don't pick a winner

1. **Default everything to the fast lane** (gemma4). It's faster and at least as
   accurate on the large majority of real traffic.
2. **Route to the reasoning lane only for the slice where it wins** — multi-step
   logic, "trick" word problems, anything where a first-glance answer is often
   wrong. That is the workload that justifies keeping a slower reasoning lane
   warm at all.
3. **Guard the reasoning lane against its own failure mode.** Its losses here
   were budget-blowout enumeration and misreads, not knowledge gaps — a tighter
   output budget plus a "don't enumerate, find the pattern / re-read the units"
   system prompt would likely recover `hcount-1` and `hunit-1`.

## Caveats (unchanged from the easy run)

- As-deployed, through the proxy, bench client co-located on the qwen35 pod.
- gemma4's tok/s is spec-decoding-inflated; qwen35's 78.8 tok/s here matches its
  isolated ~79 tok/s single-stream (the easy run's 34.5 was a low-side outlier
  under client/proxy contention).
- 28 items per tier — directional, not a leaderboard. Grow each tier for
  tighter confidence; scoring stays deterministic (no judge).

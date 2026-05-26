# Brainstorm: F4 — Long-Context Agent Platform (deep-dive)

**Date**: 2026-05-25
**Triggered by**: F1 (cross-card spec decode) closed honestly with a fail in
`internal/proxy/spec_decode/` bench (`.loom/60-validation-matrix.md`,
2026-05-25 "spec-decode standalone bench" row). Pivot to F4 from the
parent brainstorm `.loom/brainstorm-rocm-fleet-unlocks-2026-05-25.md`.
This document is a second-level brainstorm with four parallel sub-agents
each running a distinct angle on F4.

**Prior context**:

- `.loom/brainstorm-rocm-fleet-unlocks-2026-05-25.md` — parent F1+F4
  recommendation
- `.loom/60-validation-matrix.md` — 32k gemma4-26b PASS (19k smoke @ 20s);
  cross-card spec-decode BLOCK (draft ≈ verifier in tok/s)
- `docs/planning/context-bounded-admission-spec.md` — admission filter
  (LIVE)
- `docs/planning/context-aware-router-execution.md` — prefix-aware
  routing (CAR-1..CAR-5, LIVE)
- `~/.claude/rules/spec-riskiest-assumption.md` — workspace rule:
  every spec MUST surface its riskiest assumption + kill-test

## What's already live (don't rebuild it)

- gemma4-26b-a4b-gptq Ready at 32k maxModelLen with fp8 KV; single-stream
  (maxNumSeqs=1); `enablePrefixCaching: false` ← **load-bearing**
- Admission filter live (opt-in per Model)
- Prefix-aware routing library exists in `internal/routing/`; consistent-hash
  is implemented but NOT wired into the proxy's `pickReadyMember` for
  label-group requests
- gemma4-e4b-radeonvii draft serves coherently on gfx906
- agent-context MCP (loom-core): sessions, decisions, tasks, memory,
  handoffs

## Phase 1 — Diverge (24 framings across 4 angles)

### Backend / runtime engineer (6 framings)

#### F4-prefix-cache-flip — Just turn it on and measure

`deploy/models/gemma4-26b-a4b-gptq.yaml:118` sets `enablePrefixCaching: false`. vLLM PagedAttention already deduplicates KV blocks across requests sharing leading tokens. Every other framing here is speculation until we measure on the actual GPTQ+FP8 KV combo at 32k.

- **Bet**: Native APC reclaims the bulk of TTFT on follow-up turns with no proxy work.
- **Risk**: APC was disabled for an undocumented reason — possibly hybrid-GPTQ + FP8 KV + `disableHybridKVCacheManager` interaction, or OOM at `gpuMemoryUtilization=0.98`.
- **First move**: `gemma4-26b-a4b-gptq-apc-canary` clone Model with APC on + utilization knocked to 0.94; drive a 5-turn agent script; record TTFT distribution.

#### F4-proxy-prefix-pinning — Stop round-robin within label groups

`pickReadyMember` is pure round-robin (`internal/proxy/resolver.go:86`). For the 2-instance `quality-chat` fleet, turn-2 has a 50% chance of landing on the cold-cache replica. `internal/routing/router.go` already implements consistent-hash prefix routing — just not wired into the label-group code path.

- **Bet**: Prefix-consistent hashing within Ready members halves cross-replica cache thrash on multi-turn agents.
- **Risk**: Hot-prefix collision pins all traffic to one replica; rehash cliff on replica drop.
- **First move**: Behind `FLEXINFER_PROXY_LABEL_GROUP_ROUTING=prefix-or-rr` env, swap `pickReadyMember` to `router.Route(strategy=Prefix, fallback=LeastLoaded)`; instrument `flexinfer_label_group_route_decisions_total{strategy}`.

#### F4-ttft-vs-tpot-budget — Predict TTFT, gate admission on it

"Feels instant" splits cleanly: turn-1 TTFT is prefill-dominated (5-30s at 32k); turn-N TTFT with APC hit is decode-dominated. The admission filter currently only knows about token count. The proxy could refuse a turn-1 request that would push p99 TTFT past a model-declared SLO.

- **Bet**: `predicted_ttft = prompt_tokens / measured_prefill_tps - cache_hit_fraction × prefill_tps` is good enough to gate.
- **Risk**: No live cache-hit estimator without round-tripping to the engine.
- **First move**: Histogram metric `flexinfer_proxy_predicted_ttft_seconds` with no enforcement; compare to actual to see if the prediction is even close.

#### F4-chunked-prefill-knob — Promote `maxNumBatchedTokens` to a QoS lever

The current `maxNumBatchedTokens: 256` is sized as an anti-OOM thing at 32k. It's *also* vLLM's chunked-prefill chunk size. With chunked prefill on and `maxNumSeqs > 1`, the chunk size dials between TTFT (smaller = worse) and TPOT fairness (smaller = better).

- **Bet**: There's no path to `maxNumSeqs > 1` at 32k without jointly retuning chunk size; the deliverable is a documented `(TTFT, TPOT, max_concurrency)` curve per architecture, not a magic number.
- **Risk**: ROCm Triton prefill chunk overhead may make chunked prefill on gfx1100 strictly worse — the knob is decorative on this hardware.
- **First move**: Sweep `maxNumBatchedTokens ∈ {256, 1024, 4096}` × `maxNumSeqs ∈ {1, 2}` on the APC canary; publish the curve to `docs/dev/gemma4-rocm-status.md`.

#### F4-kv-eviction-noisy-neighbor — Define eviction policy for maxNumSeqs=1

`maxNumSeqs: 1` means user A holds a 32k window; user B arrives, vLLM queues. With APC on, B might share A's system-prompt prefix and reuse it cheaply — but if B's full prompt is novel, A's KV gets evicted on the next scheduler tick and A's turn-3 is a cold miss. Engine's LRU is the only arbiter.

- **Bet**: A simple session-sticky bias at the proxy (session_id or messages-hash) holds A's next turn for a short window — more useful than smarter engine eviction because agent workloads are bursty per-session.
- **Risk**: This is a queue with a fancy name; if B is interactive and A is a background agent, sticky bias inverts user-visible priority.
- **First move**: `flexinfer_kv_eviction_estimated_total{model,reason}` counter (proxy detects "same session_id, different replica or fresh miss") with no behavior change — measure how often it fires before designing policy.

#### F4-distributed-kv-skip — Explicitly rule out cross-replica KV transfer

vLLM has experimental `KVConnector`. With 2-3 ROCm replicas behind a label group and no fast interconnect, cross-replica KV pull of a 16k context is likely slower than just re-running prefill. Flag and rule out so it doesn't burn F4 cycles.

- **Bet**: For our fleet, KV transfer cost > prefill cost; right answer is prefix-stable routing, not KV migration.
- **Risk**: We assume this without measuring; lab Mellanox could make the math flip.
- **First move**: Time a single HTTP GET of a 16k-token serialized KV slab between two pods. >2× the measured 32k prefill on gfx1100 = close the question.

### Product / UX (6 framings)

#### F4-instant-followup — Second turn feels free

The biggest perceived-quality lift in chat isn't the first answer — it's the second. Today every turn re-prefills; with prefix-cached 32k, turn-N TTFT is decode-only on the last user line. Conversations stop feeling like they're getting slower.

- **Bet**: TTFT on turn N is much less than TTFT on turn 1, and the user notices.
- **Risk**: If the client doesn't replay the exact prefix bytes, cache miss silently makes it feel identical to today.
- **First move**: Proxy emits `x-prefix-hit-tokens` and `x-ttft-ms` headers; assert 10-turn turn-10-TTFT ≤ turn-2-TTFT.

#### F4-codebase-resident — The repo lives in the model

Load `services/flexinfer/` curated source (~28k tokens) into the system prompt once at session start. Every "explain this function" is decode-only. Differentiator vs. ChatGPT-with-attachments: the codebase isn't *retrieved*, it's *resident*.

- **Bet**: "explain this function" answers in <2 s end-to-end on turn 2+ vs. 15–30 s with cold context + RAG.
- **Risk**: 28k of code may push gemma4-26b past its instruction-following sweet spot; long-context recall unproven on this exact GPTQ artifact.
- **First move**: Stuff `pkg/proxy/*.go` into a system prompt; ask "what HTTP status does the admission filter return for an over-budget request, and where is it set?" — expect "413, in `admission.go`" in under 2s on turn 2+.

#### F4-413-as-feature — The error IS the product

The admission filter's <1ms 413 is currently framed as a safety net. Reframe as a first-class affordance: response carries `tokens_budget`, `tokens_submitted`, `tokens_over`, `suggest_truncate_to`. Client UIs render this as "your prompt is 4.2k tokens over budget — trim or split?" with a one-click action.

- **Bet**: Instant honest failure beats slow ambiguous success. Users develop a mental model of "the box has a size."
- **Risk**: Requires client-side rendering work; without it, 413 just looks like a generic error.
- **First move**: Extend 413 JSON body with `tokens_over` + `suggest_truncate_to`; ship a 50-line Open WebUI userscript that shows a chip "−4.2k tokens" instead of a red error.

#### F4-multi-turn-tooling — Tool history is sunk cost

In a multi-tool agent loop, each tool call appends `tool_call → tool_result` to context. By turn 5, half the prompt is past transcripts. With prefix caching, every new tool call is decode-only against accumulated history. The loop *accelerates* as the agent does more work.

- **Bet**: A 10-tool-call research session completes in ~half today's wall time because turns 2-10 skip prefill on the growing prefix.
- **Risk**: Any non-deterministic field (timestamps, random IDs) busts the cache for every subsequent turn.
- **First move**: Canonicalize tool-result framing in loom-core (no timestamps, sorted keys); run a 5-tool agent-context recall task; compare wall-time vs. baseline.

#### F4-persistence-as-vibe — The model "remembers"

agent-context already persists session state. Combine: session start hydrates the last 24k tokens of relevant history into the prefix; user reopens next morning and the model picks up mid-thought. UX is NOT "long context" — it's "the model remembers me."

- **Bet**: Users describe this as "having a coworker" not "having a chatbot." Retention behavior changes qualitatively.
- **Risk**: Choosing *what* to hydrate is the hard problem; bad selection feels worse than no memory.
- **First move**: Hydrate a fixed pinned context (last session summary + 3 most-recent decisions); ask "what were we working on yesterday?"; expect a specific, accurate answer in <2s.

#### F4-streaming-floor — Without streaming, nothing matters

All of the above assumes streaming. TTFT *is* the chat UX; total-completion-time is secondary. If we shipped 32k prefix caching but TTFT regressed because of a buffering bug, users would say it got *worse*.

- **Bet**: SSE first-byte under 200 ms on prefix-cache hit is the threshold below which the system feels "instant."
- **Risk**: A single intermediate buffer (proxy, OpenAPI translation, client) hides all the gains.
- **First move**: Integration test measures wall time from `POST /v1/chat/completions` to first SSE `data:` line on a prefix-cache hit; assert <200 ms p50.

### Application architect (6 framings)

#### F4-tool-loop-as-prefix — Mutability-ordered context with append-only history

The Claude Code-style loop: immutable system + tool schemas → append-only history → only the latest assistant/tool round at the tail. Every tool round after the first is a prefix-cache hit on everything before the new result. 32k becomes per-session working memory at near-zero prefill cost per turn.

- **Bet**: Agent loops that treat context as append-only and order by mutability rate (slowest first) make cache hit rate the dominant perf variable, not decode speed.
- **Risk**: Most agent frameworks (LangChain, Assistants API) reorder messages each turn, silently invalidating the prefix.
- **First move**: Minimal Go/TS client running a 5-tool ReAct loop against gemma4-26b with `cache_key=session_id`; if hit% stays >90% across 20 rounds, bet holds.

#### F4-context-as-database — agent-context MCP becomes the disk; 32k window is RAM

loom-core exposes sessions, decisions, tasks, memory, handoffs. Today these are recall-on-demand. Flip: agent loop hydrates a structured "session brief" at session start, packs into 24-28k of stable prefix, only ever appends. The MCP is durable storage; the context window is hot working set.

- **Bet**: Agent quality on long-horizon work is gated by *what we put into the prefix*, not by model size.
- **Risk**: Hydration becomes a quality knob with no good default; needs a ranking layer (recency × relevance × pin-status).
- **First move**: `agent_context_recall_enhanced` → "render to 24k-token prefix" formatter + CLI that dumps the rendered prefix. Eyeball it; iterate the template before writing the loop.

#### F4-codebase-view-per-session — Glob-driven views, not per-turn RAG

The obvious anti-pattern: re-running vector retrieval on every user turn and re-stuffing different chunks → guaranteed cache miss. Inverted: pick a codebase "view" (5-15 files, ~20k tokens) at session start, freeze into prefix, let the conversation explore that view. New view → new session.

- **Bet**: Users naturally chunk work by codebase area; sessions scoped to fixed view feel snappier than dynamic-RAG sessions.
- **Risk**: Users want to follow threads across the codebase; need "fork session with expanded view" UX.
- **First move**: `flexinfer code-chat <path-glob>` CLI: glob + symbol-graph picks files, packs into prefix, chats. No vector DB yet — just glob-driven views.

#### F4-planner-executor-handoff — Two context regimes, both prefix-cached

Planner uses full 32k for big-picture (full files, prior decisions, task graph). Emits structured tasks (`agent_task_add`) with minimal context. Executor agents (smaller, faster) pick up tasks, do tool calls in narrow context, write results back via `agent_task_update`. Planner reads resolutions on next invocation.

- **Bet**: Separating "thinking about the whole" from "doing one thing" is the right factoring for a homelab agent; `agent_task_*` is the IPC.
- **Risk**: Most user-visible work doesn't decompose cleanly; planner ends up doing executor work inline.
- **First move**: "rewrite-this-package" task split into planner (gemma4-26b @ 32k) + executor (qwen3-1.7b tool-router). Measure: planner stays coherent across 5 task cycles?

#### F4-prefix-as-product — Cached prefix is a saveable, shareable artifact

If the prefix is what makes turns feel instant, treat it as first-class. A "preset" is a frozen prefix: system + tool schemas + curated knowledge pack. Loading a preset = warming the cache on a target replica. Users have a library; switching is sub-second because the proxy knows where the cache lives.

- **Bet**: Persistence + portability of *context* is more compelling than persistence of *chat history*. Presets = institutional knowledge.
- **Risk**: Cold preset feels no better than cold session; needs "pin" or "warm-on-schedule" affordance.
- **First move**: Extend `cache_key` header (CAR-2) to accept a preset ID; `flexinfer preset warm <id>` issues a 1-token completion to seed KV cache. Validate sub-second warm-start TTFT.

#### F4-shared-prefix-multiplexing — Multiple agents on one cached prefix

Two-three agents (research, drafting, critique) all need the same large grounding context. Three separate sessions = three prefills. With identical `cache_key` and additive per-agent suffixes, all three run against the same KV cache on the same replica. Effective concurrency >1 on one GPU's KV.

- **Bet**: Prefix-cache + per-agent suffix is the only way multi-agent fits on one Radeon VII.
- **Risk**: vLLM may not handle high-concurrency reads on the same prefix cleanly with admission contending.
- **First move**: Two parallel streams against the same gemma4 replica with identical 20k prefix + 500-token diverging suffixes; observe whether second stream's TTFT is decode-speed (hit) or prefill-speed (miss).

### Skeptic / devil's advocate (6 framings)

#### F4-skeptic-decode-cliff — Decode tok/s at 32k is the bottleneck, not TTFT

**Prefix caching saves prefill, not decode.** The matrix already shows gemma4-26b decode at 8k is **2.62 tok/s** — a 78% collapse from 12.20 at 2k. Extrapolating to 32k, decode is likely sub-1 tok/s. A 500-token reply takes 500+ s even with a perfect cache hit.

- **Hidden assumption**: Decode throughput on gfx1100 at 32k stays usable (>10 tok/s).
- **Kill-test**: Bench at 32k context with `max_tokens=256` on the existing Ready pod; measure tok/s.
- **Failure mode**: We build prefix cache + agent loop and the result is fast-start, slow-stream — worse UX than a snappy 8k loop with no agent context.

#### F4-skeptic-cache-key-fragility — Prefix cache invalidates on tiny changes

vLLM keys on exact token-prefix match. An agent loop that injects timestamps, tool IDs, retrieved-chunk ordering, or "turn N of M" headers breaks the prefix at the first divergence. The 5%-diff = 0%-cache pathology.

- **Hidden assumption**: Our agent loop produces token-identical prefixes across turns for ≥90% of the system+history block.
- **Kill-test**: Replay 20 real agent turns from existing agent-context sessions, tokenize each, measure longest common prefix.
- **Failure mode**: Ship prefix caching, measure hit rate <20%, TTFT no better than today.

#### F4-skeptic-maxNumSeqs-1-trap — Concurrency-1 means second user blocks for minutes

To hit 32k on 22 GB we set `maxNumSeqs: 1`. One turn at 32k takes 20s+. Second concurrent request queues. Worse: agent's own parallel tool calls become serial.

- **Hidden assumption**: F4's target is single-user-single-stream; or maxNumSeqs can be raised without OOM.
- **Kill-test**: Fire 2 concurrent 16k chat completions; measure p95 TTFT for the second.
- **Failure mode**: Beautiful agent loop that breaks the moment it makes 2 parallel tool calls or a second person tries it.

#### F4-skeptic-quality-collapse-past-16k — Long context ≠ usable long context

The 19k smoke worked because "Brown" is a needle-in-haystack retrieval. Real agent workloads need *reasoning over* the full context. Gemma4-26B-GPTQ's effective context may be far below its declared 32k — common pattern in GPTQ-quantized long-context models.

- **Hidden assumption**: Gemma4-26B-GPTQ produces quality answers across the full 32k, not just recall.
- **Kill-test**: 5 reasoning prompts (not retrieval) at 4k/8k/16k/28k; manually score answer quality.
- **Failure mode**: Marketing claims 32k working memory; users find the agent forgets / confuses earlier context past 16k.

#### F4-skeptic-cache-eviction-thrash — Cache survival across users is <advertised

At 32k + FP8 KV + maxNumSeqs=1, the KV pool is sized for *one* 32k sequence. Two users with different system prompts: the second evicts the first. The "returning user" experience requires cache to survive user-switches; the current memory math forbids it.

- **Hidden assumption**: vLLM's prefix cache holds ≥2 distinct 32k prefixes simultaneously on the configured pod.
- **Kill-test**: Enable APC, alternate 2 long system prompts 5×; measure `vllm:prefix_cache_hit_rate`.
- **Failure mode**: Pitched as multi-user agent; actually single-user agent that's slow whenever the wrong user shows up.

#### F4-skeptic-end-to-end-arithmetic — TTFT improvement is dwarfed by decode at small outputs

Typical agent turn: 16k context, 150-token response. With caching: 0s prefill + (150 / decode_tps) s decode. If decode at 16k is 5 tok/s, response takes 30s either way; saving 13s prefill means 30s instead of 43s. "Feels instant" was never on the table.

- **Hidden assumption**: At F4's target contexts, prefill dominates decode in total response time.
- **Kill-test**: Compute `decode_tail = expected_output_tokens / measured_decode_tps_at_context` for 3 realistic workloads (code review 16k/300, refactor 24k/500, Q&A 8k/150). Any decode_tail >10s means "feels instant" is impossible.
- **Failure mode**: Celebrate 90% prefill speedup; ship UX that still feels like a slow chat because the bottleneck moved to a place caching can't touch.

## Phase 2 — Cross-pollinate

### The skeptic's decode-cliff is the load-bearing thread

**F4-skeptic-decode-cliff intersects every other framing.** Existing matrix data: 2.62 tok/s at 8k. At 32k, decode could be <1 tok/s. If true, every product framing (`F4-instant-followup`, `F4-codebase-resident`, `F4-multi-turn-tooling`, `F4-persistence-as-vibe`) silently fails: turns might start fast, but the stream takes minutes. Every architectural framing (`F4-tool-loop-as-prefix`, `F4-context-as-database`) assumes the agent loop can run at usable speed. Every backend framing (`F4-prefix-cache-flip`, `F4-proxy-prefix-pinning`) is irrelevant if decode dominates anyway.

**The kill-test is one bench script extension that runs in 15 minutes against existing infrastructure.** It should run before any F4 implementation work.

### Backend ↔ skeptic pairs

- **F4-prefix-cache-flip + F4-skeptic-cache-eviction-thrash**: enabling APC without raising the KV pool may mean the cache holds <2 distinct prefixes. Run the eviction-thrash test as part of the same canary that flips APC.
- **F4-chunked-prefill-knob + F4-skeptic-maxNumSeqs-1-trap**: the chunked-prefill curve IS the answer to "can we raise maxNumSeqs?" — they're the same investigation framed two ways.
- **F4-ttft-vs-tpot-budget + F4-skeptic-end-to-end-arithmetic**: the predictor needs the curve from the bench; both feed admission decisions.

### Product ↔ application pairs

- **F4-codebase-resident + F4-codebase-view-per-session**: same use case, different scope. Product framing names the demo; application framing names the architecture (glob views, not per-turn RAG). Build them together.
- **F4-multi-turn-tooling + F4-tool-loop-as-prefix**: same insight at UX and architecture layers. The mutability-ordered, append-only context is what makes tool history a sunk cost.
- **F4-persistence-as-vibe + F4-context-as-database**: identical bet, different framings. The MCP is the disk; the model context is RAM; the UX is "remembered yesterday."
- **F4-413-as-feature** is the only framing that's purely UX with no architectural prereq beyond what's already shipped.

### The tensions (real choices)

- **F4-codebase-resident vs F4-codebase-view-per-session as the demo unit**: same use case, but "load the whole repo into context" (resident) implies one giant fixed prefix per repo; "view per session" implies many small prefixes per concern area. Different cache-locality profiles. Different presets in `F4-prefix-as-product` look very different under each.
- **F4-shared-prefix-multiplexing vs F4-skeptic-maxNumSeqs-1-trap**: the multiplexing framing assumes we can serve multiple suffixes on a shared prefix. The skeptic says we can't even raise maxNumSeqs above 1 without OOM. Run the kill-test before believing the multiplexing story.
- **F4-prefix-as-product vs F4-skeptic-cache-eviction-thrash**: presets only have value if the cache survives switches. If the KV pool can only hold one preset at a time, presets are aspirational.

## Phase 3 — Converge

### The pre-spec kill-test (must run before any F4 implementation)

Per the workspace `spec-riskiest-assumption.md` rule and the skeptic agent's analysis, the riskiest assumption for F4 is:

> **Gemma4-26B-GPTQ on gfx1100 at 32k context delivers usable decode throughput (≥ 5 tok/s) on a meaningful output tail (≥ 256 tokens).**

If false, **F4 fails structurally regardless of which framing we pick.** Prefix caching, agent loops, presets, multiplexing — none recover from decode at sub-1 tok/s.

**Kill-test recipe** (one afternoon, no new infrastructure):

```bash
# Extend scripts/bench-context-curve.sh to add 16384, 28000, 32768 points
# AND raise --max-tokens from 64 → 256 so the decode tail is measurable.
# Run against the existing Ready gemma4-26b-a4b-gptq pod.

POINTS=2k,4k,8k,16k,28k MAX_TOKENS=256 ./scripts/bench-context-curve.sh \
  --model gemma4-26b-a4b-gptq \
  --endpoint http://localhost:18080 \
  --report .loom/local/validation/context-curve/2026-05-25-decode-tail/report.json
```

Pass criteria (all three):

1. Decode tok/s at 16k ≥ 10
2. Decode tok/s at 28k ≥ 5
3. p95 end-to-end for 16k prompt + 256-token reply ≤ 60 seconds

If ≥1 fails: F4 is structurally bounded. The pivot is "32k = long-context retrieval and Q&A only" — short-output workloads — and we explicitly *don't* invest in multi-tool agent loops on this hardware.

If all pass: F4 implementation work unblocks (recommended path below).

### Recommended F4 first slice (conditional on kill-test pass)

The compound `F4-prefix-cache-flip + F4-tool-loop-as-prefix + F4-413-as-feature`:

1. **F4-prefix-cache-flip** (backend) — flip `enablePrefixCaching: true` on a canary clone of the production Gemma4 26B Model with `gpuMemoryUtilization: 0.94`. Instrument vLLM's `prefix_cache_hit_rate`. Run the cache-eviction-thrash kill-test (`F4-skeptic-cache-eviction-thrash`) on the canary; if it survives at the canary's pool size, promote APC to the primary.

2. **F4-tool-loop-as-prefix** (application) — write a minimal Go ReAct loop in `cmd/spec-decode-bench/` (it has the chat HTTP plumbing already; reuse it) that issues `cache_key=session_id`, builds an append-only context with mutability ordering, and instruments per-turn TTFT + cache-hit-tokens. Run a 5-tool agent-context recall task. Measure cache hit% across turns.

3. **F4-413-as-feature** (UX) — extend the admission filter's 413 response body with `tokens_over` and `suggest_truncate_to` fields. Smallest possible client-side rendering proof: a CLI that pretty-prints the structured error. Defer Open WebUI plugin until later.

These three compose cleanly and each delivers user-visible value if the kill-test passes. They're also small enough to ship in three sequential MRs over a session.

### Runner-up

**F4-codebase-resident** as a standalone product demo. If the kill-test passes but the agent-loop framing turns out hard, the demo "load the whole repo into context, ask three questions about it" is a self-contained proof of value that doesn't need a real agent loop.

### Explicit no's

- **F4-distributed-kv-skip** — explicitly NOT pursuing cross-replica KV transfer. Locked in.
- **F4-shared-prefix-multiplexing** — defer until we know whether `maxNumSeqs > 1` is feasible at 32k.
- **F4-prefix-as-product (presets)** — defer until APC survives cache-eviction kill-test.

### Open question

> **What's the smallest viable agent-loop client we can ship that demonstrates F4's value end-to-end?**

Options:
- **(a) CLI in this repo** (`cmd/agent-loop/`) — minimal, in-process, easy to instrument
- **(b) MCP-shaped tool that loom-core hosts** — integrates with existing agent-context surface
- **(c) Open WebUI plugin** — most user-visible, biggest external integration cost

Pick before writing the loop, because each one shapes the prefix layout differently.

## Sources

- All four sub-agent reports (backend / product / application / skeptic), produced 2026-05-25 via `/parallel-slice-ship`-shaped parallel Task spawn
- `.loom/brainstorm-rocm-fleet-unlocks-2026-05-25.md` — parent F1+F4 brainstorm
- `.loom/60-validation-matrix.md` — 32k Gemma PASS row, gfx906 draft BLOCK row, the 2.62 tok/s at 8k decode data point that's the skeptic's dagger
- `docs/planning/context-bounded-admission-spec.md` — admission filter (live, opt-in)
- `docs/planning/context-aware-router-execution.md` — CAR-1..CAR-5 prefix-aware routing (library exists; wire-up incomplete)
- `deploy/models/gemma4-26b-a4b-gptq.yaml` — `enablePrefixCaching: false` (load-bearing config)
- `scripts/bench-context-curve.sh` — the runner that the pre-spec kill-test extends
- `~/.claude/rules/spec-riskiest-assumption.md` — the workspace rule that demands a kill-test before infrastructure work

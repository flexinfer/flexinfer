# RALPH iteration plan: F4-tool-loop-as-prefix kill-test (2026-06-01)

Tracking:
- Brainstorm: `.loom/brainstorm-f4-long-context-agent-2026-05-25.md`
  (`F4-tool-loop-as-prefix` lines 140-146, `F4-instant-followup` lines 90-96)
- Predecessor slice: `.loom/ralph-f4-prefix-cache-flip-canary-2026-05-26.md`
  (canary verdict **conditional** 2026-05-28 — APC works at `maxModelLen ≤ ~20480`)
- Roadmap: ROADMAP.md "ROCm gfx1100 Performance Tuning" + Innovation Roadmap
- Owner: Cody (claude-code agent)
- Status: In Progress

## Goal

Land the smallest end-to-end increment of the F4 compound's **second leg**,
`F4-tool-loop-as-prefix`: an operator-run kill-test runner that proves (or
falsifies) the append-only single-session prefix-reuse bet.

The predecessor canary slice proved the *alternating two-prefix* (multi-tenant)
pattern survives eviction at reduced context (TTFT decay 17-24×, hit_rate
0.666). This slice tests the **distinct, still-unproven** *append-only growing*
pattern: a ReAct-style loop where the context is `system + tool-schemas` (immutable)
followed by an append-only history of `(user → assistant → tool-result)` rounds.
Every round after the first should be a prefix-cache hit on everything before
the new tail.

Ships as `scripts/f4-tool-loop-as-prefix.py`, mirroring the structure, schema-
versioning, proxy-header consumption, and offline `--self-check` mode of the
sibling `scripts/f4-apc-eviction-thrash.py`. The live run is the post-merge
operator follow-up (matrix row lands `pending`), exactly as the eviction-thrash
slice shipped.

## Non-Goals

- **Do not** ship a real ReAct *client/product* (CLI in `cmd/agent-loop`, MCP
  tool, or Open WebUI plugin). The brainstorm flags "pick the client form
  before writing the loop" as an open question; this slice ships the
  *kill-test* that gates that decision. Per the workspace
  spec-riskiest-assumption rule, the kill-test must pass before building the
  client.
- **Do not** flip APC on the production primary. The canary verdict already
  bounds that decision (`maxModelLen` drop required).
- **Do not** wire real tool execution. Tool results are synthetic, deterministic
  filler — the runtime only ever sees text; what matters is the append-only
  token layout, not tool semantics.
- **Do not** add the runner to CI. Like its sibling, it is an operator kill-test;
  `--self-check` is the offline gate.

## Current Evidence

- `scripts/f4-apc-eviction-thrash.py` — sibling harness: schema
  `flexinfer.f4_apc_eviction_thrash.v3`, proxy-header verdict cascade,
  `--self-check` offline mode, `upstream_ms_decay` aggregator.
- `internal/proxy/usage_log.go:119-141` — proxy emits `X-Flexinfer-Upstream-Ms`
  (every turn) and `X-Flexinfer-Cached-Tokens` (gated on
  `usage.prompt_tokens_details.cached_tokens`; **absent** on the gemma4 engine
  per the 2026-05-28 live run).
- `.loom/ralph-f4-prefix-cache-flip-canary-2026-05-26.md` "Live verdict
  2026-05-28" — APC structurally infeasible at 32k FP8 KV on the 22 GB cap;
  works at `maxModelLen 20480`; engine omitted `cached_tokens` so the verdict
  fell back to TTFT decay + `/metrics`.
- `.loom/brainstorm-f4-long-context-agent-2026-05-25.md:144` — the bet: "agent
  loops that treat context as append-only … make cache hit rate the dominant
  perf variable"; line 146 — "if hit% stays >90% across 20 rounds, bet holds".

## Riskiest assumption + kill-test

**Load-bearing assumption**: On the APC-enabled gemma4-26b-a4b-gptq canary at
`maxModelLen ≤ 20480`, an append-only chat loop (immutable system+tool-schema
prefix, then growing `(user, assistant, tool-result)` rounds re-sent in full
each turn) re-renders through vLLM's chat template such that each turn's prompt
is a **block-aligned prefix extension** of the previous turn. Therefore per-turn
prefill cost tracks only the *new* tail tokens, not total prompt length — i.e.
prefix-hit ratio stays ≥ 0.90 after warmup, and `upstream_ms` stays roughly flat
while `prompt_tokens` grows several-fold.

**Kill test** (operator runbook): Activate canary at `maxModelLen 20480` (per
the canary verdict). Run `scripts/f4-tool-loop-as-prefix.py` against the proxy
port-forward: 1 immutable system prefix (~6k tokens), then 20 append-only rounds
each adding one synthetic tool-result (~400 tokens) + one short user turn, full
context re-sent each round. Capture per-turn `upstream_ms`, `prompt_tokens`,
`cached_tokens`.
- **Pass**: median prefix-hit ratio over rounds 2..N ≥ 0.90 (matches the
  brainstorm bet). If `cached_tokens` is absent (engine omission), pass on the
  TTFT-flatness fallback: last-round `upstream_ms` ≤ round-2 `upstream_ms`
  despite ≥3× prompt growth.
- **Fail**: prefix-hit ratio < 0.50 (template re-render busts block alignment —
  cache reuse collapses) OR `upstream_ms` grows ∝ `prompt_tokens` (no APC
  benefit; prefill reprocesses the whole context every turn).
- **Conditional**: in between.

**Failure mode if assumption wrong**: We build a ReAct client believing tool
history is a "sunk cost" at near-zero per-turn prefill, ship "F4 agent loops feel
instant", and every multi-tool session reprocesses the full growing context each
turn — TTFT climbing linearly per round. The append-only/mutability-ordering
architecture (the whole point of F4-tool-loop-as-prefix) silently doesn't pay
off, and we'd have shipped a client on a false premise.

**Status**: not run (this slice ships the runner + offline self-check; the live
run is the post-merge operator follow-up). Matrix row lands `pending`.

## Acceptance criteria

- [ ] `scripts/f4-tool-loop-as-prefix.py` exists: append-only ReAct loop builder,
  proxy-header capture, prefix-hit-ratio + TTFT-flatness verdict cascade,
  versioned schema `flexinfer.f4_tool_loop_as_prefix.v1`, `--report` output.
- [ ] `--self-check` mode runs offline (no cluster): spins a canned-header HTTP
  server, verifies header parse + ratio aggregator + flatness assertion + the
  append-only message-builder shape; exit 0 on success.
- [ ] `python3 scripts/f4-tool-loop-as-prefix.py --self-check` passes locally.
- [ ] `python3 -c "import ast; ast.parse(open('scripts/f4-tool-loop-as-prefix.py').read())"`
  (syntax) and `ruff`/`py_compile` clean if available.
- [ ] Matrix row in `pending` lands in `.loom/60-validation-matrix.md` with the
  kill-test recipe, pass/fail/conditional gates, and rollback path.
- [ ] Operator runbook (below) names exact activate / measure / restore steps,
  reusing the *corrected* mechanism from the canary verdict
  (`flux suspend` + `forcePromotion` patch, NOT the inert annotations).
- [ ] CI green; MR merged.
- [ ] **Post-merge follow-up task** queued for the live kill-test run.

## Implementation Slices

| Slice | Target files | Owner boundary | Validation | Rollback |
|-------|--------------|----------------|------------|----------|
| 1 | `scripts/f4-tool-loop-as-prefix.py` (new), `.loom/60-validation-matrix.md` (pending row), this doc, `.loom/00-index.md` (nav pointer) | script+docs only; no Go changes | `--self-check`, `py_compile` | `git revert <merge>` removes the runner; no runtime touched |

## Operator runbook — kill-test recipe (post-merge)

Pre-conditions: master reconciled with the runner; canary Model present.

```bash
# 1. Suspend Flux so the canary patch is not reverted (corrected mechanism
#    from the 2026-05-28 canary verdict — annotations are INERT).
flux -n flux-system suspend kustomization flexinfer-models

# 2. Force-promote the canary at the APC-feasible context (canary verdict:
#    32k is structurally infeasible; 20480 passed eviction-thrash).
kubectl -n flexinfer-system patch model gemma4-26b-a4b-gptq-apc-canary \
  --type=merge -p '{"spec":{"runtime":{"maxModelLen":20480},"gpu":{"forcePromotion":true},"serverless":{"minReplicas":1}}}'

kubectl -n flexinfer-system wait --for=condition=Ready \
  model/gemma4-26b-a4b-gptq-apc-canary --timeout=15m

# 3. Proxy port-forward in another shell:
#      kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
#    (optional) canary /metrics:
#      kubectl -n flexinfer-system port-forward svc/gemma4-26b-a4b-gptq-apc-canary 18000:8000

# 4. Run the append-only tool-loop kill-test.
python3 scripts/f4-tool-loop-as-prefix.py \
  --endpoint http://localhost:18080 \
  --model gemma4-26b-a4b-gptq-apc-canary \
  --metrics http://localhost:18000/metrics \
  --rounds 20 \
  --report .loom/local/validation/f4-tool-loop/$(date -u +%F)/report.json

# 5. Restore.
kubectl -n flexinfer-system patch model gemma4-26b-a4b-gptq-apc-canary \
  --type=merge -p '{"spec":{"runtime":{"maxModelLen":32768},"gpu":{"forcePromotion":false},"serverless":{"minReplicas":0}}}'
flux -n flux-system resume kustomization flexinfer-models
```

Pass/fail update path: edit the `pending` matrix row + the post-merge task with
the verdict.

## Rollback / backout

- Pre-merge: drop the commits.
- Post-merge: `git revert` the MR — removes the runner + docs; no runtime,
  controller, or proxy code touched, so nothing in the cluster changes.

## Sources

- `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` — F4-tool-loop-as-prefix
  + F4-instant-followup framings and bets
- `scripts/f4-apc-eviction-thrash.py` — sibling harness this runner mirrors
- `internal/proxy/usage_log.go` — proxy response headers consumed for the verdict
- `.loom/ralph-f4-prefix-cache-flip-canary-2026-05-26.md` — canary verdict +
  corrected force-promote mechanism
- `~/.claude/rules/spec-riskiest-assumption.md` — the rule mandating this
  kill-test before the client slice

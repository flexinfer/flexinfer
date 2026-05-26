# RALPH iteration plan: F4-prefix-cache-flip canary (2026-05-26)

Tracking:
- Brainstorm: `.loom/brainstorm-f4-long-context-agent-2026-05-25.md`
- Roadmap: ROADMAP.md "ROCm gfx1100 Performance Tuning" + Innovation Roadmap
- Owner: Cody (claude-code agent)
- Status: In Progress

## Goal

Land the smallest end-to-end increment of the F4 brainstorm's recommended
first slice (`F4-prefix-cache-flip`): a side-by-side **APC canary** Model
on cblevins-7900xtx that mirrors the production `gemma4-26b-a4b-gptq`
config except for two flips:

- `enablePrefixCaching: true`
- `gpuMemoryUtilization: "0.94"` (down from `"0.98"` to leave headroom
  for the APC block pool when KV is FP8 at 32k)

The canary stays at `minReplicas: 0` with a low priority so it never
preempts the warm primary. Operator activates it on demand to run the
cache-eviction-thrash kill-test (`F4-skeptic-cache-eviction-thrash`).

## Non-Goals

- **Do not** flip APC on the warm primary in this slice. The primary
  carries production traffic; the eviction-thrash kill-test must pass
  on the canary before promotion.
- **Do not** ship the multi-turn ReAct loop client
  (`F4-tool-loop-as-prefix`). That is a sequential follow-up slice
  after APC is proven safe.
- **Do not** extend the 413 admission filter response
  (`F4-413-as-feature`). Independent slice; queued for later.
- **Do not** sweep `maxNumBatchedTokens` / `maxNumSeqs`
  (`F4-chunked-prefill-knob`). Defer until the kill-test passes.

## Current Evidence

- `deploy/models/gemma4-26b-a4b-gptq.yaml:118` — `enablePrefixCaching: false`
  is load-bearing on the production warm primary at 32k FP8 KV.
- `deploy/models/gemma4-26b-a4b-gptq.yaml:105` —
  `gpuMemoryUtilization: "0.98"` leaves ~2% headroom; APC needs more.
- `.loom/60-validation-matrix.md` row `gemma4-26b-a4b-gptq F4 decode-tail
  kill-test (2026-05-25)` — F4 decode is flat 50-67 tok/s 2k→28k; the
  prerequisite kill-test for any F4 implementation work has PASSED.
- `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` (lines 40-46,
  300-303) — F4-prefix-cache-flip first-move spec.
- `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` (lines 222-228,
  263) — F4-skeptic-cache-eviction-thrash kill-test recipe.

## Riskiest assumption + kill-test

**Load-bearing assumption**: vLLM 0.19+'s native APC, on a hybrid
GPTQ+FP8 KV gemma4-26b at 32k context with
`disableHybridKVCacheManager: true` and `disableAiter: true`, retains
prefix-cache blocks across at least 2 distinct ≥30k prefixes at
`gpuMemoryUtilization: 0.94`. If false, APC at 32k holds only one
prefix at a time and every user-switch is a cold miss — the F4 product
framings ("instant follow-up", "model remembers me", "shared prefix
multi-tenant") all silently break.

**Kill test** (operator runbook): Activate canary with traffic. Send
two distinct system-prompt prefixes A and B, each ~30k tokens.
Alternate ABABAB × 5 with 256-token completions. Scrape
`vllm:prefix_cache_hit_rate` and per-request `prompt_tokens` / TTFT
from proxy usage logs (now live, MR !518). Pass: hit_rate ≥ 50%
**after the third A/B alternation** (cache holds both prefixes).
Fail: hit_rate stays at <20% — eviction is thrashing on every switch.

**Failure mode if assumption wrong**: We promote APC, declare F4
"feels instant" GA, and the first multi-user session shows the second
user gets fresh-prefill latency on every turn. We back out and
either raise the KV pool (requires lower `maxModelLen` or higher
`gpuMemoryUtilization`, both costly) or pivot to single-session-only
APC use cases.

**Status**: not run (this slice ships the infrastructure to run the
kill-test; the run itself is the post-merge follow-up task).

This slice does NOT pre-judge the kill-test outcome. The matrix row
lands in `pending` and is updated to `pass` / `fail` only after the
live run.

## Requirements

- Functional: a new `Model/gemma4-26b-a4b-gptq-apc-canary` CR on
  `cblevins-7900xtx`, scale-to-zero, low-priority, identical-otherwise
  to the production primary except for APC flip + utilization knob.
- Operational: an activation runbook so the operator can preempt the
  primary, run the kill-test, and revert without touching code.
- Observability: existing `vllm:prefix_cache_hit_rate` is automatic
  once APC is on; proxy usage log captures per-request prompt/completion
  tokens + TTFT (MR !518). No new instrumentation required.
- Compatibility: zero change to the production primary, sister
  5930k, or any shared-route serviceLabel.
- Security/RBAC: none changed.

## Acceptance criteria

- [ ] `deploy/models/gemma4-26b-a4b-gptq-apc-canary.yaml` exists with
  APC=true, gpuMemoryUtilization=0.94, minReplicas=0, priority well
  below primary's 350, warmPolicy=ondemand, dedicated `serviceLabels`
  (no shared aliases).
- [ ] `deploy/models/kustomization.yaml` reconciles the new Model.
- [ ] `go test ./...` (or targeted package) passes for any touched
  Go code (expected: none).
- [ ] `kubectl --dry-run=client -f` cleanly applies the new manifest.
- [ ] `kustomize build deploy/models/` includes the new resource.
- [ ] Matrix row in `pending` lands in `.loom/60-validation-matrix.md`
  with the kill-test recipe and rollback path.
- [ ] Operator runbook in this doc names the exact preempt / activate /
  measure / restore steps.
- [ ] CI green; MR merged.
- [ ] **Post-merge follow-up task** queued for the live kill-test run.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| 1 | `deploy/models/gemma4-26b-a4b-gptq-apc-canary.yaml` (new), `deploy/models/kustomization.yaml`, `.loom/60-validation-matrix.md`, `.loom/ralph-f4-prefix-cache-flip-canary-2026-05-26.md` (this doc) | manifest+docs only; no Go changes | `go test ./...` (sanity), `kubectl --dry-run=client`, `kustomize build deploy/models/` | `git revert <merge>` removes the canary Model; primary unaffected throughout |

## Operator runbook — kill-test recipe (post-merge)

Pre-conditions:
- Master is reconciled with the canary Model (Flux Ready).
- `kubectl -n flexinfer-system get model gemma4-26b-a4b-gptq-apc-canary`
  shows `Idle`.

Steps:

```bash
# 1. Park the warm primary on this node so the canary can claim both
#    AMD GPU slots (iGPU dedup pattern; same nodeSelector).
kubectl -n flexinfer-system annotate model gemma4-26b-a4b-gptq \
  flexinfer.ai/pause="true" --overwrite

# 2. Force-promote the canary.
kubectl -n flexinfer-system annotate model gemma4-26b-a4b-gptq-apc-canary \
  flexinfer.ai/force-promote="$(date -u +%FT%TZ)" --overwrite

# 3. Wait for Ready (cold cache copy + engine init expected ~5-7 min).
kubectl -n flexinfer-system wait --for=condition=Ready \
  model/gemma4-26b-a4b-gptq-apc-canary --timeout=15m

# 4. Run the alternating-prefix kill-test. Two ~30k-token system
#    prompts A and B, alternate ABABAB × 5, short 256-token user
#    suffix per turn. Capture vllm:prefix_cache_hit_rate before/after.
python3 scripts/f4-apc-eviction-thrash.py \
  --endpoint http://localhost:18080 \
  --model gemma4-26b-a4b-gptq-apc-canary \
  --report .loom/local/validation/f4-apc/2026-05-XX-eviction-thrash/report.json

# 5. Scrape metric snapshot before/after for the row.
kubectl -n flexinfer-system port-forward \
  svc/gemma4-26b-a4b-gptq-apc-canary 18000:8000 &
curl -s http://localhost:18000/metrics | grep prefix_cache_hit_rate

# 6. Restore the primary.
kubectl -n flexinfer-system annotate model gemma4-26b-a4b-gptq \
  flexinfer.ai/pause- --overwrite
kubectl -n flexinfer-system annotate model gemma4-26b-a4b-gptq-apc-canary \
  flexinfer.ai/force-promote- --overwrite
```

Pass / fail update path: edit the `pending` matrix row and the
post-merge follow-up task with the run outcome.

## Agent Delegation Notes

Single-agent slice. The manifest + matrix row + runbook all touch
disjoint files from any concurrent stream. No fan-out planned.

## Rollback / backout

- Pre-merge: drop the canary commits.
- Post-merge, pre-kill-test: `git revert` the canary MR; Flux
  deletes the Model.
- Mid-kill-test: drop `flexinfer.ai/force-promote` and `pause`
  annotations (step 6 above) — primary returns immediately.
- Post-kill-test (fail): leave the canary in place if useful as a
  diagnostic surface; otherwise `git revert`.

## Sources

- `.loom/brainstorm-f4-long-context-agent-2026-05-25.md` — F4
  brainstorm convergence + skeptic kill-test
- `.loom/60-validation-matrix.md` — F4 decode-tail PASS row (the
  prerequisite kill-test)
- `deploy/models/gemma4-26b-a4b-gptq.yaml` — production primary
  config the canary mirrors
- `deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml` —
  precedent for `force-promote` + `pause` orchestration during a
  canary kill-test
- `docs/planning/spec-capsule-template.md` — slice contract this
  doc follows

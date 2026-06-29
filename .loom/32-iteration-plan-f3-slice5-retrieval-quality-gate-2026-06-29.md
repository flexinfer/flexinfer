# Iteration plan — F3 Slice 5: retrieval-quality gate (F1 tie-in)

**Date:** 2026-06-29
**Loop:** roadmap-spec-ralph-loop
**Parent plan:** [.loom/30-implementation-plan-f3-retrieval-readpath-2026-06-25.md](30-implementation-plan-f3-retrieval-readpath-2026-06-25.md) → Slice 5
**Branch:** `claude/dazzling-chaum-6c5f5f`

## Context

F3 Slices 1–4 shipped (kill-test PASS, `codebase-answer` read-path service,
`/v1/rag` proxy route, hard 18-Q chunking bake-off, multi-file diversification,
multi-repo index coverage). The remaining build slices are **5 (retrieval-quality
gate)** and **6 (multi-turn prefill)**. Slice 6 is a *live measurement* on the APC
canary (cluster-only, out of reach from the Mac). Slice 5 is mostly **offline,
testable code** — the natural next RALPH increment.

The `model-eval-gauntlet` CronJob measures **throughput only** (runs
`flexinfer-bench` per model). Slice 5's job (the brainstorm's repurposed F1): give
it a **retrieval-quality dimension** so an index / chunker / answer-model change is
*gated*, not hoped. The kill-test harness (`eval/f3-retrieval/f3eval.py`) already
does embed→search→rerank→generate→judge and builds per-question rows; what is
missing is the reusable **gate**: absolute quality thresholds + a structured score
row + PASS/FAIL verdict (the kill-test's verdict is *retrieval-vs-naive*, not an
absolute quality bar).

## Scope

**In:**
- `eval/f3-retrieval/rqgate.py` — a **pure-stdlib kernel** (no I/O, no env at
  import) that turns f3eval's per-question rows into: (a) an aggregate quality
  score, (b) a two-dimension gate verdict (`ev_ratio` = retrieval recall,
  `judge_ratio` = answer synthesis — the two axes Slice 3 separated), (c) a flat
  `result_row` for storage/emission. Includes a `--self-check` that runs the
  kernel on a synthetic fixture (proves wiring with no cluster).
- Minimal extension to `eval/f3-retrieval/f3eval.py`: when `RQ_GATE` is set, after
  its existing aggregation, emit a single-line `RQ_RESULT_JSON {…}` gate row via
  `rqgate` (reuses the existing retrieval loop and row shape — **zero new I/O**).
  Best-effort import so f3eval is unchanged when `rqgate` is absent / `RQ_GATE`
  unset (byte-for-byte current behaviour by default).
- `eval/f3-retrieval/test_rqgate.py` — pure-stdlib `unittest` for the kernel,
  runnable standalone (mirrors `test_readpath.py`).
- CI `rqgate_test` lint job (`python:3.12-alpine`) gating the kernel + test +
  f3eval (mirrors `readpath_test` / `reembed_test`).
- `eval/f3-retrieval/job.rq.example.yaml` — the in-cluster activation recipe (a
  retrieval-only gauntlet companion: `RQ_GATE=1 SKIP_NAIVE=1`, no NFS workspace
  needed since naive stuffing is skipped), looping the answer model(s).
- README updates (eval + gauntlet) + parent-plan status update.

**Out:**
- A Flux-managed scheduled CronJob for the gate. Deferred *on purpose*: shipping a
  scheduled gate as a Flux resource means either embedding a self-contained copy of
  the retrieval primitives in a ConfigMap (two-source drift vs the canonical
  `f3eval.py`) or building+publishing an eval image (heavier than one slice). You
  also should not *schedule* an unvalidated gate. So this slice ships the kernel
  (proven offline) + the activation recipe; promoting to a scheduled Flux CronJob
  is the documented fast-follow once the gate is live-validated — same
  dormant→activate cadence as Slices 2.1 / 3.1 / 4.
- Tuning the gate thresholds against live numbers (needs the cluster). Defaults are
  set conservatively from the Slice-3 bake-off (`ev` ~15–16/18, `judge` ~8–9/18)
  and are env-overridable.
- LLM-judge / comprehension-at-length expansion (brainstorm F7) — separate slice.

## Acceptance criteria

1. `rqgate.aggregate` computes correct `n / judge_correct / judge_partial /
   kw_correct / ev_retrieved` counts and `judge_ratio / ev_ratio / kw_ratio` from
   f3eval-shaped rows (partial-credit weight configurable; default 0.5).
2. `rqgate.gate` returns `PASS` only when `judge_ratio ≥ min_judge_ratio` **and**
   `ev_ratio ≥ min_ev_ratio`; `FAIL` (with per-dimension reasons) otherwise; `n=0`
   is a `FAIL` (never a vacuous pass).
3. `rqgate.result_row` emits a flat, JSON-serialisable row tagged
   `kind=retrieval_quality` carrying model, collection, counts, ratios, verdict.
4. `python3 eval/f3-retrieval/rqgate.py --self-check` exits 0 offline.
5. `f3eval.py` with `RQ_GATE=1` prints exactly one `RQ_RESULT_JSON` line; with
   `RQ_GATE` unset its output is byte-for-byte unchanged (no `RQ_RESULT_JSON`).
6. `python3 eval/f3-retrieval/test_rqgate.py` is green; new `rqgate_test` CI job
   gates the kernel + test + f3eval.
7. Live (post-merge, documented activation): the example job emits an
   `RQ_RESULT_JSON` gate row for the answer model against
   `codebase_memory_bge_v1`.

## Risk notes

- **Threshold choice is a guess until live.** → Gate is **advisory by default**
  (`RQ_FAIL_ON_GATE` unset → the runner never fails the job on a gate FAIL, only
  reports it); thresholds are env-overridable; defaults documented as provisional
  from the Slice-3 numbers. Zero risk of a bad threshold blocking anything.
- **Can't run the live retrieval path from the Mac.** → The pure kernel is fully
  unit-tested; `--self-check` proves the wiring; the network path is identical to
  f3eval's already-proven path (reused, not rewritten). Live row is the documented
  activation step (same cadence as Slices 2.1/3.1/4).
- **f3eval drift if rqgate changes its row shape.** → `rqgate` reads the exact keys
  f3eval already writes (`row["retrieval"]["kw"|"judge"]`, `row["ev_retrieved"]`);
  a test pins the shape, and f3eval imports rqgate (single source for the gate).
- **Scope creep into a full eval framework (F7).** → Explicitly out; this slice is
  the *gate*, not new graders.

## Dependency / blocker map

- No code deps; additive + dormant. Reuses the live primitives Slices 1–4 proved.
- Scheduled Flux CronJob promotion is **blocked** on live threshold validation
  (needs cluster) — documented fast-follow, not part of this slice.

## Test plan

- `python3 eval/f3-retrieval/rqgate.py --self-check` (offline wiring gate).
- `python3 eval/f3-retrieval/test_rqgate.py` (kernel unit tests; devbox + local).
- `python3 eval/f3-retrieval/test_readpath.py` (unchanged — regression that the
  f3eval extension didn't break the read-path tests).
- f3eval default-output parity: `RQ_GATE` unset emits no `RQ_RESULT_JSON`.
- Live (post-merge activation): apply `job.rq.example.yaml` →
  expect one `RQ_RESULT_JSON {... "verdict": ...}` line per answer model.

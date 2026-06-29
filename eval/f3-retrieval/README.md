# F3 retrieval kill-test harness

The harness that proved **retrieval beats raw context-stuffing** for agent /
repo-in-context work (F3,
[.loom/30-implementation-plan-f3-retrieval-readpath-2026-06-25.md](../../.loom/30-implementation-plan-f3-retrieval-readpath-2026-06-25.md)).
Kept here (reusable) because the F3 plan's Slice 3 (chunking bake-off) and Slice 5
(retrieval-quality gate) re-run it.

## What it does

`f3eval.py` (pure stdlib, runs on `python:3-slim`) scores a set of grounded
repo-Q&A three ways through the live flexinfer-proxy + qdrant and prints a
verdict:

- **retrieval** — embed query (bge-large) → search a qdrant index → rerank
  (bge-reranker) → top-K context → generate
- **naive** — stuff the repo in walk order up to a char budget → generate
- **baseline** — no context → generate

Scoring = keyword-match (deterministic) + an **independent** LLM judge. Keyword
strictness applies identically to all three conditions, so it can't bias the
retrieval-vs-naive comparison; the judge is the semantic backstop.

## Result (2026-06-25, loom-core, `codebase_memory_bge_v1`)

| condition | keyword | judge | avg ctx tokens |
|---|---|---|---|
| retrieval | 16/16 | 16/16 | 972 |
| naive (~30K tok) | 0/16 | 0/16 | 30,000 |
| baseline | 0/16 | 0/16 | 0 |

30.9× token savings; evidence retrieved into top-6 = 16/16. A confirmatory run
favoring naive (relevant dirs only, ~42.5K tok near the 64K ceiling) still gave
retrieval 16/16, naive 0/16 (43.7×) — a 1,720-file repo doesn't fit any window.

## Hard set + Slice 3 (chunking bake-off)

`questions.hard.loomcore.json` (18 Q) is the **harder** set: multi-file /
behavioral / long-construct / semantic-distance questions that do NOT saturate
retrieval, so chunking strategies can be told apart. Run with `SKIP_NAIVE=1`
(naive/baseline are index-independent — skip the big prefills for comparison runs).

Bake-off 2026-06-25 (retrieval-only, `qwen36-35b` answerer, gemma4 judge):

| chunking | chunks | ev_retrieved | judge CORRECT | kw |
|---|---|---|---|---|
| current 45/8 (`codebase_memory_bge_v1`) | 21,607 | 15/18 | 8/18 | 4/18 |
| high-overlap 40/20 (throwaway) | 34,379 | 16/18 | 9/18 | 2/18 |

Findings:
- **Hard set discriminates** (judge 8–9/18 vs the easy set's 16/16) — Slice 3 unblocked.
- **Overlap is not the lever**: +59% chunks (4× slower indexing) → within-noise +1 judge,
  even with a freshness confound favoring the high-overlap (fresher) collection. Dropped it.
- **The bottleneck is answer synthesis, not retrieval recall**: `ev_retrieved` 15–16/18 but
  `judge` ~8–9/18 — the right file is usually found; the model fails to assemble the answer
  on multi-file/behavioral questions. Next levers: more/multi-file context per query
  (`RETR_K`↑, pull `secondary_paths`), or a stronger answer model — not chunk tuning.
- `kw` (exact multi-substring) is too strict for long-form answers; trust `judge` here.

### Slice 3.1 — multi-file context diversification

Finding (3) above (right files found, multi-file answers not assembled) is acted on
by `diversify_selection` in `f3eval.py`: it caps how many reranked chunks one file
contributes to the top-K context (`MAX_PER_PATH`, default `0` = disabled = original
top-K slice) and back-fills so context is never starved. This pulls *secondary*
files into the window with no extra upstream calls (it re-selects from the same
top-N rerank). The pure logic is unit-tested in `test_readpath.py` (CI `readpath_test`
job); the `codebase-answer` service carries an inline mirror.

To measure it, re-run the hard set twice on the **same** collection and diff `judge`:

```bash
# baseline (diversification off)
MAX_PER_PATH=0 SKIP_NAIVE=1 ...   # via the Job env
# diversified
MAX_PER_PATH=3 SKIP_NAIVE=1 ...
```

## Slice 5 — retrieval-quality gate

The kill-test's verdict is *retrieval-vs-naive* (a comparison). A **promotion
gate** needs an *absolute* bar, so an index / chunker / answer-model change is
caught when quality drops — not only when it loses to stuffing. `rqgate.py` is the
pure kernel for that: it turns the per-question rows `f3eval.py` already builds into
an aggregate score, a **two-dimension** PASS/FAIL verdict, and a flat score row.

The two axes mirror the Slice-3 finding (recall and synthesis are distinct failure
modes):

- `ev_ratio` — fraction of questions whose evidence file reached the top-K context
  (**recall**: did we even fetch the right file);
- `judge_ratio` — fraction judged CORRECT, with partial credit (**synthesis**: did
  the model assemble the answer).

A regression in *either* fails the gate. Defaults (`RQ_MIN_EV_RATIO=0.8`,
`RQ_MIN_JUDGE_RATIO=0.6`, `RQ_PARTIAL_WEIGHT=0.5`) are provisional, set from the
Slice-3 hard-set numbers (`ev` ~0.83, `judge` ~0.47) and env-overridable.

`f3eval.py` emits the gate row when `RQ_GATE=1` — it reuses the existing retrieval
loop, so this adds **zero new I/O**; with `RQ_GATE` unset the output is
byte-for-byte unchanged (no `RQ_RESULT_JSON`). A run prints one machine-readable
line:

```
RQ_RESULT_JSON {"kind":"retrieval_quality","model":"…","collection":"…","n":18,
  "judge_correct":…,"ev_retrieved":…,"judge_ratio":…,"ev_ratio":…,"verdict":"PASS|FAIL",…}
```

This is the gauntlet's retrieval-quality output — the sibling of the throughput row
`model-eval-gauntlet` stores per model. `job.rq.example.yaml` runs it in-cluster
(retrieval-only, `RQ_GATE=1 SKIP_NAIVE=1`, **no NFS mount** since naive is skipped).
Pure logic is unit-tested in `test_rqgate.py` (CI `rqgate_test`); the kernel has an
offline wiring gate:

```bash
python3 rqgate.py --self-check      # offline, no cluster
python3 test_rqgate.py              # kernel unit tests
```

**Promotion to a scheduled Flux CronJob is a documented fast-follow** once the
thresholds are validated against a first live run (you should not schedule an
unvalidated gate). Until then the gate row is advisory — emitted, not enforced.

## Run it (in-cluster Job)

`job.example.yaml` is the exact Job used (mirrors `deploy/tasks/codebase-reembed`:
NFS workspace mount + proxy + qdrant secret). To run:

```bash
kubectl -n flexinfer-system create configmap f3-killtest-script --from-file=f3eval.py=f3eval.py
kubectl -n flexinfer-system create configmap f3-killtest-questions --from-file=questions.json=questions.loomcore.json
kubectl -n flexinfer-system apply -f job.example.yaml
kubectl -n flexinfer-system logs -f job/f3-killtest
```

Key env knobs (see `f3eval.py`): `COLLECTION` (swap to compare indexes — Slice 3),
`NAIVE_CHAR_BUDGET`, `NAIVE_ROOTS` (focus naive on specific dirs), `RETR_N`/`RETR_K`,
`CHAT_MODEL`, `JUDGE_MODEL`.

## Files

- `f3eval.py` — the harness (emits `RQ_RESULT_JSON` when `RQ_GATE=1`)
- `rqgate.py` — pure retrieval-quality gate kernel (Slice 5): aggregate / gate / row
- `questions.loomcore.json` — 16 adversarially-verified, non-guessable loom-core Q&A
- `questions.hard.loomcore.json` — 18 harder, non-saturating Q&A (Slice 3+)
- `test_readpath.py` — unit tests for `diversify_selection` (Slice 3.1)
- `test_rqgate.py` — unit tests for the retrieval-quality gate kernel (Slice 5)
- `job.example.yaml` — in-cluster kill-test runner
- `job.rq.example.yaml` — in-cluster retrieval-quality gate runner (Slice 5)

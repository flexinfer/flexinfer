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

- `f3eval.py` — the harness
- `questions.loomcore.json` — 16 adversarially-verified, non-guessable loom-core Q&A
- `job.example.yaml` — in-cluster runner

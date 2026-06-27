# Iteration plan — F3 Slice 4: index coverage (multi-repo re-embed)

**Date:** 2026-06-26
**Loop:** roadmap-spec-ralph-loop
**Parent plan:** [.loom/30-implementation-plan-f3-retrieval-readpath-2026-06-25.md](30-implementation-plan-f3-retrieval-readpath-2026-06-25.md) → Slice 4
**Branch:** `feat/f3-index-coverage-multirepo`

## Context

F3 Slices 1–3.1 shipped (kill-test PASS, `codebase-answer` read-path service,
`/v1/rag` proxy route, hard 18-Q bake-off, multi-file diversification). The
retrieval index (`deploy/tasks/codebase-reembed/`) still covers **loom-core
only**. Slice 4 acceptance: **≥2 agent-relevant repos queryable through the
Slice-2 endpoint.**

## Scope

**In:**
- Generalize the nightly `codebase-reembed` CronJob to index **multiple repos in
  one run**, each into its **own Qdrant collection** (loom-core keeps its
  existing `codebase_memory_bge_v1`; loom → `codebase_memory_bge_loom_v1`).
- Backward-compatible config: new `REPOS` env (`name=path[=collection]`,
  `;`-separated); empty `REPOS` falls back to the legacy
  `REPO_PATH`/`REPO_NAME`/`COLLECTION` behaviour byte-for-byte.
- Per-repo resilience: a missing repo path is skipped with a `WARN` (one absent
  repo never fails the whole nightly); exit non-zero only if **no** repo indexed.
- Offline unit test (`test_reembed.py`) + CI lint gate, mirroring `readpath_test`.
- README + parent-plan status update.

**Out:**
- Repo-filtered single-collection search (chose per-repo collections instead —
  zero retrieval-semantics change for existing loom-core consumers; the
  `/v1/answer` endpoint already takes a `collection` param).
- Widening the `devbox-ws` NFS export to mirror flexinfer / other repos — that's
  a **platform/gitops** change (flagged as a follow-up). Only loom + loom-core
  are on the mirror today, which is exactly the ≥2 this slice needs.
- AST chunking bake-off (Slice 3 finding: chunk tuning is not the lever).
- On-change vs nightly freshness redesign (kept nightly; out of scope).

## Acceptance criteria

1. `parse_repos` yields the legacy single-repo record when `REPOS` is empty and a
   correct multi-repo list when set; malformed records raise `ValueError`.
2. Point IDs are repo-namespaced so the same `path:chunk_index` in two repos does
   **not** collide (regression-guarded by test).
3. loom-core still lands in `codebase_memory_bge_v1` (no live read-path change).
4. `python3 deploy/tasks/codebase-reembed/test_reembed.py` is green; new
   `reembed_test` CI job gates the configmap + test.
5. `kustomize build deploy/tasks/codebase-reembed` renders cleanly.
6. (Post-merge / live) a one-shot job indexes loom + loom-core; `/v1/answer` with
   `collection=codebase_memory_bge_loom_v1` answers a loom question.

## Risk notes

- **Multi-repo in one collection would mix results** (no repo filter in
  `qdrant_search`). → Mitigated by per-repo collections.
- **Legacy collection rename would break the live default.** → loom-core's
  collection is pinned explicitly to `codebase_memory_bge_v1` in `REPOS`.
- **loom may be absent from the NFS mirror.** → skipped with WARN, not fatal;
  acceptance still met by loom-core alone, but the intent is ≥2.
- **Can't run the cluster job from the Mac** (qdrant/proxy unreachable). → Offline
  test covers the pure multi-repo logic; live one-shot is the documented
  activation step (same cadence as Slices 2.1/3.1).

## Dependency / blocker map

- No code deps; additive. Live coverage of flexinfer itself is **blocked** on the
  platform/gitops NFS-export widening (separate repo, flagged as follow-up).

## Test plan

- `python3 deploy/tasks/codebase-reembed/test_reembed.py` (devbox + local).
- `kustomize build deploy/tasks/codebase-reembed`.
- Live (post-merge): `kubectl -n flexinfer-system create job
  --from=cronjob/codebase-reembed reembed-adhoc` → expect
  `re-embed DONE repos=2/2 …`; then `/v1/answer` against the loom collection.

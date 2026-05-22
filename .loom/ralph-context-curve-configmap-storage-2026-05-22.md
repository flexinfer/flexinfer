# RALPH: Context-Curve ConfigMap Storage

Date: 2026-05-22

## Intent

Close CC-4 from `docs/planning/next-roadmap.md` after the first live
context-curve run proved the report shape. The slice keeps scheduler behavior
unchanged and adds only an opt-in storage path for report evidence.

## Alignment

- Scope in:
  - Add `--store-configmap` / `STORE_CONFIGMAP=1` to
    `scripts/bench-context-curve.sh`.
  - Store the generated JSON report in `flexinfer-context-curve-results` under
    a unique per-run key.
  - Document the storage command and mark CC-4 complete.
- Scope out:
  - No scheduler scoring changes.
  - No CRD changes.
  - No changes to the existing one-number benchmark result ConfigMaps.

## Acceptance

- The runner still writes the local JSON report by default.
- ConfigMap storage is disabled unless explicitly requested.
- Stored reports are additive: a new key is patched into the ConfigMap without
  replacing older report keys.
- Docs explain that context-curve reports are evidence only and are ignored by
  scheduler consumers.

## Validation

Commands run from the branch worktree:

```bash
REPORT_DIR=$(mktemp -d); REPORT_DIR="$REPORT_DIR" ./scripts/bench-context-curve.sh --dry-run --points 2k,8k --iterations 1 --warmup 0 && python3 -m json.tool "$REPORT_DIR"/bench-context-curve-*.json >/dev/null
REPORT_DIR=$(mktemp -d); REPORT_DIR="$REPORT_DIR" ./scripts/bench-context-curve.sh --dry-run --points 2k --store-configmap --kubectl true
git diff --check
```

Result: all passed. The ConfigMap command used `--kubectl true` to validate the
opt-in path without mutating cluster state during local proof.

## Handoff

Next roadmap work should move back to measured evidence rather than storage
plumbing:

- Run another live context curve on a second model family before proposing any
  scheduler or controller use.
- Keep the existing benchmark result ConfigMaps as the scheduler-owned data
  path until a later spec proves the curve data should influence placement.

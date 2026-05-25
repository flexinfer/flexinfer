# RALPH: gfx906 Proxy Soak Diag Probe

Date: 2026-05-25

## Review

- Previous live verdict:
  `.loom/ralph-gfx906-proxy-soak-activation-gate-2026-05-23.md` Decision
  section. The 900s activation preflight passed activation but failed on
  4/10 measured requests with proxy log `dial tcp 10.43.137.91:8000:
  i/o timeout` against `svc/qwen3-8b-radeonvii-soak`. Runtime logs had no
  matching entries at the failed timestamps while successful attempts
  returned HTTP 200 from llama.cpp.
- Closed blockers in the cascade leading here:
  - MR !480 — cross-group runtime unload guard
    (`controllers/model_runtime.go`).
  - MR !484 — `file://` source preserved as runtime `modelPath`.
- The current blocker is upstream of the runtime, somewhere on the
  proxy → selectorless-Service → backend `:8000` path. Runtime logs
  staying silent at failure timestamps is the strongest tell.

## Align

- Slice name: proxy-soak diag probe.
- Scope in:
  - Add a single, bounded diagnostic probe that runs immediately after
    each soak request failure (preflight and measured).
  - Embed the diag result inline in the same JSONL record so the failure
    moment is self-correlated.
  - Default the probe at `flexinfer-proxy /healthz`. Override surface is
    one env var so the next preflight can test:
    - the proxy itself (`/healthz`),
    - a sibling model via the proxy
      (`/model/qwen3-1p7b-tools-radeonvii/v1/chat/completions`),
    - or any other target without re-rolling the manifest.
- Scope out:
  - No controller / proxy / runtime code changes.
  - No new manifests, RBAC, or Service objects.
  - No live cluster run in this source-only slice.

## Acceptance Criteria

- `python3 -m py_compile` passes on the extracted `proxy-soak.py`.
- A zero-attempt, no-network smoke prints a clean `soak_start` +
  `soak_summary` pair with no traceback.
- A forced-failure smoke (`SOAK_ENDPOINT` and `SOAK_DIAG_ENDPOINT`
  pointed at an unreachable port) emits a single JSONL record with both
  the outer failure and an embedded `diag` object containing
  `endpoint`, `ok`, `elapsed_seconds`, and `error` keys.
- `yaml.safe_load_all` parses all three docs in the manifest
  (`PersistentVolumeClaim`, `ConfigMap`, `Job`).
- The Job env block documents the diag intent and the override surface.

## Test Plan

- `python3 -m py_compile` on the extracted script.
- Zero-attempt no-network smoke:
  `SOAK_DURATION_SECONDS=0 SOAK_PREFLIGHT_ATTEMPTS=0
  SOAK_DIAG_ENDPOINT="" python3 proxy-soak.py`.
- Forced-failure smoke:
  `SOAK_ENDPOINT=http://127.0.0.1:1/will/fail
  SOAK_DIAG_ENDPOINT=http://127.0.0.1:1/will/fail
  SOAK_DIAG_TIMEOUT_SECONDS=1 SOAK_PREFLIGHT_ATTEMPTS=1
  SOAK_DURATION_SECONDS=0 python3 proxy-soak.py`.
- `python3 -c "import yaml; list(yaml.safe_load_all(open('deploy/debug/gfx906-llamacpp-proxy-soak.yaml')))"`.

All four passed locally on 2026-05-25.

## Risk Notes

- The diag probe runs ONLY in failure paths, so a healthy preflight or
  soak is bit-equivalent to the prior script. Worst case if the diag
  endpoint is itself unreachable: each failure record gains one
  `diag.ok: false` entry with a short error string — no extra latency
  on the main loop because the diag timeout defaults to 5s.
- The default `flexinfer-proxy /healthz` is a known-cheap GET. It will
  not exacerbate proxy load during a failure storm.
- If the operator wants a sibling-model diag (to test whether other
  models on the same runtime answer when `qwen3-8b-radeonvii-soak`
  times out), the env override is one line at apply time.

## Handoff

Next live gate:

1. Apply `deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml`.
2. Apply `deploy/debug/gfx906-llamacpp-proxy-soak.yaml` with
   `SOAK_DURATION_SECONDS=900` for an activation preflight rerun. The
   default diag endpoint will record proxy `/healthz` reachability at
   every failure moment.
3. On any failure: read `proxy-soak.jsonl` filtered to records with
   `ok: false`. The embedded `diag` block tells you whether the proxy
   itself was alive when the per-Model request timed out.
4. If `diag.ok=true` consistently while measured `ok=false`: the gap
   is the per-Model selectorless Service or the backend `:8000` port,
   not the proxy. Next slice is either an EndpointSlice audit or a
   sibling-model A/B (set `SOAK_DIAG_ENDPOINT` to the
   `qwen3-1p7b-tools-radeonvii` proxy route and rerun).
5. If `diag.ok=false` correlates with measured `ok=false`: the proxy
   itself is unreachable at those moments, which would shift the
   investigation to `flexinfer-proxy` pod health or upstream service
   networking.
6. Harvest `proxy-soak.jsonl` and `proxy-soak-summary.json` from the
   evidence PVC and update `.loom/60-validation-matrix.md` with the
   verdict.

## Sources

- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md` Status
  section (updated by MR !488 to point at row 186 as the current
  blocker).
- `.loom/60-validation-matrix.md` row 186 (`gfx906 proxy-soak
  activation preflight (2026-05-23)`).
- `.loom/ralph-gfx906-proxy-soak-activation-gate-2026-05-23.md`
  Decision section (handoff: "fix [selectorless Service /proxy
  reachability] before rerunning the full 24 hour proxy-backed soak").

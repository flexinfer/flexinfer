# RALPH: Context-Curve Live Capture

Date: 2026-05-22

## Review

- Prior slice: MR !472 added `scripts/bench-context-curve.sh` and marked CC-2
  complete.
- Current roadmap item: CC-3, capture one live curve on an existing Gemma4 or
  Qwen lane and link it from `.loom/60-validation-matrix.md`.
- Gfx906 soak status during selection: `gfx906-llamacpp-soak-traffic` was still
  `Running` at age `6h49m`; the traffic log tail showed attempts `358` through
  `407` returning HTTP 200 around `13.7-14.0 ms/token`. Soak harvest and
  Radeon VII promotion remain out of scope until the 24 hour gate completes.

## Align

- Slice name: context-curve live capture.
- Scope in:
  - Run the new context-curve script against one Ready warm model.
  - Store raw evidence under `.loom/local/validation/context-curve/2026-05-22/`.
  - Add a concise tracked evidence summary to `.loom/60-validation-matrix.md`.
- Scope out:
  - No scheduler scoring.
  - No controller, CRD, runtime profile, or ConfigMap consumer changes.
  - No service-label or alias promotion.
- Target chosen:
  - `gemma4-26b-a4b-gptq` on `cblevins-7900xtx`.
  - Reason: Ready, warm, production-relevant, explicit model route avoids
    shared-label round-robin across the slower 5930k sister lane.

## Land

- Evidence command:

```bash
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 18080:80
REPORT_DIR=.loom/local/validation/context-curve/2026-05-22 \
  MODEL=gemma4-26b-a4b-gptq \
  ENDPOINT=http://127.0.0.1:18080 \
  ./scripts/bench-context-curve.sh --points 2048,8192 --iterations 1 --warmup 1 --timeout 900
```

## Prove

- 2048 target:
  - observed prompt tokens: `1872`
  - completion tokens: `13`
  - measured elapsed: `1.066s`
  - prefill throughput: `1756.2 tok/s`
  - decode throughput: `12.20 tok/s`
- 8192 target:
  - observed prompt tokens: `7292`
  - completion tokens: `13`
  - measured elapsed: `4.958s`
  - prefill throughput: `1470.8 tok/s`
  - decode throughput: `2.62 tok/s`
- Summary:
  - `total_points=2`
  - `passed=2`
  - `failed=0`
  - `skipped=0`
  - `first_failure_point=null`

## Handoff/Harvest

- Raw report:
  `.loom/local/validation/context-curve/2026-05-22/bench-context-curve-gemma4-26b-a4b-gptq-context-curve-20260521T215333-dcd797.json`
- Stdout:
  `.loom/local/validation/context-curve/2026-05-22/context-curve-stdout.log`
- Tracked summary:
  `.loom/60-validation-matrix.md`
- Next-slice candidates:
  - CC-4: decide whether additive ConfigMap storage is worth doing now or
    whether to first capture a second model family for comparison.
  - If the `gfx906` soak completes first, harvest soak logs and update the
    matrix before more context-curve work.

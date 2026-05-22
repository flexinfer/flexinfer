---
title: Context-Curve Benchmarking
description: Reporting-only benchmark workflow for long-context runtime curves.
---

# Context-Curve Benchmarking

Use `scripts/bench-context-curve.sh` when you need a quick, machine-readable
view of how a model/runtime lane behaves as prompt context grows. The script is
reporting-only: it does not change scheduler scoring, controller behavior,
runtime profiles, CRDs, or benchmark ConfigMap consumers.

## What It Measures

For each requested context point, the runner records:

- target context tokens
- observed prompt and completion tokens when the backend reports usage
- average elapsed seconds
- p95 elapsed seconds
- approximate prefill throughput
- approximate decode throughput
- optional free-VRAM samples before and after the point
- per-sample status and error details

Larger points are independent. If a `32k` point fails, the report still keeps
the `2k` and `8k` evidence.

## Common Commands

Dry-run the default ladder and inspect the JSON shape:

```bash
./scripts/bench-context-curve.sh --dry-run
```

Run a small two-point curve through the FlexInfer proxy:

```bash
MODEL=gemma4-26b-a4b-gptq \
ENDPOINT=http://flexinfer-proxy.flexinfer-system.svc.cluster.local \
./scripts/bench-context-curve.sh --points 2048,8192 --iterations 2 --warmup 1
```

Run against a direct port-forwarded backend:

```bash
DIRECT=1 \
MODEL=/models \
ENDPOINT=http://127.0.0.1:8000 \
./scripts/bench-context-curve.sh --points 2k,8k --max-tokens 64
```

Capture free-VRAM samples with a custom command that prints a numeric MB value:

```bash
VRAM_COMMAND='kubectl get node cblevins-7900xtx -o jsonpath={.metadata.annotations.flexinfer\.ai/gpu-free-memory}' \
./scripts/bench-context-curve.sh --points 2048,8192
```

Store the same JSON report in a ConfigMap for later comparison:

```bash
MODEL=gemma4-26b-a4b-gptq \
STORE_CONFIGMAP=1 \
./scripts/bench-context-curve.sh --points 2048,8192 --iterations 1
```

## Output

The script writes one JSON report under `REPORT_DIR`:

```text
/tmp/bench-context-curve-<model>-context-curve-<timestamp>-<rand>.json
```

The stable top-level contract is:

```json
{
  "schema_version": "flexinfer.context_curve.v1",
  "model": "gemma4-26b-a4b-gptq",
  "context_curve": {
    "points": [
      {
        "context_tokens_target": 2048,
        "status": "pass",
        "prompt_tokens_observed": 1560,
        "prefill_tokens_per_second_avg": 123.4,
        "decode_tokens_per_second_avg": 45.6
      }
    ],
    "summary": {
      "total_points": 2,
      "passed": 2,
      "failed": 0,
      "skipped": 0,
      "first_failure_point": null
    }
  }
}
```

`status` is one of `pass`, `fail`, or `skip`. Dry runs mark all points as
`skip` with `reason: dry_run`.

When `--store-configmap` or `STORE_CONFIGMAP=1` is set, the runner also stores
the report JSON in `flexinfer-context-curve-results` in `flexinfer-system` by
default. Each run gets a unique `<model>-<run_id>.json` key, so storing a new
report does not replace older reports. Override the target with
`--configmap-name`, `--configmap-namespace`, or the matching environment
variables `CONTEXT_CURVE_CONFIGMAP` and `NAMESPACE`.

## Notes

- Prompt token targets are approximate because the script does not use a
  tokenizer. Use the backend's reported `prompt_tokens_observed` for measured
  evidence.
- Prefill and decode throughput are approximations derived from total request
  elapsed time unless the backend exposes more detailed timing in the response.
- Treat the first implementation as evidence capture only. Scheduler changes
  require a later spec and at least two model families with comparable live
  curves.
- ConfigMap storage is additive evidence only. The scheduler still reads the
  existing benchmark result ConfigMaps and ignores context-curve reports.

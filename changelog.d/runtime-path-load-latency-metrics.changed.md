`flexinfer_model_cold_start_duration_seconds` buckets now reach 1800 s (from 300 s) and
`flexinfer_model_swap_duration_seconds` 900 s (from 120 s, matching
`flexinfer_runtime_model_swap_duration_seconds`), so the 5930k's ~767 s dedicated-Deployment cold
start is resolved instead of landing in `+Inf`. Both gained a `serving_path` label
(`deployment` | `runtime`). Runtime-served Models now observe both histograms, once per
transition to Ready. Before this they recorded neither, so the two serving paths could not be
compared. The label is `serving_path`, not `path`, because the exporter's cardinality guard
reserves `path` for URL paths. No series was renamed. The chart's Grafana panels
(`charts/flexinfer/templates/grafana-dashboard.yaml`) aggregate with `sum by (le, model)` and
`sum by (le, group)`, so they keep working and sum across both paths. The
`flexinfer_model_load_seconds` help text now says what the gauge records: the most recent
activation-to-Ready load, not cache pull time.

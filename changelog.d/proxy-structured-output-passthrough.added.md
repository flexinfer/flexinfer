flexinfer-proxy forwards structured-output requests byte for byte (D4). The `model` rewrite, the
`max_tokens` clamp and the QoS `priority` field now splice only their top-level value, so
`response_format` (`json_schema`, `json_object`, `structural_tag`), the Responses `text.format`,
vLLM `structured_outputs` and `tools` reach the lane exactly as sent on every forward, reroute and
replay. Before, a body whose schema declared a `model` property ahead of the top-level `model`
was re-encoded through a Go map, which sorted the schema's keys (xgrammar generates fields in
schema order) and could overwrite a nested string `model` value. Schema-constrained requests are
tagged on the `request usage` log line (`schema_constrained=true constraint=<kind>`) and counted
in `flexinfer_proxy_schema_constrained_requests_total{model,constraint,code}`, and the
`GET /v1/models/{name}` descriptor reports `structured_output: true` for a lane whose
structured-output certificate is rendered.

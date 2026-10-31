flexinfer-proxy resolves every inference request to a caller QoS class (`interactive`,
`background`, `batch`): the `X-FlexInfer-QoS` header, then the caller map in ConfigMap
`flexinfer-proxy-qos` (chart values `proxy.qos`, hashed bearer key or caller name), then a LiteLLM
alias suffix such as `workhorse-background`, then `interactive`. The class labels the usage log and
three new metrics (`flexinfer_proxy_qos_decisions_total`, `_qos_ttft_seconds`,
`_qos_request_duration_seconds`). Nothing is enforced yet: every lane counts requests as
`unenforced` and serves them as before.

flexinfer-proxy enforces caller QoS classes on lanes that opt in (`spec.config.schedulingPolicy:
priority`, or the annotation `flexinfer.ai/qos: proxy`). Interactive requests are always forwarded;
background and batch requests take a seat only while `flexinfer.ai/qos-interactive-reserve` seats
(default 1 of `maxNumSeqs`) stay free, otherwise they wait in a class-ordered queue (60 s, 32 deep
by default) and then get 503 `queue_full` in the error envelope with `Retry-After` and their queue
position (`flexinfer.queue_position`, a new always-present envelope key, and
`X-FlexInfer-Queue-Position`). The cold-start queue drains in the same class order. No lane opts
in yet, so nothing changes in production.

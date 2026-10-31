On a lane with `spec.config.schedulingPolicy: priority`, flexinfer-proxy sets vLLM's per-request
`priority` on chat and text completions from the caller's QoS class (`interactive` 0, `background`
10, `batch` 20), overwriting any caller-supplied value. Lanes without priority scheduling forward
the body untouched, since vLLM rejects a non-zero priority there. No lane runs priority scheduling
yet.

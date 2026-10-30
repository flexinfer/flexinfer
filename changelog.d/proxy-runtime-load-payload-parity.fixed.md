The proxy's direct runtime load now sends the same payload as the controller. Both paths render it
with `pkg/runtime.NewModelLoadPayload`, which lists the Model's LoRAAdapters itself. Before, the
proxy left out `enableLora`, `maxLoras` and `maxLoraRank`, so a load the proxy won started the
lane without its adapters, under a `specHash` the controller never rendered. A LoRAAdapter list
error now fails the render instead of loading without adapters; the dedicated Deployment path keeps
its old behaviour. The proxy also waits for a direct load as long as the Model's own
`serverless.coldStartTimeout` allows (the same budget the runtime gets as `startupTimeoutSeconds`),
not the proxy-wide `PROXY_COLD_START_TIMEOUT`, which is 60 s by default.

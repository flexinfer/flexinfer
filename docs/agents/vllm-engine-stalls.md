# vLLM engine stalls

vLLM's API server can answer /health while its GPU engine is hung. FlexInfer checks
/metrics every 30 seconds. Outstanding running or waiting requests with unchanged
prompt and generation token totals for five minutes indicate an engine stall.
Idle engines and cold loads with no requests do not trigger the detector.

Set the Model's backend config to override the window:

```yaml
spec:
  backend: vllm
  config:
    stallWindow: "5m"
```

Dedicated vLLM pods use an exec readiness probe that checks both /health and token
progress. Startup probes retain their existing cold-load allowance. Missing or
invalid metrics fail the readiness probe; controller and runtime scrape failures
reset their progress history rather than infer a stall across missing samples.

On a stall the controller sets EngineStalled=True, emits a Warning/EngineStalled
event naming the GPU node and counters, marks the model Failed so the proxy
returns 503, and deletes the serving pod once. Recovery stays Failed until the
replacement becomes Ready. A recurring stall or replacement startup timeout
latches Failed/GPUUnresponsive and scales its Deployment to zero.

The restart budget survives pod/controller restarts in the parent Model annotation
flexinfer.ai/engine-stall-recovery. To retry after repairing the host GPU, remove it:

```sh
kubectl annotate model MODEL -n flexinfer-system flexinfer.ai/engine-stall-recovery-
```

A GPUUnresponsive latch also clears if the controller observes the node's
amd.com/gpu or nvidia.com/gpu allocatable become unavailable and then recover.
This does not reset the host GPU. /proc/1/wchan inspection is not required and
no pods/exec permission is added.

Runtime-managed vLLM processes use the same detector, mark model health Failed
(503), and expose the frozen counters in their error. The controller publishes
their EngineStalled condition/event; pod restart recovery applies to dedicated
model Deployments, not the shared runtime pod.

Metrics: flexinfer_model_engine_stalled{model,namespace} and
flexinfer_model_engine_stall_restarts_total{model,namespace}. Runtime metrics use
POD_NAMESPACE, defaulting to flexinfer-system.

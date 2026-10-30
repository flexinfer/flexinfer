A Model whose launch depends on `spec.config.command` is no longer eligible for the node runtime.
The runtime load payload has no command field, and the runtime always starts the backend's own
launcher, so a wrapper such as `bash -lc 'export FLEXINFER_QWEN35_MTP_EXPERTS_GPTQ=1; exec vllm …'`
would have been dropped silently, together with the env it sets (for the INT4 MTP head, garbage
output). Both the controller and the proxy's direct-load path now send such a Model to its
dedicated Deployment. If the runtime already holds it, the controller unloads it first, as it does
for `dedicatedDeployment`. The Model carries `RuntimeEligible=False` (reason `LaunchOverride`), set
even while `dedicatedDeployment` is what routes it, so the blocker is visible before anyone drops
the opt-out. A `RuntimeLaunchOverride` warning, throttled to one per hour per Model, is recorded
when the override is the deciding reason. Today both wrapper lanes (`qwen35-35b-clean-mtp-32k` and
`qwen36-35b-mtp-uncensored-5930k`) also set `dedicatedDeployment`, so only the condition is new for
them.

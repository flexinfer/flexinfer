`spec.capabilities.structuredOutput` on the Model (D4). A vllm Model that sets it gets
`--structured-outputs-config {"backend":"auto"}` only when it runs as a dedicated Deployment on a
digest-pinned image and the GPUProfile carries the complete `vllm/structured-outputs`
backend-capability certificate for that exact image (`flexinfer.ai/backend-certificate-vllm-structured-outputs`
plus `-since` and `-evidence`). Otherwise nothing is rendered, the lane keeps serving, and the new
`StructuredOutput` condition says why. WorkflowProfile capability matching knows the field. No GPUProfile
carries the certificate and no Model sets the field, so nothing live changes; the certificate follows
kill-test KT-SO.

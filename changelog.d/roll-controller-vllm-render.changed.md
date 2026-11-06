The k3s controller moves from the d5aa35a7 build (`sha256:1286a2f1…`, tag 20261102-144015) to the
51e6bd81 build (`sha256:6a3ad9e6…`, tag 20261105-153904), so `spec.config.vllmVersion` takes effect:
a lane that declares vLLM 0.32 or 0.33 no longer renders the `VLLM_*` variables that release removed
(or `--mamba-cache-mode` from 0.33), on vLLM and vllm-omni alike. The roll also carries the
refused-fit runtime unload (3a502a43), `enableAiter` rendered on CDNA 3+ only, and the structured-output
gate that certifies backend `auto` only (!1291). No live pod template or runtime launch payload of a
Model without `vllmVersion` changes, so no lane restarts. The proxy stays on the 72628e0e build, so
declare `vllmVersion` on Deployment-path lanes only until the proxy is rolled.

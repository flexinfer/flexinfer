The k3s controller moves from the d5aa35a7 build (`sha256:1286a2f1…`, tag 20261102-144015) to the
ccb2b61b build (`sha256:78a5ef9a…`, tag 20261105-030246), so `spec.config.vllmVersion` takes effect:
a lane that declares vLLM 0.32 or 0.33 no longer renders the `VLLM_*` variables that release removed
(or `--mamba-cache-mode` from 0.33). The roll also carries the refused-fit runtime unload
(3a502a43). No live pod template or runtime launch payload changes, so no lane restarts. The proxy
stays on d5aa35a7, so declare `vllmVersion` on Deployment-path lanes only until the proxy is rolled.

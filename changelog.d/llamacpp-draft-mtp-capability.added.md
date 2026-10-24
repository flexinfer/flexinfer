The certified llama.cpp feature surface accepts `spec.config.specType: draft-mtp` behind a new
GPUProfile capability certificate, `flexinfer.ai/backend-certificate-llamacpp-draft-mtp`
(plus `-since` / `-evidence`). It drives the MTP (NextN) head inside the target GGUF with no
`--model-draft`, at llama.cpp's default draft window: `draftMax` / `draftMin` are rejected for
this spec type because b11847 removed `--draft-max` / `--draft-min` and aborts at startup when
they are passed. The existing gates still apply (`dedicatedDeployment: true`, a digest-pinned
selected image equal to the certified one). No GPUProfile carries the certificate yet, so
behaviour is unchanged until the gfx1100 b11847 measurement lands.

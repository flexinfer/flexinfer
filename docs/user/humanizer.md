# Humanizer in the unified runtime

Humanizer is a Gemma 4 12B prose-rewriting model. It accepts one draft per
request. The serving recipe is `deploy/models/humanizer.yaml`:
Q6_K on the Radeon VII, 8192 context tokens, one slot, and scale to zero after
30 idle seconds. It uses the existing `radeonvii-models` group and the unified
runtime's llama.cpp subprocess loader. It has no model-specific image or
`dedicatedDeployment` setting.

The pinned model revision is `7232532cb8466bd393544e97826e12e6b5e471ac`.
`humanizer-12b-Q6_K.gguf` is 10,029,799,584 bytes with SHA-256
`c98f03bb9e71456181f99b0e1d3391e07ce1afc357f9db4c33d6d379b8dd9f0d`.
The Local cache stages only this file and `prompt_format.json`.

## Runtime build and qualification

`build/Dockerfile.runtime-humanizer-gfx906` overlays llama.cpp b11335 onto the
digest-pinned unified gfx906 image. Compilation uses that image's ROCm SDK,
and the final image refreshes the Go runtime manager while retaining Ollama,
diffusers, and the HIP memory-info shim. No serving components run in the build stage. This avoids
rebuilding the other backends while qualifying a new llama.cpp version.

```sh
docker --context 7900xtx build \
  -f build/Dockerfile.runtime-humanizer-gfx906 \
  -t registry.harbor.lan/flexinfer/runtime:rocm-gfx906-humanizer-b11335 .
docker --context 7900xtx push \
  registry.harbor.lan/flexinfer/runtime:rocm-gfx906-humanizer-b11335
```

The load-bearing assumption is that b11335 runs Gemma 4 on gfx906 while
preserving the existing embedding, reranking, and voice lanes. The kill-test
is an all-layer GPU load and coherent rewrite inside the unified runtime,
followed by embedding, reranking, and voice probes and a return-to-idle check.
Compilation alone does not pass this gate. The canonical gfx906 matrix, local
build helper, and CI publish job also select b11335; other GPU profiles retain
their qualified versions. Profile drift checks prevent a standard promotion
from silently replacing this qualified image with an older off-profile build.

Promote the resulting immutable digest through the gfx906 runtime profile in
`deploy/system/values-k3s.yaml` and `deploy/gpuprofiles/gfx906.yaml`, using Git
and Flux. Retain the original digest as rollback:
`sha256:171081dee2b593153b1e2b999f77d080f5a86e42144c3b20b4153a7b3b73910b`.
For rollback, also disable Humanizer registration (`litellm.enabled: false`)
in Git: that older image cannot load the Gemma 4 architecture.
The recipe is registered as `humanizer` in FlexInfer and LiteLLM. It serves
completion and chat-completion paths only. The GPU probe on 2026-10-12
offloaded 49/49 layers, loaded cached Q6_K weights in 6.13 seconds, and rewrote
the 185-token test prompt into 89 output tokens in 2.87 seconds (~37 tokens/s).
The same binary passed GPU embedding dimension/finite-value checks, relevance
ordering, and Orpheus audio-token generation. These native probe timings exclude
proxy election and queueing; measure that overhead during the managed swap test.

Priority 130 permits rewrite demand to borrow VRAM from the voice subprocess.
The 11,000 MB scheduling estimate retains the 1,500 MB BGE embedding, 700 MB
reranker, and 2,600 MB GTE members within the 16,384 MB group budget. Orpheus
may unload during rewrites. The initial managed probe measured 10,956 MiB for
Humanizer plus BGE; the additional retrieval reservations leave more than
2,000 MiB of physical headroom. Qualify the full resident set before promotion.
The group uses a five-second `gpu.swapCooldown` override. The controller honors
shorter overrides and releases short-idle VRAM reservations when their idle
window ends, instead of retaining the legacy five-minute reservation.
Foreground activity, including queued cold requests, refreshes every five
seconds and at request completion. Health and metrics probes do not refresh
inference activity. Multi-model runtimes admit elected peers without applying
the single-model replacement guard; runtime capacity checks still enforce fit.

The gfx906 runtime enables `FLEXINFER_RUNTIME_REQUEST_DRAIN`. Each model's
advertised port passes through an ingress inside the same container; its
backend subprocess listens on a separate local port. Unload closes admission
to new requests and returns HTTP 409 while accepted requests finish, including
streams and background requests. The controller retains the endpoint and
retries after drainage. Insufficient VRAM returns retryable HTTP 503 instead
of launching before the previous subprocess releases memory. The proxy's
direct-load path also waits for shared-GPU election, so it respects cooldown.

These are subprocess swaps inside one persistent pod. Image upgrades require
a one-time rollout; requests do not create or replace model containers. Check
pod UID and restart count across load, idle unload, and reload when qualifying.
On gfx906, `HSA_OVERRIDE_GFX_VERSION` must be absent, including inherited image
values. An empty string still hides the GPU in HIP. Humanizer selects `ROCm0`
explicitly so missing GPU support fails startup instead of serving on CPU.

## Stack calls

For the most reproducible integration, read the pinned `prompt_format.json`
once and build `instr + "\n\n" + draft.strip() + sep`. Send that prompt to
`POST /v1/completions` through the FlexInfer proxy with `model: "humanizer"`:

```json
{
  "model": "humanizer",
  "prompt": "<instruction, trimmed draft, and separator from prompt_format.json>",
  "temperature": 1.0,
  "top_p": 0.95,
  "top_k": 0,
  "min_p": 0,
  "repeat_penalty": 1.0,
  "max_tokens": 2048,
  "stream": false
}
```

Read `choices[0].text`; require `finish_reason != "length"`. Do not supply stop
strings. Validate the prompt by checking that the SHA-256 of the prompt built
for draft `X` starts with `cc51d66b4c593fbe`.

The pinned GGUF also contains the author's custom chat template. With Jinja
enabled, `/v1/chat/completions` can accept a single user message containing the
draft, using the same sampling values. Keep the GGUF's template; do not
override it with a generic Gemma template. The direct completion route avoids
chat-template handling differences across intermediaries.

Use this as an optional final prose pass after generation. Preserve the
original draft, check numbers/names/quotes and language, and fall back to the
draft on a failed rewrite. The author's evaluation reports occasional factual
changes and wrong-language output. Split long documents by prose section;
8192 tokens must hold the instruction, draft, and output together.

Sources: [model card](https://huggingface.co/jialinyyzz/humanizer),
[runtime and prompt requirements](https://huggingface.co/jialinyyzz/humanizer/blob/7232532cb8466bd393544e97826e12e6b5e471ac/AGENTS.md),
[prompt format](https://huggingface.co/jialinyyzz/humanizer/blob/7232532cb8466bd393544e97826e12e6b5e471ac/prompt_format.json),
[llama.cpp b11335](https://github.com/ggml-org/llama.cpp/releases/tag/b11335).

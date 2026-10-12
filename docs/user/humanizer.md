# Humanizer in the unified runtime

Humanizer is a Gemma 4 12B prose-rewriting model. It accepts one draft per
request. The qualification candidate is `deploy/debug/humanizer-radeonvii.yaml`:
Q6_K on the Radeon VII, 8192 context tokens, one slot, and scale to zero after
two idle minutes. It uses the existing `radeonvii-models` group and the unified
runtime's llama.cpp subprocess loader. It has no model-specific image or
`dedicatedDeployment` setting.

The pinned model revision is `7232532cb8466bd393544e97826e12e6b5e471ac`.
`humanizer-12b-Q6_K.gguf` is 10,029,799,584 bytes with SHA-256
`c98f03bb9e71456181f99b0e1d3391e07ce1afc357f9db4c33d6d379b8dd9f0d`.
The Local cache stages only this file and `prompt_format.json`.

## Runtime build and qualification

`build/Dockerfile.runtime-humanizer-gfx906` overlays llama.cpp b11335 onto the
digest-pinned unified gfx906 image. Compilation uses that image's ROCm SDK,
and the final image retains its runtime manager, Ollama, diffusers, and HIP
memory-info shim. No serving components run in the build stage. This avoids
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
Compilation alone does not pass this gate.

Promote the resulting immutable digest through the gfx906 runtime profile in
`deploy/system/values-k3s.yaml` and `deploy/gpuprofiles/gfx906.yaml`, using Git
and Flux. Retain the original digest as rollback:
`sha256:171081dee2b593153b1e2b999f77d080f5a86e42144c3b20b4153a7b3b73910b`.
Once qualified, move the candidate to `deploy/models`, list it in that
directory's kustomization, and enable its LiteLLM registration as `humanizer`.

Priority 130 permits rewrite demand to borrow VRAM from the existing
retrieval/voice subprocesses. The initial 12,500 MB estimate should retain the
1,500 MB BGE embedding and 700 MB reranker members within the 16,384 MB group
budget. GTE and Orpheus may unload during rewrites. Measure the actual peak
before changing this estimate. The group's five-minute swap cooldown can
delay returning all members to the card after a rewrite.

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

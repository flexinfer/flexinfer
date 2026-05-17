# Qwen3.5 GPTQ — Layout Adapter Design (Framing B)

**Date:** 2026-05-10
**Branch:** `feat/qwen35-gptq-layout-adapter`
**Status:** Design + initial implementation
**Lineage:** See `.loom/brainstorm-qwen35-gptq-monkeypatch-2026-05-10.md` (Framing B). Replaces the
monkey-patch chain shipped across MRs !293, !294, !300, !301, !302, !304, !305.

## 1. Problem statement

GPTQModel's `Qwen3_5*QModel` definitions hardcode the multimodal Qwen3-VL layout
(`model.language_model.*`, `model.visual.*`, `model.lm_head` etc.). Our pipeline ships a
**text-only** abliterated checkpoint with the standard causal-LM layout (`model.layers.*`,
`model.norm`, `model.embed_tokens`, top-level `lm_head`). Every cycle, GPTQModel internals
expose another hardcoded attribute that disagrees with our flat layout, and we add another
monkey-patch (`module_tree`, `loader`, `pre_lm_head_norm_module`, `rotary_embedding`, ...).

The layout adapter inverts the strategy: instead of teaching GPTQModel about our layout, we
**wrap our checkpoint into the layout GPTQModel already expects**, run quantization unmodified,
then unwrap the result for vLLM serving.

## 2. Source layout (text-only abliterated Qwen3.5-27B)

Authoritative key prefixes (from a real abliteration output, mirroring HuggingFace
`Qwen/Qwen3.5-27B`-style flat layout). Every `*` below stands for sublayer paths
`(self_attn|mlp|input_layernorm|post_attention_layernorm).*`.

```
model.embed_tokens.weight
model.layers.{0..63}.*
model.norm.weight
lm_head.weight
```

`config.json`:
```json
{
  "architectures": ["Qwen3_5ForCausalLM"],
  "model_type": "qwen3_5_text",  // or "qwen3_5", flat
  "num_hidden_layers": 64,
  ...
}
```

## 3. Target layout (Qwen3-VL multimodal sibling)

Pulled from `Qwen/Qwen3-VL-30B-A3B-Instruct/model.safetensors.index.json`. This is the
ground-truth schema GPTQModel's `Qwen3_5*QModel` was written against (Qwen3-VL is the same
class hierarchy as the unreleased Qwen3.5-VL we're shadowing).

```
lm_head.weight                              # top-level (882 keys total)
model.language_model.embed_tokens.weight
model.language_model.layers.{0..N}.input_layernorm
model.language_model.layers.{0..N}.mlp.*
model.language_model.layers.{0..N}.post_attention_layernorm
model.language_model.layers.{0..N}.self_attn.*
model.language_model.norm.weight
model.visual.blocks.{0..M}.attn.*
model.visual.blocks.{0..M}.mlp.*
model.visual.blocks.{0..M}.norm{1,2}.*
model.visual.deepstack_merger_list.*
model.visual.merger.*
model.visual.patch_embed.*
model.visual.pos_embed
```

`config.json`:
```json
{
  "architectures": ["Qwen3VLMoeForConditionalGeneration"],
  "model_type": "qwen3_vl_moe",
  "text_config": {...},
  "vision_config": {...}
}
```

Key observations:
- `lm_head.weight` stays at **top level** in the VL layout. It does NOT move under
  `model.language_model`.
- The text decoder is wrapped under `model.language_model.*`. The bare `model.layers.*`
  prefix does not appear in VL.
- `model.norm` becomes `model.language_model.norm`.
- `model.embed_tokens` becomes `model.language_model.embed_tokens`.

## 4. Key rename map

Forward (wrap, source → target):

| Source                          | Target                                  | Notes |
|---------------------------------|-----------------------------------------|-------|
| `lm_head.weight`                | `lm_head.weight`                        | unchanged |
| `model.embed_tokens.*`          | `model.language_model.embed_tokens.*`   | |
| `model.layers.{i}.*`            | `model.language_model.layers.{i}.*`     | covers all sublayers verbatim |
| `model.norm.*`                  | `model.language_model.norm.*`           | |
| `model.rotary_emb.*`            | `model.language_model.rotary_emb.*`     | only present in some HF revisions |

Reverse (unwrap, target → source) is the inverse plus drop of any `model.visual.*` keys.

## 5. Vision tower strategy — DECIDED: option (c) "config-only declaration"

We considered three strategies:

### (a) Synthetic zero-param `vision_tower` placeholder
Pro: GPTQModel's module walker would find a key under that prefix. Con: GPTQModel's loader
calls `from_pretrained` on the wrapped config, which instantiates the full multimodal
architecture. Even synthetic zeros would have to match the architectural shape exactly, and
we'd still need transformers to find a working VL module class. Ruled out.

### (b) Real vision tower copied from Qwen3-VL sibling
Pro: most "honest". Con: pulls a ~5GB vision tower into our cache PVC. Quantizes the vision
tower as a side effect (wasted GPU time and disk). Then we strip it. Multi-GB I/O for nothing.
Ruled out.

### (c) **Selected: text-only architecture, wrapped layout**
We keep the architecture name **`Qwen3_5ForCausalLM`** in `config.json`, but rewrite the
weight key namespace into `model.language_model.*`. We do NOT declare a multimodal
architecture, do NOT instantiate a vision tower, and do NOT set `text_config`/`vision_config`.

The bet: **GPTQModel's module_tree only walks the prefix the model definition declares**.
For Qwen3.5-VL it declares `model.language_model.layers.*`. As long as the model that gets
instantiated has matching attribute paths (i.e., `model.language_model.layers` is a real
`nn.ModuleList`), GPTQModel can do its work. We do NOT need to satisfy the visual prefix —
the text-only definition family in GPTQModel reaches `model.language_model.norm` for
`pre_lm_head_norm_module` and stops there.

If the hardcoded class definition is the **VL** class (`Qwen3_5MultimodalQModel` or similar)
that requires a visual prefix, we fall back to a small "language-only" subclass register at
quant time. But based on cycle 11 evidence — GPTQModel reached `Quantizing layer 0 of 63`
with our existing module_tree rewrite alone — the text-only flow does work once the prefix
matches.

**Concrete config.json transformation:**
- `architectures` stays `["Qwen3_5ForCausalLM"]` (unchanged from source).
- `model_type` stays `"qwen3_5_text"` (unchanged).
- Add an `auto_map` hint pointing the multimodal-style key paths to the text-only class.
  Optional; only emitted if `--vision-strategy` argues for it.
- `_layout_adapter_version` metadata field embedded in `quantize_config.json` and
  `config.json` so the unwrap step is idempotent and detectable.

## 6. config.json round-trip

Pre-quant wrap:
- Write a sibling `config.json` in the wrapped output dir with one new field:
  `_flexinfer_layout = "qwen3_5_vl_wrapped"` (free metadata field, transformers ignores it).
- Everything else (`hidden_size`, `num_hidden_layers`, `vocab_size`, etc.) stays at the
  top level. transformers parses these for the `Qwen3_5ForCausalLM` model_type unchanged.

Post-quant unwrap:
- Strip the `_flexinfer_layout` marker.
- Reaffirm `architectures = ["Qwen3_5ForCausalLM"]` and `model_type = "qwen3_5_text"`.
- Remove any `text_config`/`vision_config` GPTQModel may have written during save.

## 7. quantize_config.json round-trip

GPTQModel writes per-module quantization parameters. With a wrapped layout it will reference
keys like `model.language_model.layers.{i}.self_attn.q_proj`. We rewrite those back to
`model.layers.{i}.self_attn.q_proj` during unwrap.

The `dynamic` exclusion patterns (regex strings) need string-replacement of the
`model.language_model.` prefix → `model.` if present.

## 8. Reverse adapter (post-quant unwrap)

Operates on the GPTQModel output dir (`{MODEL_DIR}/gptq-w4-g128/`):

1. Open `model.safetensors.index.json`. For each key:
   - Drop if starts with `model.visual.` (defensive — should not exist with strategy (c)).
   - Rewrite `model.language_model.` → `model.`.
   - Keep `lm_head.*` untouched.
2. Stream-rewrite each shard with renamed tensors.
3. Rewrite `config.json`: ensure `architectures=["Qwen3_5ForCausalLM"]`,
   `model_type="qwen3_5_text"`. Strip `_flexinfer_layout`, `text_config`, `vision_config`.
4. Rewrite `quantize_config.json`: rename keys, dedupe.

## 9. Integration in `pkg/quantization/gptq.go`

Wiring:
- Add `JobParams` field is not needed — env var is sufficient. Threading via `buildEnv`.
- Add `FLEXINFER_GPTQ_LAYOUT_ADAPTER` env var with values `0` (default, off) / `1` (on).
- When on:
  1. Wrap step runs in the bash wrapper script *after* the existing weight-key remap
     (which strips `model.language_model.` from VLM checkpoints in the source) but *before*
     the Python quantize script runs.
     - Source: `${MODEL_DIR}` (post-abliteration, post-remap, flat causal-LM).
     - Destination: `${MODEL_DIR}/.flexinfer-vl-wrap/`.
     - The wrap creates a parallel mirror dir; we point `MODEL_DIR=${MODEL_DIR}/.flexinfer-vl-wrap`
       for the quantize call. Output dir name remains `gptq-w4-g128/`.
  2. Quantize runs unmodified against the wrapped layout. Existing module_tree
     monkey-patches stay no-ops because the prefix already matches.
  3. After save succeeds (after the `OUT_DIR` integrity check passes,
     before the FP16 source cleanup at gptq.go:1607), run unwrap:
     - Source: `${MODEL_DIR}/.flexinfer-vl-wrap/gptq-w4-g128/`.
     - Destination: `${MODEL_DIR}/gptq-w4-g128/` (canonical output path vLLM expects).
  4. Cleanup: remove `${MODEL_DIR}/.flexinfer-vl-wrap/` after unwrap completes.
- When off: no-op. The existing monkey-patch path runs as today. This is the production path
  until the adapter is validated.

## 10. Flag gating

| Layer            | Flag                                  | Default | Effect when set |
|------------------|---------------------------------------|---------|-----------------|
| Env var          | `FLEXINFER_GPTQ_LAYOUT_ADAPTER`        | `0`     | Enables wrap+unwrap in the bash wrapper. |
| CRD spec field   | `spec.quantization.useLayoutAdapter`   | `false` | (Future) sets env var per-ModelCache. Not in this MR — env var override only. |

The CRD field is intentionally **not** added in this MR. Adding it requires a CRD schema
bump + Helm chart update. Env var override is enough to flip on a single test job from a
manifest patch. The CRD field can be a follow-up after the adapter is proven in the wild.

## 11. Test plan

Three layers of validation, in order of cost:

### a. Offline unit tests (this MR)
`build/scripts/test_qwen35_layout_adapter.py`:
- Synthetic 1-layer fake checkpoint with the right key shapes (random small tensors,
  `hidden_size=64`, `num_hidden_layers=1`, ~10 keys total).
- Round-trip test: wrap → unwrap should produce keys identical to source (modulo
  metadata).
- Schema test: wrapped output's `model.safetensors.index.json` must use
  `model.language_model.*` prefix and must have `lm_head.weight` at top level.
- Idempotency: invoking wrap on already-wrapped output is a no-op.
- Runs in <5s on a laptop. No GPU. No real model.

### b. Live dry-run validation (manual, before flipping flag)
Before setting `FLEXINFER_GPTQ_LAYOUT_ADAPTER=1` in production:
1. Pick a stable, small model that already quantizes cleanly today (e.g.
   Qwen3-14B-abliterated).
2. Patch the quantize Job manifest: `spec.template.spec.containers[0].env` add
   `{name: FLEXINFER_GPTQ_LAYOUT_ADAPTER, value: "1"}`.
3. Re-run the job with `kubectl delete job ... && kubectl wait`. Watch logs for
   `wrap_complete` / `unwrap_complete` events.
4. Compare quantized output sizes and `quantize_config.json` to a prior run. Sizes should
   be within ±0.1%.
5. Activate the model and run a smoke test (vLLM serving + 3 prompts).

### c. End-to-end Qwen3.5-27B run
Only after (b) succeeds. The 27B run is the actual target; cycle 11+ runs use this.

## 12. Why this matters (the real reason, in plain English)

The monkey-patch chain has a fundamental scaling problem: every GPTQModel release
re-randomizes which class attributes exist and which are required. We've shipped 7 MRs
plastering over individual instances. Each one was "the last one needed" until the next
GPTQModel internal changed.

The layout adapter's surface area is bounded by the **safetensors keys** of two well-defined
checkpoints (the source and the VL sibling). That's a finite, inspectable, testable set.
Once it's right for one Qwen3.5 sub-variant it's right for all of them, because the VL
schema is what GPTQModel locked into. Future GPTQModel releases can change anything *inside*
the quant code path; as long as the input schema matches the VL sibling, we don't care.

## 13. Open questions / assumptions to verify

- **GPTQModel loader on wrapped layout:** the `Qwen3_5MultimodalQModel.loader` is currently
  `AutoModelForImageTextToText`. With our config keeping `architectures=Qwen3_5ForCausalLM`
  it should fall through to causal-LM loading. The existing override at
  `quantize_gptq.py:2820-2833` ("Overriding GPTQ loader for qwen3_5_text") still applies and
  is compatible. Confirm in dry-run.
- **lm_head placement:** in some VL revisions `lm_head` is under `model.language_model`. We
  default to top-level (verified from `Qwen/Qwen3-VL-30B-A3B-Instruct`). If a future variant
  moves it, the adapter rule table is the only place to update.
- **`model.rotary_emb`:** transformers HF Qwen3 stores the rotary_emb at the model level.
  Some checkpoints don't serialize it (it's a buffer). The wrap rule covers it conditionally.

## 14. Files

- `.loom/qwen35-layout-adapter-design.md` (this doc)
- `build/scripts/qwen35_wrap_to_vl_layout.py` (pre-quant)
- `build/scripts/qwen35_unwrap_from_vl_layout.py` (post-quant)
- `build/scripts/test_qwen35_layout_adapter.py` (tests)
- `pkg/quantization/gptq.go` (env var threading + bash wrapper additions)

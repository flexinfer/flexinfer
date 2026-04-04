#!/bin/bash
# Runtime entrypoint: source profile env vars, normalize model configs, then exec.
# The profile env file is baked into the image by build/build-runtime.sh.
set -e

if [ -f /etc/flexinfer/runtime.env ]; then
    set -a
    . /etc/flexinfer/runtime.env
    set +a
fi

# ── Normalize Qwen3.5 model configs ──────────────────────────────────
# The Go runtime starts vLLM with model paths under /models/.
# Qwen3.5 checkpoints need:
#   1. text_config handling:
#      - VLM (qwen3_5): add text_config sub-dict if missing
#      - Text-only (qwen3_5_text): REMOVE text_config if present
#        (transformers stores it as raw dict, vLLM's get_text_config()
#         returns it, hasattr(dict, "num_attention_heads") fails)
#   2. M-RoPE fields stripped (triggers unsupported M-RoPE path)
#   3. TokenizersBackend → PreTrainedTokenizerFast (transformers 5.x compat)
# This runs at container startup, modifying config files on the hostPath.
normalize_qwen35_configs() {
    local models_dir="/models"
    [ -d "$models_dir" ] || return 0

    find "$models_dir" -maxdepth 5 -name config.json -type f 2>/dev/null | while read -r cfg; do
        local model_type
        model_type=$(python3 -c "import json; print(json.load(open('$cfg')).get('model_type',''))" 2>/dev/null) || continue

        # Only process Qwen3.5 models
        case "$model_type" in
            qwen3_5_text|qwen3_5) ;;
            *) continue ;;
        esac

        python3 - "$cfg" <<'NORMALIZE_PY' || echo "[entrypoint] WARNING: config normalization failed for $cfg"
import json, sys

path = sys.argv[1]
with open(path) as f:
    cfg = json.load(f)

changed = False
model_type = cfg.get("model_type", "")

# 1. text_config handling depends on model type:
#    - qwen3_5 (VLM): needs text_config sub-dict for proper config nesting
#    - qwen3_5_text (text-only): must NOT have text_config key
#      (PretrainedConfig stores it as raw dict; get_text_config() returns it;
#       hasattr(dict, attr) fails for all config attributes)
if model_type == "qwen3_5" and "text_config" not in cfg:
    keys = [
        "vocab_size", "hidden_size", "intermediate_size", "num_hidden_layers",
        "num_attention_heads", "num_key_value_heads", "hidden_act",
        "max_position_embeddings", "initializer_range", "rms_norm_eps",
        "use_cache", "tie_word_embeddings", "rope_parameters",
        "attention_bias", "attention_dropout", "head_dim",
        "linear_conv_kernel_dim", "linear_key_head_dim", "linear_value_head_dim",
        "linear_num_key_heads", "linear_num_value_heads",
        "layer_types", "pad_token_id", "bos_token_id", "eos_token_id",
        "full_attention_interval", "partial_rotary_factor", "attn_output_gate",
        "mlp_only_layers", "mamba_ssm_dtype", "dtype",
        "mtp_num_hidden_layers", "mtp_use_dedicated_embeddings",
    ]
    text_cfg = {k: cfg[k] for k in keys if k in cfg}
    text_cfg["model_type"] = "qwen3_5_text"
    cfg["text_config"] = text_cfg
    changed = True
    print(f"[entrypoint] Added text_config to VLM config {path}")
elif model_type == "qwen3_5_text" and "text_config" in cfg:
    del cfg["text_config"]
    changed = True
    print(f"[entrypoint] Removed text_config from text-only model {path}")

# 2. Normalize architectures to Qwen3_5ForCausalLM (top-level only)
target_arch = ["Qwen3_5ForCausalLM"]
archs = cfg.get("architectures", [])
if archs != target_arch:
    cfg["architectures"] = target_arch
    changed = True
    print(f"[entrypoint] Fixed architectures -> {target_arch} in {path}")

# 3. Strip M-RoPE config (text-only models use standard RoPE)
for k in ["mrope_section", "mrope_interleaved"]:
    if k in cfg:
        del cfg[k]
        changed = True
rp = cfg.get("rope_parameters", {})
for k in ["mrope_section", "mrope_interleaved"]:
    if k in rp:
        del rp[k]
        changed = True

if changed:
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
NORMALIZE_PY

        # 3. Fix TokenizersBackend (transformers 5.x compat)
        local tok_cfg
        tok_cfg="$(dirname "$cfg")/tokenizer_config.json"
        if [ -f "$tok_cfg" ] && grep -q '"TokenizersBackend"' "$tok_cfg"; then
            sed -i 's/"TokenizersBackend"/"PreTrainedTokenizerFast"/g' "$tok_cfg"
            echo "[entrypoint] Fixed TokenizersBackend in $tok_cfg"
        fi
    done
}

# ── Normalize Gemma4 model configs ──────────────────────────────────
# Gemma4 GPTQ checkpoints need:
#   1. text_config removal: quantizer extracts text_config to top level;
#      vLLM text-only path must NOT have a nested text_config dict
#   2. Architectures normalized to Gemma4ForCausalLM
#   3. Multimodal artifact keys stripped (vision_config, audio_config, etc.)
normalize_gemma4_configs() {
    local models_dir="/models"
    [ -d "$models_dir" ] || return 0

    find "$models_dir" -maxdepth 5 -name config.json -type f 2>/dev/null | while read -r cfg; do
        local model_type
        model_type=$(python3 -c "import json; print(json.load(open('$cfg')).get('model_type',''))" 2>/dev/null) || continue

        case "$model_type" in
            gemma4_text|gemma4) ;;
            *) continue ;;
        esac

        python3 - "$cfg" <<'NORMALIZE_GEMMA4_PY' || echo "[entrypoint] WARNING: Gemma4 config normalization failed for $cfg"
import json, sys

path = sys.argv[1]
with open(path) as f:
    cfg = json.load(f)

changed = False
model_type = cfg.get("model_type", "")

# 1. Remove text_config if present in text-only (gemma4_text) config
if model_type == "gemma4_text" and "text_config" in cfg:
    del cfg["text_config"]
    changed = True
    print(f"[entrypoint] Removed text_config from Gemma4 text-only model {path}")

# 2. Normalize architectures to Gemma4ForCausalLM
target_arch = ["Gemma4ForCausalLM"]
archs = cfg.get("architectures", [])
if archs != target_arch:
    cfg["architectures"] = target_arch
    changed = True
    print(f"[entrypoint] Fixed Gemma4 architectures -> {target_arch} in {path}")

# 3. Strip multimodal artifact keys (not needed for text-only serving)
multimodal_keys = [
    "vision_config", "audio_config", "image_token_id", "audio_token_id",
    "mm_tokens_per_image", "boi_token_id", "eoi_token_id",
    "image_token_index", "video_token_index",
]
for k in multimodal_keys:
    if k in cfg:
        del cfg[k]
        changed = True

if changed:
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
    print(f"[entrypoint] Gemma4 config normalized: {path}")
NORMALIZE_GEMMA4_PY
    done
}

if command -v python3 >/dev/null 2>&1; then
    normalize_qwen35_configs
    normalize_gemma4_configs
fi

if [ "${FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC:-}" = "turboquant" ]; then
    echo "[entrypoint] TurboQuant requested via FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC=turboquant"
    case "${FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC_STATUS:-planned}" in
        plugin)
            echo "[entrypoint] TurboQuant plugin is bundled in this image; vLLM CUSTOM attention can activate it per-model"
            ;;
        planned)
            echo "[entrypoint] No vLLM KV-cache TurboQuant integration is bundled in this image yet; using the standard cache path"
            ;;
        *)
            echo "[entrypoint] TurboQuant status=${FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC_STATUS}; continuing with configured runtime path"
            ;;
    esac
fi

if [ "${FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC:-}" = "turboquant" ]; then
    echo "[entrypoint] TurboQuant requested via FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC=turboquant"
    case "${FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC_STATUS:-planned}" in
        plugin)
            echo "[entrypoint] TurboQuant plugin is bundled in this image; vLLM CUSTOM attention can activate it per-model"
            ;;
        planned)
            echo "[entrypoint] No vLLM KV-cache TurboQuant integration is bundled in this image yet; using the standard cache path"
            ;;
        *)
            echo "[entrypoint] TurboQuant status=${FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC_STATUS}; continuing with configured runtime path"
            ;;
    esac
fi

case "$1" in
    flexinfer-runtime)
        shift
        exec flexinfer-runtime --gpu-vendor "${GPU_VENDOR}" --gpu-arch "${GPU_ARCH}" "$@"
        ;;
    *)
        exec "$@"
        ;;
esac

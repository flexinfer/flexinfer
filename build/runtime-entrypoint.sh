#!/bin/bash
# Runtime entrypoint: source profile env vars, normalize model configs, then exec.
# The profile env file is baked into the image by build/build-runtime.sh.
set -e

if [ -f /etc/flexinfer/runtime.env ]; then
    set -a
    . /etc/flexinfer/runtime.env
    set +a
fi

# ── Normalize Qwen3.5 text-only model configs ────────────────────────
# The Go runtime starts vLLM with model paths under /models/.
# Qwen3.5 text-only checkpoints need:
#   1. text_config added (vLLM defaults to wrong dimensions without it)
#   2. M-RoPE fields stripped (triggers unsupported M-RoPE path)
#   3. TokenizersBackend → PreTrainedTokenizerFast (transformers 5.x compat)
# This runs once at container startup, modifying config files on the hostPath.
normalize_qwen35_configs() {
    local models_dir="/models"
    [ -d "$models_dir" ] || return 0

    find "$models_dir" -maxdepth 3 -name config.json -type f 2>/dev/null | while read -r cfg; do
        local model_type
        model_type=$(python3 -c "import json; print(json.load(open('$cfg')).get('model_type',''))" 2>/dev/null) || continue

        # Only process Qwen3.5 text-only models
        case "$model_type" in
            qwen3_5_text|qwen3_5) ;;
            *) continue ;;
        esac

        python3 - "$cfg" <<'NORMALIZE_PY' || echo "[entrypoint] WARNING: config normalization failed for $cfg"
import json, sys, os

path = sys.argv[1]
with open(path) as f:
    cfg = json.load(f)

changed = False

# 1. Add text_config if missing
if "text_config" not in cfg:
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
    print(f"[entrypoint] Added text_config to {path}")

# 2. Normalize architectures to Qwen3_5ForCausalLM
archs = cfg.get("architectures", [])
if archs and archs != ["Qwen3_5ForCausalLM"]:
    cfg["architectures"] = ["Qwen3_5ForCausalLM"]
    changed = True
    print(f"[entrypoint] Fixed architectures {archs} → ['Qwen3_5ForCausalLM'] in {path}")

# 3. Strip M-RoPE config (text-only models use standard RoPE)
for target in [cfg, cfg.get("text_config", {})]:
    for k in ["mrope_section", "mrope_interleaved"]:
        if k in target:
            del target[k]
            changed = True
    rp = target.get("rope_parameters", {})
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

if command -v python3 >/dev/null 2>&1; then
    normalize_qwen35_configs
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

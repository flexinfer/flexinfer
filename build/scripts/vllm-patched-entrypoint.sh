#!/bin/bash
set -e

# Extract --model path from args to find config.json
MODEL_PATH=""
next_is_model=false
for arg in "$@"; do
    if $next_is_model; then
        MODEL_PATH="$arg"
        break
    fi
    if [[ "$arg" == "--model" ]]; then
        next_is_model=true
    fi
done

# Fallback: scan /models for config.json (up to 2 levels deep)
if [ -z "$MODEL_PATH" ] || [ ! -f "${MODEL_PATH}/config.json" ]; then
    for d in /models /models/*/ /models/*/*/; do
        if [ -f "${d}/config.json" ]; then
            MODEL_PATH="${d}"
            break
        fi
    done
fi

# Normalize Qwen3.5 text-only checkpoints that were exported without nested
# text_config. vLLM's Qwen3_5Config defaults text_config to a 4096-wide model
# when the field is missing, which mismatches 27B GPTQ checkpoints (5120-wide).
if [ -n "$MODEL_PATH" ] && [ -f "${MODEL_PATH}/config.json" ]; then
    model_type=$(python3 -c "import json; print(json.load(open('${MODEL_PATH}/config.json')).get('model_type',''))" 2>/dev/null || echo "")
    if [[ "$model_type" == "qwen3_5" ]]; then
        python3 - <<PY || echo "[entrypoint] WARNING: Qwen3.5 text_config normalization failed"
import json
import os
import shutil
import time

path = "${MODEL_PATH}/config.json"
with open(path) as f:
    cfg = json.load(f)

if "text_config" not in cfg:
    keys = [
        "vocab_size",
        "hidden_size",
        "intermediate_size",
        "num_hidden_layers",
        "num_attention_heads",
        "num_key_value_heads",
        "hidden_act",
        "max_position_embeddings",
        "initializer_range",
        "rms_norm_eps",
        "use_cache",
        "tie_word_embeddings",
        "rope_parameters",
        "attention_bias",
        "attention_dropout",
        "head_dim",
        "linear_conv_kernel_dim",
        "linear_key_head_dim",
        "linear_value_head_dim",
        "linear_num_key_heads",
        "linear_num_value_heads",
        "layer_types",
        "pad_token_id",
        "bos_token_id",
        "eos_token_id",
        "full_attention_interval",
        "partial_rotary_factor",
        "attn_output_gate",
        "mlp_only_layers",
        "mamba_ssm_dtype",
        "dtype",
        "mtp_num_hidden_layers",
        "mtp_use_dedicated_embeddings",
    ]
    text_cfg = {k: cfg[k] for k in keys if k in cfg}
    text_cfg["model_type"] = "qwen3_5_text"
    backup = f"{path}.bak-textcfg-{time.strftime('%Y%m%d%H%M%S')}"
    shutil.copy2(path, backup)
    cfg["text_config"] = text_cfg
    with open(path, "w") as f:
        json.dump(cfg, f, indent=2, ensure_ascii=False)
    print(f"[entrypoint] Added missing text_config to {path}")
    print(f"[entrypoint] Backup written to {backup}")
PY
    fi
fi

# Apply Qwen3.5 patches if model config contains qwen3_5 AND patch file exists.
# vLLM v0.18.0+ has native Qwen3.5 GDN support — patch.py is not needed.
if [ -f "/opt/patches/patch.py" ] && [ -n "$MODEL_PATH" ] && [ -f "${MODEL_PATH}/config.json" ]; then
    model_type=$(python3 -c "import json; print(json.load(open('${MODEL_PATH}/config.json')).get('model_type',''))" 2>/dev/null || echo "")
    if [[ "$model_type" == *"qwen3_5"* ]]; then
        echo "[entrypoint] Qwen3.5 model detected at ${MODEL_PATH}, applying vLLM patches..."
        export FLEXINFER_MODEL_PATH="${MODEL_PATH}"
        python3 /opt/patches/patch.py || echo "[entrypoint] WARNING: patches failed, continuing anyway"
    fi
elif [ -n "$MODEL_PATH" ] && [ -f "${MODEL_PATH}/config.json" ]; then
    model_type=$(python3 -c "import json; print(json.load(open('${MODEL_PATH}/config.json')).get('model_type',''))" 2>/dev/null || echo "")
    if [[ "$model_type" == *"qwen3_5"* ]]; then
        echo "[entrypoint] Qwen3.5 model detected — vLLM has native support, no patches needed"
    fi
fi

# Fix transformers 5.x TokenizersBackend incompatibility.
# Models quantized with transformers>=5 save tokenizer_class=TokenizersBackend,
# which doesn't exist in transformers<5 (used by this vLLM build).
if [ -n "$MODEL_PATH" ] && [ -f "${MODEL_PATH}/tokenizer_config.json" ]; then
    if grep -q '"TokenizersBackend"' "${MODEL_PATH}/tokenizer_config.json"; then
        echo "[entrypoint] Fixing TokenizersBackend → PreTrainedTokenizerFast in tokenizer_config.json"
        sed -i 's/"TokenizersBackend"/"PreTrainedTokenizerFast"/g' "${MODEL_PATH}/tokenizer_config.json"
    fi
fi

# Strip M-RoPE config from Qwen3.5 text-only models.
# The VLM parent model's config.json contains mrope_section/mrope_interleaved
# which triggers vLLM's M-RoPE path ("M-RoPE support is not implemented").
# Text-only inference uses standard RoPE; these fields must be removed.
if [ -n "$MODEL_PATH" ] && [ -f "${MODEL_PATH}/config.json" ]; then
    if grep -q '"mrope_section"' "${MODEL_PATH}/config.json"; then
        echo "[entrypoint] Stripping M-RoPE config from config.json (text-only model)"
        python3 -c "
import json
p = '${MODEL_PATH}/config.json'
with open(p) as f:
    c = json.load(f)
changed = False
if 'rope_parameters' in c:
    for k in ['mrope_section', 'mrope_interleaved']:
        if k in c['rope_parameters']:
            del c['rope_parameters'][k]
            changed = True
for k in ['mrope_section', 'mrope_interleaved']:
    if k in c:
        del c[k]
        changed = True
if changed:
    with open(p, 'w') as f:
        json.dump(c, f, indent=2, ensure_ascii=False)
    print('[entrypoint] M-RoPE fields removed')
else:
    print('[entrypoint] No M-RoPE fields found')
" || echo "[entrypoint] WARNING: M-RoPE cleanup failed"
    fi
fi

# Launch vllm
exec vllm serve "$@"

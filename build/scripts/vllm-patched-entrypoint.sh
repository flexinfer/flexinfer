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

# Apply Qwen3.5 patches if model config contains qwen3_5 AND patch file exists.
# vLLM v0.18.0+ has native Qwen3.5 GDN support — patch.py is not needed.
if [ -f "/opt/patches/patch.py" ] && [ -n "$MODEL_PATH" ] && [ -f "${MODEL_PATH}/config.json" ]; then
    model_type=$(python3 -c "import json; print(json.load(open('${MODEL_PATH}/config.json')).get('model_type',''))" 2>/dev/null || echo "")
    if [[ "$model_type" == *"qwen3_5"* ]]; then
        echo "[entrypoint] Qwen3.5 model detected at ${MODEL_PATH}, applying vLLM patches..."
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

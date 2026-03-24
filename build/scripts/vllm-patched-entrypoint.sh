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

# Fallback: scan /models for config.json
if [ -z "$MODEL_PATH" ] || [ ! -f "${MODEL_PATH}/config.json" ]; then
    for d in /models /models/*/; do
        if [ -f "${d}/config.json" ]; then
            MODEL_PATH="${d}"
            break
        fi
    done
fi

# Apply Qwen3.5 patches if model config contains qwen3_5
if [ -n "$MODEL_PATH" ] && [ -f "${MODEL_PATH}/config.json" ]; then
    model_type=$(python3 -c "import json; print(json.load(open('${MODEL_PATH}/config.json')).get('model_type',''))" 2>/dev/null || echo "")
    if [[ "$model_type" == *"qwen3_5"* ]]; then
        echo "[entrypoint] Qwen3.5 model detected at ${MODEL_PATH}, applying vLLM patches..."
        python3 /opt/patches/patch.py || echo "[entrypoint] WARNING: patches failed, continuing anyway"
    fi
fi

# Launch vllm
exec vllm serve "$@"

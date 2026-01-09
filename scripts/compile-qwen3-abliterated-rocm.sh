#!/bin/bash
# Compile Qwen3-8B-abliterated for MLC-LLM on AMD ROCm
#
# This script downloads and compiles the abliterated Qwen3-8B model
# for use with MLC-LLM on AMD GPUs (ROCm 6.2).
#
# IMPORTANT: Uses q4f32_1 quantization instead of q4f16 to avoid
# TVM compilation bug: https://github.com/mlc-ai/mlc-llm/issues/3283
#
# Usage:
#   ./scripts/compile-qwen3-abliterated-rocm.sh [output_dir]
#
# Prerequisites:
#   pip install --pre -f https://mlc.ai/wheels mlc-llm-nightly-rocm62 mlc-ai-nightly-rocm62

set -euo pipefail

# Configuration
MODEL_ID="${MODEL_ID:-huihui-ai/Qwen3-8B-abliterated}"
OUTPUT_DIR="${1:-/models/Qwen3-8B-abliterated-q4f32-MLC}"
QUANTIZATION="${QUANTIZATION:-q4f32_1}"  # DO NOT use q4f16 - TVM bug on ROCm
CONTEXT_SIZE="${CONTEXT_SIZE:-32768}"
PREFILL_CHUNK="${PREFILL_CHUNK:-512}"

echo "=== MLC-LLM Compilation for AMD ROCm ==="
echo "Model: ${MODEL_ID}"
echo "Output: ${OUTPUT_DIR}"
echo "Quantization: ${QUANTIZATION}"
echo "Context: ${CONTEXT_SIZE}"
echo ""

# Check for MLC-LLM
if ! command -v mlc_llm &> /dev/null; then
    echo "ERROR: mlc_llm not found. Install with:"
    echo "  pip install --pre -f https://mlc.ai/wheels mlc-llm-nightly-rocm62 mlc-ai-nightly-rocm62"
    exit 1
fi

# Check for ROCm
if ! command -v rocm-smi &> /dev/null; then
    echo "WARNING: rocm-smi not found. Make sure ROCm is installed."
fi

# Create output directory
mkdir -p "${OUTPUT_DIR}"

echo "=== Step 1: Generate Model Config ==="
mlc_llm gen_config "${MODEL_ID}" \
    --quantization "${QUANTIZATION}" \
    --context-window-size "${CONTEXT_SIZE}" \
    --prefill-chunk-size "${PREFILL_CHUNK}" \
    --output "${OUTPUT_DIR}"

echo ""
echo "=== Step 2: Convert Model Weights ==="
mlc_llm convert_weight "${MODEL_ID}" \
    --quantization "${QUANTIZATION}" \
    --output "${OUTPUT_DIR}"

echo ""
echo "=== Step 3: Compile Model Library for ROCm ==="
mlc_llm compile "${OUTPUT_DIR}" \
    --device rocm \
    --output "${OUTPUT_DIR}/lib.so"

echo ""
echo "=== Compilation Complete ==="
echo "Model artifacts: ${OUTPUT_DIR}"
echo "Library: ${OUTPUT_DIR}/lib.so"
echo ""
echo "To serve the model:"
echo "  mlc_llm serve ${OUTPUT_DIR} \\"
echo "    --model-lib ${OUTPUT_DIR}/lib.so \\"
echo "    --host 0.0.0.0 --port 8000 --mode local"
echo ""
echo "To test:"
echo '  curl http://localhost:8000/v1/chat/completions \'
echo '    -H "Content-Type: application/json" \'
echo '    -d '\''{"model": "Qwen3-8B-abliterated", "messages": [{"role": "user", "content": "Hello"}]}'\'''

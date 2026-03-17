#!/bin/sh
# GGUF quantization via llama.cpp convert + quantize.
#
# Environment variables:
#   MODEL_DIR, GGUF_TYPE, FLEXINFER_TELEMETRY (optional)
set -eu

MODEL_DIR="${MODEL_DIR:?MODEL_DIR required}"
WORKSPACE="/workspace"
GGUF_TYPE="${GGUF_TYPE:?GGUF_TYPE required}"
START_TS=$(date +%s)

emit_progress() {
    if [ "${FLEXINFER_TELEMETRY:-}" = "true" ]; then
        printf '{"event":"%s","phase":"%s","percent":%s,"ts":"%s"}\n' \
            "$1" "$2" "$3" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi
}

echo "=== GGUF Quantization ==="
echo "Model: ${MODEL_DIR}"
echo "Type: ${GGUF_TYPE}"
echo "Start: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

ORIGINAL_SIZE=$(du -sb "${MODEL_DIR}" | cut -f1)
echo "Original size: ${ORIGINAL_SIZE} bytes"

emit_progress "start" "quantizing" "0"

# Step 1: Convert HuggingFace to FP16 GGUF
echo "--- Step 1: Converting to FP16 GGUF ---"
emit_progress "progress" "converting" "10"
python3 /opt/llama.cpp/convert_hf_to_gguf.py \
    "${MODEL_DIR}" \
    --outfile "${WORKSPACE}/model-fp16.gguf" \
    --outtype f16

# Step 2: Quantize to target type
echo "--- Step 2: Quantizing to ${GGUF_TYPE} ---"
emit_progress "progress" "quantizing" "50"
/opt/llama.cpp/llama-quantize \
    "${WORKSPACE}/model-fp16.gguf" \
    "${WORKSPACE}/model-${GGUF_TYPE}.gguf" \
    "${GGUF_TYPE}"

# Step 3: Move quantized model to PVC
QUANTIZED_FILE="${MODEL_DIR}/model-${GGUF_TYPE}.gguf"
mv "${WORKSPACE}/model-${GGUF_TYPE}.gguf" "${QUANTIZED_FILE}"

# Clean up intermediate FP16 file
rm -f "${WORKSPACE}/model-fp16.gguf"

# Record compressed size
COMPRESSED_SIZE=$(stat -c %s "${QUANTIZED_FILE}" 2>/dev/null || stat -f %z "${QUANTIZED_FILE}")
echo "Compressed size: ${COMPRESSED_SIZE} bytes"
END_TS=$(date +%s)
DURATION_SEC=$((END_TS - START_TS))

emit_progress "progress" "saving" "95"

cat > "${MODEL_DIR}/.quantization-status.json" << METADATA
{
  "format": "GGUF",
  "type": "${GGUF_TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC},
  "outputFile": "model-${GGUF_TYPE}.gguf"
}
METADATA

cat > /dev/termination-log << TERMINATION
{
  "type": "${GGUF_TYPE}",
  "originalSizeBytes": ${ORIGINAL_SIZE},
  "compressedSizeBytes": ${COMPRESSED_SIZE},
  "quantizationTimeSeconds": ${DURATION_SEC}
}
TERMINATION

emit_progress "complete" "quantizing" "100"

echo "=== Quantization complete ==="
echo "Output: ${QUANTIZED_FILE}"
echo "End: $(date -u +%Y-%m-%dT%H:%M:%SZ)"

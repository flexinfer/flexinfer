#!/usr/bin/env bash
# Qwen-Image 2.1 GGUF loader kill-test (research doc §6 / §6b), CPU only.
# K1: ranged GGUF/safetensors header dumps + name/shape diff (~50 MiB).
# K2: diffusers 0.41.0 single-file GGUF load + forward, UC Q8_0 vs a
#     diffusers-layout control (~11.9 GB download, ~8 GB RAM per run).
# Usage: WORK=/some/scratch/dir ./run.sh   (WORK defaults to ./work, gitignored)
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
WORK="${WORK:-$HERE/work}"
mkdir -p "$WORK"/{dl,hdr,out}

AB=https://huggingface.co/abenzerps/Qwen-Image-2.1-Uncensored-Quantized/resolve
AB_REV=7959e658915f52274897fb573ffeda49de8dc16b          # main (UC files)
AB_BASE_REV=4db8efdbe3426175988fca2ac50670f35ccb7ec9     # base branch (same-converter, no UC delta)
M9=https://huggingface.co/m9ter1a/Qwen-Image-2.1-Turbo-GGUF/resolve/a94df596a422bc8dfe406655154c0364f79ba042
CO=https://huggingface.co/Comfy-Org/Qwen-Image-2.1/resolve/0217efd1d9c9452e84f6448903d6e21bfa171ffd/diffusion_models/qwen_image_2.1_bf16.safetensors
QWEN_REV=b3179ad355be050328e483a9dfdd9e60cd62adfa        # Qwen/Qwen-Image-2.1, the derivative's source revision
TURBO_REV=fc13ba1986fcb905854dc8de2f52eee395f49b5b       # Qwen/Qwen-Image-2.1-Turbo

# --- venv: CPU torch + diffusers 0.41.0 (the R1 pin target) ---
if [ ! -x "$WORK/venv/bin/python" ]; then
  uv venv --python 3.12 "$WORK/venv"
  uv pip install --python "$WORK/venv/bin/python" "diffusers==0.41.0" "transformers>=5.17,<5.18" \
    torch gguf accelerate safetensors huggingface_hub numpy
fi
PY="$WORK/venv/bin/python -I"

# --- K1: header dumps from 16 MiB ranges ---
cd "$WORK/hdr"
curl -sSL -r 0-16777215 -o uc-q8_0.head "$AB/$AB_REV/qwen-image-2.1-UC-Q8_0.gguf"
curl -sSL -r 0-16777215 -o base-q8_0.head "$AB/$AB_BASE_REV/qwen-image-2.1-Q8_0.gguf"
curl -sSL -r 0-16777215 -o m9-turbo-q4km.head "$M9/qwen-image-2.1-turbo-Q4_K_M.gguf"
curl -sSL -r 0-7 -o comfy.len "$CO"
N=$(python3 -I -c 'import struct,sys;print(struct.unpack("<Q",open(sys.argv[1],"rb").read(8))[0])' comfy.len)
curl -sSL -r "0-$((N + 7))" -o comfy-bf16.head "$CO"
for f in uc-q8_0 base-q8_0 m9-turbo-q4km; do $PY "$HERE/k1_headers.py" gguf $f.head $f.json; done
$PY "$HERE/k1_headers.py" safetensors comfy-bf16.head comfy-bf16.json
for b in m9-turbo-q4km base-q8_0 comfy-bf16; do $PY "$HERE/k1_diff.py" uc-q8_0.json $b.json; done

# --- K2 downloads, verified against the HF LFS sha256 oids ---
cd "$WORK/dl"
curl -sSL --retry 5 -C - -o qwen-image-2.1-UC-Q8_0.gguf "$AB/$AB_REV/qwen-image-2.1-UC-Q8_0.gguf"
curl -sSL --retry 5 -C - -o qwen-image-2.1-turbo-Q4_K_M.gguf "$M9/qwen-image-2.1-turbo-Q4_K_M.gguf"
shasum -a 256 -c <<'EOF'
cde456c72ea3ecebfc1be783300e972711d875e0c5f1bed33d42b66b156affa8  qwen-image-2.1-UC-Q8_0.gguf
d56b6a27de40029cc400b3daab929589c18905d9dc1365cf30adfcc20bc1fa0a  qwen-image-2.1-turbo-Q4_K_M.gguf
EOF

# --- K2 runs (sequential: each holds ~8 GB) ---
cd "$WORK/out"
K2="$HERE/k2_load_forward.py"
$PY "$K2" "$WORK/dl/qwen-image-2.1-turbo-Q4_K_M.gguf" "$WORK/hdr/m9-turbo-q4km.json" \
  Qwen/Qwen-Image-2.1-Turbo "$TURBO_REV" m9-turbo-q4km-stock "$WORK/out" || true
$PY "$K2" "$WORK/dl/qwen-image-2.1-UC-Q8_0.gguf" "$WORK/hdr/uc-q8_0.json" \
  Qwen/Qwen-Image-2.1 "$QWEN_REV" uc-q8_0-stock "$WORK/out" || true
$PY "$K2" "$WORK/dl/qwen-image-2.1-UC-Q8_0.gguf" "$WORK/hdr/uc-q8_0.json" \
  Qwen/Qwen-Image-2.1 "$QWEN_REV" uc-q8_0-shim "$WORK/out" shim || true
$PY "$HERE/k2_compare.py" uc-q8_0-shim.pt m9-turbo-q4km-stock.pt

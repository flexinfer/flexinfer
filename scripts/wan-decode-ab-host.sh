#!/usr/bin/env bash
# S9 Wan decode A/B driver. Runs ON cblevins-7900xtx (passwordless sudo for
# k3s ctr and journalctl) while a GPULease holds 7900xtx-textgen.
#
# Each phase starts one `ctr run` container from an image already in the
# node's containerd (nothing is pulled), with the GPU devices, the lane's
# weights read-only at /models, a per-arm compile/MIOpen cache, the lane's
# rendered env and scripts/wan-decode-ab.py. A 5 Hz sampler records the
# card's VRAM; the kernel log of the phase is saved.
#
# Pass rules: rows gfx1100-wan-decode-ab-v032-2026-11-05,
# gfx1100-wan-vace-vae-bypass-2026-11-05, gfx1100-wan-denoise-cache-2026-11-05
# (.loom/60-validation-matrix.md). Procedure and phase order:
# .loom/32-iteration-plan-s9-wan-decode-ab-2026-11-05.md.
#
# Usage: wan-decode-ab-host.sh ROOT PHASE...
#   ROOT holds code/wan-decode-ab.py and code/server-diffusers.py (the branch
#   copy, used only by the kill-test phases); results land in ROOT/<phase>/.
#   PHASE: t2v-A t2v-A-cold t2v-B t2v-B-cold vace-A vace-B vace-B-bf16
#          vace-B-fp32 sa-A sa-B cache-B-fbc005 cache-B-fbc010 cache-B-ts541
set -uo pipefail

ROOT=${1:?usage: wan-decode-ab-host.sh ROOT PHASE...}
shift
CODE=$ROOT/code

IMG_A=registry.harbor.lan/flexinfer/diffusers-video@sha256:b1f98ad0d9adbf8927145bed242e5e49b5cc0f6ac3fa27196cbc4c3e885644bb
IMG_B=registry.harbor.lan/flexinfer/runtime@sha256:527e2714c163f3c284bcba7e168590e8cb8960e2b1cf247b00976bb7b8f30d0b
IMG_SA=registry.harbor.lan/flexinfer/diffusers@sha256:d87f75f8c69bd3e266b11fbb11ee6528d1134001721a960b6e738f6af7e788ce
SRV_A=/app/server.py
SRV_B=/opt/flexinfer/server-diffusers.py
SRV_BRANCH=/wanab/server-diffusers.py
MODELS=/var/lib/flexinfer/models/flexinfer-system
T2V=wan21-t2v-1p3b-gfx1100
VACE=wan21-vace-1p3b-i2v-gfx1100
SA=stable-audio-open-gfx1100
VRAM=/sys/class/drm/card0/device/mem_info_vram_used # 0000:03:00.0, the Navi 31 dGPU
GIB=$((1024 * 1024 * 1024))
BUSY_LIMIT=$((2 * GIB))

# The GPUProfile and cache env every dedicated Wan/SA Deployment renders
# (kubectl get deploy wan21-t2v-1p3b-gfx1100 -o json, 2026-11-05), minus the
# ABLITERATION_* and VLLM_* keys. Cache dirs point at the per-arm mount.
common_env() {
	cat <<'EOF'
HSA_OVERRIDE_GFX_VERSION=11.0.0
TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1
HIP_FORCE_DEV_KERNARG=1
TORCH_BLAS_PREFER_HIPBLASLT=1
PYTORCH_ROCM_ARCH=gfx1100
ROCR_VISIBLE_DEVICES=0
HIP_VISIBLE_DEVICES=0
MIOPEN_FIND_MODE=2
PYTORCH_HIP_ALLOC_CONF=garbage_collection_threshold:0.9,max_split_size_mb:512,expandable_segments:True
HF_HOME=/models/.cache/huggingface
HF_HUB_CACHE=/models/.cache/huggingface/hub
HUGGINGFACE_HUB_CACHE=/models/.cache/huggingface/hub
TRANSFORMERS_CACHE=/models/.cache/huggingface/transformers
HF_HUB_OFFLINE=1
TRANSFORMERS_OFFLINE=1
MIOPEN_CUSTOM_CACHE_DIR=/cache/compile/miopen
MIOPEN_USER_DB_PATH=/cache/compile/miopen/user.db
TORCHINDUCTOR_CACHE_DIR=/cache/compile/inductor
TRITON_CACHE_DIR=/cache/compile/triton
TORCH_HOME=/cache/compile/torch
LOCAL_MODEL_PATH=/models
PORT=8000
EOF
}

t2v_env() {
	cat <<'EOF'
MODEL_ID=Wan-AI/Wan2.1-T2V-1.3B-Diffusers
MODEL=Wan-AI/Wan2.1-T2V-1.3B-Diffusers
DEFAULT_NUM_INFERENCE_STEPS=30
DEFAULT_GUIDANCE_SCALE=5.0
DEFAULT_NEGATIVE_PROMPT=static, blurry, low quality, distorted, watermark, subtitles, text
MODEL_FAMILY=wan
COMPILE_MODE=max-autotune-no-cudagraphs
COMPILE_FULLGRAPH=false
PIPELINE_MODE=text2video
DIFFUSERS_DTYPE=bfloat16
DEFAULT_VIDEO_NUM_FRAMES=81
DEFAULT_VIDEO_FPS=16
DEFAULT_VIDEO_SIZE=832x480
SKIP_WARMUP=1
USE_CPU_OFFLOAD=1
EOF
}

vace_env() {
	cat <<'EOF'
MODEL_ID=Wan-AI/Wan2.1-VACE-1.3B-diffusers
MODEL=Wan-AI/Wan2.1-VACE-1.3B-diffusers
DEFAULT_NUM_INFERENCE_STEPS=20
DEFAULT_GUIDANCE_SCALE=5.0
DEFAULT_NEGATIVE_PROMPT=static, blurry, low quality, distorted, watermark, subtitles, text
MODEL_FAMILY=wan
PIPELINE_MODE=image2video
DIFFUSERS_DTYPE=bfloat16
DEFAULT_VIDEO_NUM_FRAMES=33
DEFAULT_VIDEO_FPS=16
DEFAULT_VIDEO_SIZE=832x480
ENABLE_VAE_TILING=1
SKIP_WARMUP=1
USE_CPU_OFFLOAD=1
EOF
}

sa_env() {
	cat <<'EOF'
MODEL_ID=stabilityai/stable-audio-open-1.0
MODEL=stabilityai/stable-audio-open-1.0
DEFAULT_NUM_INFERENCE_STEPS=100
PIPELINE_MODE=text2audio
DIFFUSERS_DTYPE=float16
SKIP_WARMUP=1
USE_CPU_OFFLOAD=0
EOF
}

# What an in-pod lane adds: the XTX runtime pod's own env
# (flexinfer-runtime-gfx1100-v95tv, 2026-11-05). The image's runtime.env comes
# from its entrypoint, which ctr keeps.
pod_env() {
	cat <<'EOF'
GPU_VENDOR=amd
GPU_ARCH=gfx1100
PYTORCH_ALLOC_CONF=garbage_collection_threshold:0.7,max_split_size_mb:256,expandable_segments:True
XDG_CACHE_HOME=/cache/compile/xdg
EOF
}

log() { echo "$(date -u +%FT%TZ) $*" | tee -a "$ROOT/phases.log"; }

# run_arm NAME IMAGE SERVER ENVKIND CACHEKEY CPUS MEMGIB MODELDIR EXTRA_ENV [harness args...]
run_arm() {
	local name=$1 image=$2 server=$3 kind=$4 cachekey=$5 cpus=$6 mem=$7 model=$8 extra=$9
	shift 9
	local out=$ROOT/$name envf=$ROOT/env-$name.env
	local used
	# A crashed arm frees its VRAM over several seconds; give it 90 s.
	local waited=0
	used=$(cat "$VRAM")
	while [ "$used" -gt "$BUSY_LIMIT" ] && [ "$waited" -lt 90 ]; do
		sleep 5
		waited=$((waited + 5))
		used=$(cat "$VRAM")
	done
	if [ "$used" -gt "$BUSY_LIMIT" ]; then
		log "phase $name SKIPPED: card busy ($((used / 1024 / 1024)) MiB used); is the GPULease active?"
		return 3
	fi
	mkdir -p "$out" "$ROOT/cache/$cachekey"
	{
		common_env
		"${kind}_env"
		# EXTRA_ENV is ';'-separated (PYTORCH_ALLOC_CONF values hold commas);
		# a PYTORCH_ALLOC_CONF there replaces the pod's.
		if [ "$image" = "$IMG_B" ]; then
			if [[ ";$extra" == *";PYTORCH_ALLOC_CONF="* ]]; then
				pod_env | grep -v '^PYTORCH_ALLOC_CONF='
			else
				pod_env
			fi
		fi
		if [ -n "$extra" ]; then tr ';' '\n' <<<"$extra"; fi
		echo "WAN_AB_IMAGE=$image"
	} >"$envf"
	echo "$used" >"$ROOT/baseline-$name.txt"
	(while :; do
		printf '%s %s\n' "$(date +%s.%N)" "$(cat "$VRAM")"
		sleep 0.2
	done) >"$ROOT/vram-$name.tsv" &
	local sampler=$!
	local start t0
	start=$(date +%s)
	t0=$(date +%s.%N)
	local -a limits=(--memory-limit $((mem * GIB)))
	if [ -n "$cpus" ]; then limits+=(--cpus "$cpus"); fi
	log "phase $name start image=${image##*@sha256:} server=$server cache=$cachekey cpus=${cpus:-none} mem=${mem}GiB extra=${extra:-none}"
	sudo -n k3s ctr -n k8s.io run --rm \
		--device /dev/kfd --device /dev/dri "${limits[@]}" \
		--mount "type=bind,src=$MODELS/$model,dst=/models,options=rbind:ro" \
		--mount "type=bind,src=$ROOT/cache/$cachekey,dst=/cache/compile,options=rbind:rw" \
		--mount "type=bind,src=$CODE,dst=/wanab,options=rbind:ro" \
		--mount "type=bind,src=$ROOT,dst=/out,options=rbind:rw" \
		--env-file "$envf" \
		"$image" "wanab-$name-$start" \
		python /wanab/wan-decode-ab.py run --server "$server" --out "/out/$name" --label "$name" --t0 "$t0" "$@" \
		>"$out/run.log" 2>&1
	local rc=$?
	kill "$sampler" 2>/dev/null
	wait "$sampler" 2>/dev/null
	sudo -n journalctl -k --since "@$start" --no-pager -o short-iso >"$ROOT/kernel-$name.log" 2>&1 || true
	log "phase $name done rc=$rc seconds=$(($(date +%s) - start))"
	return $rc
}

# smoke_arm NAME IMAGE SERVER ENVKIND MODELDIR: no GPU devices, no lease needed.
# Checks the mounts, the env file, the image entrypoint and that both the
# image's server and the branch server import, and prints the clip settings.
smoke_arm() {
	local name=$1 image=$2 server=$3 kind=$4 model=$5
	local envf=$ROOT/env-smoke-$name.env
	{
		common_env
		"${kind}_env"
		if [ "$image" = "$IMG_B" ]; then pod_env; fi
	} >"$envf"
	sudo -n k3s ctr -n k8s.io run --rm \
		--mount "type=bind,src=$MODELS/$model,dst=/models,options=rbind:ro" \
		--mount "type=bind,src=$CODE,dst=/wanab,options=rbind:ro" \
		--env-file "$envf" \
		"$image" "wanab-smoke-$name-$(date +%s)" \
		python -c "
import importlib.util as u, os
s = u.spec_from_file_location('h', '/wanab/wan-decode-ab.py'); h = u.module_from_spec(s); s.loader.exec_module(h)
for path in ('$server', '/wanab/server-diffusers.py'):
    srv = h._load_server(path)
    print('[smoke]', path, 'mode', srv.PIPELINE_MODE, 'size', srv._parse_video_size(None), 'neg', bool(srv._resolve_negative_prompt(None)),
          'branch flags', hasattr(srv, '_apply_wan_denoise_cache'))
print('[smoke] model_index', os.path.exists('/models/model_index.json'), 'versions', h._versions())
" 2>&1 | grep -E '^\[smoke\]|Error|Traceback' | tee "$ROOT/smoke-$name.log"
}

KF=/out/t2v-A/keyframe.png
# Addendum row gfx1100-wan-decode-ab-v032-fix-2026-11-06: the pod allocator
# without expandable segments, and the fix arm's env for the cache phases.
NOEXP="PYTORCH_ALLOC_CONF=garbage_collection_threshold:0.7,max_split_size_mb:256"
FIX_ENV=${FIX_ENV:-ENABLE_VAE_TILING=1}
FIX_CACHE=${FIX_CACHE:-t2v-B-tile}
phase() {
	case "$1" in
	smoke)
		smoke_arm t2v-A "$IMG_A" "$SRV_A" t2v "$T2V"
		smoke_arm t2v-B "$IMG_B" "$SRV_B" t2v "$T2V"
		smoke_arm vace-B "$IMG_B" "$SRV_B" vace "$VACE"
		smoke_arm sa-A "$IMG_SA" "$SRV_A" sa "$SA"
		;;
	t2v-A) run_arm t2v-A "$IMG_A" "$SRV_A" t2v t2v-A 8 48 "$T2V" "" \
		--clip c0:boat:42 --clip c1:boat:42:keyframe.png --clip c2:fox:7 \
		--decode /out/t2v-A/t2v-A-c1.latents.pt:own ;;
	t2v-A-cold) run_arm t2v-A-cold "$IMG_A" "$SRV_A" t2v t2v-A 8 48 "$T2V" "" --clip k:boat:42 ;;
	t2v-B) run_arm t2v-B "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "" \
		--clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 \
		--decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	t2v-B-cold) run_arm t2v-B-cold "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "" --clip k:boat:42 ;;
	vace-A) run_arm vace-A "$IMG_A" "$SRV_A" vace vace-A 8 48 "$VACE" "" \
		--keyframe "$KF" --clip v0:boat:42 --clip v1:boat:42 ;;
	vace-B) run_arm vace-B "$IMG_B" "$SRV_B" vace vace-B "" 48 "$VACE" "" \
		--keyframe "$KF" --clip v0:boat:42 --clip v1:boat:42 ;;
	vace-B-bf16) run_arm vace-B-bf16 "$IMG_B" "$SRV_BRANCH" vace vace-B "" 48 "$VACE" \
		"WAN_VAE_DECODE_DTYPE=bfloat16" \
		--keyframe "$KF" --clip v1:boat:42 --decode /out/vace-B/vace-B-v1.latents.pt:x0 ;;
	vace-B-fp32) run_arm vace-B-fp32 "$IMG_B" "$SRV_BRANCH" vace vace-B "" 48 "$VACE" \
		"WAN_VAE_MIOPEN_BYPASS=fp32" \
		--keyframe "$KF" --clip v1:boat:42 --decode /out/vace-B/vace-B-v1.latents.pt:x0 ;;
	sa-A) run_arm sa-A "$IMG_SA" "$SRV_A" sa sa-A 6 16 "$SA" "" ;;
	sa-B) run_arm sa-B "$IMG_B" "$SRV_B" sa sa-B "" 48 "$SA" "" ;;
	t2v-B-tile) run_arm t2v-B-tile "$IMG_B" "$SRV_B" t2v t2v-B-tile "" 48 "$T2V" "ENABLE_VAE_TILING=1" \
		--clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 \
		--decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	t2v-B-noexp) run_arm t2v-B-noexp "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "$NOEXP" \
		--clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 \
		--decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	t2v-B-tile-noexp) run_arm t2v-B-tile-noexp "$IMG_B" "$SRV_B" t2v t2v-B-tile "" 48 "$T2V" "ENABLE_VAE_TILING=1;$NOEXP" \
		--clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 \
		--decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	t2v-B-tile-cold) run_arm t2v-B-tile-cold "$IMG_B" "$SRV_B" t2v t2v-B-tile "" 48 "$T2V" "ENABLE_VAE_TILING=1" --clip k:boat:42 ;;
	t2v-B-tile-noexp-cold) run_arm t2v-B-tile-noexp-cold "$IMG_B" "$SRV_B" t2v t2v-B-tile "" 48 "$T2V" "ENABLE_VAE_TILING=1;$NOEXP" --clip k:boat:42 ;;
	vace-B-noexp) run_arm vace-B-noexp "$IMG_B" "$SRV_B" vace vace-B "" 48 "$VACE" "$NOEXP" \
		--keyframe "$KF" --clip v0:boat:42 --clip v1:boat:42 ;;
	# Second addendum (row gfx1100-wan-decode-ab-v032-fix2-2026-11-06):
	# single-clip diagnostics, then the judged arm with FIX2_ARGS.
	dx-B-noexp2) run_arm dx-B-noexp2 "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "" \
		--setenv "PYTORCH_ALLOC_CONF=garbage_collection_threshold:0.7,max_split_size_mb:256" \
		--setenv "PYTORCH_HIP_ALLOC_CONF=garbage_collection_threshold:0.9,max_split_size_mb:512" \
		--clip c1:boat:42 --clip c2:fox:7 --decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	dx-B-scratch) run_arm dx-B-scratch "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "" \
		--unsetenv HSA_NO_SCRATCH_RECLAIM \
		--clip c1:boat:42 --clip c2:fox:7 --decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	dx-B-rocblas) run_arm dx-B-rocblas "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "" \
		--setenv TORCH_BLAS_PREFER_HIPBLASLT=0 \
		--clip c1:boat:42 --clip c2:fox:7 --decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	dx-B-tile-nooffload) run_arm dx-B-tile-nooffload "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "" \
		--setenv ENABLE_VAE_TILING=1 --setenv USE_CPU_OFFLOAD=0 \
		--clip c1:boat:42 --clip c2:fox:7 --decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	dx-B-tile-nocompile) run_arm dx-B-tile-nocompile "$IMG_B" "$SRV_B" t2v t2v-B "" 48 "$T2V" "" \
		--setenv ENABLE_VAE_TILING=1 --setenv COMPILE_MODE=off \
		--clip c1:boat:42 --clip c2:fox:7 --decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	t2v-B-fix2)
		[ -n "${FIX2_ARGS:-}" ] || { log "phase t2v-B-fix2 needs FIX2_ARGS"; return 2; }
		# shellcheck disable=SC2086 # FIX2_ARGS is a list of harness flags
		run_arm t2v-B-fix2 "$IMG_B" "$SRV_B" t2v t2v-B-fix2 "" 48 "$T2V" "" $FIX2_ARGS \
			--clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 \
			--decode /out/t2v-A/t2v-A-c1.latents.pt:xA ;;
	t2v-B-fix2-cold)
		[ -n "${FIX2_ARGS:-}" ] || { log "phase t2v-B-fix2-cold needs FIX2_ARGS"; return 2; }
		# shellcheck disable=SC2086
		run_arm t2v-B-fix2-cold "$IMG_B" "$SRV_B" t2v t2v-B-fix2 "" 48 "$T2V" "" $FIX2_ARGS --clip k:boat:42 ;;
	cache-B-fbc005-fix) run_arm cache-B-fbc005-fix "$IMG_B" "$SRV_BRANCH" t2v "$FIX_CACHE" "" 48 "$T2V" \
		"WAN_DENOISE_CACHE=fbc:0.05;$FIX_ENV" --clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 ;;
	cache-B-fbc010-fix) run_arm cache-B-fbc010-fix "$IMG_B" "$SRV_BRANCH" t2v "$FIX_CACHE" "" 48 "$T2V" \
		"WAN_DENOISE_CACHE=fbc:0.1;$FIX_ENV" --clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 ;;
	cache-B-ts541-fix) run_arm cache-B-ts541-fix "$IMG_B" "$SRV_BRANCH" t2v "$FIX_CACHE" "" 48 "$T2V" \
		"WAN_DENOISE_CACHE=taylorseer:5:4:1;$FIX_ENV" --clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 ;;
	cache-B-fbc005) run_arm cache-B-fbc005 "$IMG_B" "$SRV_BRANCH" t2v t2v-B "" 48 "$T2V" \
		"WAN_DENOISE_CACHE=fbc:0.05" --clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 ;;
	cache-B-fbc010) run_arm cache-B-fbc010 "$IMG_B" "$SRV_BRANCH" t2v t2v-B "" 48 "$T2V" \
		"WAN_DENOISE_CACHE=fbc:0.1" --clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 ;;
	cache-B-ts541) run_arm cache-B-ts541 "$IMG_B" "$SRV_BRANCH" t2v t2v-B "" 48 "$T2V" \
		"WAN_DENOISE_CACHE=taylorseer:5:4:1" --clip c0:boat:42 --clip c1:boat:42 --clip c2:fox:7 ;;
	*)
		log "unknown phase $1"
		return 2
		;;
	esac
}

mkdir -p "$ROOT"
for p in "$@"; do
	phase "$p" || log "phase $p returned $?"
done
log "all requested phases finished"

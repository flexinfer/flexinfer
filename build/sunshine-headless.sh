#!/bin/bash
# Sunshine headless wrapper — GPU-accelerated game streaming for Moonlight.
#
# Replaces the Xvfb-based steam-headless.sh (software GL, never ran a game).
# Brings up a headless GPU-accelerated wlroots (sway) session and a Sunshine
# host that Moonlight clients pair against. Launched by flexinfer-runtime when
# gaming mode is activated (NodeMode=gaming). The AMD/RDNA3 substrate — Mesa
# RADV render + VA-API HW encode inside a privileged container with /dev/dri —
# was validated by Slice 1's kill-test
# (.loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md).
#
# Compositor is sway (headless wlroots): gamescope is not packaged for Ubuntu
# 24.04. Sunshine captures sway's wlr-screencopy output and HW-encodes it.
set -euo pipefail

# ── GPU selection (AMD/RDNA3) ─────────────────────────────────────────────────
# This host exposes multiple DRM render nodes (dGPU renderD128 + iGPU renderD129
# on cblevins-7900xtx). Pin the discrete card explicitly for BOTH render and
# encode — VA-API device auto-selection is buggy (Sunshine #2521/#4555) and the
# compositor must render on the dGPU, not llvmpipe/iGPU.
: "${GAMING_RENDER_NODE:=/dev/dri/renderD128}"   # NAVI31 (7900 XTX)
export LIBVA_DRIVER_NAME="${LIBVA_DRIVER_NAME:-radeonsi}"
export WLR_RENDER_DRM_DEVICE="${WLR_RENDER_DRM_DEVICE:-$GAMING_RENDER_NODE}"

# ── Headless wlroots session (no physical display / HDMI dummy) ────────────────
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp/xdg-runtime}"
mkdir -p "$XDG_RUNTIME_DIR" && chmod 700 "$XDG_RUNTIME_DIR"
export WLR_BACKENDS=headless
export WLR_LIBINPUT_NO_DEVICES=1
export LIBSEAT_BACKEND=noop
export WLR_NO_HARDWARE_CURSORS=1
export WLR_RENDERER="${WLR_RENDERER:-vulkan}"
export WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-1}"

GAMING_RES="${GAMING_RESOLUTION:-1920x1080}"
GAMING_FPS="${GAMING_FPS:-60}"

# Generate a minimal sway config: one headless output at the target mode, and
# (optionally) autostart the game command.
SWAY_CONFIG="${XDG_RUNTIME_DIR}/sway.config"
{
    echo "output HEADLESS-1 resolution ${GAMING_RES}@${GAMING_FPS}Hz"
    echo "output HEADLESS-1 background #101014 solid_color"
    if [ -n "${GAMING_LAUNCH_CMD:-}" ]; then
        echo "exec ${GAMING_LAUNCH_CMD}"
    fi
} > "$SWAY_CONFIG"

cleanup() {
    [ -n "${SUNSHINE_PID:-}" ] && kill "$SUNSHINE_PID" 2>/dev/null || true
    [ -n "${SWAY_PID:-}" ] && kill "$SWAY_PID" 2>/dev/null || true
    [ -n "${DBUS_PID:-}" ] && kill "$DBUS_PID" 2>/dev/null || true
}
trap cleanup EXIT

# Session bus + audio (game audio streamed to Moonlight).
eval "$(dbus-launch --sh-syntax)"; DBUS_PID="${DBUS_SESSION_BUS_PID:-}"
pipewire & pipewire-pulse & wireplumber & 2>/dev/null || \
    pulseaudio --start --exit-idle-time=-1 2>/dev/null || true

# mDNS so Moonlight auto-discovers the host on the LAN (requires hostNetwork,
# Slice 4). Best-effort: without it, pair Moonlight by IP instead.
(dbus-daemon --system --fork 2>/dev/null; avahi-daemon --daemonize --no-drop-root 2>/dev/null) || true

# Headless GPU compositor, rendering on the pinned dGPU.
sway -c "$SWAY_CONFIG" &
SWAY_PID=$!

# Wait for the Wayland socket before starting Sunshine.
for _ in $(seq 1 30); do
    [ -S "${XDG_RUNTIME_DIR}/${WAYLAND_DISPLAY}" ] && break
    sleep 1
done

# Sunshine host. Pin the encoder to the dGPU; capture the wlroots output.
# Config/state (pairing, apps) persist on the gaming PVC (Slice 4). Moonlight
# pairs over the LAN (47984/47989/48010 TCP, 47998-48010 UDP — exposed by the
# gaming profile's hostNetwork in Slice 4).
SUNSHINE_CONF="${SUNSHINE_CONFIG:-/opt/flexinfer/sunshine/sunshine.conf}"
if [ ! -f "$SUNSHINE_CONF" ]; then
    mkdir -p "$(dirname "$SUNSHINE_CONF")"
    {
        echo "adapter_name = ${GAMING_RENDER_NODE}"
        echo "encoder = vaapi"
        echo "capture = wlr"
    } > "$SUNSHINE_CONF"
fi

sunshine "$SUNSHINE_CONF" &
SUNSHINE_PID=$!
wait "$SUNSHINE_PID"

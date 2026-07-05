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
#
# Process/user split (Steam refuses to run as uid 0):
#   gamer (non-root): dbus session bus, PipeWire stack, sway (+ Xwayland for
#                     Steam's X11 client), Steam.
#   root:             avahi (mDNS discovery) and Sunshine — Sunshine needs
#                     /dev/uinput for virtual gamepad/mouse input, and as root
#                     it can reach the gamer session's Wayland/Pulse sockets.
#   pod mounts:       /dev/input, /dev/uinput, and /run/udev so libinput sees
#                     Sunshine's virtual devices inside the sway session.
#
# Persistent state lives under GAMING_STATE_DIR (a hostPath volume on the
# gaming node — deploy/system/values-k3s.yaml gfx1100 profile):
#   sunshine/  — sunshine.conf, pairing state, apps.json, and Sunshine's
#                TLS credentials (survives restarts: Moonlight clients stay
#                paired)
#   home/      — gamer $HOME: Steam client, account login, game library
set -euo pipefail

# ── GPU selection (AMD/RDNA3) ─────────────────────────────────────────────────
# This host exposes multiple DRM render nodes (dGPU renderD128 + iGPU renderD129
# on cblevins-7900xtx). Pin the discrete card explicitly for BOTH render and
# encode — VA-API device auto-selection is buggy (Sunshine #2521/#4555) and the
# compositor must render on the dGPU, not llvmpipe/iGPU.
: "${GAMING_RENDER_NODE:=/dev/dri/renderD128}"   # NAVI31 (7900 XTX)
export LIBVA_DRIVER_NAME="${LIBVA_DRIVER_NAME:-radeonsi}"
export WLR_RENDER_DRM_DEVICE="${WLR_RENDER_DRM_DEVICE:-$GAMING_RENDER_NODE}"

# ── Persistent state + non-root session user ─────────────────────────────────
GAMING_STATE_DIR="${GAMING_STATE_DIR:-/var/lib/flexinfer-gaming}"
GAMING_USER="${GAMING_USER:-gamer}"
GAMING_UID="${GAMING_UID:-1000}"
GAMER_HOME="${GAMING_STATE_DIR}/home"
SUNSHINE_STATE_DIR="${GAMING_STATE_DIR}/sunshine"
SUNSHINE_HOME="${SUNSHINE_STATE_DIR}/home"
SUNSHINE_CONFIG_HOME="${SUNSHINE_STATE_DIR}/xdg-config"
SUNSHINE_DATA_HOME="${SUNSHINE_STATE_DIR}/xdg-data"
SUNSHINE_CONFIG_DIR="${SUNSHINE_CONFIG_HOME}/sunshine"
mkdir -p "$GAMER_HOME" "$SUNSHINE_STATE_DIR" "$SUNSHINE_HOME/.config" "$SUNSHINE_CONFIG_DIR" "$SUNSHINE_DATA_HOME"
if [ ! -e "${SUNSHINE_HOME}/.config/sunshine" ]; then
    ln -s "$SUNSHINE_CONFIG_DIR" "${SUNSHINE_HOME}/.config/sunshine"
fi

if id -u "$GAMING_USER" >/dev/null 2>&1; then
    usermod -d "$GAMER_HOME" "$GAMING_USER"
elif existing="$(getent passwd "$GAMING_UID" | cut -d: -f1)" && [ -n "$existing" ]; then
    # The uid is already taken (Ubuntu 24.04 bases ship an `ubuntu` uid-1000
    # user): rename that account instead of failing useradd (exit 4).
    usermod -l "$GAMING_USER" -d "$GAMER_HOME" "$existing"
else
    groupadd -g "$GAMING_UID" "$GAMING_USER" 2>/dev/null || true
    useradd -u "$GAMING_UID" -g "$GAMING_UID" -d "$GAMER_HOME" -s /bin/bash -M "$GAMING_USER"
fi
# Primary gid, numeric: a renamed base-image account keeps its old group NAME
# (usermod -l does not touch the group), so setpriv must not resolve by name.
GAMING_GID="$(id -g "$GAMING_USER")"
# GPU access for the non-root session: join the gids that own the DRM nodes
# (host gids leak through the /dev/dri hostPath; create matching groups).
for dev in /dev/dri/renderD* /dev/dri/card*; do
    [ -e "$dev" ] || continue
    gid="$(stat -c %g "$dev")"
    getent group "$gid" >/dev/null || groupadd -g "$gid" "drm-$gid"
    usermod -aG "$gid" "$GAMING_USER"
done
# Input access for the non-root compositor: Sunshine creates virtual
# keyboard/mouse/gamepad devices through /dev/uinput, then sway/libinput
# consumes the resulting /dev/input/event* devices as $GAMING_USER.
for dev in /dev/input/event* /dev/input/js*; do
    [ -e "$dev" ] || continue
    gid="$(stat -c %g "$dev")"
    group_name="$(getent group "$gid" | cut -d: -f1 || true)"
    if [ -z "$group_name" ]; then
        group_name="input-$gid"
        groupadd -g "$gid" "$group_name"
    fi
    usermod -aG "$group_name" "$GAMING_USER"
done
# Own the persistent state. Top-level dirs only — recursing into a multi-100GB
# game library on every start would take minutes.
chown "$GAMING_USER:$GAMING_GID" "$GAMING_STATE_DIR" "$GAMER_HOME" "$SUNSHINE_STATE_DIR" 2>/dev/null || true

# Sunshine runs as root so it can inject virtual input devices, but its default
# config home would be pod-local /root/.config/sunshine. Keep the root-run
# Sunshine credentials on the gaming hostPath so Moonlight/Steam Deck clients
# do not see a new host certificate after every runtime pod recreation.
if [ ! -d "${SUNSHINE_CONFIG_DIR}/credentials" ] && [ -d /root/.config/sunshine/credentials ]; then
    cp -a /root/.config/sunshine/credentials "$SUNSHINE_CONFIG_DIR/"
fi

# ── Headless wlroots session (no physical display / HDMI dummy) ────────────────
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp/xdg-runtime}"
mkdir -p "$XDG_RUNTIME_DIR"
chown "$GAMING_USER:$GAMING_GID" "$XDG_RUNTIME_DIR" && chmod 700 "$XDG_RUNTIME_DIR"
# Headless renders the virtual display; libinput is required for Moonlight
# controller/keyboard/mouse events that Sunshine injects through uinput.
export WLR_BACKENDS="${WLR_BACKENDS:-headless,libinput}"
# Do not set WLR_LIBINPUT_NO_DEVICES here. In the Ubuntu 24.04 wlroots build
# used by Sway 1.9, setting it kept the backend alive but prevented Sunshine's
# uinput devices from being added to the Sway seat.
export LIBSEAT_BACKEND=noop
export WLR_NO_HARDWARE_CURSORS=1
export WLR_RENDERER="${WLR_RENDERER:-vulkan}"
export WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-1}"

GAMING_RES="${GAMING_RESOLUTION:-1920x1080}"
GAMING_FPS="${GAMING_FPS:-60}"
# Match the headless output to each connecting Moonlight client (e.g. a Mac on a
# 3440x1440 21:9 ultrawide) instead of a single fixed mode: Sunshine exports the
# client's requested WIDTH/HEIGHT/FPS into a global_prep_cmd, which resizes the
# sway output on stream start and reverts to GAMING_RES on end (see below). The
# ${GAMING_RES}@${GAMING_FPS} values above stay the pre-connection/desktop base.
# Set GAMING_DYNAMIC_RESOLUTION=false to pin the base mode for every client.
GAMING_DYNAMIC_RESOLUTION="${GAMING_DYNAMIC_RESOLUTION:-true}"

# Steam launcher (steam-installer puts it in /usr/games, often not on PATH).
STEAM_BIN="$(command -v steam || true)"
[ -z "$STEAM_BIN" ] && [ -x /usr/games/steam ] && STEAM_BIN=/usr/games/steam

# Debian's steam launcher gates its first-run bootstrap on a zenity
# Install/Cancel question (--default-cancel, no env override) — on a headless
# host nobody clicks it and the client never downloads. Shim just that dialog
# to "Install"; every other zenity call passes through to the real binary.
SHIM_DIR="${XDG_RUNTIME_DIR}/shim"
mkdir -p "$SHIM_DIR"
cat > "$SHIM_DIR/zenity" <<'SH'
#!/bin/bash
for a in "$@"; do case "$a" in "--title=Steam installer") exit 0;; esac; done
exec /usr/bin/zenity "$@"
SH
chmod 755 "$SHIM_DIR/zenity"

# Run a command as the session user. Overrides the pod's inference-cache XDG_*
# env — Steam state must land in the persistent $GAMER_HOME, not the
# compile-cache hostPath the runtime profile points XDG_CACHE_HOME at.
GAMER_ENV=(
    HOME="$GAMER_HOME" USER="$GAMING_USER" LOGNAME="$GAMING_USER"
    PATH="$SHIM_DIR:$PATH:/usr/games"
    XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR"
    XDG_CACHE_HOME="$GAMER_HOME/.cache"
    XDG_CONFIG_HOME="$GAMER_HOME/.config"
    XDG_DATA_HOME="$GAMER_HOME/.local/share"
    DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
)
as_gamer() {
    setpriv --reuid "$GAMING_USER" --regid "$GAMING_GID" --init-groups \
        env "${GAMER_ENV[@]}" "$@"
}

# Generate a minimal sway config: Xwayland for Steam, one headless output at
# the base mode, and the session autostart (Steam unless overridden). Use
# `mode --custom` so exotic modelines (e.g. 3440x1440@120 21:9 ultrawide) are
# forced onto the headless output, which advertises no modes of its own; the
# global_prep_cmd below overrides this per connecting client.
SWAY_CONFIG="${XDG_RUNTIME_DIR}/sway.config"
{
    echo "xwayland enable"
    echo "output HEADLESS-1 mode --custom ${GAMING_RES}@${GAMING_FPS}Hz"
    echo "output HEADLESS-1 background #101014 solid_color"
    if [ -n "${GAMING_LAUNCH_CMD:-}" ]; then
        echo "exec ${GAMING_LAUNCH_CMD}"
    elif [ "${GAMING_STEAM_AUTOSTART:-true}" = "true" ] && [ -n "$STEAM_BIN" ]; then
        # First run bootstraps the client into $GAMER_HOME (one-time ~400MB
        # download), then shows the login window; the account session persists
        # on the gaming volume so login is a one-time step.
        echo "exec ${STEAM_BIN}"
    fi
} > "$SWAY_CONFIG"
chown "$GAMING_USER:$GAMING_GID" "$SWAY_CONFIG"

cleanup() {
    [ -n "${SUNSHINE_PID:-}" ] && kill "$SUNSHINE_PID" 2>/dev/null || true
    [ -n "${SWAY_PID:-}" ] && kill "$SWAY_PID" 2>/dev/null || true
    [ -n "${DBUS_PID:-}" ] && kill "$DBUS_PID" 2>/dev/null || true
}
trap cleanup EXIT

# Session bus + audio (game audio streamed to Moonlight), all as the session
# user so Steam can reach the sockets directly.
as_gamer dbus-daemon --session --address="unix:path=$XDG_RUNTIME_DIR/bus" --fork --nopidfile 2>/dev/null || true
as_gamer pipewire &
as_gamer pipewire-pulse &
as_gamer wireplumber &

# mDNS so Moonlight auto-discovers the host on the LAN (requires hostNetwork,
# Slice 4). Best-effort: without it, pair Moonlight by IP instead.
(dbus-daemon --system --fork 2>/dev/null; avahi-daemon --daemonize --no-drop-root 2>/dev/null) || true

# Headless GPU compositor, rendering on the pinned dGPU. The exported WLR_*/
# WAYLAND_DISPLAY vars flow through as_gamer's env(1) into the session.
as_gamer sway -c "$SWAY_CONFIG" &
SWAY_PID=$!

# Wait for the Wayland socket before starting Sunshine.
for _ in $(seq 1 30); do
    [ -S "${XDG_RUNTIME_DIR}/${WAYLAND_DISPLAY}" ] && break
    sleep 1
done

# Sunshine host. Pin the encoder to the dGPU; capture the wlroots output.
# Config, pairing state, and the app list persist under SUNSHINE_STATE_DIR so
# Moonlight clients stay paired across pod restarts. Moonlight pairs over the
# LAN (47984/47989/48010 TCP, 47998-48010 UDP — exposed by the gaming
# profile's hostNetwork, Slice 4).
SUNSHINE_CONF="${SUNSHINE_CONFIG:-${SUNSHINE_STATE_DIR}/sunshine.conf}"
SUNSHINE_APPS="${SUNSHINE_STATE_DIR}/apps.json"
if [ ! -f "$SUNSHINE_CONF" ]; then
    mkdir -p "$(dirname "$SUNSHINE_CONF")"
    {
        echo "adapter_name = ${GAMING_RENDER_NODE}"
        echo "encoder = vaapi"
        echo "capture = wlr"
        echo "file_state = ${SUNSHINE_STATE_DIR}/sunshine_state.json"
        echo "file_apps = ${SUNSHINE_APPS}"
    } > "$SUNSHINE_CONF"
fi

# Per-client resolution: match the headless sway output to whatever the
# Moonlight client requests (a Mac on a 3440x1440 21:9 ultrawide, a 1080p TV, a
# 1280x800 Steam Deck) so no client is letter/pillar-boxed. Sunshine runs this
# helper as a global_prep_cmd, exporting SUNSHINE_CLIENT_WIDTH/HEIGHT/FPS on
# stream start ("do") and again on stream end ("undo", reverting to the base
# mode). The helper is regenerated every start so script updates ship with the
# image, and is best-effort — a failed resize leaves the client on the base mode
# rather than aborting the stream.
RESIZE_HELPER="${SUNSHINE_STATE_DIR}/sunshine-resize.sh"
if [ "$GAMING_DYNAMIC_RESOLUTION" = "true" ]; then
    {
        echo "#!/bin/bash"
        echo "BASE_RES=\"${GAMING_RES}\""
        echo "BASE_FPS=\"${GAMING_FPS}\""
        echo "OUTPUT=\"HEADLESS-1\""
        cat <<'RESIZE_BODY'
# Generated by sunshine-headless.sh — resizes the headless sway output to the
# connecting Moonlight client. Runs as Sunshine's user (root), reaching the
# gamer session's sway IPC socket by globbing XDG_RUNTIME_DIR (root bypasses the
# socket owner perms). Every path exits 0: a resize failure must not kill a
# stream, the client just keeps the base mode.
MAX_W=3840
MAX_H=2160
log() { echo "[sunshine-resize] $*" >&2; }

sock=""
for s in "${XDG_RUNTIME_DIR:-/tmp/xdg-runtime}"/sway-ipc.*.sock; do
    [ -S "$s" ] && sock="$s" && break
done
if [ -z "$sock" ]; then
    log "no sway IPC socket under ${XDG_RUNTIME_DIR:-/tmp/xdg-runtime}; keeping base mode"
    exit 0
fi

set_mode() {
    # `--` stops swaymsg's own getopt from eating `--custom` as a flag before it
    # reaches the sway command parser (verified: without it swaymsg errors out).
    if swaymsg -s "$sock" -- output "$OUTPUT" mode --custom "${1}x${2}@${3}Hz" >/dev/null 2>&1; then
        log "set $OUTPUT -> ${1}x${2}@${3}Hz"
    else
        log "swaymsg failed for ${1}x${2}@${3}Hz; client keeps current mode"
    fi
}

if [ "${1:-}" = "--undo" ]; then
    set_mode "${BASE_RES%x*}" "${BASE_RES#*x}" "$BASE_FPS"
    exit 0
fi

W="${SUNSHINE_CLIENT_WIDTH:-}"
H="${SUNSHINE_CLIENT_HEIGHT:-}"
F="${SUNSHINE_CLIENT_FPS:-$BASE_FPS}"
# Integers only, within sane caps — a bogus client value must not wedge the
# compositor, so fall back to the base mode instead of setting it.
case "$W" in ''|*[!0-9]*) log "invalid client width '$W'; keeping base mode"; exit 0;; esac
case "$H" in ''|*[!0-9]*) log "invalid client height '$H'; keeping base mode"; exit 0;; esac
case "$F" in ''|*[!0-9]*) F="$BASE_FPS";; esac
if [ "$W" -lt 640 ] || [ "$H" -lt 480 ] || [ "$W" -gt "$MAX_W" ] || [ "$H" -gt "$MAX_H" ]; then
    log "client resolution ${W}x${H} out of range; keeping base mode"
    exit 0
fi
set_mode "$W" "$H" "$F"
exit 0
RESIZE_BODY
    } > "$RESIZE_HELPER"
    chmod +x "$RESIZE_HELPER"
    # Wire the helper into Sunshine. (Re)write the global_prep_cmd when it is
    # absent, an empty placeholder, or already ours; preserve a real
    # operator-authored one (a non-empty "do" pointing elsewhere). Sunshine
    # writes an empty `[{"do":"","undo":""}]` line whenever settings are saved
    # via the web UI, so a plain "does a global_prep_cmd line exist" guard would
    # wrongly skip wiring on a persisted conf — key off the actual "do" value.
    existing_do=$(grep -oE '"do":"[^"]*"' "$SUNSHINE_CONF" 2>/dev/null | head -1 | sed 's/^"do":"//; s/"$//')
    if [ -z "$existing_do" ] || [ "$existing_do" = "$RESIZE_HELPER" ]; then
        sed -i -E '/^[[:space:]]*global_prep_cmd/d' "$SUNSHINE_CONF"
        echo "global_prep_cmd = [{\"do\":\"${RESIZE_HELPER}\",\"undo\":\"${RESIZE_HELPER} --undo\"}]" >> "$SUNSHINE_CONF"
    fi
elif [ -f "$SUNSHINE_CONF" ]; then
    # Dynamic resolution disabled: drop the global_prep_cmd we manage (matched by
    # the helper path) so the base mode pins for every client, even on a conf
    # that persisted one from an earlier dynamic=true run. An operator-authored
    # prep cmd on a different path is left untouched.
    sed -i '\#global_prep_cmd.*sunshine-resize\.sh#d' "$SUNSHINE_CONF"
fi

# Steam app entry: Sunshine spawns app commands as its own user (root), so the
# wrapper re-enters the gamer session. Regenerated every start (env may move);
# apps.json is generated once and then user-editable via the Sunshine web UI.
STEAM_APP_SH="${SUNSHINE_STATE_DIR}/steam-app.sh"
if [ -n "$STEAM_BIN" ]; then
    {
        echo "#!/bin/bash"
        echo "exec setpriv --reuid ${GAMING_USER} --regid ${GAMING_GID} --init-groups \\"
        echo "  env ${GAMER_ENV[*]} DISPLAY=:0 \\"
        echo "  ${STEAM_BIN} steam://open/bigpicture"
    } > "$STEAM_APP_SH"
    chmod +x "$STEAM_APP_SH"
fi
if [ ! -f "$SUNSHINE_APPS" ]; then
    if [ -n "$STEAM_BIN" ]; then
        cat > "$SUNSHINE_APPS" <<EOF
{
  "env": {},
  "apps": [
    { "name": "Desktop", "image-path": "desktop" },
    { "name": "Steam Big Picture", "detached": ["${STEAM_APP_SH}"], "image-path": "steam" }
  ]
}
EOF
    else
        echo '{ "env": {}, "apps": [ { "name": "Desktop", "image-path": "desktop" } ] }' > "$SUNSHINE_APPS"
    fi
fi

env PULSE_SERVER="unix:${XDG_RUNTIME_DIR}/pulse/native" \
    HOME="$SUNSHINE_HOME" \
    XDG_CONFIG_HOME="$SUNSHINE_CONFIG_HOME" \
    XDG_DATA_HOME="$SUNSHINE_DATA_HOME" \
    sunshine "$SUNSHINE_CONF" &
SUNSHINE_PID=$!
wait "$SUNSHINE_PID"

#!/bin/bash
# Steam headless wrapper — starts virtual display + audio for Remote Play.
# Launched by flexinfer-runtime when gaming mode is activated.
set -euo pipefail

# Start virtual framebuffer
export DISPLAY=:99
Xvfb :99 -screen 0 1920x1080x24 &
XVFB_PID=$!

# Start PulseAudio for game audio over Remote Play
pulseaudio --start --exit-idle-time=-1 2>/dev/null || true

cleanup() {
    kill "$XVFB_PID" 2>/dev/null || true
    pulseaudio --kill 2>/dev/null || true
}
trap cleanup EXIT

# Initialize Steam via steamcmd (downloads client runtime on first run)
steamcmd +@sSteamCmdForcePlatformType linux +login anonymous +quit

# Launch Steam client in headless mode
exec steam -no-browser -silent -tcp

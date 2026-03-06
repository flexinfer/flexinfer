#!/usr/bin/env bash
# mount-devbox-nfs.sh — Mount nfs-media-v2 NFS on macOS, visible in Finder.
# Usage: sudo ./scripts/mount-devbox-nfs.sh
set -euo pipefail

NFS_SERVER="192.168.50.217"
NFS_PATH="/srv/nfs/nas-media-bulk"
MOUNT_POINT="/Volumes/nas-media-bulk"
PLIST="/Library/LaunchDaemons/com.loom.mount-nfs.plist"

if [[ $EUID -ne 0 ]]; then
  echo "Run with sudo: sudo $0"
  exit 1
fi

# ── Nuke everything ──
# Stop LaunchDaemon so it doesn't remount while we clean up
launchctl unload "$PLIST" 2>/dev/null || true

# Force unmount ALL stacked mounts on this path
while mount | grep -q "$MOUNT_POINT"; do
  umount -f "$MOUNT_POINT" 2>/dev/null || break
  sleep 0.5
done

# Remove all autofs remnants
rm -f /etc/auto_nfs /etc/auto_nfs_devbox 2>/dev/null
sed -i '' '/auto_nfs/d' /etc/auto_master 2>/dev/null || true
automount -cv 2>/dev/null || true

# Remove fstab remnants
sed -i '' "/nas-media-bulk/d" /etc/fstab 2>/dev/null || true

# ── Single clean mount ──
mkdir -p "$MOUNT_POINT"
# Map all NFS operations to uid/gid 1000 so both macOS user and
# Radarr/Sonarr (PUID=1000) can read/write without permission conflicts.
mount_nfs -o rw,resvport,noowners,nolockd,hard,intr,tcp,noatime,acregmin=3,acdirmin=0,acdirmax=5,mapall=1000:1000 \
  "${NFS_SERVER}:${NFS_PATH}" "$MOUNT_POINT"

# Verify write access
if touch "${MOUNT_POINT}/.mount-test" 2>/dev/null; then
  rm -f "${MOUNT_POINT}/.mount-test"
  echo "Write access: OK"
else
  echo "WARNING: write test failed"
fi

# ── LaunchDaemon for boot persistence ──
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.loom.mount-nfs</string>
    <key>ProgramArguments</key>
    <array>
        <string>/sbin/mount_nfs</string>
        <string>-o</string>
        <string>rw,resvport,noowners,nolockd,hard,intr,tcp,noatime,acregmin=3,acdirmin=0,acdirmax=5,mapall=1000:1000</string>
        <string>${NFS_SERVER}:${NFS_PATH}</string>
        <string>${MOUNT_POINT}</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/var/log/loom-mount-nfs.log</string>
</dict>
</plist>
EOF

chmod 644 "$PLIST"
chown root:wheel "$PLIST"
launchctl load "$PLIST"

echo ""
mount | grep "$MOUNT_POINT"
echo ""
echo "Mounted at ${MOUNT_POINT}"
ls "$MOUNT_POINT"/ | head -10

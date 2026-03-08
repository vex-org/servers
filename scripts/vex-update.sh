#!/bin/bash
# vex-update.sh — Auto-update Vex compiler from GitHub releases
# Called by: systemd timer (every 15min) + vex-api ExecStartPre
set -euo pipefail

VEX_DIR="${VEX_DIR:-/opt/vex}"
VEX_BIN="$VEX_DIR/bin/vex"
REPO="${VEX_REPO:-meftunca/vex}"
LOCK_FILE="/tmp/vex-update.lock"
LOG_TAG="vex-update"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; logger -t "$LOG_TAG" "$*" 2>/dev/null || true; }

# Prevent concurrent runs
exec 9>"$LOCK_FILE"
if ! flock -n 9; then
    log "Another update is already running, skipping"
    exit 0
fi

# Determine architecture
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  SUFFIX="linux-x86_64" ;;
    aarch64) SUFFIX="linux-aarch64" ;;
    *)       log "Unsupported arch: $ARCH"; exit 0 ;;
esac

# Fetch latest release tag
LATEST=$(curl -sf --max-time 10 "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//') || true

if [ -z "$LATEST" ]; then
    log "Could not fetch latest release tag, skipping"
    exit 0
fi

LATEST_VER=$(echo "$LATEST" | sed 's/^v//')

# Check current version
CURRENT=""
if [ -f "$VEX_BIN" ]; then
    CURRENT=$("$VEX_BIN" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || true)
fi

if [ "$CURRENT" = "$LATEST_VER" ]; then
    log "Vex already at $LATEST_VER"
    exit 0
fi

log "Updating Vex: ${CURRENT:-not installed} → $LATEST_VER"

# Download release tarball
TARBALL="vex-${LATEST}-${SUFFIX}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${LATEST}/${TARBALL}"
TMPDIR=$(mktemp -d /tmp/vex-update.XXXXXX)

cleanup() { rm -rf "$TMPDIR"; }
trap cleanup EXIT

curl -sfL --max-time 120 "$URL" -o "$TMPDIR/release.tar.gz" || {
    log "Download failed: $URL"
    exit 1
}

# Verify tarball is valid
if ! tar -tzf "$TMPDIR/release.tar.gz" >/dev/null 2>&1; then
    log "Invalid tarball, aborting"
    exit 1
fi

tar -xzf "$TMPDIR/release.tar.gz" -C "$TMPDIR" --strip-components=1

# Backup current binary
if [ -f "$VEX_BIN" ]; then
    cp "$VEX_BIN" "$VEX_BIN.bak"
fi

# Install
mkdir -p "$VEX_DIR/bin" "$VEX_DIR/lib/runtime" "$VEX_DIR/lib/std"
cp "$TMPDIR/bin/"* "$VEX_DIR/bin/" 2>/dev/null || true
chmod +x "$VEX_DIR/bin/"*
cp -r "$TMPDIR/lib/runtime/"* "$VEX_DIR/lib/runtime/" 2>/dev/null || true
cp -r "$TMPDIR/lib/std/"* "$VEX_DIR/lib/std/" 2>/dev/null || true

# Verify new binary works
if "$VEX_BIN" --version >/dev/null 2>&1; then
    NEW_VER=$("$VEX_BIN" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || echo "$LATEST_VER")
    log "Vex updated to $NEW_VER"
    rm -f "$VEX_BIN.bak"
else
    # Rollback
    log "New binary failed verification, rolling back"
    if [ -f "$VEX_BIN.bak" ]; then
        mv "$VEX_BIN.bak" "$VEX_BIN"
    fi
    exit 1
fi

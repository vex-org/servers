#!/bin/bash
# vex-update.sh — Auto-update Vex compiler from GitHub releases
# Called by: systemd timer (every 15min) + vex-api ExecStartPre
set -euo pipefail

VEX_USER="${VEX_USER:-vex}"
VEX_HOME="${VEX_HOME:-/home/${VEX_USER}/.vex}"
VEX_BIN="${VEX_BIN:-${VEX_HOME}/bin/vex}"
VEX_LINK_BIN="${VEX_LINK_BIN:-/usr/local/bin/vex}"
REPO="${VEX_REPO:-vex-org/releases}"
LOCK_FILE="/tmp/vex-update.lock"
LOG_TAG="vex-update"

log() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; logger -t "$LOG_TAG" "$*" 2>/dev/null || true; }

# Ensure clang-22 is available for Vex AOT compilation.
# vex_runtime.bc is compiled with LLVM 22; an older system clang would fail with
# "Unknown attribute kind" when linking. We install clang-22 from the LLVM apt
# repo if it is not already present and wire it up as the default clang.
ensure_clang22() {
    if command -v clang-22 >/dev/null 2>&1; then
        log "clang-22 already installed: $(clang-22 --version 2>&1 | head -1)"
        # apt installs versioned wrappers to /usr/bin/clang-22, not llvm-22/bin/clang-22
        update-alternatives --install /usr/bin/clang clang /usr/bin/clang-22 100 2>/dev/null || true
        update-alternatives --install /usr/bin/clang++ clang++ /usr/bin/clang++-22 100 2>/dev/null || true
        return 0
    fi
    log "Installing clang-22 for Vex AOT compilation..."
    command -v wget >/dev/null 2>&1 || apt-get install -y --no-install-recommends wget 2>/dev/null || true
    . /etc/os-release
    wget -qO- https://apt.llvm.org/llvm-snapshot.gpg.key \
        | gpg --dearmor -o /usr/share/keyrings/llvm.gpg 2>/dev/null || true
    echo "deb [signed-by=/usr/share/keyrings/llvm.gpg] http://apt.llvm.org/${VERSION_CODENAME}/ llvm-toolchain-${VERSION_CODENAME}-22 main" \
        > /etc/apt/sources.list.d/llvm-22.list
    apt-get update -o Dir::Etc::sourcelist="sources.list.d/llvm-22.list" \
                   -o Dir::Etc::sourceparts="-" \
                   -o APT::Get::List-Cleanup="0" 2>/dev/null || true
    if apt-get install -y --no-install-recommends clang-22 lld-22 2>/dev/null; then
        # Use /usr/bin/clang-22 (where apt installs the wrapper), NOT llvm-22/bin/clang-22
        update-alternatives --install /usr/bin/clang clang /usr/bin/clang-22 100 2>/dev/null || true
        update-alternatives --install /usr/bin/clang++ clang++ /usr/bin/clang++-22 100 2>/dev/null || true
        log "clang-22 installed and set as default clang"
    else
        log "WARNING: clang-22 install failed; Vex AOT will fall back to static linking with system clang"
    fi
}
ensure_clang22 || true

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

# Fetch latest release tag (includes pre-releases: rc, alpha, beta)
LATEST=$(curl -sf --max-time 10 "https://api.github.com/repos/${REPO}/releases?per_page=1" \
    | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//') || true

# Fallback to /releases/latest if list endpoint fails
if [ -z "$LATEST" ]; then
    LATEST=$(curl -sf --max-time 10 "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//') || true
fi

if [ -z "$LATEST" ]; then
    log "Could not fetch latest release tag, skipping"
    [ -f "$VEX_BIN" ] && exit 0
    exit 1
fi

LATEST_VER=$(echo "$LATEST" | sed 's/^v//')

# Check current version
CURRENT=""
if [ -f "$VEX_BIN" ]; then
    CURRENT=$("$VEX_BIN" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?' || true)
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
    [ -f "$VEX_BIN" ] && { log "Keeping current version"; exit 0; }
    exit 1
}

# Verify tarball is valid
if ! tar -tzf "$TMPDIR/release.tar.gz" >/dev/null 2>&1; then
    log "Invalid tarball, aborting"
    exit 1
fi

# Backup current binary
if [ -f "$VEX_BIN" ]; then
    cp "$VEX_BIN" "$VEX_BIN.bak"
fi

# Extract package and locate root directory
tar -xzf "$TMPDIR/release.tar.gz" -C "$TMPDIR"
PKG_DIR="$TMPDIR"
if [ ! -f "$PKG_DIR/bin/vex" ]; then
    PKG_DIR=$(find "$TMPDIR" -mindepth 1 -maxdepth 1 -type d -name 'vex-*' | head -1)
fi
if [ -z "$PKG_DIR" ] || [ ! -f "$PKG_DIR/bin/vex" ]; then
    log "Failed to locate extracted package directory"
    exit 1
fi

# Install
mkdir -p "$VEX_HOME/bin" "$VEX_HOME/lib"

for bin in vex vex-lsp vex-formatter vex-doc vex-pm; do
    if [ -f "$PKG_DIR/bin/$bin" ]; then
        cp "$PKG_DIR/bin/$bin" "$VEX_HOME/bin/$bin"
        chmod +x "$VEX_HOME/bin/$bin"
    fi
done

if [ -d "$PKG_DIR/lib/std" ]; then
    rm -rf "$VEX_HOME/lib/std"
    cp -r "$PKG_DIR/lib/std" "$VEX_HOME/lib/std"
fi

if [ -d "$PKG_DIR/lib/runtime" ]; then
    rm -rf "$VEX_HOME/lib/runtime"
    cp -r "$PKG_DIR/lib/runtime" "$VEX_HOME/lib/runtime"
fi

cat > "$VEX_HOME/config.json" <<EOF
{
  "version": "${LATEST}",
  "vex_home": "${VEX_HOME}",
  "std_path": "${VEX_HOME}/lib/std",
  "runtime_path": "${VEX_HOME}/lib/runtime",
  "tools": ["vex", "vex-lsp"]
}
EOF

mkdir -p "$(dirname "$VEX_LINK_BIN")"
ln -sfn "$VEX_HOME/bin/vex" "$VEX_LINK_BIN"

if id "$VEX_USER" >/dev/null 2>&1; then
    chown -R "$VEX_USER:$VEX_USER" "$VEX_HOME"
fi

# Verify new binary works
if "$VEX_BIN" --version >/dev/null 2>&1; then
    NEW_VER=$("$VEX_BIN" --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?' || echo "$LATEST_VER")
    log "Vex updated to $NEW_VER"
    rm -f "$VEX_BIN.bak"
    # Restart the API server so it picks up the new binary and version.
    # vex-api caches VexVersion at startup — a restart is the only way to refresh it.
    if systemctl is-active --quiet vex-api 2>/dev/null; then
        log "Restarting vex-api to pick up new version..."
        systemctl restart vex-api 2>/dev/null && log "vex-api restarted" || log "WARNING: failed to restart vex-api"
    fi
else
    # Rollback
    log "New binary failed verification, rolling back"
    if [ -f "$VEX_BIN.bak" ]; then
        mv "$VEX_BIN.bak" "$VEX_BIN"
    fi
    exit 1
fi

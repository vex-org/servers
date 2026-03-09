#!/bin/bash
# Oracle Cloud ARM server initial setup script
# Run as root on fresh Ubuntu 22.04/24.04 ARM instance
set -euo pipefail

echo "=== Vex API Server Setup ==="

# System updates
apt-get update && apt-get upgrade -y
apt-get install -y curl wget git nginx certbot python3-certbot-nginx \
    build-essential gcc nsjail

# Create service user
useradd -r -m -s /bin/bash vex || true
mkdir -p /opt/vex-api /opt/vex/bin /tmp/vex-sandbox
chown -R vex:vex /opt/vex-api /tmp/vex-sandbox

# Install Go
GO_VERSION="1.26.1"
ARCH=$(uname -m)
case "$ARCH" in
    x86_64) GO_ARCH="amd64" ;;
    aarch64|arm64) GO_ARCH="arm64" ;;
    *) echo "Unsupported Go arch: $ARCH" >&2; exit 1 ;;
esac
wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -O /tmp/go.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz
ln -sf /usr/local/go/bin/go /usr/local/bin/go
ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
source /etc/profile.d/go.sh

# Install Rust (for benchmark comparison)
su - vex -c 'curl --proto "=https" --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y'
su - vex -c 'source "$HOME/.cargo/env" && rustup update stable && rustup default stable'
RUSTC_BIN=$(su - vex -c 'source "$HOME/.cargo/env" && rustup which rustc')
CARGO_BIN=$(dirname "$RUSTC_BIN")/cargo
ln -sf "$RUSTC_BIN" /usr/local/bin/rustc
ln -sf "$CARGO_BIN" /usr/local/bin/cargo

# Install Zig
ZIG_VERSION="0.14.0"
ARCH=$(uname -m)
case "$ARCH" in
    x86_64) ZIG_ARCH="x86_64" ;;
    aarch64|arm64) ZIG_ARCH="aarch64" ;;
    *) echo "Unsupported Zig arch: $ARCH" >&2; exit 1 ;;
esac
wget -q "https://ziglang.org/download/${ZIG_VERSION}/zig-linux-${ZIG_ARCH}-${ZIG_VERSION}.tar.xz" -O /tmp/zig.tar.xz
mkdir -p /opt/zig && tar -C /opt/zig --strip-components=1 -xf /tmp/zig.tar.xz
ln -sf /opt/zig/zig /usr/local/bin/zig

# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh
systemctl enable ollama
systemctl start ollama

# Pull Qwen3.5 model
sleep 5  # Wait for Ollama to start
ollama pull qwen3.5:2b

# Setup systemd services
cp /opt/vex-api/scripts/vex-api.service /etc/systemd/system/
cp /opt/vex-api/scripts/vex-update.service /etc/systemd/system/
cp /opt/vex-api/scripts/vex-update.timer /etc/systemd/system/
chmod +x /opt/vex-api/scripts/vex-update.sh
systemctl daemon-reload
systemctl enable vex-api
systemctl enable --now vex-update.timer

# Setup Nginx
cp /opt/vex-api/vex-api.conf /etc/nginx/sites-available/
ln -sf /etc/nginx/sites-available/vex-api.conf /etc/nginx/sites-enabled/
nginx -t && systemctl reload nginx

# TLS certificate
certbot --nginx -d api.vex-lang.org --non-interactive --agree-tos -m admin@vex-lang.org || \
    echo "⚠️  Certbot failed — configure DNS first, then re-run certbot"

# Firewall
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

echo ""
echo "✅ Setup complete!"
echo "   Next steps:"
echo "   1. Build and copy vex-api binary to /opt/vex-api/"
echo "   2. Copy .env to /opt/vex-api/.env"
echo "   3. Install vex from release tarball:"
echo "      curl -sfL https://github.com/nicholaschenai/vex_lang/releases/latest/download/vex-vX.Y.Z-linux-\$(uname -m | sed 's/x86_64/x86_64/;s/aarch64/aarch64/').tar.gz | tar xz -C /opt/vex --strip-components=1"
echo "      This installs bin/vex + lib/runtime/libvex_runtime_core.a (needed for AOT)"
echo "   4. systemctl start vex-api"
echo "   5. Verify: curl http://localhost:8080/api/website/health"

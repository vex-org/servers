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

# Install Go 1.23
GO_VERSION="1.23.4"
wget -q "https://go.dev/dl/go${GO_VERSION}.linux-arm64.tar.gz" -O /tmp/go.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
source /etc/profile.d/go.sh

# Install Rust (for benchmark comparison)
su - vex -c 'curl --proto "=https" --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y'

# Install Zig
ZIG_VERSION="0.13.0"
wget -q "https://ziglang.org/download/${ZIG_VERSION}/zig-linux-aarch64-${ZIG_VERSION}.tar.xz" -O /tmp/zig.tar.xz
mkdir -p /opt/zig && tar -C /opt/zig --strip-components=1 -xf /tmp/zig.tar.xz
ln -sf /opt/zig/zig /usr/local/bin/zig

# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh
systemctl enable ollama
systemctl start ollama

# Pull Qwen3.5 model
sleep 5  # Wait for Ollama to start
ollama pull qwen3.5:0.8b

# Setup systemd service
cp /opt/vex-api/vex-api.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable vex-api

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

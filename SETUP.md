# Oracle Cloud ARM Server — Kurulum Rehberi

## 1. Oracle Cloud Hesap + Instance Oluşturma

### 1.1 Hesap Açma
1. https://cloud.oracle.com adresine git → "Start for Free"
2. Email, isim, ülke, kredi kartı (doğrulama için, ücret alınmaz)
3. Home Region seç (Frankfurt/Amsterdam tavsiye — Avrupa'ya yakın)

### 1.2 ARM Instance Oluşturma
1. Console → Compute → Instances → "Create Instance"
2. Ayarlar:
   - **Name**: `vex-api`
   - **Image**: Ubuntu 22.04 (Canonical)
   - **Shape**: VM.Standard.A1.Flex
     - OCPU: **4** (max free)
     - Memory: **24 GB** (max free)
   - **Networking**: Yeni VCN oluştur (default subnet tamam)
   - **SSH Key**: Public key yapıştır
   - **Boot Volume**: 100 GB (free tier'da 200GB'e kadar)
3. "Create" tıkla → 2-5 dk'da hazır

> **Not**: A1 instance bazen "Out of Capacity" verebilir. Gece 3-4 civarı tekrar dene veya farklı AD (Availability Domain) seç.

### 1.3 SSH Bağlantısı
```bash
# Public IP'yi console'dan al
ssh -i ~/.ssh/oracle_key ubuntu@<PUBLIC_IP>
```

## 2. Firewall + Network Security

### 2.1 Oracle Cloud Security List
Console → Networking → VCN → Security Lists → Default:

| Direction | Protocol | Port | Source |
|-----------|----------|------|--------|
| Ingress | TCP | 22 | 0.0.0.0/0 (SSH) |
| Ingress | TCP | 80 | 0.0.0.0/0 (HTTP) |
| Ingress | TCP | 443 | 0.0.0.0/0 (HTTPS) |

### 2.2 OS Firewall (iptables)
```bash
# Ubuntu default: iptables kuralları ekle
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 80 -j ACCEPT
sudo iptables -I INPUT 6 -m state --state NEW -p tcp --dport 443 -j ACCEPT
sudo netfilter-persistent save
```

## 3. Temel Sistem Kurulumu

```bash
# Sistem güncelle
sudo apt update && sudo apt upgrade -y

# Temel araçlar
sudo apt install -y \
  build-essential \
  git \
  curl \
  wget \
  unzip \
  htop \
  tmux \
  jq \
  pkg-config \
  libssl-dev \
  cmake \
  ninja-build \
  python3 \
  python3-pip

# Swap ekle (derleme için faydalı)
sudo fallocate -l 4G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile
echo '/swapfile swap swap defaults 0 0' | sudo tee -a /etc/fstab
```

## 4. Derleyici Kurulumları

### 4.1 Vex (kendi binary'miz)
```bash
sudo mkdir -p /opt/vex/bin
# Binary'yi SCP veya CI artifact olarak kopyala:
scp vex-linux-arm64 ubuntu@<SERVER>:/opt/vex/bin/vex
sudo chmod +x /opt/vex/bin/vex

# PATH'e ekle
echo 'export PATH="/opt/vex/bin:$PATH"' | sudo tee /etc/profile.d/vex.sh
source /etc/profile.d/vex.sh

# Doğrula
vex --version
```

### 4.2 LLVM (Vex runtime için gerekli)
```bash
# LLVM 21 (veya mevcut stable)
wget https://apt.llvm.org/llvm.sh
chmod +x llvm.sh
sudo ./llvm.sh 21
sudo apt install -y libllvm21 llvm-21-dev
```

### 4.3 Go
```bash
GO_VERSION="1.26.1"
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) GO_ARCH="amd64" ;;
  aarch64|arm64) GO_ARCH="arm64" ;;
  *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac
wget "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
sudo ln -sf /usr/local/go/bin/go /usr/local/bin/go
sudo ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
echo 'export PATH="/usr/local/go/bin:$PATH"' | sudo tee /etc/profile.d/go.sh
source /etc/profile.d/go.sh
go version
```

### 4.4 Rust
```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
source "$HOME/.cargo/env"
rustup update stable
rustup default stable
sudo ln -sf "$(rustup which rustc)" /usr/local/bin/rustc
sudo ln -sf "$(dirname "$(rustup which rustc)")/cargo" /usr/local/bin/cargo
rustc --version
```

### 4.5 Zig
```bash
ZIG_VERSION="0.14.0"
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ZIG_ARCH="x86_64" ;;
  aarch64|arm64) ZIG_ARCH="aarch64" ;;
  *) echo "Unsupported arch: $ARCH"; exit 1 ;;
esac
wget "https://ziglang.org/download/${ZIG_VERSION}/zig-linux-${ZIG_ARCH}-${ZIG_VERSION}.tar.xz"
sudo tar -C /opt -xf "zig-linux-${ZIG_ARCH}-${ZIG_VERSION}.tar.xz"
sudo ln -s "/opt/zig-linux-${ZIG_ARCH}-${ZIG_VERSION}/zig" /usr/local/bin/zig
zig version
```

## 5. Sandbox Kurulumu (nsjail)

```bash
# nsjail bağımlılıkları
sudo apt install -y \
  libprotobuf-dev \
  protobuf-compiler \
  libnl-3-dev \
  libnl-route-3-dev \
  libcap-dev \
  libseccomp-dev \
  flex \
  bison

# nsjail derle
git clone https://github.com/google/nsjail.git /tmp/nsjail
cd /tmp/nsjail
make -j$(nproc)
sudo cp nsjail /usr/local/bin/
nsjail --version

# Sandbox kullanıcısı
sudo useradd -r -s /usr/sbin/nologin sandbox
```

### nsjail Test
```bash
# Basit test: ls çalıştır
echo 'fn main(): i32 { println("sandbox works"); return 0; }' > /tmp/test.vx
sudo nsjail \
  --mode once \
  --time_limit 5 \
  --rlimit_as 256 \
  --rlimit_cpu 5 \
  --disable_proc \
  --iface_no_lo \
  --user sandbox \
  --group sandbox \
  --cwd /tmp \
  -- /opt/vex/bin/vex run /tmp/test.vx
```

## 6. AI Model Kurulumu (Ollama + Qwen3.5)

```bash
# Ollama kur
curl -fsSL https://ollama.com/install.sh | sh

# Qwen3.5 2B model indir
ollama pull qwen3.5:2b

# Test: model çalışıyor mu?
ollama run qwen3.5:2b "Explain this Vex code: fn main(): i32 { return 0; }"
```

## 7. Nginx + TLS (Let's Encrypt)

```bash
# Nginx kur
sudo apt install -y nginx certbot python3-certbot-nginx

# DNS: api.vex-lang.org → Oracle server public IP (A record)

# TLS sertifikası al
sudo certbot --nginx -d api.vex-lang.org --non-interactive --agree-tos -m admin@vex-lang.org

# Nginx config
sudo tee /etc/nginx/sites-available/vex-api << 'EOF'
server {
    listen 443 ssl http2;
    server_name api.vex-lang.org;

    ssl_certificate /etc/letsencrypt/live/api.vex-lang.org/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/api.vex-lang.org/privkey.pem;

    # Security headers
    add_header X-Frame-Options DENY;
    add_header X-Content-Type-Options nosniff;
    add_header X-XSS-Protection "1; mode=block";
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    # CORS
    add_header Access-Control-Allow-Origin "https://vex-lang.org" always;
    add_header Access-Control-Allow-Methods "GET, POST, OPTIONS" always;
    add_header Access-Control-Allow-Headers "Content-Type" always;

    # Request size limit (10KB kod)
    client_max_body_size 16k;

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 15s;
    }
}

server {
    listen 80;
    server_name api.vex-lang.org;
    return 301 https://$host$request_uri;
}
EOF

sudo ln -sf /etc/nginx/sites-available/vex-api /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx

# Auto-renew TLS
sudo systemctl enable certbot.timer
```

## 8. Systemd Servisleri

### vex-api.service
```bash
sudo tee /etc/systemd/system/vex-api.service << 'EOF'
[Unit]
Description=Vex API Server
After=network.target

[Service]
Type=simple
User=ubuntu
WorkingDirectory=/opt/vex-api
ExecStart=/opt/vex-api/vex-api
Restart=always
RestartSec=3
Environment=PORT=8080
Environment=SANDBOX_ENABLED=true
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable vex-api
```

### llama-server.service
```bash
sudo tee /etc/systemd/system/llama-server.service << 'EOF'
[Unit]
Description=Ollama AI Server (Qwen3.5 2B)
After=network.target

[Service]
Type=simple
User=ubuntu
ExecStart=/usr/local/bin/ollama serve
Environment=OLLAMA_HOST=127.0.0.1:11434
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable ollama
sudo systemctl start ollama

# Model'i önceden indir
ollama pull qwen3.5:2b
```

## 9. Monitoring

```bash
# Node exporter (Prometheus)
sudo apt install -y prometheus-node-exporter

# Basit health check cron
echo '*/5 * * * * curl -sf http://127.0.0.1:8080/api/health || systemctl restart vex-api' | sudo crontab -
```

## 10. Deploy Workflow

```bash
# Yeni Vex binary deploy etmek:
scp vex-linux-arm64 ubuntu@<SERVER>:/opt/vex/bin/vex
ssh ubuntu@<SERVER> "chmod +x /opt/vex/bin/vex && sudo systemctl restart vex-api"

# API kodu güncelleme:
ssh ubuntu@<SERVER> "cd /opt/vex-api && git pull && go build -o vex-api . && sudo systemctl restart vex-api"
```

## Checklist

- [ ] Oracle Cloud hesabı oluşturuldu
- [ ] A1 ARM instance ayağa kalktı
- [ ] SSH erişimi çalışıyor
- [ ] Firewall kuralları eklendi
- [ ] Swap eklendi
- [ ] Vex binary yüklendi
- [ ] LLVM kuruldu
- [ ] Go kuruldu
- [ ] Rust kuruldu
- [ ] Zig kuruldu
- [ ] nsjail kuruldu ve test edildi
- [ ] llama.cpp + Qwen3 0.6B çalışıyor
- [ ] Nginx + TLS konfigüre edildi
- [ ] DNS api.vex-lang.org ayarlandı
- [ ] systemd servisleri aktif
- [ ] Health check çalışıyor
- [ ] İlk /api/run testi başarılı

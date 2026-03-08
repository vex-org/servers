# Vex Servers — Backend Infrastructure

Oracle Cloud ARM (Ampere A1, 4 OCPU / 24GB RAM, free tier) üzerinde çalışan backend servisleri.

## Mimari

```
                         ┌─ vex-lang.org (Vercel) ─┐
                         │  Vue 3 Frontend          │
                         │  Playground UI            │
                         └──────────┬───────────────┘
                                    │ HTTPS API
                                    ▼
                    ┌─ Oracle ARM Server ─────────────────────┐
                    │                                         │
                    │  ┌─ Nginx (reverse proxy + TLS) ──────┐ │
                    │  │  api.vex-lang.org                   │ │
                    │  └──────────┬──────────────────────────┘ │
                    │             │                            │
                    │  ┌──────────▼──────────────────────────┐ │
                    │  │  vex-api (Go Fiber v3)               │ │
                    │  │  :8080                               │ │
                    │  │                                      │ │
                    │  │  POST /api/website/run     → compile │ │
                    │  │  POST /api/website/ir      → LLVM IR │ │
                    │  │  POST /api/website/compare → arena   │ │
                    │  │  POST /api/website/ai/ask  → AI      │ │
                    │  │  GET  /api/website/health  → health  │ │
                    │  │  */api/website/mcp-server  → MCP     │ │
                    │  └──────────┬──────────────────────────┘ │
                    │             │                            │
                    │  ┌──────────▼──────────────────────────┐ │
                    │  │  Sandbox (nsjail/firejail)          │ │
                    │  │  - CPU: 5s timeout                  │ │
                    │  │  - RAM: 256MB limit                 │ │
                    │  │  - No network / no fs write         │ │
                    │  │  - Separate PID/mount namespace     │ │
                    │  │                                      │ │
                    │  │  Derleyiciler:                       │ │
                    │  │  ├─ /opt/vex/bin/vex (Vex)          │ │
                    │  │  ├─ /usr/local/go/bin/go (Go)       │ │
                    │  │  ├─ /usr/bin/rustc (Rust)           │ │
                    │  │  └─ /usr/bin/zig (Zig)              │ │
                    │  └─────────────────────────────────────┘ │
                    │                                         │
                    │  ┌─ Ollama (AI) ───────────────────────┐ │
                    │  │  Qwen3.5 0.8B model                  │ │
                    │  │  :11434 (Ollama native API)          │ │
                    │  └─────────────────────────────────────┘ │
                    │                                         │
                    │  ┌─ Monitoring ───────────────────────┐  │
                    │  │  Prometheus metrics + Grafana       │  │
                    │  │  systemd journal logging            │  │
                    │  └────────────────────────────────────┘  │
                    └─────────────────────────────────────────┘
```

## API Endpoints

### `POST /api/website/run`
Vex kodunu derler ve çalıştırır.
```json
// Request
{ "code": "fn main(): i32 { println(\"hello\"); return 0; }" }

// Response
{ "stdout": "hello\n", "stderr": "", "exit_code": 0, "compile_time_ms": 120, "run_time_ms": 5 }
```

### `POST /api/website/ir`
Vex kodunun LLVM IR çıktısını döndürür.
```json
// Request
{ "code": "fn main(): i32 { return 42; }" }

// Response
{ "ir": "define i32 @main() {\nentry:\n  ret i32 42\n}", "compile_time_ms": 95 }
```

### `POST /api/website/compare`
Benchmark Arena — Vex kodunu + AI-transpile edilen Go/Rust/Zig kodlarını derleyip karşılaştırır.
```json
// Request
{ "code": "fn main(): i32 { ... }", "langs": ["go", "rust", "zig"] }

// Response
{
  "results": {
    "vex":  { "time_ms": 12, "binary_kb": 142, "memory_kb": 2100, "code": "..." },
    "go":   { "time_ms": 18, "binary_kb": 1800, "memory_kb": 8400, "code": "..." },
    "rust": { "time_ms": 11, "binary_kb": 312, "memory_kb": 3100, "code": "..." },
    "zig":  { "time_ms": 13, "binary_kb": 189, "memory_kb": 2300, "code": "..." }
  },
  "ai_disclaimer": "Go/Rust/Zig code was AI-generated and may not be idiomatic."
}
```

### `POST /api/website/ai/ask`
AI asistan — Ollama + Qwen3.5 0.8B ile Vex kodu hakkında soru cevap.
```json
// Request
{ "question": "Bu kodu açıkla", "code": "fn main(): i32 { ... }", "mode": "explain" }

// Modes: "explain" | "translate" | "fix"
// Response
{ "answer": "Bu fonksiyon...", "model": "qwen3.5:0.8b" }
```

### `GET /api/website/mcp-server/` + `POST /api/website/mcp-server/messages`
Model Context Protocol — VS Code / Cursor / AI agent entegrasyonu.
```json
// GET / → tool listesi (vex_run, vex_ir, vex_explain, vex_translate)
// POST /messages → MCP tool call
{ "method": "tools/call", "params": { "name": "vex_run", "arguments": { "code": "..." } } }
```

### `GET /api/website/health`
```json
{ "status": "ok", "uptime": 86400, "arch": "arm64", "os": "linux", "compilers": { "vex": "0.2.0", "go": "go1.23", "rustc": "1.82", "zig": "0.13" } }
```

## Rate Limiting

| Endpoint | Limit | Pencere |
|----------|-------|---------|
| `/api/run` | 30 req | /dakika/IP |
| `/api/ir` | 30 req | /dakika/IP |
| `/api/compare` | 5 req | /dakika/IP |
| `/api/ai/ask` | 20 req | /dakika/IP |

## Güvenlik

### Sandbox Kuralları
- **Namespace isolation**: PID, mount, network, user namespace
- **Zaman limiti**: 5 saniye (compile + run toplam)
- **Bellek limiti**: 256MB
- **Disk**: Read-only root, tmpfs /tmp (16MB)
- **Ağ**: Tamamen kapalı
- **Syscall filtresi**: seccomp whitelist (read, write, mmap, brk, exit, execve vb.)

### API Güvenliği
- CORS: Sadece `vex-lang.org` origin
- Input validation: Max 10KB kod, UTF-8 only
- No eval, no shell injection — kod dosyaya yazılır, sandbox içinde derlenir
- Rate limiting per IP (token bucket)

## Tech Stack

| Bileşen | Teknoloji |
|---------|----------|
| **API Server** | Go Fiber v3 |
| **AI Model** | Ollama + Qwen3.5 0.8B |
| **Sandbox** | nsjail (namespace isolation) |
| **CI/CD** | GitHub Actions → SSH deploy |
| **TLS** | Nginx + Let's Encrypt |
| **Database** | Supabase PostgreSQL (pgx) |

## Dizin Yapısı

```
web/server/
├── main.go                # Fiber v3 app entry
├── go.mod / go.sum
├── Dockerfile
├── .env.example
├── config/
│   └── config.go          # Env-based config loader
├── handlers/
│   ├── health.go          # GET /api/website/health
│   ├── run.go             # POST /api/website/run + /ir
│   ├── compare.go         # POST /api/website/compare
│   ├── ai.go              # POST /api/website/ai/ask + Ollama client
│   └── mcp.go             # MCP Server endpoints
├── sandbox/
│   └── executor.go        # nsjail wrapper + multi-lang compile
├── middleware/
│   └── ratelimit.go       # Token bucket per-IP rate limiter
├── .github/workflows/
│   └── deploy.yml         # CI: build ARM64 → SSH deploy → health check
├── nginx/
│   └── vex-api.conf       # Reverse proxy + TLS config
└── scripts/
    └── setup.sh           # Oracle server ilk kurulum
```

## Oracle Cloud Free Tier Specs

| Kaynak | Değer |
|--------|-------|
| Instance | Ampere A1 Flex |
| CPU | 4 OCPU (ARM64) |
| RAM | 24 GB |
| Storage | 200 GB block volume |
| Network | 4 Gbps |
| OS | Ubuntu 22.04 / Oracle Linux 9 |
| Maliyet | **$0/ay** (always free) |

## Kurulum

Bkz: [SETUP.md](./SETUP.md)

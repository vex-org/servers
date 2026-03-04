package config

import "os"

type Config struct {
	Port           string
	AllowedOrigin  string
	VexBinary      string
	SandboxEnabled bool
	SandboxBinary  string
	AIBackend      string // "groq" or "ollama"
	GroqAPIKey     string
	GroqModel      string
	OllamaURL      string
	OllamaModel    string
	SupabaseURL    string
	SupabaseKey    string
	DatabaseURL    string
}

func Load() *Config {
	return &Config{
		Port:           getEnv("PORT", "8080"),
		AllowedOrigin:  getEnv("ALLOWED_ORIGIN", "https://vex-lang.org"),
		VexBinary:      getEnv("VEX_BINARY", "/opt/vex/bin/vex"),
		SandboxEnabled: getEnv("SANDBOX_ENABLED", "true") == "true",
		SandboxBinary:  getEnv("SANDBOX_BINARY", "/usr/local/bin/nsjail"),
		AIBackend:      getEnv("AI_BACKEND", "groq"),
		GroqAPIKey:     getEnv("GROQ_API_KEY", ""),
		GroqModel:      getEnv("GROQ_MODEL", "openai/gpt-oss-120b"),
		OllamaURL:      getEnv("OLLAMA_URL", "http://127.0.0.1:11434"),
		OllamaModel:    getEnv("OLLAMA_MODEL", "qwen3.5:2b"),
		SupabaseURL:    getEnv("SUPABASE_URL", ""),
		SupabaseKey:    getEnv("SUPABASE_SERVICE_KEY", ""),
		DatabaseURL:    getEnv("DATABASE_URL", ""),
	}
}

func (c *Config) AllowedOrigins() []string {
	origins := []string{c.AllowedOrigin}
	// Always include www variant
	if c.AllowedOrigin == "https://vex-lang.org" {
		origins = append(origins, "https://www.vex-lang.org")
	}
	// Dev origins
	origins = append(origins, "http://localhost:3334", "http://localhost:5173")
	return origins
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

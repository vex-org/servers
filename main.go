package main

import (
	"log"
	"os"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"github.com/vex-org/servers/config"
	"github.com/vex-org/servers/handlers"
	"github.com/vex-org/servers/middleware"
	"github.com/vex-org/servers/sandbox"
)

func main() {
	cfg := config.Load()

	// Initialize sandbox executor
	exec := sandbox.NewExecutor(cfg.VexBinary, cfg.SandboxBinary, cfg.SandboxEnabled)
	handlers.InitExecutor(exec)

	// Initialize AI backend (Groq or Ollama)
	handlers.InitAI(cfg.AIBackend, cfg.GroqAPIKey, cfg.GroqModel, cfg.OllamaURL, cfg.OllamaModel)

	app := fiber.New(fiber.Config{
		AppName:   "Vex API Server",
		BodyLimit: 16 * 1024, // 16KB max
	})

	// Global middleware
	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} | ${status} | ${latency} | ${ip} | ${method} ${path}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: []string{cfg.AllowedOrigin},
		AllowMethods: []string{"GET", "POST", "OPTIONS"},
		AllowHeaders: []string{"Content-Type"},
	}))

	// API routes: /api/website/*
	api := app.Group("/api/website")
	api.Get("/health", handlers.Health)

	// Playground endpoints (rate limited)
	playground := api.Group("", middleware.RateLimit(30, 60))
	playground.Post("/run", handlers.Run)
	playground.Post("/ir", handlers.EmitIR)

	// Benchmark arena (stricter rate limit)
	arena := api.Group("", middleware.RateLimit(5, 60))
	arena.Post("/compare", handlers.Compare)

	// AI assistant
	ai := api.Group("", middleware.RateLimit(20, 60))
	ai.Post("/ai/ask", handlers.AskAI)

	// MCP Server
	mcp := api.Group("/mcp-server")
	mcp.Get("/", handlers.MCPInfo)
	mcp.Post("/messages", handlers.MCPMessages)

	port := cfg.Port
	if port == "" {
		port = "8080"
	}

	log.Printf("Vex API Server starting on :%s", port)
	log.Fatal(app.Listen(":" + port))
}

func init() {
	// Ensure required env for production
	if os.Getenv("ENV") == "production" {
		required := []string{"OLLAMA_URL"}
		for _, key := range required {
			if os.Getenv(key) == "" {
				log.Printf("WARNING: %s not set", key)
			}
		}
	}
}

package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"github.com/vex-org/servers/config"
	"github.com/vex-org/servers/handlers"
	mcpserver "github.com/vex-org/servers/mcp"
	"github.com/vex-org/servers/middleware"
	"github.com/vex-org/servers/rag"
	"github.com/vex-org/servers/sandbox"
)

func main() {
	cfg := config.Load()

	// Initialize sandbox executor
	exec := sandbox.NewExecutor(cfg.VexBinary, cfg.SandboxBinary, cfg.SandboxEnabled)
	handlers.InitExecutor(exec)

	// Initialize AI backend (Groq or Ollama)
	handlers.InitAI(cfg.AIBackend, cfg.GroqAPIKey, cfg.GroqModel, cfg.OllamaURL, cfg.OllamaModel)

	// Initialize RAG index
	vexRoot := findVexRoot(cfg.VexBinary)
	ragIdx := rag.NewIndex()
	indexer := rag.NewIndexer(vexRoot, ragIdx)
	go func() {
		if err := indexer.IndexAll(); err != nil {
			log.Printf("RAG indexing error: %v", err)
		}
	}()
	handlers.InitRAG(ragIdx)

	// Initialize MCP server
	mcpSrv := mcpserver.NewServer(ragIdx, exec, vexRoot)
	mcpSrv.AIChat = handlers.AIChat

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
		AllowOriginsFunc: func(origin string) bool {
			for _, o := range cfg.AllowedOrigins() {
				if o == origin {
					return true
				}
			}
			// Allow Vercel preview deployments
			return strings.HasSuffix(origin, ".vercel.app")
		},
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
	arena.Post("/compare-preset", handlers.ComparePreset)

	// AI assistant
	ai := api.Group("", middleware.RateLimit(20, 60))
	ai.Post("/ai/ask", handlers.AskAI)

	// MCP Server (protocol-compliant)
	mcpGroup := api.Group("/mcp")
	mcpGroup.Post("/message", mcpSrv.HandleMessage)      // SSE message endpoint
	mcpGroup.Post("/", mcpSrv.HandleStreamable)           // Streamable HTTP transport
	mcpGroup.Get("/info", func(c fiber.Ctx) error {       // MCP server metadata
		chunks, terms := ragIdx.Stats()
		return c.JSON(fiber.Map{
			"name":        "vex-lang-mcp",
			"version":     "0.3.2",
			"description": "Vex Language MCP Server with RAG knowledge base",
			"transport":   []string{"streamable-http"},
			"stats":       fiber.Map{"chunks": chunks, "terms": terms},
			"endpoints": fiber.Map{
				"streamable": "/api/website/mcp/",
				"message":    "/api/website/mcp/message",
			},
		})
	})

	// Legacy MCP endpoint (backward compat)
	mcp := api.Group("/mcp-server")
	mcp.Get("/", handlers.MCPInfo)
	mcp.Post("/messages", handlers.MCPMessages)

	// RAG search endpoint (for website AI chat)
	api.Post("/rag/search", func(c fiber.Ctx) error {
		var req struct {
			Query    string `json:"query"`
			Category string `json:"category"`
			Limit    int    `json:"limit"`
		}
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid JSON"})
		}
		if req.Limit <= 0 {
			req.Limit = 5
		}
		if req.Limit > 20 {
			req.Limit = 20
		}
		results := ragIdx.Search(req.Query, req.Limit)
		var items []fiber.Map
		for _, r := range results {
			if req.Category != "" && r.Chunk.Category != req.Category {
				continue
			}
			items = append(items, fiber.Map{
				"title":    r.Chunk.Title,
				"category": r.Chunk.Category,
				"source":   r.Chunk.Source,
				"content":  r.Chunk.Content,
				"score":    r.Score,
			})
		}
		chunks, terms := ragIdx.Stats()
		return c.JSON(fiber.Map{
			"results":  items,
			"stats":    fiber.Map{"chunks": chunks, "terms": terms},
		})
	})

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

// findVexRoot locates the Vex project root directory
func findVexRoot(vexBinary string) string {
	// Try VEX_ROOT env var first
	if root := os.Getenv("VEX_ROOT"); root != "" {
		return root
	}
	// Try to derive from vex binary path: /opt/vex_lang/target/.../vex → /opt/vex_lang
	dir := filepath.Dir(vexBinary)
	for dir != "/" && dir != "." {
		if _, err := os.Stat(filepath.Join(dir, "docs")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "crates")); err == nil {
				return dir
			}
		}
		dir = filepath.Dir(dir)
	}
	// Default fallback
	return "/opt/vex_lang"
}

package handlers

import (
	"github.com/gofiber/fiber/v3"
)

// MCP Server stub — Model Context Protocol endpoint
// https://spec.modelcontextprotocol.io/

// GET /api/website/mcp-server/
func MCPInfo(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"name":        "vex-mcp-server",
		"version":     "0.1.0",
		"description": "Vex Language MCP Server — compile, run, and analyze Vex code",
		"capabilities": fiber.Map{
			"tools": []fiber.Map{
				{
					"name":        "vex_run",
					"description": "Compile and run Vex code, returning stdout/stderr and timing",
					"inputSchema": fiber.Map{
						"type": "object",
						"properties": fiber.Map{
							"code": fiber.Map{"type": "string", "description": "Vex source code to compile and run"},
						},
						"required": []string{"code"},
					},
				},
				{
					"name":        "vex_ir",
					"description": "Compile Vex code and return LLVM IR output",
					"inputSchema": fiber.Map{
						"type": "object",
						"properties": fiber.Map{
							"code": fiber.Map{"type": "string", "description": "Vex source code"},
						},
						"required": []string{"code"},
					},
				},
				{
					"name":        "vex_explain",
					"description": "Explain Vex code using AI assistant",
					"inputSchema": fiber.Map{
						"type": "object",
						"properties": fiber.Map{
							"code": fiber.Map{"type": "string", "description": "Vex code to explain"},
						},
						"required": []string{"code"},
					},
				},
				{
					"name":        "vex_translate",
					"description": "Translate code from another language to Vex",
					"inputSchema": fiber.Map{
						"type": "object",
						"properties": fiber.Map{
							"code":        fiber.Map{"type": "string", "description": "Source code to translate"},
							"source_lang": fiber.Map{"type": "string", "description": "Source language (rust, go, python, etc.)"},
						},
						"required": []string{"code"},
					},
				},
			},
		},
	})
}

// POST /api/website/mcp-server/messages
// Stub: processes MCP tool call messages
func MCPMessages(c fiber.Ctx) error {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := c.Bind().JSON(&msg); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid MCP message"})
	}

	switch msg.Method {
	case "tools/call":
		return handleMCPToolCall(c, msg.Params.Name, msg.Params.Arguments)
	case "tools/list":
		return MCPInfo(c)
	default:
		return c.Status(400).JSON(fiber.Map{
			"error": "unsupported MCP method: " + msg.Method,
		})
	}
}

func handleMCPToolCall(c fiber.Ctx, toolName string, args map[string]any) error {
	code, _ := args["code"].(string)

	switch toolName {
	case "vex_run":
		if err := validateCode(code); err != nil {
			return err
		}
		result, err := executor.RunVex(code, "")
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "execution failed"})
		}
		return c.JSON(fiber.Map{
			"content": []fiber.Map{
				{"type": "text", "text": result.Stdout + result.Stderr},
			},
		})

	case "vex_ir":
		if err := validateCode(code); err != nil {
			return err
		}
		result, err := executor.EmitIR(code, "")
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "IR generation failed"})
		}
		return c.JSON(fiber.Map{
			"content": []fiber.Map{
				{"type": "text", "text": result.Stdout},
			},
		})

	case "vex_explain":
		answer, err := ollamaChat(vexSystemPrompt, "Explain this Vex code:\n\n```vex\n"+code+"\n```")
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": "AI unavailable"})
		}
		return c.JSON(fiber.Map{
			"content": []fiber.Map{
				{"type": "text", "text": answer},
			},
		})

	case "vex_translate":
		answer, err := ollamaChat(vexSystemPrompt, "Translate this code to Vex:\n\n```\n"+code+"\n```")
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": "AI unavailable"})
		}
		return c.JSON(fiber.Map{
			"content": []fiber.Map{
				{"type": "text", "text": answer},
			},
		})

	default:
		return c.Status(400).JSON(fiber.Map{"error": "unknown tool: " + toolName})
	}
}

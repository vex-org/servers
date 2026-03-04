package handlers

import (
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"

	"github.com/vex-org/servers/sandbox"
)

var executor *sandbox.Executor

func InitExecutor(e *sandbox.Executor) {
	executor = e
}

type CodeRequest struct {
	Code     string `json:"code"`
	OptLevel string `json:"opt_level,omitempty"`
}

func validateCode(code string) error {
	if code == "" {
		return fiber.NewError(400, "code is required")
	}
	if len(code) > 10*1024 {
		return fiber.NewError(400, "code exceeds 10KB limit")
	}
	if !utf8.ValidString(code) {
		return fiber.NewError(400, "code must be valid UTF-8")
	}
	return nil
}

// POST /api/website/run
func Run(c fiber.Ctx) error {
	var req CodeRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON"})
	}
	if err := validateCode(req.Code); err != nil {
		return err
	}

	result, err := executor.RunVex(req.Code, req.OptLevel)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "execution failed"})
	}

	return c.JSON(result)
}

// POST /api/website/ir
func EmitIR(c fiber.Ctx) error {
	var req CodeRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON"})
	}
	if err := validateCode(req.Code); err != nil {
		return err
	}

	result, err := executor.EmitIR(req.Code, req.OptLevel)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "IR generation failed"})
	}

	return c.JSON(fiber.Map{
		"ir":              result.Stdout,
		"stderr":          result.Stderr,
		"compile_time_ms": result.CompileTimeMs,
	})
}

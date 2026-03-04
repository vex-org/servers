package handlers

import (
	"github.com/gofiber/fiber/v3"
	"os/exec"
	"runtime"
	"time"
)

// GET /api/website/health
func Health(c fiber.Ctx) error {
	compilers := map[string]string{}
	for name, bin := range map[string]string{
		"vex": "vex", "go": "go", "rustc": "rustc", "zig": "zig",
	} {
		if out, err := exec.Command(bin, "--version").Output(); err == nil {
			compilers[name] = string(out[:min(len(out), 32)])
		} else {
			compilers[name] = "not found"
		}
	}

	return c.JSON(fiber.Map{
		"status":    "ok",
		"arch":      runtime.GOARCH,
		"os":        runtime.GOOS,
		"uptime":    time.Since(startTime).Seconds(),
		"compilers": compilers,
	})
}

var startTime = time.Now()

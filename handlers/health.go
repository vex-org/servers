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

	// Always resolve vex version dynamically: vex-update.sh may have swapped
	// the binary since startup (executor.VexVersion is a startup-time snapshot).
	vexBin := "vex"
	if executor != nil && executor.VexBinary != "" {
		vexBin = executor.VexBinary
	}
	if out, err := exec.Command(vexBin, "--version").Output(); err == nil {
		compilers["vex"] = string(out[:min(len(out), 64)])
	} else if executor != nil && executor.VexVersion != "" {
		compilers["vex"] = executor.VexVersion // fallback to cached
	}

	if executor != nil {
		if v := executor.ToolVersions["go"]; v != "" && v != "unknown" {
			compilers["go"] = v
		}
		if v := executor.ToolVersions["rust"]; v != "" && v != "unknown" {
			compilers["rustc"] = v
		}
		if v := executor.ToolVersions["zig"]; v != "" && v != "unknown" {
			compilers["zig"] = v
		}
	}

	for name, bin := range map[string]string{
		"go": "go", "rustc": "rustc", "zig": "zig",
	} {
		if _, ok := compilers[name]; ok {
			continue
		}
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

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type RunResult struct {
	Stdout        string  `json:"stdout"`
	Stderr        string  `json:"stderr"`
	ExitCode      int     `json:"exit_code"`
	CompileTimeMs float64 `json:"compile_time_ms"`
	RunTimeMs     float64 `json:"run_time_ms"`
	TimedOut      bool    `json:"timed_out,omitempty"`
}

type Executor struct {
	VexBinary      string
	SandboxBinary  string
	SandboxEnabled bool
	Timeout        time.Duration
	MemoryLimitMB  int
	TmpDir         string
}

func NewExecutor(vexBin, sandboxBin string, sandboxEnabled bool) *Executor {
	tmpDir := filepath.Join(os.TempDir(), "vex-sandbox")
	os.MkdirAll(tmpDir, 0755)
	return &Executor{
		VexBinary:      vexBin,
		SandboxBinary:  sandboxBin,
		SandboxEnabled: sandboxEnabled,
		Timeout:        10 * time.Second,
		MemoryLimitMB:  256,
		TmpDir:         tmpDir,
	}
}

// RunVex compiles and runs Vex code, returns stdout/stderr/timing
func (e *Executor) RunVex(code string) (*RunResult, error) {
	return e.runCompiler(code, e.VexBinary, "run", ".vx")
}

// EmitIR compiles Vex code and returns LLVM IR
func (e *Executor) EmitIR(code string) (*RunResult, error) {
	return e.runCompiler(code, e.VexBinary, "compile --emit-llvm", ".vx")
}

// RunGo compiles and runs Go code
func (e *Executor) RunGo(code string) (*RunResult, error) {
	return e.runCompiler(code, "go", "run", ".go")
}

// RunRust compiles and runs Rust code
func (e *Executor) RunRust(code string) (*RunResult, error) {
	workDir, srcFile, cleanup, err := e.writeSource(code, ".rs")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	binFile := filepath.Join(workDir, "program")
	result := &RunResult{}

	// Compile
	start := time.Now()
	compileCmd := e.buildCommand("rustc", []string{"-o", binFile, srcFile}, workDir)
	var compStderr bytes.Buffer
	compileCmd.Stderr = &compStderr
	if err := compileCmd.Run(); err != nil {
		result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
		result.Stderr = compStderr.String()
		result.ExitCode = 1
		return result, nil
	}
	result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000

	// Run
	start = time.Now()
	runCmd := e.buildCommand(binFile, nil, workDir)
	var stdout, stderr bytes.Buffer
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	if err := runCmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
		}
	}
	result.RunTimeMs = float64(time.Since(start).Microseconds()) / 1000
	result.Stdout = stdout.String()
	result.Stderr = result.Stderr + stderr.String()
	return result, nil
}

// RunZig compiles and runs Zig code
func (e *Executor) RunZig(code string) (*RunResult, error) {
	workDir, srcFile, cleanup, err := e.writeSource(code, ".zig")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	binFile := filepath.Join(workDir, "program")
	result := &RunResult{}

	start := time.Now()
	compileCmd := e.buildCommand("zig", []string{"build-exe", "-o", binFile, srcFile}, workDir)
	var compStderr bytes.Buffer
	compileCmd.Stderr = &compStderr
	if err := compileCmd.Run(); err != nil {
		result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
		result.Stderr = compStderr.String()
		result.ExitCode = 1
		return result, nil
	}
	result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000

	start = time.Now()
	runCmd := e.buildCommand(binFile, nil, workDir)
	var stdout, stderr bytes.Buffer
	runCmd.Stdout = &stdout
	runCmd.Stderr = &stderr
	if err := runCmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
		}
	}
	result.RunTimeMs = float64(time.Since(start).Microseconds()) / 1000
	result.Stdout = stdout.String()
	result.Stderr = result.Stderr + stderr.String()
	return result, nil
}

// BinarySize returns file size in KB
func (e *Executor) BinarySize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size() / 1024
}

func (e *Executor) runCompiler(code, compiler, subCmd, ext string) (*RunResult, error) {
	workDir, srcFile, cleanup, err := e.writeSource(code, ext)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	result := &RunResult{}
	args := []string{}
	if subCmd != "" {
		for _, a := range splitArgs(subCmd) {
			args = append(args, a)
		}
	}
	args = append(args, srcFile)

	start := time.Now()
	cmd := e.buildCommand(compiler, args, workDir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}
	elapsed := time.Since(start)
	result.CompileTimeMs = float64(elapsed.Microseconds()) / 1000
	result.RunTimeMs = result.CompileTimeMs // JIT mode: compile + run together
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	return result, nil
}

func (e *Executor) writeSource(code, ext string) (workDir, srcFile string, cleanup func(), err error) {
	id := uuid.New().String()[:8]
	workDir = filepath.Join(e.TmpDir, id)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", "", nil, fmt.Errorf("failed to create work dir: %w", err)
	}
	srcFile = filepath.Join(workDir, "main"+ext)
	if err := os.WriteFile(srcFile, []byte(code), 0644); err != nil {
		os.RemoveAll(workDir)
		return "", "", nil, fmt.Errorf("failed to write source: %w", err)
	}
	cleanup = func() { os.RemoveAll(workDir) }
	return workDir, srcFile, cleanup, nil
}

func (e *Executor) buildCommand(bin string, args []string, workDir string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), e.Timeout)
	_ = cancel // context auto-cancels on timeout

	if e.SandboxEnabled {
		nsjailArgs := []string{
			"--mode", "once",
			"--time_limit", "5",
			"--rlimit_as", fmt.Sprintf("%d", e.MemoryLimitMB),
			"--rlimit_cpu", "5",
			"--disable_proc",
			"--iface_no_lo",
			"--cwd", workDir,
			"--bindmount_ro", "/usr:/usr",
			"--bindmount_ro", "/lib:/lib",
			"--bindmount_ro", "/opt:/opt",
			"--bindmount", workDir + ":" + workDir,
			"--", bin,
		}
		nsjailArgs = append(nsjailArgs, args...)
		cmd := exec.CommandContext(ctx, e.SandboxBinary, nsjailArgs...)
		cmd.Dir = workDir
		return cmd
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	return cmd
}

func splitArgs(s string) []string {
	var args []string
	current := ""
	for _, c := range s {
		if c == ' ' {
			if current != "" {
				args = append(args, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}

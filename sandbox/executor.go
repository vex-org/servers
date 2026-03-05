package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

type RunResult struct {
	Stdout        string  `json:"stdout"`
	Stderr        string  `json:"stderr"`
	ExitCode      int     `json:"exit_code"`
	CompileTimeMs float64 `json:"compile_time_ms"`
	RunTimeMs     float64 `json:"run_time_ms"`
	UserTimeMs    float64 `json:"user_time_ms"`
	SysTimeMs     float64 `json:"sys_time_ms"`
	MemoryKB      int64   `json:"memory_kb"`
	BinaryKB      int64   `json:"binary_kb"`
	TimedOut      bool    `json:"timed_out,omitempty"`
	VexVersion    string  `json:"vex_version,omitempty"`
}

type Executor struct {
	VexBinary      string
	SandboxBinary  string
	SandboxEnabled bool
	Timeout        time.Duration
	MemoryLimitMB  int
	TmpDir         string
	VexVersion     string
}

func NewExecutor(vexBin, sandboxBin string, sandboxEnabled bool) *Executor {
	tmpDir := filepath.Join(os.TempDir(), "vex-sandbox")
	os.MkdirAll(tmpDir, 0755)

	// Cache vex version at startup
	vexVersion := "unknown"
	if out, err := exec.Command(vexBin, "--version").CombinedOutput(); err == nil {
		v := strings.TrimSpace(string(out))
		if v != "" {
			vexVersion = v
		}
	}

	return &Executor{
		VexBinary:      vexBin,
		SandboxBinary:  sandboxBin,
		SandboxEnabled: sandboxEnabled,
		Timeout:        10 * time.Second,
		MemoryLimitMB:  256,
		TmpDir:         tmpDir,
		VexVersion:     vexVersion,
	}
}

func extractProcessMetrics(cmd *exec.Cmd) (userMs, sysMs float64, memKB int64) {
	if cmd.ProcessState == nil {
		return
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return
	}
	userMs = float64(ru.Utime.Sec)*1000.0 + float64(ru.Utime.Usec)/1000.0
	sysMs = float64(ru.Stime.Sec)*1000.0 + float64(ru.Stime.Usec)/1000.0
	memKB = ru.Maxrss
	if runtime.GOOS == "darwin" {
		memKB = memKB / 1024
	}
	return
}

// validOptLevel returns a sanitized optimization level or default
func validOptLevel(level string) string {
	switch level {
	case "O0", "O1", "O2", "O3":
		return level
	default:
		return "O2"
	}
}

// RunVex compiles Vex to native binary then runs it (AOT for fair benchmark)
func (e *Executor) RunVex(code string, optLevel string) (*RunResult, error) {
	opt := validOptLevel(optLevel)
	workDir, srcFile, cleanup, err := e.writeSource(code, ".vx")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	result := &RunResult{}

	// Compile: vex compile -O{level} <file>
	start := time.Now()
	compileCmd := e.buildCommand(e.VexBinary, []string{"compile", "-" + opt, srcFile}, workDir)
	var compStdout, compStderr bytes.Buffer
	compileCmd.Stdout = &compStdout
	compileCmd.Stderr = &compStderr
	if err := compileCmd.Run(); err != nil {
		// AOT compile failed - fall back to --no-jit AOT mode
		jitResult, jitErr := e.runVexNoJIT(code, opt)
		if jitErr != nil {
			result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
			result.Stderr = compStdout.String() + compStderr.String()
			result.ExitCode = 1
			return result, nil
		}
		return jitResult, nil
	}
	result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000

	// Find compiled binary (vex-builds/main or ./main)
	binFile := ""
	for _, candidate := range []string{
		filepath.Join(workDir, "vex-builds", "main"),
		filepath.Join(workDir, "main"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			binFile = candidate
			break
		}
	}
	if binFile == "" {
		// Fallback: --no-jit AOT mode
		return e.runVexNoJIT(code, validOptLevel(optLevel))
	}
	result.BinaryKB = e.BinarySize(binFile)

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
	result.UserTimeMs, result.SysTimeMs, result.MemoryKB = extractProcessMetrics(runCmd)
	result.Stdout = stdout.String()
	result.Stderr = result.Stderr + stderr.String()
	result.VexVersion = e.VexVersion
	return result, nil
}

// runVexNoJIT falls back to vex run --no-jit for AOT execution in a single step
func (e *Executor) runVexNoJIT(code string, opt string) (*RunResult, error) {
	subCmd := "run --no-jit -" + opt
	r, err := e.runCompiler(code, e.VexBinary, subCmd, ".vx")
	if err != nil {
		return r, err
	}
	parseVexTiming(r)
	r.VexVersion = e.VexVersion
	return r, nil
}

// EmitIR compiles Vex code and returns LLVM IR from generated .ll file
func (e *Executor) EmitIR(code string, optLevel string) (*RunResult, error) {
	opt := validOptLevel(optLevel)
	workDir, srcFile, cleanup, err := e.writeSource(code, ".vx")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	result := &RunResult{}

	start := time.Now()
	cmd := e.buildCommand(e.VexBinary, []string{"compile", "--emit-llvm", "-" + opt, srcFile}, workDir)
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
	result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
	result.Stderr = stderr.String()

	// Read the generated .ll file
	for _, candidate := range []string{
		filepath.Join(workDir, "vex-builds", "main.ll"),
		filepath.Join(workDir, "main.ll"),
	} {
		if data, err := os.ReadFile(candidate); err == nil {
			result.Stdout = string(data)
			return result, nil
		}
	}

	// If no .ll file found, return whatever stdout had (error messages)
	result.Stdout = stdout.String()
	return result, nil
}

// RunGo compiles and runs Go code (separate compile + run for fair timing)
func (e *Executor) RunGo(code string, optLevel string) (*RunResult, error) {
	workDir, srcFile, cleanup, err := e.writeSource(code, ".go")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	binFile := filepath.Join(workDir, "program")
	result := &RunResult{}

	// Build compile args based on opt level
	opt := validOptLevel(optLevel)
	args := []string{"build", "-o", binFile}
	if opt == "O0" {
		args = append(args, `-gcflags=all=-N -l`)
	}
	args = append(args, srcFile)

	// Compile
	start := time.Now()
	compileCmd := e.buildCommand("go", args, workDir)
	var compStderr bytes.Buffer
	compileCmd.Stderr = &compStderr
	compileCmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(workDir, ".cache"))
	if err := compileCmd.Run(); err != nil {
		result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
		result.Stderr = compStderr.String()
		result.ExitCode = 1
		return result, nil
	}
	result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
	result.BinaryKB = e.BinarySize(binFile)

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
	result.UserTimeMs, result.SysTimeMs, result.MemoryKB = extractProcessMetrics(runCmd)
	result.Stdout = stdout.String()
	result.Stderr = result.Stderr + stderr.String()
	return result, nil
}

// RunRust compiles and runs Rust code
func (e *Executor) RunRust(code string, optLevel string) (*RunResult, error) {
	workDir, srcFile, cleanup, err := e.writeSource(code, ".rs")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	binFile := filepath.Join(workDir, "program")
	result := &RunResult{}

	// Map opt level to rustc flag
	opt := validOptLevel(optLevel)
	rustOpt := map[string]string{"O0": "0", "O1": "1", "O2": "2", "O3": "3"}[opt]

	// Compile
	start := time.Now()
	compileCmd := e.buildCommand("rustc", []string{"-C", "opt-level=" + rustOpt, "-o", binFile, srcFile}, workDir)
	var compStderr bytes.Buffer
	compileCmd.Stderr = &compStderr
	if err := compileCmd.Run(); err != nil {
		result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
		result.Stderr = compStderr.String()
		result.ExitCode = 1
		return result, nil
	}
	result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
	result.BinaryKB = e.BinarySize(binFile)

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
	result.UserTimeMs, result.SysTimeMs, result.MemoryKB = extractProcessMetrics(runCmd)
	result.Stdout = stdout.String()
	result.Stderr = result.Stderr + stderr.String()
	return result, nil
}

// RunZig compiles and runs Zig code
func (e *Executor) RunZig(code string, optLevel string) (*RunResult, error) {
	workDir, srcFile, cleanup, err := e.writeSource(code, ".zig")
	if err != nil {
		return nil, err
	}
	defer cleanup()

	binFile := filepath.Join(workDir, "program")
	result := &RunResult{}

	// Map opt level to Zig flag
	opt := validOptLevel(optLevel)
	zigOpt := map[string]string{"O0": "-ODebug", "O1": "-OReleaseSafe", "O2": "-OReleaseFast", "O3": "-OReleaseFast"}[opt]

	start := time.Now()
	compileCmd := e.buildCommand("zig", []string{"build-exe", zigOpt, "-femit-bin=" + binFile, srcFile}, workDir)
	var compStderr bytes.Buffer
	compileCmd.Stderr = &compStderr
	if err := compileCmd.Run(); err != nil {
		result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
		result.Stderr = compStderr.String()
		result.ExitCode = 1
		return result, nil
	}
	result.CompileTimeMs = float64(time.Since(start).Microseconds()) / 1000
	result.BinaryKB = e.BinarySize(binFile)

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
	result.UserTimeMs, result.SysTimeMs, result.MemoryKB = extractProcessMetrics(runCmd)
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
	result.UserTimeMs, result.SysTimeMs, result.MemoryKB = extractProcessMetrics(cmd)
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

var vexCompileTimeRe = regexp.MustCompile(`Compile time:\s*([\d.]+)ms`)
var vexRunTimeRe = regexp.MustCompile(`Run time:\s*([\d.]+)ms`)

// parseVexTiming extracts Vex's own timing lines from stdout and updates result
func parseVexTiming(r *RunResult) {
	if m := vexCompileTimeRe.FindStringSubmatch(r.Stdout); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			r.CompileTimeMs = v
		}
	}
	if m := vexRunTimeRe.FindStringSubmatch(r.Stdout); len(m) == 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			r.RunTimeMs = v
		}
	}
}

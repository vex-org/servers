package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/vex-org/servers/sandbox"
)

// --- Transpilation Cache ---

type cacheEntry struct {
	code      string
	createdAt time.Time
}

var (
	transpileCache   = make(map[string]cacheEntry)
	transpileCacheMu sync.RWMutex
	cacheTTL         = 24 * time.Hour
	cacheMaxSize     = 256
)

func cacheKey(vexCode, lang string) string {
	h := sha256.Sum256([]byte(lang + ":" + vexCode))
	return hex.EncodeToString(h[:16])
}

func cacheGet(key string) (string, bool) {
	transpileCacheMu.RLock()
	defer transpileCacheMu.RUnlock()
	e, ok := transpileCache[key]
	if !ok || time.Since(e.createdAt) > cacheTTL {
		return "", false
	}
	return e.code, true
}

func cacheSet(key, code string) {
	transpileCacheMu.Lock()
	defer transpileCacheMu.Unlock()
	// Evict oldest entries if cache is full
	if len(transpileCache) >= cacheMaxSize {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range transpileCache {
			if oldestKey == "" || v.createdAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.createdAt
			}
		}
		delete(transpileCache, oldestKey)
	}
	transpileCache[key] = cacheEntry{code: code, createdAt: time.Now()}
}

// --- Execution Results Cache ---

type runCacheEntry struct {
	result    *LangResult
	createdAt time.Time
}

var (
	runCache        = make(map[string]runCacheEntry)
	runCacheMu      sync.RWMutex
	runCacheTTL     = 24 * time.Hour
	runCacheMaxSize = 1000
)

func runCacheKey(lang, optLevel, code string) string {
	h := sha256.Sum256([]byte(lang + ":" + optLevel + ":" + code))
	return hex.EncodeToString(h[:16])
}

func runCacheGet(key string) (*LangResult, bool) {
	runCacheMu.RLock()
	defer runCacheMu.RUnlock()
	e, ok := runCache[key]
	if !ok || time.Since(e.createdAt) > runCacheTTL {
		return nil, false
	}
	res := *e.result
	return &res, true
}

func runCacheSet(key string, res *LangResult) {
	runCacheMu.Lock()
	defer runCacheMu.Unlock()
	if len(runCache) >= runCacheMaxSize {
		var oldestKey string
		var oldestTime time.Time
		for k, v := range runCache {
			if oldestKey == "" || v.createdAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.createdAt
			}
		}
		delete(runCache, oldestKey)
	}
	resCopy := *res
	runCache[key] = runCacheEntry{result: &resCopy, createdAt: time.Now()}
}

type CompareRequest struct {
	Code     string   `json:"code"`
	Langs    []string `json:"langs"`
	OptLevel string   `json:"opt_level,omitempty"`
}

type LangResult struct {
	TimeMs        float64 `json:"time_ms"`
	CompileTimeMs float64 `json:"compile_time_ms"`
	RunTimeMs     float64 `json:"run_time_ms"`
	UserTimeMs    float64 `json:"user_time_ms"`
	SysTimeMs     float64 `json:"sys_time_ms"`
	BinaryKB      int64   `json:"binary_kb"`
	MemoryKB      int64   `json:"memory_kb"`
	Code          string  `json:"code"`
	Stdout        string  `json:"stdout,omitempty"`
	Error         string  `json:"error,omitempty"`
}

// PresetCompareRequest accepts pre-written code for each language (no AI needed)
type PresetCompareRequest struct {
	VexCode  string `json:"vex_code"`
	GoCode   string `json:"go_code"`
	RustCode string `json:"rust_code"`
	ZigCode  string `json:"zig_code"`
	CppCode  string `json:"cpp_code"`
	CCode    string `json:"c_code"`
	OptLevel string `json:"opt_level,omitempty"`
}

// POST /api/website/compare-preset — runs pre-written code (no AI transpilation)
func ComparePreset(c fiber.Ctx) error {
	var req PresetCompareRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON"})
	}

	if req.VexCode == "" {
		return c.Status(400).JSON(fiber.Map{"error": "vex_code is required"})
	}

	results := make(map[string]*LangResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	type langJob struct {
		name string
		code string
		run  func(string, string) (*sandbox.RunResult, error)
	}

	jobs := []langJob{
		{"vex", req.VexCode, executor.RunVex},
	}
	if req.GoCode != "" {
		jobs = append(jobs, langJob{"go", req.GoCode, executor.RunGo})
	}
	if req.RustCode != "" {
		jobs = append(jobs, langJob{"rust", req.RustCode, executor.RunRust})
	}
	if req.ZigCode != "" {
		jobs = append(jobs, langJob{"zig", req.ZigCode, executor.RunZig})
	}
	if req.CCode != "" {
		jobs = append(jobs, langJob{"c", req.CCode, executor.RunC})
	}
	if req.CppCode != "" {
		jobs = append(jobs, langJob{"cpp", req.CppCode, executor.RunCpp})
	}

	for _, j := range jobs {
		wg.Add(1)
		go func(j langJob) {
			defer wg.Done()

			key := runCacheKey(j.name, req.OptLevel, j.code)
			if cached, ok := runCacheGet(key); ok {
				mu.Lock()
				results[j.name] = cached
				mu.Unlock()
				return
			}

			r, err := j.run(j.code, req.OptLevel)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || r == nil {
				results[j.name] = &LangResult{Error: "execution failed", Code: j.code}
				return
			}
			if r.ExitCode != 0 {
				errMsg := r.Stderr
				if errMsg == "" {
					errMsg = "compilation failed"
				}
				results[j.name] = &LangResult{Error: errMsg, Code: j.code}
				return
			}
			res := &LangResult{
				TimeMs:        r.RunTimeMs,
				CompileTimeMs: r.CompileTimeMs,
				RunTimeMs:     r.RunTimeMs,
				UserTimeMs:    r.UserTimeMs,
				SysTimeMs:     r.SysTimeMs,
				BinaryKB:      r.BinaryKB,
				MemoryKB:      r.MemoryKB,
				Code:          j.code,
				Stdout:        r.Stdout,
			}
			runCacheSet(key, res)
			results[j.name] = res
		}(j)
	}

	wg.Wait()
	return c.JSON(fiber.Map{"results": results, "versions": executor.ToolVersions})
}

// POST /api/website/compare
func Compare(c fiber.Ctx) error {
	var req CompareRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON"})
	}
	if err := validateCode(req.Code); err != nil {
		return err
	}

	// Allowed languages
	allowed := map[string]bool{"go": true, "rust": true, "zig": true}
	for _, l := range req.Langs {
		if !allowed[l] {
			return c.Status(400).JSON(fiber.Map{"error": "unsupported language: " + l})
		}
	}
	if len(req.Langs) == 0 {
		req.Langs = []string{"go", "rust", "zig"}
	}

	results := make(map[string]*LangResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Run Vex (always)
	wg.Add(1)
	go func() {
		defer wg.Done()

		key := runCacheKey("vex", req.OptLevel, req.Code)
		if cached, ok := runCacheGet(key); ok {
			mu.Lock()
			results["vex"] = cached
			mu.Unlock()
			return
		}

		r, err := executor.RunVex(req.Code, req.OptLevel)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			results["vex"] = &LangResult{Error: "execution failed"}
			return
		}
		if r.ExitCode != 0 {
			errMsg := r.Stderr
			if errMsg == "" {
				errMsg = "compilation failed"
			}
			results["vex"] = &LangResult{Error: errMsg, Code: req.Code}
			return
		}
		res := &LangResult{
			TimeMs:        r.RunTimeMs,
			CompileTimeMs: r.CompileTimeMs,
			RunTimeMs:     r.RunTimeMs,
			UserTimeMs:    r.UserTimeMs,
			SysTimeMs:     r.SysTimeMs,
			BinaryKB:      r.BinaryKB,
			MemoryKB:      r.MemoryKB,
			Code:          req.Code,
			Stdout:        r.Stdout,
		}
		runCacheSet(key, res)
		results["vex"] = res
	}()

	// Transpile + run other languages via AI
	for _, lang := range req.Langs {
		wg.Add(1)
		go func(lang string) {
			defer wg.Done()

			// Ask Ollama to transpile Vex → target language
			transpiled, err := transpileViaAI(req.Code, lang)
			if err != nil {
				mu.Lock()
				results[lang] = &LangResult{Error: "transpilation failed", Code: ""}
				mu.Unlock()
				return
			}

			key := runCacheKey(lang, req.OptLevel, transpiled)
			if cached, ok := runCacheGet(key); ok {
				mu.Lock()
				results[lang] = cached
				mu.Unlock()
				return
			}

			// Compile + run in target language
			var r *sandbox.RunResult
			switch lang {
			case "go":
				r, err = executor.RunGo(transpiled, req.OptLevel)
			case "rust":
				r, err = executor.RunRust(transpiled, req.OptLevel)
			case "zig":
				r, err = executor.RunZig(transpiled, req.OptLevel)
			}

			mu.Lock()
			defer mu.Unlock()
			if err != nil || r == nil {
				results[lang] = &LangResult{Error: "execution failed", Code: transpiled}
				return
			}
			if r.ExitCode != 0 {
				errMsg := r.Stderr
				if errMsg == "" {
					errMsg = "compilation failed"
				}
				results[lang] = &LangResult{Error: errMsg, Code: transpiled}
				return
			}
			res := &LangResult{
				TimeMs:        r.RunTimeMs,
				CompileTimeMs: r.CompileTimeMs,
				RunTimeMs:     r.RunTimeMs,
				UserTimeMs:    r.UserTimeMs,
				SysTimeMs:     r.SysTimeMs,
				BinaryKB:      r.BinaryKB,
				MemoryKB:      r.MemoryKB,
				Code:          transpiled,
				Stdout:        r.Stdout,
			}
			runCacheSet(key, res)
			results[lang] = res
		}(lang)
	}

	wg.Wait()

	return c.JSON(fiber.Map{
		"results":        results,
		"ai_disclaimer":  "Go/Rust/Zig code was AI-generated and may not be idiomatic.",
		"versions":       executor.ToolVersions,
	})
}

var transpileSystemPrompt = `You are an expert software engineer translating code from the Vex programming language to an idiomatic, correct, and optimized target language. 
You MUST write production-ready code that matches the semantics of the input Vex code exactly.

Guidelines:
1. Output ONLY valid, compilable code in the target language.
2. Do NOT wrap the output in markdown code blocks (e.g., do not use ` + "```" + `zig or ` + "```" + `rust).
3. Do NOT include any explanations, comments, or extra text.
4. Match the target language's standard performance guidelines (e.g. for Zig (v0.16.0), the entry point MUST be pub fn main(init: std.process.Init) !void and output must use try std.Io.File.stdout().writeStreamingAll(init.io, ...); ensure no unused imports or variables, and use @Vector/SIMD features where appropriate to match Vex's SIMD behavior).`

// transpileViaAI uses AI to convert Vex code to another language (cached)
func transpileViaAI(vexCode, targetLang string) (string, error) {
	key := cacheKey(vexCode, targetLang)
	if cached, ok := cacheGet(key); ok {
		return cached, nil
	}

	prompt := "Convert this Vex code to idiomatic " + targetLang + ". " +
		"Return ONLY the code, no markdown, no explanation.\n\n" + vexCode

	resp, err := aiGenerateTranspile(transpileSystemPrompt, prompt)
	if err != nil {
		return "", err
	}

	cacheSet(key, resp)
	return resp, nil
}

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
		results["vex"] = &LangResult{
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
			results[lang] = &LangResult{
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
		}(lang)
	}

	wg.Wait()

	return c.JSON(fiber.Map{
		"results":        results,
		"ai_disclaimer":  "Go/Rust/Zig code was AI-generated and may not be idiomatic.",
	})
}

// transpileViaAI uses AI to convert Vex code to another language (cached)
func transpileViaAI(vexCode, targetLang string) (string, error) {
	key := cacheKey(vexCode, targetLang)
	if cached, ok := cacheGet(key); ok {
		return cached, nil
	}

	prompt := "Convert this Vex code to idiomatic " + targetLang + ". " +
		"Return ONLY the code, no markdown, no explanation.\n\n" + vexCode

	resp, err := aiGenerate(prompt)
	if err != nil {
		return "", err
	}

	cacheSet(key, resp)
	return resp, nil
}

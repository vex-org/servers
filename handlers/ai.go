package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/vex-org/servers/rag"
)

var aiCfg struct {
	Backend     string // "groq" or "ollama"
	GroqKey     string
	GroqModel   string
	OllamaURL   string
	OllamaModel string
}

// RAG index reference (set by InitRAG)
var ragIndex *rag.Index

// InitAI configures the AI backend (called from main)
func InitAI(backend, groqKey, groqModel, ollamaURL, ollamaModel string) {
	aiCfg.Backend = backend
	aiCfg.GroqKey = groqKey
	aiCfg.GroqModel = groqModel
	aiCfg.OllamaURL = ollamaURL
	aiCfg.OllamaModel = ollamaModel
}

// InitRAG sets the RAG index for AI context enrichment
func InitRAG(index *rag.Index) {
	ragIndex = index
}

// AIChat is the exported AI chat function for MCP server usage
func AIChat(system, user string) (string, error) {
	return aiChat(system, user)
}

var vexSystemPrompt = `You are an expert Vex programming language assistant (Vex v0.3.2, LLVM 21).

Vex = Rust safety + Go simplicity + automatic SIMD. "Every Cycle, Every Core, Every Time."

### CRITICAL SYNTAX (NOT Rust!)
- fn main(): i32 { }           — colon for return type, NOT ->
- let x = 5;                   — immutable by default
- let! y = 10;                 — mutable with !
- Vec.new()                    — dot for member access, NOT ::
- Some(x)                      — NOT Option::Some(x) — NO :: operator!
- go { await task(); };        — goroutine syntax
- for i in 0..10 { }           — range loop

### Type System
Primitives: i8-i128/isize, u8-u128/usize, f32/f64, bool, char, string, str
Generics: Vec<T>, Map<K,V>, OrderedMap<K,V>, Set<T>, Box<T>, Ptr<T>, Span<T>, Channel<T>
SIMD: Tensor<T,N> (static), DynTensor<T> (runtime), Mask<N>, DynMask
Option<T> = Some(T) | None, Result<T,E> = Ok(T) | Err(E)

### Functions & Methods (Go-style receivers)
fn add(a: i32, b: i32): i32 { a + b }
fn Point(x: f64, y: f64): Point { Point { x, y } }
fn Point.origin(): Point { Point { x: 0.0, y: 0.0 } }
fn (self: &Point) length(): f64 { ... }
fn (self: &Point!) translate(dx: f64, dy: f64) { ... }

### Contracts (like traits) & Operator Overloading
contract Display { toString(): string; }
contract Drawable: Display { draw(); }
struct Point impl Display + Clone { x: f64, y: f64 }
fn (self: &Point) op+(other: Point): Point { ... }

### Concurrency
go { await someTask(); };
async fn fetch(): string { }
let ch = Channel.new<i32>(10);

### SIMD (Automatic Vectorization)
let a = [1.0, 2.0, 3.0, 4.0];
let c = a + [5.0, 6.0, 7.0, 8.0];   // auto-vectorized
let sum = <+ a;                       // reduce sum
let mask = a > 2.0;                   // Mask<4>

### Memory (VUMM)
Box<T> — compiler auto-selects: Unique, SharedRc, or AtomicArc.

### Error Handling
fn parse(s: string): Result<i32, string> { Ok(42) }
let val = parse("42")?;

When asked to explain code, be concise and accurate.
When asked to translate, produce idiomatic Vex code.
When asked to fix, identify the error and provide corrected code.`

type AskRequest struct {
	Question string `json:"question"`
	Code     string `json:"code"`
	Mode     string `json:"mode"` // "explain" | "translate" | "fix"
}

// POST /api/website/ai/ask
func AskAI(c fiber.Ctx) error {
	var req AskRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid JSON"})
	}

	if req.Question == "" && req.Code == "" {
		return c.Status(400).JSON(fiber.Map{"error": "question or code is required"})
	}

	if len(req.Question) > 2048 || len(req.Code) > 10*1024 {
		return c.Status(400).JSON(fiber.Map{"error": "input too large"})
	}

	prompt := buildPrompt(req)

	// Enrich system prompt with RAG context
	system := vexSystemPrompt
	if ragIndex != nil {
		query := req.Question + " " + req.Code
		results := ragIndex.Search(query, 3)
		if len(results) > 0 {
			var ctx strings.Builder
			ctx.WriteString("\n\n## Relevant Vex Documentation\n")
			for _, r := range results {
				content := r.Chunk.Content
				if len(content) > 1200 {
					content = content[:1200] + "\n..."
				}
				ctx.WriteString(fmt.Sprintf("\n### %s [%s]\n%s\n", r.Chunk.Title, r.Chunk.Category, content))
			}
			system += ctx.String()
		}
	}

	answer, err := aiChat(system, prompt)
	if err != nil {
		return c.Status(502).JSON(fiber.Map{"error": "AI service unavailable"})
	}

	model := aiCfg.GroqModel
	if aiCfg.Backend == "ollama" {
		model = aiCfg.OllamaModel
	}
	return c.JSON(fiber.Map{
		"answer": answer,
		"model":  model,
	})
}

func buildPrompt(req AskRequest) string {
	switch req.Mode {
	case "explain":
		return fmt.Sprintf("Explain this Vex code concisely:\n\n```vex\n%s\n```", req.Code)
	case "translate":
		return fmt.Sprintf("Translate this code to Vex:\n\n```\n%s\n```", req.Code)
	case "fix":
		return fmt.Sprintf("Fix this Vex code. The user says: %s\n\n```vex\n%s\n```", req.Question, req.Code)
	default:
		if req.Code != "" {
			return fmt.Sprintf("%s\n\n```vex\n%s\n```", req.Question, req.Code)
		}
		return req.Question
	}
}

// aiChat tries Groq first, falls back to Ollama on rate limit (429)
func aiChat(system, user string) (string, error) {
	if aiCfg.Backend == "groq" && aiCfg.GroqKey != "" {
		resp, err := groqChat(system, user)
		if err != nil && isRateLimited(err) && aiCfg.OllamaURL != "" {
			log.Println("Groq rate limited, falling back to Ollama")
			return ollamaChat(system, user)
		}
		return resp, err
	}
	return ollamaChat(system, user)
}

// aiGenerate tries Groq first, falls back to Ollama on rate limit
func aiGenerate(prompt string) (string, error) {
	if aiCfg.Backend == "groq" && aiCfg.GroqKey != "" {
		resp, err := groqChat(vexSystemPrompt, prompt)
		if err != nil && isRateLimited(err) && aiCfg.OllamaURL != "" {
			log.Println("Groq rate limited, falling back to Ollama")
			return ollamaGenerate(prompt)
		}
		return resp, err
	}
	return ollamaGenerate(prompt)
}

func isRateLimited(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "rate"))
}

// --- Groq Backend (OpenAI-compatible API) ---

func groqChat(system, user string) (string, error) {
	payload := map[string]any{
		"model": aiCfg.GroqModel,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.3,
		"max_tokens":  512,
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+aiCfg.GroqKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("groq request failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("groq error %d: %s", resp.StatusCode, string(data))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("groq parse error: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("groq: no response")
	}
	return result.Choices[0].Message.Content, nil
}

// --- Ollama Backend (self-hosted fallback) ---

func ollamaChat(system, user string) (string, error) {
	payload := map[string]any{
		"model": aiCfg.OllamaModel,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream": false,
		"options": map[string]any{
			"temperature": 0.3,
			"num_predict": 512,
		},
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(aiCfg.OllamaURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ollama error %d: %s", resp.StatusCode, string(data))
	}

	var result struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("ollama parse error: %w", err)
	}
	return result.Message.Content, nil
}

func ollamaGenerate(prompt string) (string, error) {
	payload := map[string]any{
		"model":  aiCfg.OllamaModel,
		"prompt": prompt,
		"stream": false,
		"options": map[string]any{
			"temperature": 0.2,
			"num_predict": 1024,
		},
	}

	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(aiCfg.OllamaURL+"/api/generate", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ollama error %d", resp.StatusCode)
	}

	var result struct {
		Response string `json:"response"`
	}
	json.Unmarshal(data, &result)
	return result.Response, nil
}

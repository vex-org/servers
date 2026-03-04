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
)

var aiCfg struct {
	Backend    string // "groq" or "ollama"
	GroqKey    string
	GroqModel  string
	OllamaURL  string
	OllamaModel string
}

// InitAI configures the AI backend (called from main)
func InitAI(backend, groqKey, groqModel, ollamaURL, ollamaModel string) {
	aiCfg.Backend = backend
	aiCfg.GroqKey = groqKey
	aiCfg.GroqModel = groqModel
	aiCfg.OllamaURL = ollamaURL
	aiCfg.OllamaModel = ollamaModel
}

var vexSystemPrompt = `You are a Vex programming language expert assistant.

Vex syntax rules (NOT Rust!):
- fn main(): i32 { }     — colon for return type, NOT ->
- let x = 5;             — immutable by default
- let! y = 10;           — mutable with !
- Vec.new()              — dot for member access, NOT ::
- Some(x)                — NOT Option::Some(x)
- go { await task(); };  — goroutine syntax
- for i in 0..10 { }     — range loop

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
	answer, err := aiChat(vexSystemPrompt, prompt)
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

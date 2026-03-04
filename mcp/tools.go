package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
)

// --- Tool Definitions ---

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func allTools() []toolDef {
	return []toolDef{
		{
			Name:        "vex_search",
			Description: "Search the Vex language knowledge base (docs, prelude API, stdlib, examples). Returns relevant code snippets and documentation chunks ranked by relevance.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query — e.g. 'Vec push', 'async await goroutine', 'struct method syntax'",
					},
					"category": map[string]any{
						"type":        "string",
						"description": "Filter by category: 'spec', 'prelude', 'stdlib', 'example', 'doc'. Empty = all.",
						"enum":        []string{"", "spec", "prelude", "stdlib", "example", "doc"},
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max results (default 5, max 20)",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "vex_run",
			Description: "Compile and run Vex code. Returns stdout, stderr, exit code, and timing metrics (compile time, run time, memory usage).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "Vex source code to compile and execute",
					},
					"opt_level": map[string]any{
						"type":        "string",
						"description": "Optimization level: O0, O1, O2, O3 (default O2)",
						"enum":        []string{"O0", "O1", "O2", "O3"},
					},
				},
				"required": []string{"code"},
			},
		},
		{
			Name:        "vex_ir",
			Description: "Compile Vex code and return LLVM IR output for analysis. Useful for understanding codegen.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "Vex source code",
					},
					"opt_level": map[string]any{
						"type":        "string",
						"description": "Optimization level: O0, O1, O2, O3 (default O2)",
						"enum":        []string{"O0", "O1", "O2", "O3"},
					},
				},
				"required": []string{"code"},
			},
		},
		{
			Name:        "vex_explain",
			Description: "Explain Vex code using AI with full language knowledge base context. Provides accurate explanations based on real Vex syntax and semantics.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "Vex code to explain",
					},
				},
				"required": []string{"code"},
			},
		},
		{
			Name:        "vex_translate",
			Description: "Translate code from another language (Rust, Go, Python, C, etc.) to idiomatic Vex. Uses RAG context to ensure correct Vex syntax.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "Source code to translate to Vex",
					},
					"source_lang": map[string]any{
						"type":        "string",
						"description": "Source language (rust, go, python, c, etc.)",
					},
				},
				"required": []string{"code"},
			},
		},
		{
			Name:        "vex_examples",
			Description: "Find Vex code examples by topic. Returns working example code from the Vex repository.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"topic": map[string]any{
						"type":        "string",
						"description": "Topic to search for — e.g. 'fibonacci', 'map usage', 'async channel', 'struct method'",
					},
				},
				"required": []string{"topic"},
			},
		},
	}
}

// --- Tool Handlers ---

func (s *Server) handleToolsList(req jsonRPCRequest) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"tools": allTools()},
	}
}

func (s *Server) handleToolsCall(req jsonRPCRequest) jsonRPCResponse {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	switch params.Name {
	case "vex_search":
		return s.toolSearch(req.ID, params.Arguments)
	case "vex_run":
		return s.toolRun(req.ID, params.Arguments)
	case "vex_ir":
		return s.toolIR(req.ID, params.Arguments)
	case "vex_explain":
		return s.toolExplain(req.ID, params.Arguments)
	case "vex_translate":
		return s.toolTranslate(req.ID, params.Arguments)
	case "vex_examples":
		return s.toolExamples(req.ID, params.Arguments)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "unknown tool: " + params.Name},
		}
	}
}

func (s *Server) toolSearch(id any, args map[string]any) jsonRPCResponse {
	query, _ := args["query"].(string)
	category, _ := args["category"].(string)
	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if limit > 20 {
		limit = 20
	}

	results := s.index.Search(query, limit*2) // fetch extra, filter by category

	var filtered []string
	for _, r := range results {
		if category != "" && r.Chunk.Category != category {
			continue
		}
		// Format result
		entry := fmt.Sprintf("--- [%s] %s (score: %.2f) ---\nSource: %s\n\n%s",
			r.Chunk.Category, r.Chunk.Title, r.Score, r.Chunk.Source, r.Chunk.Content)
		filtered = append(filtered, entry)
		if len(filtered) >= limit {
			break
		}
	}

	if len(filtered) == 0 {
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: id,
			Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: "No results found for: " + query}}},
		}
	}

	text := fmt.Sprintf("Found %d results for '%s':\n\n%s", len(filtered), query, strings.Join(filtered, "\n\n"))
	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}},
	}
}

func (s *Server) toolRun(id any, args map[string]any) jsonRPCResponse {
	code, _ := args["code"].(string)
	optLevel, _ := args["opt_level"].(string)

	if len(code) > 10*1024 {
		return s.toolError(id, "code too large (max 10KB)")
	}

	r, err := s.exec.RunVex(code, optLevel)
	if err != nil {
		return s.toolError(id, "execution failed: "+err.Error())
	}

	text := fmt.Sprintf("Exit code: %d\n\nStdout:\n%s", r.ExitCode, r.Stdout)
	if r.Stderr != "" {
		text += fmt.Sprintf("\n\nStderr:\n%s", r.Stderr)
	}
	text += fmt.Sprintf("\n\nMetrics:\n- Compile: %.2fms\n- Run: %.2fms\n- Memory: %dKB\n- Binary: %dKB",
		r.CompileTimeMs, r.RunTimeMs, r.MemoryKB, r.BinaryKB)

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}},
	}
}

func (s *Server) toolIR(id any, args map[string]any) jsonRPCResponse {
	code, _ := args["code"].(string)
	optLevel, _ := args["opt_level"].(string)

	if len(code) > 10*1024 {
		return s.toolError(id, "code too large (max 10KB)")
	}

	r, err := s.exec.EmitIR(code, optLevel)
	if err != nil {
		return s.toolError(id, "IR generation failed: "+err.Error())
	}

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: r.Stdout}}},
	}
}

func (s *Server) toolExplain(id any, args map[string]any) jsonRPCResponse {
	code, _ := args["code"].(string)
	if s.AIChat == nil {
		return s.toolError(id, "AI not configured")
	}

	// RAG: find relevant docs to augment the AI prompt
	ragContext := s.buildRAGContext(code, 3)

	systemPrompt := vexExpertPrompt + "\n\n## Relevant Documentation\n" + ragContext
	answer, err := s.AIChat(systemPrompt, "Explain this Vex code concisely:\n\n```vex\n"+code+"\n```")
	if err != nil {
		return s.toolError(id, "AI unavailable")
	}

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: answer}}},
	}
}

func (s *Server) toolTranslate(id any, args map[string]any) jsonRPCResponse {
	code, _ := args["code"].(string)
	sourceLang, _ := args["source_lang"].(string)
	if s.AIChat == nil {
		return s.toolError(id, "AI not configured")
	}

	// RAG: get Vex syntax reference
	ragContext := s.buildRAGContext("syntax function struct method contract "+sourceLang, 5)

	systemPrompt := vexExpertPrompt + "\n\n## Vex Syntax Reference\n" + ragContext
	prompt := fmt.Sprintf("Translate this %s code to idiomatic Vex. Return ONLY the Vex code:\n\n```%s\n%s\n```",
		sourceLang, sourceLang, code)

	answer, err := s.AIChat(systemPrompt, prompt)
	if err != nil {
		return s.toolError(id, "AI unavailable")
	}

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: answer}}},
	}
}

func (s *Server) toolExamples(id any, args map[string]any) jsonRPCResponse {
	topic, _ := args["topic"].(string)
	results := s.index.Search(topic, 5)

	var examples []string
	for _, r := range results {
		if r.Chunk.Category == "example" || r.Chunk.Category == "prelude" {
			entry := fmt.Sprintf("--- %s (%s) ---\n```vex\n%s\n```", r.Chunk.Title, r.Chunk.Source, r.Chunk.Content)
			examples = append(examples, entry)
		}
	}
	if len(examples) == 0 {
		// Fallback: return any category
		for _, r := range results {
			entry := fmt.Sprintf("--- %s [%s] ---\n%s", r.Chunk.Title, r.Chunk.Category, r.Chunk.Content)
			examples = append(examples, entry)
			if len(examples) >= 3 {
				break
			}
		}
	}

	if len(examples) == 0 {
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: id,
			Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: "No examples found for: " + topic}}},
		}
	}

	text := fmt.Sprintf("Found %d examples for '%s':\n\n%s", len(examples), topic, strings.Join(examples, "\n\n"))
	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}},
	}
}

func (s *Server) toolError(id any, msg string) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: mcpToolResult{
			Content: []mcpContent{{Type: "text", Text: msg}},
			IsError: true,
		},
	}
}

// buildRAGContext searches for relevant docs and returns formatted context
func (s *Server) buildRAGContext(query string, limit int) string {
	results := s.index.Search(query, limit)
	if len(results) == 0 {
		return "(no relevant documentation found)"
	}

	var parts []string
	for _, r := range results {
		// Truncate content to avoid bloating the prompt
		content := r.Chunk.Content
		if len(content) > 1500 {
			content = content[:1500] + "\n... (truncated)"
		}
		parts = append(parts, fmt.Sprintf("### %s [%s]\n%s", r.Chunk.Title, r.Chunk.Category, content))
	}
	return strings.Join(parts, "\n\n")
}

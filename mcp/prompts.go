package mcp

import (
	"encoding/json"
)

// --- Prompt Definitions ---

type promptDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Arguments   []promptArg `json:"arguments,omitempty"`
}

type promptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type promptMessage struct {
	Role    string     `json:"role"`
	Content mcpContent `json:"content"`
}

func allPrompts() []promptDef {
	return []promptDef{
		{
			Name:        "learn-vex",
			Description: "Get a comprehensive introduction to the Vex programming language, tailored to your background.",
			Arguments: []promptArg{
				{Name: "background", Description: "Your programming background (e.g. 'rust', 'go', 'python', 'c++')", Required: false},
				{Name: "topic", Description: "Specific topic to learn about (e.g. 'concurrency', 'generics', 'memory model')", Required: false},
			},
		},
		{
			Name:        "translate-to-vex",
			Description: "Translate code from another language to idiomatic Vex with explanations.",
			Arguments: []promptArg{
				{Name: "code", Description: "The source code to translate", Required: true},
				{Name: "source_lang", Description: "Source language", Required: false},
			},
		},
		{
			Name:        "vex-review",
			Description: "Review Vex code for correctness, style, and performance. Suggests improvements.",
			Arguments: []promptArg{
				{Name: "code", Description: "Vex code to review", Required: true},
			},
		},
		{
			Name:        "vex-debug",
			Description: "Help debug a Vex code error. Provide the code and error message.",
			Arguments: []promptArg{
				{Name: "code", Description: "Vex code that has an error", Required: true},
				{Name: "error", Description: "The error message or description", Required: false},
			},
		},
	}
}

func (s *Server) handlePromptsList(req jsonRPCRequest) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"prompts": allPrompts()},
	}
}

func (s *Server) handlePromptsGet(req jsonRPCRequest) jsonRPCResponse {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	switch params.Name {
	case "learn-vex":
		return s.promptLearnVex(req.ID, params.Arguments)
	case "translate-to-vex":
		return s.promptTranslate(req.ID, params.Arguments)
	case "vex-review":
		return s.promptReview(req.ID, params.Arguments)
	case "vex-debug":
		return s.promptDebug(req.ID, params.Arguments)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "unknown prompt: " + params.Name},
		}
	}
}

func (s *Server) promptLearnVex(id any, args map[string]string) jsonRPCResponse {
	background := args["background"]
	topic := args["topic"]

	// Get relevant RAG context
	query := "vex language syntax overview"
	if topic != "" {
		query = topic
	}
	if background != "" {
		query += " " + background
	}
	ragContext := s.buildRAGContext(query, 5)

	systemMsg := vexExpertPrompt + "\n\n## Language Reference\n" + ragContext

	userMsg := "Teach me the Vex programming language."
	if background != "" {
		userMsg += " I come from a " + background + " background."
	}
	if topic != "" {
		userMsg += " Focus on: " + topic + "."
	}
	userMsg += " Include code examples."

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: map[string]any{
			"description": "Learn the Vex programming language",
			"messages": []promptMessage{
				{Role: "system", Content: mcpContent{Type: "text", Text: systemMsg}},
				{Role: "user", Content: mcpContent{Type: "text", Text: userMsg}},
			},
		},
	}
}

func (s *Server) promptTranslate(id any, args map[string]string) jsonRPCResponse {
	code := args["code"]
	sourceLang := args["source_lang"]
	if sourceLang == "" {
		sourceLang = "unknown"
	}

	ragContext := s.buildRAGContext("syntax function struct method contract operator "+sourceLang, 5)

	systemMsg := vexExpertPrompt + "\n\n## Vex Syntax Reference\n" + ragContext

	userMsg := "Translate this " + sourceLang + " code to idiomatic Vex. " +
		"Show the Vex code first, then explain key differences.\n\n```" + sourceLang + "\n" + code + "\n```"

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: map[string]any{
			"description": "Translate code to Vex",
			"messages": []promptMessage{
				{Role: "system", Content: mcpContent{Type: "text", Text: systemMsg}},
				{Role: "user", Content: mcpContent{Type: "text", Text: userMsg}},
			},
		},
	}
}

func (s *Server) promptReview(id any, args map[string]string) jsonRPCResponse {
	code := args["code"]
	ragContext := s.buildRAGContext(code, 3)

	systemMsg := vexExpertPrompt + "\n\n## Reference\n" + ragContext

	userMsg := "Review this Vex code for correctness, style, and performance. " +
		"Point out any syntax errors (remember: Vex uses `:` for return types, `let!` for mutable, `.` not `::` for member access). " +
		"Suggest improvements.\n\n```vex\n" + code + "\n```"

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: map[string]any{
			"description": "Review Vex code",
			"messages": []promptMessage{
				{Role: "system", Content: mcpContent{Type: "text", Text: systemMsg}},
				{Role: "user", Content: mcpContent{Type: "text", Text: userMsg}},
			},
		},
	}
}

func (s *Server) promptDebug(id any, args map[string]string) jsonRPCResponse {
	code := args["code"]
	errMsg := args["error"]
	ragContext := s.buildRAGContext(code+" "+errMsg, 3)

	systemMsg := vexExpertPrompt + "\n\n## Reference\n" + ragContext

	userMsg := "Debug this Vex code."
	if errMsg != "" {
		userMsg += " Error: " + errMsg
	}
	userMsg += "\n\n```vex\n" + code + "\n```\n\nIdentify the issue and provide the corrected code."

	return jsonRPCResponse{
		JSONRPC: "2.0", ID: id,
		Result: map[string]any{
			"description": "Debug Vex code",
			"messages": []promptMessage{
				{Role: "system", Content: mcpContent{Type: "text", Text: systemMsg}},
				{Role: "user", Content: mcpContent{Type: "text", Text: userMsg}},
			},
		},
	}
}

// vexExpertPrompt is the comprehensive system prompt for Vex AI interactions
var vexExpertPrompt = `You are an expert Vex programming language assistant with deep knowledge of the language (Vex v0.3.2).

## Vex Language Quick Reference

Vex is a modern systems programming language: "Every Cycle, Every Core, Every Time"
- Rust's safety + Go's simplicity + automatic SIMD vectorization
- Compiles to native via LLVM 21

### CRITICAL SYNTAX (NOT Rust!)
- fn main(): i32 { }           — colon for return type, NOT ->
- let x = 5;                   — immutable by default
- let! y = 10;                 — mutable with !
- Vec.new()                    — dot for member access, NOT ::
- Some(x)                      — NOT Option::Some(x)
- NO :: operator anywhere!

### Type System
Primitives: i8/i16/i32/i64/i128/isize, u8-u128/usize, f32/f64, bool, char, string, str
Generics: Vec<T>, Map<K,V>, OrderedMap<K,V>, Set<T>, Box<T>, Ptr<T>, Span<T>, Channel<T>
SIMD: Tensor<T,N> (static SIMD vector), DynTensor<T> (runtime-sized), Mask<N>, DynMask
Complex: Complex<T>
Option<T> = Some(T) | None
Result<T,E> = Ok(T) | Err(E)

### Functions & Methods (Go-style receivers)
fn add(a: i32, b: i32): i32 { a + b }                    // standalone
fn Point(x: f64, y: f64): Point { Point { x, y } }       // constructor
fn Point.origin(): Point { Point { x: 0.0, y: 0.0 } }    // static method
fn (self: &Point) length(): f64 { ... }                    // immutable method
fn (self: &Point!) translate(dx: f64, dy: f64) { ... }    // mutable method

### Contracts (like traits/interfaces) — support inheritance
contract Display { toString(): string; }
contract Drawable: Display { draw(); }       // inherits Display
struct Point impl Display + Clone { x: f64, y: f64 }
fn (self: &Point) toString(): string { format("({}, {})", self.x, self.y) }

### Operator Overloading
fn (self: &Point) op+(other: Point): Point { ... }

### Concurrency
go { await someTask(); };      // fire-and-forget goroutine
async fn fetch(): string { }   // async function
let ch = Channel.new<i32>(10); // bounded channel

### SIMD (Automatic Vectorization)
let a = [1.0, 2.0, 3.0, 4.0];
let b = [5.0, 6.0, 7.0, 8.0];
let c = a + b;                 // auto-vectorized SIMD
let sum = <+ a;                // reduce sum
let prod = <* a;               // reduce product
let mask = a > b;              // Mask<4> (SIMD boolean)
// Saturating: +| -| *|, Min/Max: <? >?, FMA: *+

### Memory Model (VUMM)
Box<T> — compiler auto-selects: Unique (zero-cost), SharedRc, or AtomicArc
No manual Rc/Arc decisions needed.

### Control Flow
for i in 0..10 { }            // range loop
for item in collection { }    // iterator loop
match value {
    Some(x) if x > 0 => ...,  // guard clause
    Some(_) => ...,            // wildcard
    None => ...
}
if let Some(x) = opt { }      // pattern binding

### Error Handling
fn parse(s: string): Result<i32, string> { Ok(42) }
let val = parse("42")?;       // ? propagation

When answering questions:
- Always use correct Vex syntax (NOT Rust)
- Provide code examples
- Reference specific prelude types and their methods
- Be concise and accurate`

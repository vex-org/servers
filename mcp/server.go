package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/vex-org/servers/rag"
	"github.com/vex-org/servers/sandbox"
)

// Server implements the Model Context Protocol over SSE + HTTP
type Server struct {
	index   *rag.Index
	exec    *sandbox.Executor
	vexRoot string

	// AI function for chat (injected from handlers)
	AIChat func(system, user string) (string, error)

	// SSE clients
	mu      sync.Mutex
	clients map[string]chan []byte
	nextID  atomic.Int64
}

// NewServer creates a new MCP server
func NewServer(index *rag.Index, exec *sandbox.Executor, vexRoot string) *Server {
	return &Server{
		index:   index,
		exec:    exec,
		vexRoot: vexRoot,
		clients: make(map[string]chan []byte),
	}
}

// --- JSON-RPC types ---

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- MCP Protocol Types ---

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type mcpCapabilities struct {
	Tools     *mcpToolsCap    `json:"tools,omitempty"`
	Resources *mcpResourceCap `json:"resources,omitempty"`
	Prompts   *mcpPromptsCap  `json:"prompts,omitempty"`
}

type mcpToolsCap struct {
	ListChanged bool `json:"listChanged,omitempty"`
}
type mcpResourceCap struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}
type mcpPromptsCap struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"` // "text" or "resource"
	Text string `json:"text,omitempty"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// --- SSE Endpoint ---

// HandleSSE serves the MCP SSE transport as a raw net/http handler.
// Use HandleSSEHTTP with Fiber's adaptor or mount on a separate mux.
func (s *Server) HandleSSEHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	clientID := fmt.Sprintf("mcp-%d", s.nextID.Add(1))
	ch := make(chan []byte, 32)

	s.mu.Lock()
	s.clients[clientID] = ch
	s.mu.Unlock()

	defer s.RemoveClient(clientID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Send endpoint event
	endpointURL := "/api/website/mcp/message?sessionId=" + clientID
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", msg)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// HandleMessage processes incoming JSON-RPC messages at POST /mcp/message
func (s *Server) HandleMessage(c fiber.Ctx) error {
	sessionID := fiber.Query[string](c, "sessionId")

	var req jsonRPCRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error"},
		})
	}
	req.JSONRPC = "2.0"

	resp := s.handleMethod(req)

	// If SSE client exists, also push response to SSE channel
	if sessionID != "" {
		s.mu.Lock()
		ch, ok := s.clients[sessionID]
		s.mu.Unlock()
		if ok {
			data, _ := json.Marshal(resp)
			select {
			case ch <- data:
			default: // channel full, skip
			}
		}
	}

	return c.JSON(resp)
}

// HandleStreamable handles the MCP Streamable HTTP transport (POST /mcp)
func (s *Server) HandleStreamable(c fiber.Ctx) error {
	var req jsonRPCRequest
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error"},
		})
	}
	req.JSONRPC = "2.0"

	resp := s.handleMethod(req)
	return c.JSON(resp)
}

// handleMethod dispatches JSON-RPC methods
func (s *Server) handleMethod(req jsonRPCRequest) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourcesRead(req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		return s.handlePromptsGet(req)
	case "ping":
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

// --- Initialization ---

func (s *Server) handleInitialize(req jsonRPCRequest) jsonRPCResponse {
	chunks, terms := s.index.Stats()
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": mcpServerInfo{
				Name:    "vex-lang-mcp",
				Version: "0.3.2",
			},
			"capabilities": mcpCapabilities{
				Tools:     &mcpToolsCap{},
				Resources: &mcpResourceCap{},
				Prompts:   &mcpPromptsCap{},
			},
			"instructions": fmt.Sprintf(
				"Vex Language MCP Server. Knowledge base: %d chunks, %d terms. "+
					"Use vex_search to find docs, vex_run to execute code, vex_explain for AI help.",
				chunks, terms,
			),
		},
	}
}

// Cleanup ---

func (s *Server) RemoveClient(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch, ok := s.clients[id]; ok {
		close(ch)
		delete(s.clients, id)
	}
}

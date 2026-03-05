package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// --- Resource Definitions ---

type resourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

func (s *Server) handleResourcesList(req jsonRPCRequest) jsonRPCResponse {
	resources := []resourceDef{
		{URI: "vex://spec/language", Name: "Language Specification", Description: "Vex language syntax and type system spec", MimeType: "text/markdown"},
		{URI: "vex://spec/abi", Name: "ABI Specification", Description: "Vex ABI and calling convention spec", MimeType: "text/markdown"},
		{URI: "vex://spec/vumm", Name: "VUMM Specification", Description: "Vex Unified Memory Model spec", MimeType: "text/markdown"},
		{URI: "vex://spec/visibility", Name: "Visibility Specification", Description: "Struct field visibility spec", MimeType: "text/markdown"},
		{URI: "vex://prelude/vec", Name: "Vec<T> API", Description: "Dynamic array type", MimeType: "text/x-vex"},
		{URI: "vex://prelude/map", Name: "Map<K,V> API", Description: "Hash map type", MimeType: "text/x-vex"},
		{URI: "vex://prelude/ordered_map", Name: "OrderedMap<K,V> API", Description: "Insertion-ordered hash map", MimeType: "text/x-vex"},
		{URI: "vex://prelude/set", Name: "Set<T>", Description: "Hash set type", MimeType: "text/x-vex"},
		{URI: "vex://prelude/string", Name: "string API", Description: "String type (VexString)", MimeType: "text/x-vex"},
		{URI: "vex://prelude/str", Name: "str API", Description: "Borrowed string view (VexStr)", MimeType: "text/x-vex"},
		{URI: "vex://prelude/ptr", Name: "Ptr<T> API", Description: "Typed pointer wrapper", MimeType: "text/x-vex"},
		{URI: "vex://prelude/span", Name: "Span<T> API", Description: "Bounds-checked array view", MimeType: "text/x-vex"},
		{URI: "vex://prelude/rawbuf", Name: "RawBuf API", Description: "Byte-level memory accessor (internal)", MimeType: "text/x-vex"},
		{URI: "vex://prelude/option", Name: "Option<T>", Description: "Optional value type", MimeType: "text/x-vex"},
		{URI: "vex://prelude/result", Name: "Result<T,E>", Description: "Error handling type", MimeType: "text/x-vex"},
		{URI: "vex://prelude/channel", Name: "Channel<T>", Description: "MPMC channel for concurrency", MimeType: "text/x-vex"},
		{URI: "vex://prelude/box", Name: "Box<T>", Description: "Heap-allocated owner", MimeType: "text/x-vex"},
		{URI: "vex://prelude/complex", Name: "Complex<T>", Description: "Complex number type", MimeType: "text/x-vex"},
		{URI: "vex://prelude/mem", Name: "mem API", Description: "Memory management utilities", MimeType: "text/x-vex"},
		{URI: "vex://prelude/comptime", Name: "Comptime API", Description: "Compile-time introspection types", MimeType: "text/x-vex"},
		{URI: "vex://prelude/ops", Name: "Operator Contracts", Description: "$Add, $Sub, $Mul, $Eq, $Ord, $Index", MimeType: "text/x-vex"},
		{URI: "vex://prelude/contracts", Name: "Builtin Contracts", Description: "$Display, $Debug, $Clone, $Drop, $Hash", MimeType: "text/x-vex"},
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"resources": resources},
	}
}

func (s *Server) handleResourcesRead(req jsonRPCRequest) jsonRPCResponse {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	content, mimeType, err := s.resolveResource(params.URI)
	if err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32602, Message: err.Error()},
		}
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"contents": []map[string]any{
				{"uri": params.URI, "mimeType": mimeType, "text": content},
			},
		},
	}
}

// resolveResource maps vex:// URIs to file content
func (s *Server) resolveResource(uri string) (content string, mimeType string, err error) {
	if !strings.HasPrefix(uri, "vex://") {
		return "", "", fmt.Errorf("unsupported URI scheme: %s", uri)
	}

	path := strings.TrimPrefix(uri, "vex://")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid resource URI: %s", uri)
	}

	category, name := parts[0], parts[1]

	switch category {
	case "spec":
		return s.readSpecFile(name)
	case "prelude":
		return s.readPreludeFile(name)
	case "stdlib":
		return s.readStdlibModule(name)
	default:
		return "", "", fmt.Errorf("unknown resource category: %s", category)
	}
}

func (s *Server) readSpecFile(name string) (string, string, error) {
	fileMap := map[string]string{
		"language":   "LANGUAGE_SPEC.md",
		"abi":        "VEX_ABI_SPEC.md",
		"vumm":       "VUMM_STATIC_SPEC.md",
		"visibility": "STRUCT_VISIBILITY_SPEC.md",
	}
	fileName, ok := fileMap[name]
	if !ok {
		return "", "", fmt.Errorf("unknown spec: %s", name)
	}
	data, err := os.ReadFile(filepath.Join(s.vexRoot, "docs", "specs", fileName))
	if err != nil {
		return "", "", err
	}
	return string(data), "text/markdown", nil
}

func (s *Server) readPreludeFile(name string) (string, string, error) {
	preludeDir := filepath.Join(s.vexRoot, "crates", "vex-compiler", "src", "prelude")

	// Try .vxc first, then .vx
	for _, ext := range []string{".vxc", ".vx"} {
		path := filepath.Join(preludeDir, name+ext)
		data, err := os.ReadFile(path)
		if err == nil {
			return string(data), "text/x-vex", nil
		}
	}
	// Special mappings
	special := map[string]string{
		"contracts": "builtin_contracts.vx",
		"ops":       "ops.vx",
	}
	if fileName, ok := special[name]; ok {
		data, err := os.ReadFile(filepath.Join(preludeDir, fileName))
		if err == nil {
			return string(data), "text/x-vex", nil
		}
	}
	return "", "", fmt.Errorf("prelude type not found: %s", name)
}

func (s *Server) readStdlibModule(name string) (string, string, error) {
	modDir := filepath.Join(s.vexRoot, "lib", "std", name)
	info, err := os.Stat(modDir)
	if err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("stdlib module not found: %s", name)
	}

	// Concatenate all .vx/.vxc files in the module
	var content strings.Builder
	content.WriteString(fmt.Sprintf("// Vex stdlib module: %s\n\n", name))

	err = filepath.Walk(modDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".vx" && ext != ".vxc" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			return nil
		}
		rel, _ := filepath.Rel(modDir, path)
		content.WriteString(fmt.Sprintf("// --- %s ---\n", rel))
		content.Write(data)
		content.WriteString("\n\n")
		return nil
	})
	if err != nil {
		return "", "", err
	}

	return content.String(), "text/x-vex", nil
}

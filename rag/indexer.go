package rag

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Indexer loads and chunks Vex documentation into a searchable index
type Indexer struct {
	vexRoot string
	index   *Index
}

// NewIndexer creates an indexer rooted at the Vex project directory
func NewIndexer(vexRoot string, index *Index) *Indexer {
	return &Indexer{vexRoot: vexRoot, index: index}
}

// IndexAll loads all documentation sources
func (ix *Indexer) IndexAll() error {
	var allChunks []Chunk

	// 1. Specs
	specs, err := ix.indexSpecs()
	if err != nil {
		log.Printf("RAG: specs indexing error: %v", err)
	}
	allChunks = append(allChunks, specs...)

	// 2. Docs (top-level markdown)
	docs, err := ix.indexDocs()
	if err != nil {
		log.Printf("RAG: docs indexing error: %v", err)
	}
	allChunks = append(allChunks, docs...)

	// 3. Prelude types (.vx/.vxc)
	prelude, err := ix.indexPrelude()
	if err != nil {
		log.Printf("RAG: prelude indexing error: %v", err)
	}
	allChunks = append(allChunks, prelude...)

	// 4. Stdlib modules
	stdlib, err := ix.indexStdlib()
	if err != nil {
		log.Printf("RAG: stdlib indexing error: %v", err)
	}
	allChunks = append(allChunks, stdlib...)

	// 5. Examples
	examples, err := ix.indexExamples()
	if err != nil {
		log.Printf("RAG: examples indexing error: %v", err)
	}
	allChunks = append(allChunks, examples...)

	ix.index.AddChunks(allChunks)

	chunks, terms := ix.index.Stats()
	log.Printf("RAG: indexed %d chunks, %d unique terms", chunks, terms)
	return nil
}

// --- Spec files ---

func (ix *Indexer) indexSpecs() ([]Chunk, error) {
	dir := filepath.Join(ix.vexRoot, "docs", "specs")
	return ix.indexMarkdownDir(dir, "spec")
}

func (ix *Indexer) indexDocs() ([]Chunk, error) {
	var chunks []Chunk
	// Top-level docs only (not subdirs — specs already handled)
	entries, err := os.ReadDir(filepath.Join(ix.vexRoot, "docs"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(ix.vexRoot, "docs", e.Name())
		c, err := ix.chunkMarkdown(path, "doc")
		if err != nil {
			continue
		}
		chunks = append(chunks, c...)
	}
	return chunks, nil
}

// --- Prelude types ---

func (ix *Indexer) indexPrelude() ([]Chunk, error) {
	dir := filepath.Join(ix.vexRoot, "crates", "vex-compiler", "src", "prelude")
	return ix.indexVexDir(dir, "prelude")
}

// --- Stdlib modules ---

func (ix *Indexer) indexStdlib() ([]Chunk, error) {
	var chunks []Chunk
	stdDir := filepath.Join(ix.vexRoot, "lib", "std")
	entries, err := os.ReadDir(stdDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		modDir := filepath.Join(stdDir, e.Name())
		c, err := ix.indexVexDir(modDir, "stdlib")
		if err != nil {
			continue
		}
		chunks = append(chunks, c...)
	}
	return chunks, nil
}

// --- Examples ---

func (ix *Indexer) indexExamples() ([]Chunk, error) {
	dir := filepath.Join(ix.vexRoot, "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var chunks []Chunk
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vx") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		content := string(data)
		// Skip very large examples (>500 lines) — chunk them
		lines := strings.Split(content, "\n")
		if len(lines) > 300 {
			// Split into function-based chunks
			c := ix.chunkVexFunctions(path, content, "example")
			chunks = append(chunks, c...)
		} else {
			rel, _ := filepath.Rel(ix.vexRoot, path)
			name := strings.TrimSuffix(e.Name(), ".vx")
			chunks = append(chunks, Chunk{
				ID:       "example/" + name,
				Source:   rel,
				Category: "example",
				Title:    name,
				Content:  content,
				Tags:     extractVexTags(content),
				Lines:    [2]int{1, len(lines)},
			})
		}
	}
	return chunks, nil
}

// --- Chunking strategies ---

// chunkMarkdown splits a markdown file by ## headers into chunks
func (ix *Indexer) chunkMarkdown(path, category string) ([]Chunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	rel, _ := filepath.Rel(ix.vexRoot, path)
	fileName := filepath.Base(path)
	baseName := strings.TrimSuffix(fileName, ".md")

	lines := strings.Split(content, "\n")
	var chunks []Chunk
	var section strings.Builder
	sectionTitle := baseName
	sectionStart := 1

	for i, line := range lines {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ") {
			// Flush previous section
			if section.Len() > 50 {
				chunks = append(chunks, Chunk{
					ID:       fmt.Sprintf("%s/%s:%s", category, baseName, slugify(sectionTitle)),
					Source:   rel,
					Category: category,
					Title:    sectionTitle,
					Content:  section.String(),
					Tags:     extractMdTags(section.String()),
					Lines:    [2]int{sectionStart, i},
				})
			}
			sectionTitle = strings.TrimLeft(line, "# ")
			sectionStart = i + 1
			section.Reset()
		}
		section.WriteString(line)
		section.WriteString("\n")
	}

	// Final section
	if section.Len() > 50 {
		chunks = append(chunks, Chunk{
			ID:       fmt.Sprintf("%s/%s:%s", category, baseName, slugify(sectionTitle)),
			Source:   rel,
			Category: category,
			Title:    sectionTitle,
			Content:  section.String(),
			Tags:     extractMdTags(section.String()),
			Lines:    [2]int{sectionStart, len(lines)},
		})
	}

	return chunks, nil
}

// indexMarkdownDir indexes all .md files in a directory
func (ix *Indexer) indexMarkdownDir(dir, category string) ([]Chunk, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var chunks []Chunk
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		c, err := ix.chunkMarkdown(filepath.Join(dir, e.Name()), category)
		if err != nil {
			continue
		}
		chunks = append(chunks, c...)
	}
	return chunks, nil
}

// indexVexDir indexes all .vx/.vxc files in a dir (recursive)
func (ix *Indexer) indexVexDir(dir, category string) ([]Chunk, error) {
	var chunks []Chunk
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
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
		content := string(data)
		c := ix.chunkVexFunctions(path, content, category)
		chunks = append(chunks, c...)
		return nil
	})
	return chunks, err
}

// chunkVexFunctions splits Vex source into function/struct based chunks
func (ix *Indexer) chunkVexFunctions(path, content, category string) []Chunk {
	rel, _ := filepath.Rel(ix.vexRoot, path)
	fileName := filepath.Base(path)
	baseName := strings.TrimSuffix(strings.TrimSuffix(fileName, ".vx"), ".vxc")

	lines := strings.Split(content, "\n")
	var chunks []Chunk

	var current strings.Builder
	currentTitle := baseName
	currentStart := 1
	braceDepth := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect top-level declarations
		isTopLevel := braceDepth == 0 && (strings.HasPrefix(trimmed, "fn ") ||
			strings.HasPrefix(trimmed, "struct ") ||
			strings.HasPrefix(trimmed, "enum ") ||
			strings.HasPrefix(trimmed, "contract ") ||
			strings.HasPrefix(trimmed, "export fn ") ||
			strings.HasPrefix(trimmed, "export struct ") ||
			strings.HasPrefix(trimmed, "export contract "))

		if isTopLevel && current.Len() > 30 {
			// Flush previous chunk
			chunks = append(chunks, Chunk{
				ID:       fmt.Sprintf("%s/%s:%s", category, baseName, slugify(currentTitle)),
				Source:   rel,
				Category: category,
				Title:    currentTitle,
				Content:  current.String(),
				Tags:     extractVexTags(current.String()),
				Lines:    [2]int{currentStart, i},
			})
			current.Reset()
			currentStart = i + 1
			currentTitle = extractDeclName(trimmed)
		}

		current.WriteString(line)
		current.WriteString("\n")

		braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
		if braceDepth < 0 {
			braceDepth = 0
		}
	}

	// Final chunk
	if current.Len() > 30 {
		chunks = append(chunks, Chunk{
			ID:       fmt.Sprintf("%s/%s:%s", category, baseName, slugify(currentTitle)),
			Source:   rel,
			Category: category,
			Title:    currentTitle,
			Content:  current.String(),
			Tags:     extractVexTags(current.String()),
			Lines:    [2]int{currentStart, len(lines)},
		})
	}

	return chunks
}

// --- Helpers ---

func extractDeclName(line string) string {
	// "fn (self: &Vec<T>) push(value: T)" → "Vec.push"
	// "fn main(): i32" → "main"
	// "struct Point { ... }" → "Point"
	line = strings.TrimPrefix(line, "export ")

	if strings.HasPrefix(line, "fn (") {
		// Method: fn (self: &Type) name(...)
		if idx := strings.Index(line, ")"); idx > 0 {
			receiver := line[4:idx]
			// Extract type from "self: &Point!" or "self: &Vec<T>"
			if colonIdx := strings.Index(receiver, ":"); colonIdx > 0 {
				typePart := strings.TrimSpace(receiver[colonIdx+1:])
				typePart = strings.TrimPrefix(typePart, "&")
				typePart = strings.TrimSuffix(typePart, "!")
				if genIdx := strings.Index(typePart, "<"); genIdx > 0 {
					typePart = typePart[:genIdx]
				}
				// Get method name
				rest := line[idx+1:]
				rest = strings.TrimSpace(rest)
				if parenIdx := strings.Index(rest, "("); parenIdx > 0 {
					methodName := strings.TrimSpace(rest[:parenIdx])
					return typePart + "." + methodName
				}
			}
		}
	}

	if strings.HasPrefix(line, "fn ") {
		rest := line[3:]
		if parenIdx := strings.Index(rest, "("); parenIdx > 0 {
			name := strings.TrimSpace(rest[:parenIdx])
			if genIdx := strings.Index(name, "<"); genIdx > 0 {
				name = name[:genIdx]
			}
			return name
		}
	}

	for _, kw := range []string{"struct ", "enum ", "contract "} {
		if strings.HasPrefix(line, kw) {
			rest := line[len(kw):]
			words := strings.Fields(rest)
			if len(words) > 0 {
				name := words[0]
				if genIdx := strings.Index(name, "<"); genIdx > 0 {
					name = name[:genIdx]
				}
				return strings.TrimRight(name, "{")
			}
		}
	}

	return "unknown"
}

func extractVexTags(content string) []string {
	tagSet := make(map[string]bool)
	keywords := []string{
		"Vec", "Map", "Set", "Ptr", "Span", "Box", "Channel", "Option", "Result",
		"string", "str", "Tensor", "Mask", "Range", "RawBuf",
		"fn", "struct", "enum", "contract", "async", "await", "go",
		"for", "while", "match", "if", "let", "let!",
		"import", "export", "defer", "return",
	}
	lower := strings.ToLower(content)
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			tagSet[strings.ToLower(kw)] = true
		}
	}
	var tags []string
	for t := range tagSet {
		tags = append(tags, t)
	}
	return tags
}

func extractMdTags(content string) []string {
	// Extract code block language refs and Vex keywords
	return extractVexTags(content)
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == '.':
			out.WriteRune('-')
		}
	}
	result := out.String()
	if len(result) > 60 {
		result = result[:60]
	}
	return result
}

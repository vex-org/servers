package rag

// Chunk represents a searchable piece of documentation
type Chunk struct {
	ID       string   // unique id: "prelude/vec.vxc:push"
	Source   string   // file path relative to vex root
	Category string   // "spec", "prelude", "stdlib", "example", "doc"
	Title    string   // section or function name
	Content  string   // actual text content
	Tags     []string // searchable tags: ["vec", "push", "array"]
	Lines    [2]int   // start/end line in source file
}

// SearchResult is a scored chunk from a search query
type SearchResult struct {
	Chunk Chunk
	Score float64
}

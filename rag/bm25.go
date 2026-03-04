package rag

import (
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// BM25 parameters
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// Index is an in-memory BM25 search index over documentation chunks
type Index struct {
	mu     sync.RWMutex
	chunks []Chunk

	// Inverted index: token → list of (chunkIdx, termFreq)
	postings map[string][]posting

	// Per-chunk document length (in tokens)
	docLens []int

	// Average document length
	avgDL float64

	// Total number of documents
	numDocs int
}

type posting struct {
	docIdx int
	freq   int
}

// NewIndex creates an empty index
func NewIndex() *Index {
	return &Index{
		postings: make(map[string][]posting),
	}
}

// AddChunks indexes a batch of chunks
func (idx *Index) AddChunks(chunks []Chunk) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, c := range chunks {
		docIdx := len(idx.chunks)
		idx.chunks = append(idx.chunks, c)

		// Tokenize content + title + tags
		text := strings.ToLower(c.Title + " " + c.Content + " " + strings.Join(c.Tags, " "))
		tokens := tokenize(text)

		idx.docLens = append(idx.docLens, len(tokens))

		// Count term frequencies
		tf := make(map[string]int)
		for _, t := range tokens {
			tf[t]++
		}

		// Update postings
		for term, freq := range tf {
			idx.postings[term] = append(idx.postings[term], posting{
				docIdx: docIdx,
				freq:   freq,
			})
		}
	}

	// Recompute stats
	idx.numDocs = len(idx.chunks)
	total := 0
	for _, dl := range idx.docLens {
		total += dl
	}
	if idx.numDocs > 0 {
		idx.avgDL = float64(total) / float64(idx.numDocs)
	}
}

// Search performs BM25 search and returns top-k results
func (idx *Index) Search(query string, topK int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.numDocs == 0 {
		return nil
	}

	queryTokens := tokenize(strings.ToLower(query))
	if len(queryTokens) == 0 {
		return nil
	}

	// Score each document
	scores := make(map[int]float64)

	for _, qt := range queryTokens {
		posts, ok := idx.postings[qt]
		if !ok {
			continue
		}

		// IDF: log((N - df + 0.5) / (df + 0.5) + 1)
		df := float64(len(posts))
		n := float64(idx.numDocs)
		idf := math.Log((n-df+0.5)/(df+0.5) + 1.0)

		for _, p := range posts {
			dl := float64(idx.docLens[p.docIdx])
			tf := float64(p.freq)

			// BM25 score
			num := tf * (bm25K1 + 1)
			den := tf + bm25K1*(1-bm25B+bm25B*(dl/idx.avgDL))
			scores[p.docIdx] += idf * (num / den)
		}
	}

	// Boost exact matches in title
	queryLower := strings.ToLower(query)
	for docIdx, score := range scores {
		title := strings.ToLower(idx.chunks[docIdx].Title)
		if strings.Contains(title, queryLower) {
			scores[docIdx] = score * 2.0 // title match boost
		}
		// Category boost: specs and prelude rank higher
		switch idx.chunks[docIdx].Category {
		case "prelude":
			scores[docIdx] *= 1.3
		case "spec":
			scores[docIdx] *= 1.2
		}
	}

	// Collect and sort
	type scored struct {
		idx   int
		score float64
	}
	var results []scored
	for docIdx, score := range scores {
		results = append(results, scored{docIdx, score})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if topK > len(results) {
		topK = len(results)
	}

	out := make([]SearchResult, topK)
	for i := 0; i < topK; i++ {
		out[i] = SearchResult{
			Chunk: idx.chunks[results[i].idx],
			Score: results[i].score,
		}
	}
	return out
}

// Stats returns index statistics
func (idx *Index) Stats() (chunks int, terms int) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return idx.numDocs, len(idx.postings)
}

// tokenize splits text into searchable tokens
func tokenize(text string) []string {
	var tokens []string
	// Split on non-alphanumeric characters
	var current strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '#' {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tok := current.String()
				if len(tok) >= 2 { // skip single chars
					tokens = append(tokens, tok)
				}
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tok := current.String()
		if len(tok) >= 2 {
			tokens = append(tokens, tok)
		}
	}

	// Also add camelCase / snake_case splits
	var extra []string
	for _, t := range tokens {
		parts := splitCamelSnake(t)
		for _, p := range parts {
			p = strings.ToLower(p)
			if len(p) >= 2 && p != t {
				extra = append(extra, p)
			}
		}
	}
	return append(tokens, extra...)
}

// splitCamelSnake splits "readAt" → ["read", "at"], "my_func" → ["my", "func"]
func splitCamelSnake(s string) []string {
	// Snake case
	if strings.Contains(s, "_") {
		return strings.Split(s, "_")
	}
	// CamelCase
	var parts []string
	var current strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		}
		current.WriteRune(unicode.ToLower(r))
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

// Package chunker provides utilities for splitting large code chunks into smaller pieces.
package chunker

import (
	"sort"
	"strings"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

// Config controls how chunks are split.
type Config struct {
	// MaxTokens is the maximum token count before splitting.
	// Default: 2000 (~8KB of code)
	MaxTokens int

	// OverlapTokens is how many tokens to overlap between windows.
	// Default: 200 (~10% overlap)
	OverlapTokens int

	// MinTokens is the minimum tokens for a chunk to be worth keeping.
	// Default: 50
	MinTokens int
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		MaxTokens:     2000,
		OverlapTokens: 200,
		MinTokens:     50,
	}
}

// SplitLargeChunks splits chunks that exceed MaxTokens into smaller overlapping windows.
// Chunks below MaxTokens are passed through unchanged.
func SplitLargeChunks(chunks []schema.Chunk, cfg Config) []schema.Chunk {
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultConfig().MaxTokens
	}
	if cfg.OverlapTokens <= 0 {
		cfg.OverlapTokens = DefaultConfig().OverlapTokens
	}
	if cfg.MinTokens <= 0 {
		cfg.MinTokens = DefaultConfig().MinTokens
	}

	var result []schema.Chunk
	for _, chunk := range chunks {
		if chunk.TokenCount <= cfg.MaxTokens {
			result = append(result, chunk)
			continue
		}

		// Split this chunk
		windows := splitChunk(chunk, cfg)
		result = append(result, windows...)
	}
	return result
}

// splitChunk splits a single large chunk into overlapping windows.
func splitChunk(chunk schema.Chunk, cfg Config) []schema.Chunk {
	lines := strings.Split(chunk.Content, "\n")
	if len(lines) == 0 {
		return []schema.Chunk{chunk}
	}

	lineOffsets := make([]int, len(lines)+1)
	offset := 0
	for i, line := range lines {
		lineOffsets[i] = offset
		offset += len(line)
		if i < len(lines)-1 {
			offset++
		}
	}
	lineOffsets[len(lines)] = len(chunk.Content)

	// Estimate tokens per line (rough: ~4 chars per token)
	tokensPerLine := make([]int, len(lines))
	for i, line := range lines {
		tokensPerLine[i] = estimateLineTokens(line)
	}

	var windows []schema.Chunk
	windowNum := 0
	startLine := 0
	prevStartLine := -1

	for startLine < len(lines) {
		// Safety check: prevent infinite loop
		if startLine == prevStartLine {
			// Force progress
			startLine++
			if startLine >= len(lines) {
				break
			}
		}
		prevStartLine = startLine

		// Find end of this window
		endLine := startLine
		tokenCount := 0
		for endLine < len(lines) && tokenCount < cfg.MaxTokens {
			tokenCount += tokensPerLine[endLine]
			endLine++
		}

		// Ensure we process at least one line
		if endLine == startLine {
			endLine = startLine + 1
			if endLine > len(lines) {
				endLine = len(lines)
			}
		}

		// Re-slice the original content to avoid rebuilding strings line-by-line.
		windowContent := chunk.Content[lineOffsets[startLine]:lineOffsets[endLine]]

		if tokenCount >= cfg.MinTokens || len(windows) == 0 {
			window := schema.Chunk{
				ID:        chunk.ID + "_w" + itoa(windowNum),
				RepoID:    chunk.RepoID,
				FilePath:  chunk.FilePath,
				Language:  chunk.Language,
				ChunkType: chunk.ChunkType + "_window",

				GitCommit: chunk.GitCommit,
				GitBlame:  chunk.GitBlame,

				StartLine:   chunk.StartLine + startLine,
				EndLine:     chunk.StartLine + endLine - 1,
				StartColumn: 0,
				EndColumn:   len(lines[endLine-1]),

				Name:       chunk.Name,
				Signature:  chunk.Signature,
				Docstring:  "", // Only keep docstring in first window
				ParentName: chunk.ParentName,
				ParentType: chunk.ParentType,
				Imports:    nil, // Only keep imports in first window
				Calls:      extractCallsFromContent(windowContent),
				Defs:       nil,

				TokenCount:  tokenCount,
				IndexedAt:   chunk.IndexedAt,
				SchemaVer:   schema.Version,
				ContentHash: schema.ContentHash(windowContent),
				Content:     windowContent,
			}

			// First window keeps docstring and imports
			if windowNum == 0 {
				window.Docstring = chunk.Docstring
				window.Imports = chunk.Imports
			}

			windows = append(windows, window)
			windowNum++
		}

		// If we reached the end, stop
		if endLine >= len(lines) {
			break
		}

		// Move start forward, accounting for overlap
		overlapLines := 0
		overlapTokens := 0
		for i := endLine - 1; i > startLine && overlapTokens < cfg.OverlapTokens; i-- {
			overlapTokens += tokensPerLine[i]
			overlapLines++
		}

		newStartLine := endLine - overlapLines
		// Ensure forward progress
		if newStartLine <= startLine {
			newStartLine = startLine + 1
		}
		startLine = newStartLine
	}

	if len(windows) == 0 {
		return []schema.Chunk{chunk}
	}

	return windows
}

// estimateLineTokens roughly estimates token count for a line.
// Uses ~4 characters per token as a rough average.
func estimateLineTokens(line string) int {
	// Count non-whitespace runs as tokens, plus some overhead
	tokens := 0
	inWord := false
	for _, r := range line {
		if r == ' ' || r == '\t' {
			if inWord {
				tokens++
				inWord = false
			}
		} else {
			inWord = true
		}
	}
	if inWord {
		tokens++
	}

	// Add some overhead for punctuation and operators
	for _, r := range line {
		switch r {
		case '(', ')', '{', '}', '[', ']', ',', ';', ':', '.', '=', '+', '-', '*', '/', '<', '>', '!', '&', '|':
			tokens++
		}
	}

	if tokens == 0 {
		return 1 // Empty lines still count
	}
	return tokens
}

// extractCallsFromContent extracts function calls from content.
// This is a best-effort extraction for windowed chunks.
func extractCallsFromContent(content string) []string {
	// Simple heuristic: find identifiers followed by (
	var calls []string
	seen := make(map[string]bool)

	i := 0
	for i < len(content) {
		// Skip to potential identifier start
		for i < len(content) && !isIdentStart(content[i]) {
			i++
		}
		if i >= len(content) {
			break
		}

		// Read identifier
		start := i
		for i < len(content) && isIdentChar(content[i]) {
			i++
		}
		ident := content[start:i]

		// Skip whitespace
		for i < len(content) && (content[i] == ' ' || content[i] == '\t' || content[i] == '\n') {
			i++
		}

		// Check for (
		if i < len(content) && content[i] == '(' {
			// Skip common keywords
			if !isKeyword(ident) && !seen[ident] {
				calls = append(calls, ident)
				seen[ident] = true
			}
		}
	}

	return calls
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "do", "switch", "case", "break", "continue",
		"return", "try", "catch", "finally", "throw", "new", "delete", "typeof",
		"instanceof", "in", "of", "with", "yield", "await", "async", "class",
		"extends", "import", "export", "from", "as", "default", "const", "let",
		"var", "function", "func", "def", "fn", "pub", "priv", "static", "final",
		"abstract", "interface", "implements", "package", "range", "select", "go",
		"defer", "chan", "map", "struct", "type", "enum", "match", "loop", "impl",
		// Common type names that aren't useful for search
		"error", "string", "int", "bool", "float", "byte", "rune", "nil", "null",
		"true", "false", "void", "this", "self", "super", "None", "True", "False":
		return true
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ExtractIdentifiers extracts unique identifiers from code content for hybrid search.
// Returns a deduplicated, sorted list of identifiers (min 2 chars, max 100 identifiers).
func ExtractIdentifiers(content string) []string {
	seen := make(map[string]bool)
	var identifiers []string

	i := 0
	for i < len(content) {
		// Skip to potential identifier start
		for i < len(content) && !isIdentStart(content[i]) {
			i++
		}
		if i >= len(content) {
			break
		}

		// Read identifier
		start := i
		for i < len(content) && isIdentChar(content[i]) {
			i++
		}
		ident := content[start:i]

		// Filter: min 2 chars, not a keyword, not seen before
		if len(ident) >= 2 && !isKeyword(ident) && !seen[ident] {
			seen[ident] = true
			identifiers = append(identifiers, ident)
		}
	}

	// Sort for consistency
	sort.Strings(identifiers)

	// Limit to 100 identifiers (most common/useful ones come first alphabetically)
	if len(identifiers) > 100 {
		identifiers = identifiers[:100]
	}

	return identifiers
}

// EnrichChunkIdentifiers adds extracted identifiers to a chunk.
func EnrichChunkIdentifiers(chunk *schema.Chunk) {
	if chunk.Content == "" {
		return
	}
	chunk.Identifiers = ExtractIdentifiers(chunk.Content)
}

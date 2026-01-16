//go:build !cgo

package rsindex

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

type Indexer struct{}

func New() *Indexer { return &Indexer{} }

func (i *Indexer) Language() string { return "rust" }

func (i *Indexer) Extensions() []string { return []string{".rs"} }

func (i *Indexer) IndexFile(ctx context.Context, absRoot, absPath, repoID string) ([]schema.Chunk, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	srcBytes, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	src := string(srcBytes)
	fileHash := schema.ContentHash(src)

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	lines := strings.Split(src, "\n")

	imports, moduleContent, moduleEndLine := rustModuleImports(lines)
	moduleDoc := rustModuleDoc(lines)

	chunks := []schema.Chunk{
		makeModuleChunk(repoID, rel, "rust", moduleDoc, imports, moduleContent, fileHash, moduleEndLine),
	}

	defs := rustTopLevelDefs(lines)
	for _, def := range defs {
		end := rustFindBlockEnd(lines, def.startLine)
		content := strings.TrimSpace(strings.Join(lines[def.startLine-1:end], "\n"))
		doc := rustDocComment(lines, def.startLine-1)
		calls := rustExtractCalls(content)

		ch := schema.Chunk{
			RepoID:      repoID,
			FilePath:    rel,
			Language:    "rust",
			ChunkType:   def.kind,
			Name:        def.name,
			Signature:   def.signature,
			Docstring:   doc,
			Imports:     imports,
			Calls:       calls,
			Defs:        nonEmptyDefs(def.name),
			StartLine:   def.startLine,
			EndLine:     end,
			StartColumn: 0,
			EndColumn:   0,
			TokenCount:  len(content) / 4,
			IndexedAt:   time.Now(),
			SchemaVer:   schema.Version,
			ContentHash: schema.ContentHash(content),
			Content:     content,
		}
		ch.ID = schema.ChunkID(repoID, rel, ch.StartLine, ch.EndLine, ch.ContentHash)
		chunks = append(chunks, ch)
	}

	sort.Slice(chunks, func(a, b int) bool {
		if chunks[a].StartLine == chunks[b].StartLine {
			return chunks[a].Name < chunks[b].Name
		}
		return chunks[a].StartLine < chunks[b].StartLine
	})

	return chunks, nil
}

func makeModuleChunk(repoID, filePath, lang, doc string, imports []string, content string, fileHash string, endLine int) schema.Chunk {
	content = strings.TrimSpace(content)
	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    filePath,
		Language:    lang,
		ChunkType:   "module",
		StartLine:   1,
		EndLine:     maxInt(1, endLine),
		StartColumn: 0,
		EndColumn:   0,
		Docstring:   doc,
		Imports:     imports,
		TokenCount:  len(content) / 4,
		IndexedAt:   time.Now(),
		SchemaVer:   schema.Version,
		ContentHash: fileHash,
		Content:     content,
	}
	ch.ID = schema.ChunkID(repoID, filePath, ch.StartLine, ch.EndLine, ch.ContentHash)
	return ch
}

type rsDef struct {
	kind      string
	name      string
	signature string
	startLine int
}

var (
	reRsFn    = regexp.MustCompile(`^(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	reRsType  = regexp.MustCompile(`^(?:pub\s+)?(struct|enum|trait)\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	reRsCall  = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_:]*)\s*\(`)
	reRsUse   = regexp.MustCompile(`^use\s+([^;]+);`)
	reRsDocLn = regexp.MustCompile(`^\s*///\s?`)
)

func rustTopLevelDefs(lines []string) []rsDef {
	var out []rsDef
	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			continue
		}
		if m := reRsFn.FindStringSubmatch(line); len(m) == 2 {
			out = append(out, rsDef{kind: "function", name: m[1], signature: line, startLine: i + 1})
			continue
		}
		if m := reRsType.FindStringSubmatch(line); len(m) == 3 {
			out = append(out, rsDef{kind: m[1], name: m[2], signature: line, startLine: i + 1})
			continue
		}
	}
	return out
}

func rustFindBlockEnd(lines []string, startLine int) int {
	if startLine < 1 {
		return 1
	}
	depth := 0
	seenOpen := false
	for i := startLine; i <= len(lines); i++ {
		line := lines[i-1]
		for _, ch := range line {
			switch ch {
			case '{':
				depth++
				seenOpen = true
			case '}':
				if depth > 0 {
					depth--
				}
			}
		}
		if seenOpen && depth == 0 {
			return i
		}
		// Handle single-line declarations ending with ';'
		if !seenOpen && strings.Contains(line, ";") {
			return i
		}
	}
	return len(lines)
}

func rustModuleImports(lines []string) ([]string, string, int) {
	seen := map[string]bool{}
	var imports []string
	var importLines []string
	endLine := 1

	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			continue
		}
		if m := reRsUse.FindStringSubmatch(line); len(m) == 2 {
			importLines = append(importLines, "use "+strings.TrimSpace(m[1])+";")
			endLine = i + 1
			val := strings.TrimSpace(m[1])
			if val != "" && !seen[val] {
				seen[val] = true
				imports = append(imports, val)
			}
		}
	}

	sort.Strings(imports)
	return imports, strings.Join(importLines, "\n"), endLine
}

func rustModuleDoc(lines []string) string {
	var parts []string
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if strings.HasPrefix(line, "//!") {
			parts = append(parts, strings.TrimSpace(strings.TrimPrefix(line, "//!")))
			continue
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func rustDocComment(lines []string, beforeLine int) string {
	var parts []string
	for i := beforeLine; i >= 0 && i < len(lines); i-- {
		line := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
		if !strings.HasPrefix(line, "///") {
			break
		}
		parts = append(parts, strings.TrimSpace(reRsDocLn.ReplaceAllString(line, "")))
	}
	// reverse
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func rustExtractCalls(content string) []string {
	seen := map[string]bool{}
	var calls []string
	for _, m := range reRsCall.FindAllStringSubmatch(content, -1) {
		if len(m) != 2 {
			continue
		}
		name := m[1]
		switch name {
		case "if", "for", "while", "match", "loop":
			continue
		}
		if !seen[name] {
			seen[name] = true
			calls = append(calls, name)
		}
	}
	sort.Strings(calls)
	return calls
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nonEmptyDefs(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	return []string{name}
}

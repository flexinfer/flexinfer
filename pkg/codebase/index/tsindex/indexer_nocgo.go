//go:build !cgo

package tsindex

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

type Indexer struct {
	language string
}

func NewTypeScript() *Indexer { return &Indexer{language: "typescript"} }
func NewJavaScript() *Indexer { return &Indexer{language: "javascript"} }

func (i *Indexer) Language() string { return i.language }

func (i *Indexer) Extensions() []string {
	if i.language == "typescript" {
		return []string{".ts", ".tsx"}
	}
	return []string{".js", ".jsx", ".mjs", ".cjs"}
}

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

	imports, moduleContent, moduleEndLine := tsModuleImports(lines)
	chunks := []schema.Chunk{
		makeModuleChunk(repoID, rel, i.language, imports, moduleContent, fileHash, moduleEndLine),
	}

	defs := tsTopLevelDefs(lines)
	for idx := range defs {
		def := defs[idx]
		end := len(lines)
		if idx+1 < len(defs) {
			end = defs[idx+1].startLine - 1
		}
		end = clampLine(end, def.startLine, len(lines))

		content := strings.TrimSpace(strings.Join(lines[def.startLine-1:end], "\n"))
		doc := tsLeadingBlockComment(lines, def.startLine-1)
		calls := tsExtractCalls(content)

		ch := schema.Chunk{
			RepoID:      repoID,
			FilePath:    rel,
			Language:    i.language,
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

func makeModuleChunk(repoID, filePath, lang string, imports []string, content string, fileHash string, endLine int) schema.Chunk {
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

type tsDef struct {
	kind      string
	name      string
	signature string
	startLine int
}

var (
	reTSFunc   = regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	reTSClass  = regexp.MustCompile(`^(?:export\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	reTSVar    = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`)
	reTSImport = regexp.MustCompile(`^\s*(?:import|export)\b`)
	reTSFrom   = regexp.MustCompile(`from\s+['"]([^'"]+)['"]`)
	reTSQuote  = regexp.MustCompile(`['"]([^'"]+)['"]`)
	reTSCall   = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$\.]+)\s*\(`)
)

func tsTopLevelDefs(lines []string) []tsDef {
	var out []tsDef
	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			continue
		}

		if m := reTSFunc.FindStringSubmatch(line); len(m) == 2 {
			out = append(out, tsDef{kind: "function", name: m[1], signature: line, startLine: i + 1})
			continue
		}
		if m := reTSClass.FindStringSubmatch(line); len(m) == 2 {
			out = append(out, tsDef{kind: "class", name: m[1], signature: line, startLine: i + 1})
			continue
		}
		if m := reTSVar.FindStringSubmatch(line); len(m) == 2 {
			kind := "variable"
			if strings.Contains(line, "=>") {
				kind = "function"
			}
			out = append(out, tsDef{kind: kind, name: m[1], signature: line, startLine: i + 1})
			continue
		}
	}
	return out
}

func tsModuleImports(lines []string) ([]string, string, int) {
	seen := map[string]bool{}
	var imports []string
	var importLines []string
	endLine := 1

	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			continue
		}
		if !reTSImport.MatchString(line) {
			continue
		}
		if strings.HasPrefix(line, "export ") && !strings.Contains(line, " from ") && !strings.HasPrefix(line, "export * from") {
			// Likely a declaration, not an import-export.
			continue
		}

		importLines = append(importLines, line)
		endLine = i + 1

		if m := reTSFrom.FindStringSubmatch(line); len(m) == 2 {
			mod := m[1]
			if mod != "" && !seen[mod] {
				seen[mod] = true
				imports = append(imports, mod)
			}
			continue
		}
		if m := reTSQuote.FindStringSubmatch(line); len(m) == 2 {
			mod := m[1]
			if mod != "" && !seen[mod] {
				seen[mod] = true
				imports = append(imports, mod)
			}
		}
	}

	sort.Strings(imports)
	return imports, strings.Join(importLines, "\n"), endLine
}

func tsLeadingBlockComment(lines []string, beforeLine int) string {
	// Best-effort: collect a /** ... */ immediately before the definition.
	for i := beforeLine - 1; i >= 0 && i < len(lines); i-- {
		line := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, "*/") {
			var parts []string
			for j := i; j >= 0; j-- {
				l := strings.TrimSpace(strings.TrimRight(lines[j], "\r"))
				parts = append(parts, strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(l, "*"), "/**")))
				if strings.Contains(l, "/**") {
					break
				}
			}
			// reverse
			for a, b := 0, len(parts)-1; a < b; a, b = a+1, b-1 {
				parts[a], parts[b] = parts[b], parts[a]
			}
			txt := strings.Join(parts, "\n")
			txt = strings.ReplaceAll(txt, "*/", "")
			return strings.TrimSpace(txt)
		}
		if strings.HasPrefix(line, "//") {
			return strings.TrimSpace(strings.TrimPrefix(line, "//"))
		}
		break
	}
	return ""
}

func tsExtractCalls(content string) []string {
	seen := map[string]bool{}
	var calls []string
	for _, m := range reTSCall.FindAllStringSubmatch(content, -1) {
		if len(m) != 2 {
			continue
		}
		name := m[1]
		switch name {
		case "if", "for", "while", "switch", "catch", "function":
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

func clampLine(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
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

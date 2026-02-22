//go:build !cgo

package pyindex

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

func (i *Indexer) Language() string { return "python" }

func (i *Indexer) Extensions() []string { return []string{".py", ".pyi"} }

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

	return i.IndexFileFromContent(ctx, absRoot, absPath, repoID, srcBytes)
}

func (i *Indexer) IndexFileFromContent(ctx context.Context, absRoot, absPath, repoID string, content []byte) ([]schema.Chunk, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	src := string(content)
	fileHash := schema.ContentHash(src)

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	lines := strings.Split(src, "\n")

	imports, moduleContent, moduleEndLine := pythonModuleImports(lines)
	moduleDoc := pythonModuleDocstring(lines)

	chunks := []schema.Chunk{
		makeModuleChunk(repoID, rel, "python", moduleDoc, imports, moduleContent, fileHash, moduleEndLine),
	}

	top := pythonTopLevelDefs(lines)
	for idx := range top {
		def := top[idx]
		end := len(lines)
		if idx+1 < len(top) {
			end = top[idx+1].startLine - 1
		}
		end = clampLine(end, 1, len(lines))

		content := strings.TrimSpace(strings.Join(lines[def.startLine-1:end], "\n"))
		doc := pythonBlockDocstring(lines, def.startLine, end, def.indent+4)
		calls := pythonExtractCalls(content)

		chunkType := def.kind
		ch := schema.Chunk{
			RepoID:      repoID,
			FilePath:    rel,
			Language:    "python",
			ChunkType:   chunkType,
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

		if def.kind != "class" {
			continue
		}

		// Best-effort method indexing inside the class block.
		methods := pythonClassMethods(lines, def.startLine, end, def.indent)
		for mIdx := range methods {
			m := methods[mIdx]
			mEnd := end
			if mIdx+1 < len(methods) {
				mEnd = methods[mIdx+1].startLine - 1
			}
			mEnd = clampLine(mEnd, m.startLine, end)

			mContent := strings.TrimSpace(strings.Join(lines[m.startLine-1:mEnd], "\n"))
			mDoc := pythonBlockDocstring(lines, m.startLine, mEnd, m.indent+4)
			mCalls := pythonExtractCalls(mContent)

			ch := schema.Chunk{
				RepoID:      repoID,
				FilePath:    rel,
				Language:    "python",
				ChunkType:   "method",
				Name:        m.name,
				Signature:   m.signature,
				Docstring:   mDoc,
				ParentName:  def.name,
				ParentType:  "class",
				Imports:     imports,
				Calls:       mCalls,
				Defs:        nonEmptyDefs(m.name),
				StartLine:   m.startLine,
				EndLine:     mEnd,
				StartColumn: m.indent,
				EndColumn:   0,
				TokenCount:  len(mContent) / 4,
				IndexedAt:   time.Now(),
				SchemaVer:   schema.Version,
				ContentHash: schema.ContentHash(mContent),
				Content:     mContent,
			}
			ch.ID = schema.ChunkID(repoID, rel, ch.StartLine, ch.EndLine, ch.ContentHash)
			chunks = append(chunks, ch)
		}
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

type pyDef struct {
	kind      string
	name      string
	signature string
	startLine int
	indent    int
}

var (
	rePyDef   = regexp.MustCompile(`^def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	rePyClass = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
)

func pythonTopLevelDefs(lines []string) []pyDef {
	var out []pyDef
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "def ") {
			if m := rePyDef.FindStringSubmatch(trimmed); len(m) == 2 {
				out = append(out, pyDef{kind: "function", name: m[1], signature: strings.TrimSpace(trimmed), startLine: i + 1, indent: 0})
			}
			continue
		}
		if strings.HasPrefix(trimmed, "class ") {
			if m := rePyClass.FindStringSubmatch(trimmed); len(m) == 2 {
				out = append(out, pyDef{kind: "class", name: m[1], signature: strings.TrimSpace(trimmed), startLine: i + 1, indent: 0})
			}
			continue
		}
	}
	return out
}

func pythonClassMethods(lines []string, startLine, endLine int, classIndent int) []pyDef {
	var out []pyDef
	for i := startLine; i <= endLine && i <= len(lines); i++ {
		line := strings.TrimRight(lines[i-1], "\r")
		indent := leadingSpaces(line)
		if indent <= classIndent {
			continue
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if indent != classIndent+4 {
			continue
		}
		body := strings.TrimSpace(line)
		if !strings.HasPrefix(body, "def ") {
			continue
		}
		if m := rePyDef.FindStringSubmatch(body); len(m) == 2 {
			out = append(out, pyDef{kind: "method", name: m[1], signature: body, startLine: i, indent: indent})
		}
	}
	return out
}

func pythonModuleImports(lines []string) ([]string, string, int) {
	seen := map[string]bool{}
	var imports []string
	var importLines []string
	endLine := 1

	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if leadingSpaces(line) != 0 {
			continue
		}

		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "import "):
			importLines = append(importLines, trim)
			endLine = i + 1
			rest := strings.TrimSpace(strings.TrimPrefix(trim, "import "))
			for _, part := range strings.Split(rest, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if before, _, ok := strings.Cut(part, " as "); ok {
					part = strings.TrimSpace(before)
				}
				if part != "" && !seen[part] {
					seen[part] = true
					imports = append(imports, part)
				}
			}
		case strings.HasPrefix(trim, "from "):
			if !strings.Contains(trim, " import ") {
				continue
			}
			importLines = append(importLines, trim)
			endLine = i + 1
			rest := strings.TrimSpace(strings.TrimPrefix(trim, "from "))
			mod, after, ok := strings.Cut(rest, " import ")
			if !ok {
				continue
			}
			mod = strings.TrimSpace(mod)
			if mod == "" {
				continue
			}
			for _, part := range strings.Split(after, ",") {
				part = strings.TrimSpace(part)
				if part == "" || part == "*" {
					continue
				}
				if before, _, ok := strings.Cut(part, " as "); ok {
					part = strings.TrimSpace(before)
				}
				full := mod + "." + part
				if !seen[full] {
					seen[full] = true
					imports = append(imports, full)
				}
			}
		}
	}

	sort.Strings(imports)
	return imports, strings.Join(importLines, "\n"), endLine
}

func pythonModuleDocstring(lines []string) string {
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(strings.TrimRight(lines[i], "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return pythonMaybeTripleQuoted(line, lines[i+1:])
	}
	return ""
}

func pythonBlockDocstring(lines []string, startLine, endLine int, wantIndent int) string {
	for i := startLine; i <= endLine && i <= len(lines); i++ {
		raw := strings.TrimRight(lines[i-1], "\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if leadingSpaces(raw) < wantIndent {
			continue
		}
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") {
			continue
		}
		return pythonMaybeTripleQuoted(line, lines[i:])
	}
	return ""
}

func pythonMaybeTripleQuoted(firstLine string, following []string) string {
	for _, quote := range []string{`"""`, `'''`} {
		if strings.HasPrefix(firstLine, quote) {
			rest := strings.TrimPrefix(firstLine, quote)
			if idx := strings.Index(rest, quote); idx >= 0 {
				return strings.TrimSpace(rest[:idx])
			}
			var parts []string
			parts = append(parts, rest)
			for _, line := range following {
				if idx := strings.Index(line, quote); idx >= 0 {
					parts = append(parts, line[:idx])
					break
				}
				parts = append(parts, line)
			}
			return strings.TrimSpace(strings.Join(parts, "\n"))
		}
	}
	return ""
}

var rePyCall = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_\.]*)\s*\(`)

func pythonExtractCalls(content string) []string {
	seen := map[string]bool{}
	var calls []string
	for _, m := range rePyCall.FindAllStringSubmatch(content, -1) {
		if len(m) != 2 {
			continue
		}
		name := m[1]
		switch name {
		case "if", "for", "while", "def", "class", "return", "import", "from", "with":
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

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
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

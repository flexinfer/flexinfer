package rsindex

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	rs "github.com/smacker/go-tree-sitter/rust"

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

	src, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(rs.GetLanguage())
	tree, parseErr := parser.ParseCtx(ctx, nil, src)
	if parseErr != nil {
		return nil, parseErr
	}
	root := tree.RootNode()

	imports := extractImports(root, src)

	var chunks []schema.Chunk
	if mod, ok := extractModuleChunk(repoID, rel, imports, root, src); ok {
		chunks = append(chunks, mod)
	}
	// Only consider top-level items; avoid recursively walking the full tree
	// (prevents impl methods being double-counted as free functions).
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "function_item":
			name := nodeName(n, src)
			if name == "" {
				continue
			}
			chunks = append(chunks, makeChunk(repoID, rel, "function", name, rustSignature(n, src), leadingRustDocComment(n, src), "", imports, extractCalls(n, src), src, n, false))
		case "struct_item", "enum_item", "trait_item":
			name := nodeName(n, src)
			if name == "" {
				continue
			}
			chunks = append(chunks, makeChunk(repoID, rel, "class", name, rustSignature(n, src), leadingRustDocComment(n, src), "", imports, nil, src, n, false))
		case "impl_item":
			implType := implTargetType(n, src)
			declList := findChildOfType(n, "declaration_list")
			if declList == nil {
				continue
			}
			for j := 0; j < int(declList.ChildCount()); j++ {
				m := declList.Child(j)
				if m == nil || m.Type() != "function_item" {
					continue
				}
				name := nodeName(m, src)
				if name == "" {
					continue
				}
				chunks = append(chunks, makeChunk(repoID, rel, "method", name, rustSignature(m, src), leadingRustDocComment(m, src), implType, imports, extractCalls(m, src), src, m, false))
			}
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

func extractModuleChunk(repoID, filePath string, imports []string, root *sitter.Node, src []byte) (schema.Chunk, bool) {
	doc := crateDocstring(root, src)

	var lines []string
	endLine := 1
	for i := 0; i < int(root.ChildCount()); i++ {
		ch := root.Child(i)
		if ch == nil || ch.Type() != "use_declaration" {
			continue
		}
		txt := strings.TrimSpace(ch.Content(src))
		if txt != "" {
			lines = append(lines, txt)
			endLine = int(ch.EndPoint().Row) + 1
		}
	}

	content := strings.TrimSpace(strings.Join(lines, "\n"))
	fileHash := schema.ContentHash(string(src))
	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    filePath,
		Language:    "rust",
		ChunkType:   "module",
		StartLine:   1,
		EndLine:     endLine,
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
	return ch, true
}

func extractImports(root *sitter.Node, src []byte) []string {
	seen := map[string]bool{}
	var imports []string
	for i := 0; i < int(root.ChildCount()); i++ {
		ch := root.Child(i)
		if ch == nil || ch.Type() != "use_declaration" {
			continue
		}
		txt := strings.TrimSpace(ch.Content(src))
		if txt == "" || seen[txt] {
			continue
		}
		seen[txt] = true
		imports = append(imports, txt)
	}
	sort.Strings(imports)
	return imports
}

func crateDocstring(root *sitter.Node, src []byte) string {
	if root == nil {
		return ""
	}
	var lines []string
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "line_comment", "block_comment":
			txt := strings.TrimSpace(n.Content(src))
			if strings.HasPrefix(txt, "//!") {
				lines = append(lines, strings.TrimSpace(strings.TrimPrefix(txt, "//!")))
				continue
			}
			if len(lines) > 0 {
				return strings.TrimSpace(strings.Join(lines, "\n"))
			}
		default:
			if len(lines) > 0 {
				return strings.TrimSpace(strings.Join(lines, "\n"))
			}
			return ""
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func extractCalls(node *sitter.Node, src []byte) []string {
	seen := map[string]bool{}
	var calls []string
	walk(node, func(n *sitter.Node) {
		if n.Type() != "call_expression" {
			return
		}
		fn := n.ChildByFieldName("function")
		if fn == nil {
			return
		}
		name := strings.TrimSpace(fn.Content(src))
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		calls = append(calls, name)
	})
	sort.Strings(calls)
	return calls
}

func makeChunk(
	repoID string,
	filePath string,
	chunkType string,
	name string,
	signature string,
	docstring string,
	parentName string,
	imports []string,
	calls []string,
	src []byte,
	node *sitter.Node,
	useFileHash bool,
) schema.Chunk {
	content := strings.TrimSpace(node.Content(src))
	hash := schema.ContentHash(content)
	if useFileHash {
		hash = schema.ContentHash(string(src))
	}
	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    filePath,
		Language:    "rust",
		ChunkType:   chunkType,
		Name:        name,
		Signature:   signature,
		Docstring:   docstring,
		ParentName:  parentName,
		ParentType:  ternary(parentName != "", "class", ""),
		Imports:     imports,
		Calls:       calls,
		Defs:        nonEmptyDefs(name),
		StartLine:   int(node.StartPoint().Row) + 1,
		EndLine:     int(node.EndPoint().Row) + 1,
		StartColumn: int(node.StartPoint().Column),
		EndColumn:   int(node.EndPoint().Column),
		TokenCount:  len(content) / 4,
		IndexedAt:   time.Now(),
		SchemaVer:   schema.Version,
		ContentHash: hash,
		Content:     content,
	}
	ch.ID = schema.ChunkID(repoID, filePath, ch.StartLine, ch.EndLine, ch.ContentHash)
	return ch
}

func rustSignature(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	start := int(node.StartByte())
	end := int(node.EndByte())
	body := node.ChildByFieldName("body")
	if body != nil {
		if b := int(body.StartByte()); b > start {
			end = b
		}
	}
	if start < 0 || end < 0 || end <= start || end > len(src) {
		return ""
	}
	s := strings.TrimSpace(string(src[start:end]))
	s = strings.TrimSpace(strings.TrimSuffix(s, "{"))
	return s
}

func leadingRustDocComment(node *sitter.Node, src []byte) string {
	var parts []string
	for cur := node.PrevSibling(); cur != nil; cur = cur.PrevSibling() {
		switch cur.Type() {
		case "line_comment", "block_comment":
			txt := strings.TrimSpace(cur.Content(src))
			if strings.HasPrefix(txt, "///") {
				parts = append(parts, strings.TrimSpace(strings.TrimPrefix(txt, "///")))
				continue
			}
			if strings.HasPrefix(txt, "/**") {
				parts = append(parts, normalizeBlockDoc(txt))
				continue
			}
			// Non-doc comment breaks the chain.
			return strings.TrimSpace(strings.Join(reverse(parts), "\n"))
		default:
			return strings.TrimSpace(strings.Join(reverse(parts), "\n"))
		}
	}
	return strings.TrimSpace(strings.Join(reverse(parts), "\n"))
}

func normalizeBlockDoc(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/**")
	s = strings.TrimSuffix(s, "*/")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "*"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func reverse(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := make([]string, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		out = append(out, in[i])
	}
	return out
}

func implTargetType(node *sitter.Node, src []byte) string {
	t := node.ChildByFieldName("type")
	if t == nil {
		return ""
	}
	return strings.TrimSpace(t.Content(src))
}

func nodeName(node *sitter.Node, src []byte) string {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return ""
	}
	return strings.TrimSpace(nameNode.Content(src))
}

func findChildOfType(node *sitter.Node, typ string) *sitter.Node {
	for i := 0; i < int(node.ChildCount()); i++ {
		ch := node.Child(i)
		if ch != nil && ch.Type() == typ {
			return ch
		}
	}
	return nil
}

func walk(node *sitter.Node, fn func(*sitter.Node)) {
	if node == nil {
		return
	}
	fn(node)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			walk(child, fn)
		}
	}
}

func nonEmptyDefs(name string) []string {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	return []string{name}
}

func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

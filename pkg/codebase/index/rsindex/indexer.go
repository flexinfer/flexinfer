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
			chunks = append(chunks, makeChunk(repoID, rel, "function", name, "", "", "", imports, extractCalls(n, src), src, n))
		case "struct_item", "enum_item", "trait_item":
			name := nodeName(n, src)
			if name == "" {
				continue
			}
			chunks = append(chunks, makeChunk(repoID, rel, "class", name, "", "", "", imports, nil, src, n))
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
				chunks = append(chunks, makeChunk(repoID, rel, "method", name, "", "", implType, imports, extractCalls(m, src), src, m))
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
) schema.Chunk {
	content := strings.TrimSpace(node.Content(src))
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
		ContentHash: schema.ContentHash(content),
		Content:     content,
	}
	ch.ID = schema.ChunkID(repoID, filePath, ch.StartLine, ch.EndLine, ch.ContentHash)
	return ch
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

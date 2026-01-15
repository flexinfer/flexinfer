package tsindex

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	ts "github.com/smacker/go-tree-sitter/typescript/typescript"

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

	src, err := readFile(absPath)
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
	parser.SetLanguage(selectLanguage(absPath))
	tree, parseErr := parser.ParseCtx(ctx, nil, src)
	if parseErr != nil {
		return nil, parseErr
	}
	root := tree.RootNode()

	imports := extractImports(root, src)

	var chunks []schema.Chunk
	walk(root, func(n *sitter.Node) {
		switch n.Type() {
		case "function_declaration":
			name := nodeName(n, src)
			if name == "" {
				return
			}
			chunks = append(chunks, makeChunk(repoID, rel, i.language, "function", name, "", "", "", imports, extractCalls(n, src), src, n))
		case "class_declaration":
			className := nodeName(n, src)
			if className == "" {
				return
			}
			chunks = append(chunks, makeChunk(repoID, rel, i.language, "class", className, "", "", "", imports, nil, src, n))
			body := n.ChildByFieldName("body")
			if body == nil {
				return
			}
			walk(body, func(m *sitter.Node) {
				if m.Type() != "method_definition" {
					return
				}
				mname := nodeName(m, src)
				if mname == "" {
					return
				}
				chunks = append(chunks, makeChunk(repoID, rel, i.language, "method", mname, "", "", className, imports, extractCalls(m, src), src, m))
			})
		case "interface_declaration":
			name := nodeName(n, src)
			if name == "" {
				return
			}
			chunks = append(chunks, makeChunk(repoID, rel, i.language, "class", name, "", "", "", imports, nil, src, n))
		case "type_alias_declaration":
			name := nodeName(n, src)
			if name == "" {
				return
			}
			chunks = append(chunks, makeChunk(repoID, rel, i.language, "variable", name, "", "", "", imports, nil, src, n))
		case "lexical_declaration":
			// const foo = () => {} / let foo = () => {}
			walk(n, func(v *sitter.Node) {
				if v.Type() != "variable_declarator" {
					return
				}
				value := v.ChildByFieldName("value")
				if value == nil || value.Type() != "arrow_function" {
					return
				}
				name := nodeName(v, src)
				if name == "" {
					return
				}
				chunks = append(chunks, makeChunk(repoID, rel, i.language, "function", name, "", "", "", imports, extractCalls(value, src), src, v))
			})
		}
	})

	sort.Slice(chunks, func(a, b int) bool {
		if chunks[a].StartLine == chunks[b].StartLine {
			return chunks[a].Name < chunks[b].Name
		}
		return chunks[a].StartLine < chunks[b].StartLine
	})

	return chunks, nil
}

func selectLanguage(path string) *sitter.Language {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".tsx", ".jsx":
		return tsx.GetLanguage()
	case ".ts":
		return ts.GetLanguage()
	default:
		// typescript grammar can parse JS reasonably well; keeps deps small.
		return ts.GetLanguage()
	}
}

func makeChunk(
	repoID string,
	filePath string,
	lang string,
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
		Language:    lang,
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

func extractImports(root *sitter.Node, src []byte) []string {
	var imports []string
	seen := map[string]bool{}

	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child == nil || child.Type() != "import_statement" {
			continue
		}
		source := child.ChildByFieldName("source")
		if source == nil {
			continue
		}
		mod := strings.Trim(source.Content(src), "\"'")
		if mod == "" || seen[mod] {
			continue
		}
		seen[mod] = true
		imports = append(imports, mod)
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

func nodeName(node *sitter.Node, src []byte) string {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return ""
	}
	return strings.TrimSpace(nameNode.Content(src))
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

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

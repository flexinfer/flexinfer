package pyindex

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sitter "github.com/smacker/go-tree-sitter"
	py "github.com/smacker/go-tree-sitter/python"

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
	parser.SetLanguage(py.GetLanguage())
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

	// Only consider top-level definitions; avoid recursively walking the whole tree
	// (prevents class methods being double-counted as module-level functions).
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "function_definition":
			chunks = append(chunks, extractFunction(repoID, rel, imports, src, n, "", ""))
		case "class_definition":
			className := nodeName(n, src)
			if className == "" {
				continue
			}
			chunks = append(chunks, extractClass(repoID, rel, imports, src, n))

			body := n.ChildByFieldName("body")
			if body == nil {
				continue
			}
			// class body is a "block"; take immediate function definitions as methods.
			for j := 0; j < int(body.ChildCount()); j++ {
				m := body.Child(j)
				if m == nil || m.Type() != "function_definition" {
					continue
				}
				chunks = append(chunks, extractFunction(repoID, rel, imports, src, m, className, "class"))
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
	doc := moduleDocstring(root, src)

	var lines []string
	endLine := 1
	for i := 0; i < int(root.ChildCount()); i++ {
		n := root.Child(i)
		if n == nil {
			continue
		}
		switch n.Type() {
		case "import_statement", "import_from_statement":
			txt := strings.TrimSpace(n.Content(src))
			if txt != "" {
				lines = append(lines, txt)
				endLine = int(n.EndPoint().Row) + 1
			}
		}
	}

	content := strings.TrimSpace(strings.Join(lines, "\n"))
	fileHash := schema.ContentHash(string(src))

	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    filePath,
		Language:    "python",
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

func moduleDocstring(root *sitter.Node, src []byte) string {
	if root == nil {
		return ""
	}
	for i := 0; i < int(root.ChildCount()); i++ {
		stmt := root.Child(i)
		if stmt == nil {
			continue
		}
		// expression_statement -> string
		if stmt.Type() != "expression_statement" {
			if stmt.Type() != "comment" {
				break
			}
			continue
		}
		for j := 0; j < int(stmt.ChildCount()); j++ {
			expr := stmt.Child(j)
			if expr == nil || expr.Type() != "string" {
				continue
			}
			raw := strings.TrimSpace(expr.Content(src))
			return trimPythonString(raw)
		}
		break
	}
	return ""
}

func extractClass(repoID, filePath string, imports []string, src []byte, node *sitter.Node) schema.Chunk {
	name := nodeName(node, src)
	doc := docstringFromBody(node.ChildByFieldName("body"), src)

	start := int(node.StartPoint().Row) + 1
	end := int(node.EndPoint().Row) + 1
	startCol := int(node.StartPoint().Column)
	endCol := int(node.EndPoint().Column)

	// Try to trim class content before first method to reduce duplication.
	content := strings.TrimSpace(node.Content(src))
	body := node.ChildByFieldName("body")
	if body != nil {
		firstFn := findFirstChildOfType(body, "function_definition")
		if firstFn != nil {
			content = strings.TrimSpace(string(src[node.StartByte():firstFn.StartByte()]))
		}
	}

	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    filePath,
		Language:    "python",
		ChunkType:   "class",
		Name:        name,
		Docstring:   doc,
		Imports:     imports,
		Defs:        nonEmptyDefs(name),
		StartLine:   start,
		EndLine:     end,
		StartColumn: startCol,
		EndColumn:   endCol,
		TokenCount:  len(content) / 4,
		IndexedAt:   time.Now(),
		SchemaVer:   schema.Version,
		ContentHash: schema.ContentHash(content),
		Content:     content,
	}
	ch.ID = schema.ChunkID(repoID, filePath, ch.StartLine, ch.EndLine, ch.ContentHash)
	return ch
}

func extractFunction(repoID, filePath string, imports []string, src []byte, node *sitter.Node, parentName, parentType string) schema.Chunk {
	name := nodeName(node, src)
	doc := docstringFromBody(node.ChildByFieldName("body"), src)
	signature := functionSignature(node, src)
	calls := extractCalls(node, src)

	content := strings.TrimSpace(node.Content(src))

	chunkType := "function"
	if strings.TrimSpace(parentName) != "" {
		chunkType = "method"
	}

	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    filePath,
		Language:    "python",
		ChunkType:   chunkType,
		Name:        name,
		Signature:   signature,
		Docstring:   doc,
		ParentName:  parentName,
		ParentType:  parentType,
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
	seen := map[string]bool{}
	var imports []string
	for i := 0; i < int(root.ChildCount()); i++ {
		ch := root.Child(i)
		if ch == nil {
			continue
		}
		switch ch.Type() {
		case "import_statement":
			// import os, sys
			walk(ch, func(n *sitter.Node) {
				if n.Type() == "dotted_name" {
					name := strings.TrimSpace(n.Content(src))
					if name != "" && !seen[name] {
						seen[name] = true
						imports = append(imports, name)
					}
				}
			})
		case "import_from_statement":
			module := ch.ChildByFieldName("module_name")
			moduleName := ""
			if module != nil {
				moduleName = strings.TrimSpace(module.Content(src))
			}
			if moduleName == "" {
				continue
			}
			walk(ch, func(n *sitter.Node) {
				if n.Type() == "dotted_name" && n != module {
					name := strings.TrimSpace(n.Content(src))
					if name == "" {
						return
					}
					full := moduleName + "." + name
					if !seen[full] {
						seen[full] = true
						imports = append(imports, full)
					}
				}
				if n.Type() == "aliased_import" {
					actual := n.ChildByFieldName("name")
					if actual == nil {
						return
					}
					name := strings.TrimSpace(actual.Content(src))
					if name == "" {
						return
					}
					full := moduleName + "." + name
					if !seen[full] {
						seen[full] = true
						imports = append(imports, full)
					}
				}
			})
		}
	}
	sort.Strings(imports)
	return imports
}

func extractCalls(node *sitter.Node, src []byte) []string {
	seen := map[string]bool{}
	var calls []string
	walk(node, func(n *sitter.Node) {
		if n.Type() != "call" {
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

func docstringFromBody(body *sitter.Node, src []byte) string {
	if body == nil {
		return ""
	}
	for i := 0; i < int(body.ChildCount()); i++ {
		stmt := body.Child(i)
		if stmt == nil {
			continue
		}
		// expression_statement -> string
		if stmt.Type() != "expression_statement" {
			// Stop after first non-comment statement
			if stmt.Type() != "comment" {
				break
			}
			continue
		}
		for j := 0; j < int(stmt.ChildCount()); j++ {
			expr := stmt.Child(j)
			if expr == nil || expr.Type() != "string" {
				continue
			}
			raw := strings.TrimSpace(expr.Content(src))
			return trimPythonString(raw)
		}
		break
	}
	return ""
}

func trimPythonString(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"""`) && strings.HasSuffix(s, `"""`) && len(s) >= 6 {
		return strings.TrimSpace(s[3 : len(s)-3])
	}
	if strings.HasPrefix(s, `'''`) && strings.HasSuffix(s, `'''`) && len(s) >= 6 {
		return strings.TrimSpace(s[3 : len(s)-3])
	}
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) || (strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`)) {
		if len(s) >= 2 {
			return strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

func functionSignature(node *sitter.Node, src []byte) string {
	// Best-effort: name + parameters (+ return type if present).
	name := nodeName(node, src)
	params := node.ChildByFieldName("parameters")
	ret := node.ChildByFieldName("return_type")

	var b strings.Builder
	b.WriteString(name)
	if params != nil {
		b.WriteString(strings.TrimSpace(params.Content(src)))
	}
	if ret != nil {
		b.WriteString(" -> ")
		b.WriteString(strings.TrimSpace(ret.Content(src)))
	}
	return strings.TrimSpace(b.String())
}

func nodeName(node *sitter.Node, src []byte) string {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return ""
	}
	return strings.TrimSpace(nameNode.Content(src))
}

func findFirstChildOfType(node *sitter.Node, typ string) *sitter.Node {
	if node == nil {
		return nil
	}
	var found *sitter.Node
	walk(node, func(n *sitter.Node) {
		if found != nil {
			return
		}
		if n.Type() == typ {
			found = n
		}
	})
	return found
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

package goindex

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

type Indexer struct{}

func New() *Indexer { return &Indexer{} }

func (i *Indexer) Language() string { return "go" }

func (i *Indexer) Extensions() []string { return []string{".go"} }

func (i *Indexer) IndexFile(ctx context.Context, absRoot, absPath, repoID string) ([]schema.Chunk, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	return i.IndexFileFromContent(ctx, absRoot, absPath, repoID, content)
}

func (i *Indexer) IndexFileFromContent(
	ctx context.Context,
	absRoot,
	absPath,
	repoID string,
	content []byte,
) ([]schema.Chunk, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	src := content

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, content, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	imports := collectImports(file)

	var chunks []schema.Chunk
	if mod, ok := extractModuleChunk(fset, src, repoID, rel, file, imports); ok {
		chunks = append(chunks, mod)
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			ch, ok := extractFuncChunk(fset, src, repoID, rel, imports, d)
			if ok {
				chunks = append(chunks, ch)
			}
		case *ast.GenDecl:
			// types only for v1
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				ch, ok := extractTypeChunk(fset, src, repoID, rel, imports, d, ts)
				if ok {
					chunks = append(chunks, ch)
				}
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

func collectImports(file *ast.File) []string {
	out := make([]string, 0, len(file.Imports))
	for _, imp := range file.Imports {
		p := strings.Trim(imp.Path.Value, "\"")
		if p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func extractModuleChunk(
	fset *token.FileSet,
	src []byte,
	repoID string,
	relPath string,
	file *ast.File,
	imports []string,
) (schema.Chunk, bool) {
	startPos := file.Package
	doc := ""
	if file.Doc != nil {
		startPos = file.Doc.Pos()
		doc = strings.TrimSpace(file.Doc.Text())
	}

	endPos := file.Name.End()
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		if gd.End() > endPos {
			endPos = gd.End()
		}
	}

	start, ok := toOffset(fset, file.Package, startPos)
	if !ok {
		return schema.Chunk{}, false
	}
	end, ok := toOffset(fset, file.Package, endPos)
	if !ok || end <= start || end > len(src) {
		return schema.Chunk{}, false
	}

	content := strings.TrimSpace(string(src[start:end]))
	fileHash := schema.ContentHash(string(src))

	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    relPath,
		Language:    "go",
		ChunkType:   "module",
		StartLine:   fset.Position(startPos).Line,
		EndLine:     fset.Position(endPos).Line,
		StartColumn: max0(fset.Position(startPos).Column - 1),
		EndColumn:   max0(fset.Position(endPos).Column - 1),
		Name:        file.Name.Name,
		Signature:   "package " + file.Name.Name,
		Docstring:   doc,
		Imports:     imports,
		Defs:        []string{file.Name.Name},
		TokenCount:  len(content) / 4,
		IndexedAt:   time.Now(),
		SchemaVer:   schema.Version,
		ContentHash: fileHash,
		Content:     content,
	}

	ch.ID = schema.ChunkID(repoID, relPath, ch.StartLine, ch.EndLine, ch.ContentHash)
	return ch, true
}

func extractFuncChunk(
	fset *token.FileSet,
	src []byte,
	repoID string,
	relPath string,
	imports []string,
	fn *ast.FuncDecl,
) (schema.Chunk, bool) {
	startPos := fn.Pos()
	if fn.Doc != nil {
		startPos = fn.Doc.Pos()
	}
	endPos := fn.End()

	start, ok := toOffset(fset, fn.Pos(), startPos)
	if !ok {
		return schema.Chunk{}, false
	}
	end, ok := toOffset(fset, fn.Pos(), endPos)
	if !ok || end <= start || end > len(src) {
		return schema.Chunk{}, false
	}
	content := strings.TrimSpace(string(src[start:end]))

	name := fn.Name.Name
	receiver := receiverType(fn)
	chunkType := "function"
	parentType := ""
	parentName := ""
	if receiver != "" {
		chunkType = "method"
		parentType = "class"
		parentName = receiver
	}

	signature := funcSignature(fn)
	doc := ""
	if fn.Doc != nil {
		doc = strings.TrimSpace(fn.Doc.Text())
	}

	calls := extractCalls(fn.Body)

	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    relPath,
		Language:    "go",
		ChunkType:   chunkType,
		StartLine:   fset.Position(startPos).Line,
		EndLine:     fset.Position(endPos).Line,
		StartColumn: max0(fset.Position(startPos).Column - 1),
		EndColumn:   max0(fset.Position(endPos).Column - 1),
		Name:        name,
		Signature:   signature,
		Docstring:   doc,
		ParentName:  parentName,
		ParentType:  parentType,
		Imports:     imports,
		Calls:       calls,
		Defs:        []string{name},
		TokenCount:  len(content) / 4,
		IndexedAt:   time.Now(),
		SchemaVer:   schema.Version,
		ContentHash: schema.ContentHash(content),
		Content:     content,
	}

	ch.ID = schema.ChunkID(repoID, relPath, ch.StartLine, ch.EndLine, ch.ContentHash)
	return ch, true
}

func extractTypeChunk(
	fset *token.FileSet,
	src []byte,
	repoID string,
	relPath string,
	imports []string,
	decl *ast.GenDecl,
	ts *ast.TypeSpec,
) (schema.Chunk, bool) {
	startPos := decl.Pos()
	if decl.Doc != nil {
		startPos = decl.Doc.Pos()
	}
	endPos := decl.End()

	start, ok := toOffset(fset, decl.Pos(), startPos)
	if !ok {
		return schema.Chunk{}, false
	}
	end, ok := toOffset(fset, decl.Pos(), endPos)
	if !ok || end <= start || end > len(src) {
		return schema.Chunk{}, false
	}
	content := strings.TrimSpace(string(src[start:end]))

	name := ts.Name.Name
	doc := ""
	if decl.Doc != nil {
		doc = strings.TrimSpace(decl.Doc.Text())
	}

	ch := schema.Chunk{
		RepoID:      repoID,
		FilePath:    relPath,
		Language:    "go",
		ChunkType:   "class",
		StartLine:   fset.Position(startPos).Line,
		EndLine:     fset.Position(endPos).Line,
		StartColumn: max0(fset.Position(startPos).Column - 1),
		EndColumn:   max0(fset.Position(endPos).Column - 1),
		Name:        name,
		Docstring:   doc,
		Imports:     imports,
		Defs:        []string{name},
		TokenCount:  len(content) / 4,
		IndexedAt:   time.Now(),
		SchemaVer:   schema.Version,
		ContentHash: schema.ContentHash(content),
		Content:     content,
	}

	ch.ID = schema.ChunkID(repoID, relPath, ch.StartLine, ch.EndLine, ch.ContentHash)
	return ch, true
}

func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	t := fn.Recv.List[0].Type
	switch tt := t.(type) {
	case *ast.StarExpr:
		return exprToName(tt.X)
	default:
		return exprToName(tt)
	}
}

func funcSignature(fn *ast.FuncDecl) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, token.NewFileSet(), fn.Type)
	recv := receiverType(fn)
	if recv != "" {
		return fmt.Sprintf("(%s).%s%s", recv, fn.Name.Name, b.String())
	}
	return fn.Name.Name + b.String()
}

func exprToName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprToName(v.X) + "." + v.Sel.Name
	default:
		var b bytes.Buffer
		_ = printer.Fprint(&b, token.NewFileSet(), e)
		return b.String()
	}
}

func extractCalls(body *ast.BlockStmt) []string {
	if body == nil {
		return nil
	}
	seen := map[string]bool{}
	var calls []string
	ast.Inspect(body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(ce.Fun)
		if name == "" || seen[name] {
			return true
		}
		seen[name] = true
		calls = append(calls, name)
		return true
	})
	sort.Strings(calls)
	return calls
}

func callName(fun ast.Expr) string {
	switch v := fun.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		// pkg.Func or recv.Method
		x := exprToName(v.X)
		if x == "" {
			return v.Sel.Name
		}
		return x + "." + v.Sel.Name
	default:
		return ""
	}
}

func toOffset(fset *token.FileSet, anchor token.Pos, pos token.Pos) (int, bool) {
	tf := fset.File(anchor)
	if tf == nil {
		return 0, false
	}
	return tf.Offset(pos), true
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

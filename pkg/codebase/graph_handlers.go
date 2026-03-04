package codebase

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase/qdrant"
	"github.com/crb2nu/loom/pkg/codebase/schema"
	"github.com/crb2nu/loom/pkg/validate"
)

type graphNode struct {
	Symbol     string        `json:"symbol"`
	External   bool          `json:"external,omitempty"`
	Definition *schema.Chunk `json:"definition,omitempty"`
}

type graphEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	CallExpr string `json:"call_expr,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Line     int    `json:"line,omitempty"`
}

func normalizeRenderFormat(v any) (string, error) {
	render := "none"
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		render = strings.ToLower(strings.TrimSpace(s))
	}
	switch render {
	case "none", "mermaid", "dot":
		return render, nil
	default:
		return "", fmt.Errorf("render must be one of: none, mermaid, dot")
	}
}

func escapeMermaidLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func escapeDotLabel(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

func renderCallGraph(render string, nodes []graphNode, edges []graphEdge) string {
	idBySymbol := make(map[string]string, len(nodes))
	for i := range nodes {
		idBySymbol[nodes[i].Symbol] = fmt.Sprintf("n%d", i)
	}

	var b strings.Builder
	switch render {
	case "mermaid":
		b.WriteString("graph TD\n")
		for i := range nodes {
			id := idBySymbol[nodes[i].Symbol]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString("[\"")
			b.WriteString(escapeMermaidLabel(nodes[i].Symbol))
			b.WriteString("\"]\n")
		}
		for _, e := range edges {
			from := idBySymbol[e.From]
			to := idBySymbol[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" --> ")
			b.WriteString(to)
			b.WriteByte('\n')
		}
	case "dot":
		b.WriteString("digraph G {\n")
		for i := range nodes {
			id := idBySymbol[nodes[i].Symbol]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString(" [label=\"")
			b.WriteString(escapeDotLabel(nodes[i].Symbol))
			b.WriteString("\"];\n")
		}
		for _, e := range edges {
			from := idBySymbol[e.From]
			to := idBySymbol[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" -> ")
			b.WriteString(to)
			b.WriteString(";\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func (s *Service) HandleCallGraph(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	symbol, _ := args["symbol"].(string)
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("symbol is required")
	}

	filePath, _ := args["file_path"].(string)

	direction := "out"
	if v, ok := args["direction"].(string); ok && strings.TrimSpace(v) != "" {
		direction = strings.ToLower(strings.TrimSpace(v))
	}
	if direction != "out" && direction != "in" && direction != "both" {
		return nil, fmt.Errorf("direction must be one of: out, in, both")
	}

	depth := validate.IntFromArgs(args, "depth", 2)
	if depth < 0 {
		depth = 2
	}
	if depth > 10 {
		depth = 10
	}

	limit := validate.IntFromArgs(args, "limit", s.cfg.ScrollLimit)
	if limit <= 0 {
		limit = s.cfg.ScrollLimit
	}

	maxNodes := validate.IntFromArgs(args, "max_nodes", 200)
	if maxNodes <= 0 {
		maxNodes = 200
	}
	if maxNodes > 2000 {
		maxNodes = 2000
	}

	includeExternal := validate.BoolFromArgs(args, "include_external", true)

	render, err := normalizeRenderFormat(args["render"])
	if err != nil {
		return nil, err
	}

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))

	nodes := map[string]*graphNode{}
	addNode := func(sym string, external bool) {
		if strings.TrimSpace(sym) == "" {
			return
		}
		if nodes[sym] == nil {
			nodes[sym] = &graphNode{Symbol: sym, External: external}
		} else if !external {
			nodes[sym].External = false
		}
	}

	edges := []graphEdge{}
	addEdge := func(e graphEdge) {
		if strings.TrimSpace(e.From) == "" || strings.TrimSpace(e.To) == "" {
			return
		}
		edges = append(edges, e)
	}

	seen := map[string]bool{}
	frontier := []string{symbol}
	seen[symbol] = true
	addNode(symbol, false)

	attachDefinition := func(sym string, fp string) *schema.Chunk {
		ch, err := s.qdrant.FindChunkByName(ctx, repoID, sym, fp, languages, limit)
		if err != nil || ch == nil {
			return nil
		}
		ch.Content = ""
		return ch
	}

	if def := attachDefinition(symbol, filePath); def != nil {
		nodes[symbol].Definition = def
	}

	for level := 0; level < depth; level++ {
		if len(frontier) == 0 {
			break
		}

		next := make([]string, 0, len(frontier)*2)
		for _, cur := range frontier {
			if direction == "out" || direction == "both" {
				def := nodes[cur].Definition
				if def == nil {
					def = attachDefinition(cur, "")
					nodes[cur].Definition = def
				}
				if def != nil {
					for _, call := range def.Calls {
						tok := qdrant.NormalizeCallToken(call)
						if tok == "" {
							continue
						}
						if tok == cur {
							continue
						}
						if includeExternal {
							addNode(tok, strings.Contains(call, ".") || strings.Contains(call, "::"))
						} else {
							addNode(tok, false)
						}
						addEdge(graphEdge{From: cur, To: tok, Kind: "calls", CallExpr: call})
						if len(seen) < maxNodes && !seen[tok] {
							seen[tok] = true
							next = append(next, tok)
						}
					}
				}
			}

			if direction == "in" || direction == "both" {
				callers, err := s.qdrant.FindCallers(ctx, repoID, cur, limit)
				if err != nil {
					continue
				}
				for _, c := range callers {
					if strings.TrimSpace(c.FunctionName) == "" {
						continue
					}
					addNode(c.FunctionName, false)
					addEdge(graphEdge{
						From:     c.FunctionName,
						To:       cur,
						Kind:     "calls",
						CallExpr: c.CallExpr,
						FilePath: c.FilePath,
						Line:     c.LineNumber,
					})
					if len(seen) < maxNodes && !seen[c.FunctionName] {
						seen[c.FunctionName] = true
						next = append(next, c.FunctionName)
					}
				}
			}
		}

		frontier = next
	}

	outNodes := make([]graphNode, 0, len(nodes))
	for _, n := range nodes {
		outNodes = append(outNodes, *n)
	}
	sort.Slice(outNodes, func(i, j int) bool { return outNodes[i].Symbol < outNodes[j].Symbol })

	rendered := ""
	if render != "none" {
		rendered = renderCallGraph(render, outNodes, edges)
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":   repoID,
		"symbol":    symbol,
		"file_path": filePath,
		"direction": direction,
		"depth":     depth,
		"max_nodes": maxNodes,
		"nodes":     outNodes,
		"edges":     edges,
		"languages": languages,
		"render":    render,
		"rendered":  rendered,
		"truncated": len(seen) >= maxNodes,
	})
}

type moduleGraphNode struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // file|import
	FilePath string `json:"file_path,omitempty"`
	Import   string `json:"import,omitempty"`
}

type moduleGraphEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"` // imports
	ImportRaw  string `json:"import_raw,omitempty"`
	ResolvedTo string `json:"resolved_to,omitempty"` // file_path when resolved
}

func renderModuleGraph(render string, nodes []moduleGraphNode, edges []moduleGraphEdge) string {
	idByNode := make(map[string]string, len(nodes))
	labelByNode := make(map[string]string, len(nodes))
	for i := range nodes {
		id := fmt.Sprintf("n%d", i)
		idByNode[nodes[i].ID] = id
		switch nodes[i].Kind {
		case "file":
			labelByNode[nodes[i].ID] = nodes[i].FilePath
		case "import":
			labelByNode[nodes[i].ID] = nodes[i].Import
		default:
			labelByNode[nodes[i].ID] = nodes[i].ID
		}
	}

	var b strings.Builder
	switch render {
	case "mermaid":
		b.WriteString("graph TD\n")
		for i := range nodes {
			nid := nodes[i].ID
			id := idByNode[nid]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString("[\"")
			b.WriteString(escapeMermaidLabel(labelByNode[nid]))
			b.WriteString("\"]\n")
		}
		for _, e := range edges {
			from := idByNode[e.From]
			to := idByNode[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" --> ")
			b.WriteString(to)
			b.WriteByte('\n')
		}
	case "dot":
		b.WriteString("digraph G {\n")
		for i := range nodes {
			nid := nodes[i].ID
			id := idByNode[nid]
			b.WriteString("  ")
			b.WriteString(id)
			b.WriteString(" [label=\"")
			b.WriteString(escapeDotLabel(labelByNode[nid]))
			b.WriteString("\"];\n")
		}
		for _, e := range edges {
			from := idByNode[e.From]
			to := idByNode[e.To]
			if from == "" || to == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(from)
			b.WriteString(" -> ")
			b.WriteString(to)
			b.WriteString(";\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func (s *Service) HandleModuleGraph(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	repoID := s.cfg.RepoIDDefault
	if v, ok := args["repo_id"].(string); ok && strings.TrimSpace(v) != "" {
		repoID = v
	}
	if strings.TrimSpace(repoID) == "" {
		return nil, fmt.Errorf("repo_id is required (or set CODEBASE_REPO_ID)")
	}

	maxFiles := validate.IntFromArgs(args, "max_files", 512)
	if maxFiles <= 0 {
		maxFiles = 512
	}
	if maxFiles > 10_000 {
		maxFiles = 10_000
	}

	maxEdges := validate.IntFromArgs(args, "max_edges", 4000)
	if maxEdges <= 0 {
		maxEdges = 4000
	}
	if maxEdges > 100_000 {
		maxEdges = 100_000
	}

	includeExternal := validate.BoolFromArgs(args, "include_external", true)

	render, err := normalizeRenderFormat(args["render"])
	if err != nil {
		return nil, err
	}

	languages := normalizeStringSlice(validate.StringSliceFromArgs(args, "languages"))

	modules, err := s.qdrant.ListModules(ctx, repoID, maxFiles)
	if err != nil {
		return nil, err
	}

	if len(languages) > 0 {
		want := map[string]bool{}
		for _, l := range languages {
			want[l] = true
		}
		filtered := modules[:0]
		for _, m := range modules {
			if want[strings.ToLower(m.Language)] {
				filtered = append(filtered, m)
			}
		}
		modules = filtered
	}

	fileSet := map[string]bool{}
	for _, m := range modules {
		if strings.TrimSpace(m.FilePath) != "" {
			fileSet[m.FilePath] = true
		}
	}

	resolveRelativeJSImport := func(fromFile string, raw string) (string, bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" || !strings.HasPrefix(raw, ".") {
			return "", false
		}
		dir := path.Dir(fromFile)
		if dir == "." {
			dir = ""
		}
		base := path.Clean(path.Join(dir, raw))
		cands := []string{
			base,
			base + ".ts",
			base + ".tsx",
			base + ".js",
			base + ".jsx",
			path.Join(base, "index.ts"),
			path.Join(base, "index.tsx"),
			path.Join(base, "index.js"),
			path.Join(base, "index.jsx"),
		}
		for _, c := range cands {
			if fileSet[c] {
				return c, true
			}
		}
		return "", false
	}

	resolvePythonImport := func(raw string) (string, bool) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", false
		}
		if strings.ContainsAny(raw, "/\\") {
			return "", false
		}
		base := strings.ReplaceAll(raw, ".", "/")
		cands := []string{
			base + ".py",
			base + ".pyi",
			path.Join(base, "__init__.py"),
			path.Join(base, "__init__.pyi"),
		}
		for _, c := range cands {
			if fileSet[c] {
				return c, true
			}
		}
		return "", false
	}

	nodes := map[string]moduleGraphNode{}
	addNode := func(n moduleGraphNode) {
		if n.ID == "" {
			return
		}
		if _, ok := nodes[n.ID]; !ok {
			nodes[n.ID] = n
		}
	}

	edges := make([]moduleGraphEdge, 0)

	for _, m := range modules {
		from := m.FilePath
		if strings.TrimSpace(from) == "" {
			continue
		}
		addNode(moduleGraphNode{ID: "file:" + from, Kind: "file", FilePath: from})

		for _, imp := range m.Imports {
			if strings.TrimSpace(imp) == "" {
				continue
			}
			toID := "import:" + imp
			resolved := ""

			switch strings.ToLower(m.Language) {
			case "typescript", "javascript":
				if r, ok := resolveRelativeJSImport(from, imp); ok {
					resolved = r
					toID = "file:" + r
					addNode(moduleGraphNode{ID: toID, Kind: "file", FilePath: r})
				}
			case "python":
				if r, ok := resolvePythonImport(imp); ok {
					resolved = r
					toID = "file:" + r
					addNode(moduleGraphNode{ID: toID, Kind: "file", FilePath: r})
				}
			}

			if resolved == "" {
				if !includeExternal {
					continue
				}
				addNode(moduleGraphNode{ID: toID, Kind: "import", Import: imp})
			}

			edges = append(edges, moduleGraphEdge{
				From:       "file:" + from,
				To:         toID,
				Kind:       "imports",
				ImportRaw:  imp,
				ResolvedTo: resolved,
			})
			if len(edges) >= maxEdges {
				break
			}
		}
		if len(edges) >= maxEdges {
			break
		}
	}

	outNodes := make([]moduleGraphNode, 0, len(nodes))
	for _, n := range nodes {
		outNodes = append(outNodes, n)
	}
	sort.Slice(outNodes, func(i, j int) bool { return outNodes[i].ID < outNodes[j].ID })

	rendered := ""
	if render != "none" {
		rendered = renderModuleGraph(render, outNodes, edges)
	}

	return mcp.JSONResult(map[string]any{
		"repo_id":            repoID,
		"max_files":          maxFiles,
		"max_edges":          maxEdges,
		"include_external":   includeExternal,
		"languages":          languages,
		"nodes":              outNodes,
		"edges":              edges,
		"render":             render,
		"rendered":           rendered,
		"truncated_by_files": len(modules) >= maxFiles,
		"truncated_by_edges": len(edges) >= maxEdges,
	})
}

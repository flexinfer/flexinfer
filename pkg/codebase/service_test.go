package codebase

import (
	"strings"
	"testing"
)

func TestMinInt(t *testing.T) {
	tests := []struct {
		a, b int
		want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{0, 0, 0},
		{-1, 1, -1},
		{-5, -3, -5},
		{100, 100, 100},
	}

	for _, tt := range tests {
		got := minInt(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minInt(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestMinFloat64(t *testing.T) {
	tests := []struct {
		a, b float64
		want float64
	}{
		{1.0, 2.0, 1.0},
		{2.5, 1.5, 1.5},
		{0.0, 0.0, 0.0},
		{-1.5, 1.5, -1.5},
		{0.001, 0.002, 0.001},
	}

	for _, tt := range tests {
		got := minFloat64(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minFloat64(%f, %f) = %f, want %f", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestLexicalTokens(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "simple words",
			query: "hello world",
			want:  []string{"hello", "world"},
		},
		{
			name:  "mixed case",
			query: "Hello WORLD",
			want:  []string{"hello", "world"},
		},
		{
			name:  "special characters",
			query: "hello_world-test.go",
			want:  []string{"hello", "test", "world"},
		},
		{
			name:  "short tokens filtered",
			query: "a to the foo",
			want:  []string{"foo", "the"},
		},
		{
			name:  "duplicates removed",
			query: "foo bar foo baz foo",
			want:  []string{"bar", "baz", "foo"},
		},
		{
			name:  "numbers included",
			query: "test123 foo456",
			want:  []string{"foo456", "test123"},
		},
		{
			name:  "empty string",
			query: "",
			want:  []string{},
		},
		{
			name:  "only short tokens",
			query: "a b c",
			want:  []string{},
		},
		{
			name:  "camelCase preserved as single token",
			query: "handleUserRequest",
			want:  []string{"handleuserrequest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lexicalTokens(tt.query)
			if len(got) != len(tt.want) {
				t.Errorf("lexicalTokens(%q) = %v, want %v", tt.query, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("lexicalTokens(%q)[%d] = %q, want %q", tt.query, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestEscapeMermaidLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{`hello "world"`, `hello \"world\"`},
		{`back\slash`, `back\\slash`},
		{"line\nbreak", "line break"},
		{"\"quoted\nnew\"", `\"quoted new\"`},
	}

	for _, tt := range tests {
		got := escapeMermaidLabel(tt.input)
		if got != tt.want {
			t.Errorf("escapeMermaidLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEscapeDotLabel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{`hello "world"`, `hello \"world\"`},
		{`back\slash`, `back\\slash`},
		{"line\nbreak", "line\\nbreak"},
		{`"quoted\nnew"`, `\"quoted\\nnew\"`},
	}

	for _, tt := range tests {
		got := escapeDotLabel(tt.input)
		if got != tt.want {
			t.Errorf("escapeDotLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRenderCallGraph(t *testing.T) {
	nodes := []graphNode{
		{Symbol: "main"},
		{Symbol: "helper"},
		{Symbol: "util"},
	}
	edges := []graphEdge{
		{From: "main", To: "helper", Kind: "call"},
		{From: "helper", To: "util", Kind: "call"},
	}

	t.Run("mermaid format", func(t *testing.T) {
		result := renderCallGraph("mermaid", nodes, edges)
		if !strings.HasPrefix(result, "graph TD\n") {
			t.Error("mermaid graph should start with 'graph TD'")
		}
		if !strings.Contains(result, `n0["main"]`) {
			t.Error("should contain node 0 with main")
		}
		if !strings.Contains(result, `n1["helper"]`) {
			t.Error("should contain node 1 with helper")
		}
		if !strings.Contains(result, "n0 --> n1") {
			t.Error("should contain edge from main to helper")
		}
	})

	t.Run("dot format", func(t *testing.T) {
		result := renderCallGraph("dot", nodes, edges)
		if !strings.HasPrefix(result, "digraph G {\n") {
			t.Error("dot graph should start with 'digraph G {'")
		}
		if !strings.HasSuffix(result, "}\n") {
			t.Error("dot graph should end with '}'")
		}
		if !strings.Contains(result, `n0 [label="main"]`) {
			t.Error("should contain node 0 with main label")
		}
		if !strings.Contains(result, "n0 -> n1;") {
			t.Error("should contain edge from n0 to n1")
		}
	})

	t.Run("empty graph", func(t *testing.T) {
		result := renderCallGraph("mermaid", nil, nil)
		if result != "graph TD\n" {
			t.Errorf("empty mermaid graph = %q, want 'graph TD\\n'", result)
		}
	})

	t.Run("missing edge nodes", func(t *testing.T) {
		// Edge references nodes not in the node list
		badEdges := []graphEdge{
			{From: "missing1", To: "missing2"},
		}
		result := renderCallGraph("mermaid", nodes, badEdges)
		// Should not contain the edge since nodes don't exist
		if strings.Contains(result, "-->") {
			t.Error("should not render edges for missing nodes")
		}
	})

	t.Run("unknown format returns empty", func(t *testing.T) {
		result := renderCallGraph("unknown", nodes, edges)
		if result != "" {
			t.Errorf("unknown format should return empty, got %q", result)
		}
	})
}

func TestRenderModuleGraph(t *testing.T) {
	nodes := []moduleGraphNode{
		{ID: "file1", Kind: "file", FilePath: "src/main.go"},
		{ID: "file2", Kind: "file", FilePath: "src/util.go"},
		{ID: "import1", Kind: "import", Import: "fmt"},
	}
	edges := []moduleGraphEdge{
		{From: "file1", To: "file2", Kind: "imports"},
		{From: "file1", To: "import1", Kind: "imports"},
	}

	t.Run("mermaid format", func(t *testing.T) {
		result := renderModuleGraph("mermaid", nodes, edges)
		if !strings.HasPrefix(result, "graph TD\n") {
			t.Error("mermaid graph should start with 'graph TD'")
		}
		if !strings.Contains(result, `n0["src/main.go"]`) {
			t.Error("should contain file path as label")
		}
		if !strings.Contains(result, `n2["fmt"]`) {
			t.Error("should contain import as label")
		}
		if !strings.Contains(result, "n0 --> n1") {
			t.Error("should contain edge from file1 to file2")
		}
	})

	t.Run("dot format", func(t *testing.T) {
		result := renderModuleGraph("dot", nodes, edges)
		if !strings.HasPrefix(result, "digraph G {\n") {
			t.Error("dot graph should start with 'digraph G {'")
		}
		if !strings.Contains(result, `n0 [label="src/main.go"]`) {
			t.Error("should contain file path as label")
		}
		if !strings.Contains(result, "n0 -> n1;") {
			t.Error("should contain edge")
		}
	})

	t.Run("empty graph", func(t *testing.T) {
		result := renderModuleGraph("mermaid", nil, nil)
		if result != "graph TD\n" {
			t.Errorf("empty mermaid graph = %q", result)
		}
	})

	t.Run("node with default label", func(t *testing.T) {
		// Node with unknown kind uses ID as label
		nodes := []moduleGraphNode{
			{ID: "unknown1", Kind: "unknown"},
		}
		result := renderModuleGraph("mermaid", nodes, nil)
		if !strings.Contains(result, `n0["unknown1"]`) {
			t.Errorf("should use ID as label for unknown kind, got %s", result)
		}
	})
}

func TestGraphNodeTypes(t *testing.T) {
	// Test that types are correctly defined
	node := graphNode{
		Symbol:   "testSymbol",
		External: true,
	}
	if node.Symbol != "testSymbol" {
		t.Error("Symbol not set")
	}
	if !node.External {
		t.Error("External not set")
	}

	edge := graphEdge{
		From:     "a",
		To:       "b",
		Kind:     "call",
		CallExpr: "a()",
		FilePath: "test.go",
		Line:     10,
	}
	if edge.From != "a" || edge.To != "b" {
		t.Error("Edge endpoints not set")
	}
}

func TestModuleGraphTypes(t *testing.T) {
	node := moduleGraphNode{
		ID:       "file1",
		Kind:     "file",
		FilePath: "test.go",
		Import:   "",
	}
	if node.Kind != "file" {
		t.Error("Kind not set")
	}

	edge := moduleGraphEdge{
		From:       "a",
		To:         "b",
		Kind:       "imports",
		ImportRaw:  "github.com/foo",
		ResolvedTo: "vendor/foo/foo.go",
	}
	if edge.ImportRaw != "github.com/foo" {
		t.Error("ImportRaw not set")
	}
}

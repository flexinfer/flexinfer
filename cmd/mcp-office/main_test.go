package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// TestMain pins the output format to JSON so we can assert on payload shape.
// In production the default (TOON) is more compact for LLMs, but JSON is
// trivially round-trippable in tests.
func TestMain(m *testing.M) {
	_ = os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// ---------- fixture builders -----------------------------------------------

// buildDOCX assembles a minimal valid .docx with the provided body paragraphs.
// Each paragraph is rendered with one run and the given pStyle.
func buildDOCX(t *testing.T, paras []docxParagraph) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	addFile := func(name, body string) {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	addFile("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
</Types>`)

	addFile("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
</Relationships>`)

	addFile("docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:title>Test Doc</dc:title>
<dc:creator>Test Author</dc:creator>
</cp:coreProperties>`)

	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, p := range paras {
		body.WriteString(`<w:p>`)
		if p.style != "" {
			body.WriteString(`<w:pPr><w:pStyle w:val="` + p.style + `"/></w:pPr>`)
		}
		body.WriteString(`<w:r><w:t>` + p.text + `</w:t></w:r></w:p>`)
	}
	body.WriteString(`</w:body></w:document>`)
	addFile("word/document.xml", body.String())

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// buildDOCXMultiRun assembles a .docx whose paragraph text is split across
// multiple <w:t> runs. Each entry in `runs` becomes one <w:r><w:t>...</w:t></w:r>.
// This mirrors what Word produces after edits/spellcheck, where a placeholder
// like "{{name}}" frequently lands in pieces like ["Hello {{na", "me}}"].
func buildDOCXMultiRun(t *testing.T, runs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	addFile := func(name, body string) {
		f, _ := zw.Create(name)
		_, _ = f.Write([]byte(body))
	}
	addFile("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`)
	addFile("_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`)

	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>`)
	for _, r := range runs {
		body.WriteString(`<w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">` + r + `</w:t></w:r>`)
	}
	body.WriteString(`</w:p></w:body></w:document>`)
	addFile("word/document.xml", body.String())

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// buildPPTX assembles a minimal .pptx with two slides.
func buildPPTX(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	addFile := func(name, body string) {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		_, _ = f.Write([]byte(body))
	}

	addFile("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/></Types>`)
	addFile("docProps/core.xml", `<?xml version="1.0"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Deck</dc:title></cp:coreProperties>`)

	slide := func(title, body string) string {
		return `<?xml version="1.0"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree>` +
			`<p:sp><p:nvSpPr><p:cNvPr id="1" name="t"/><p:cNvSpPr/><p:nvPr><p:ph type="ctrTitle"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:p><a:r><a:t>` + title + `</a:t></a:r></a:p></p:txBody></p:sp>` +
			`<p:sp><p:nvSpPr><p:cNvPr id="2" name="b"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:p><a:r><a:t>` + body + `</a:t></a:r></a:p></p:txBody></p:sp>` +
			`</p:spTree></p:cSld></p:sld>`
	}
	addFile("ppt/slides/slide1.xml", slide("Welcome", "Hello world"))
	addFile("ppt/slides/slide2.xml", slide("Plans", "Ship it"))

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// ---------- loader / detection ---------------------------------------------

func TestDetectFormatByExtension(t *testing.T) {
	cases := map[string]string{
		"a.pdf":  "pdf",
		"a.docx": "docx",
		"a.xlsx": "xlsx",
		"a.pptx": "pptx",
		"a.txt":  "",
	}
	for name, want := range cases {
		got := detectFormat(name, nil)
		if got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}

func TestDetectFormatByMagic(t *testing.T) {
	if got := detectFormat("unknown", []byte("%PDF-1.4 stuff")); got != "pdf" {
		t.Errorf("pdf magic: got %q", got)
	}
}

func TestLoadSourceErrors(t *testing.T) {
	if _, err := loadSource("", "", ""); err == nil {
		t.Error("expected error for empty input")
	}
	if _, err := loadSource("p", "b64", ""); err == nil {
		t.Error("expected error for mutually exclusive")
	}
	if _, err := loadSource("", "!!!not-base64!!!", "pdf"); err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestLoadSourceFromPath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.pdf")
	if err := os.WriteFile(p, []byte("%PDF-1.4 hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := loadSource(p, "", "")
	if err != nil {
		t.Fatalf("loadSource: %v", err)
	}
	if src.format != "pdf" {
		t.Errorf("got format %q", src.format)
	}
}

// ---------- handler: extract_text -----------------------------------------

func TestHandleExtractText_MissingInput(t *testing.T) {
	res, err := handleExtractText(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for missing path/bytes_b64")
	}
}

func TestHandleExtractText_DOCX(t *testing.T) {
	docx := buildDOCX(t, []docxParagraph{
		{style: "Heading1", text: "Hello"},
		{style: "Normal", text: "World"},
	})
	res, err := handleExtractText(context.Background(), map[string]any{
		"bytes_b64": base64.StdEncoding.EncodeToString(docx),
		"format":    "docx",
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %+v", res)
	}
	body := resultJSON(t, res)
	text, _ := body["text"].(string)
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "World") {
		t.Errorf("expected text to contain Hello + World, got %q", text)
	}
	if got := body["format"]; got != "docx" {
		t.Errorf("format = %v, want docx", got)
	}
}

func TestHandleExtractText_PPTX(t *testing.T) {
	pptx := buildPPTX(t)
	res, err := handleExtractText(context.Background(), map[string]any{
		"bytes_b64": base64.StdEncoding.EncodeToString(pptx),
		"format":    "pptx",
	})
	if err != nil || res.IsError {
		t.Fatalf("unexpected: err=%v res=%+v", err, res)
	}
	body := resultJSON(t, res)
	text, _ := body["text"].(string)
	for _, want := range []string{"Welcome", "Hello world", "Plans", "Ship it"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in: %q", want, text)
		}
	}
}

// ---------- handler: create_xlsx + roundtrip -------------------------------

func TestHandleCreateXLSX_RoundTrip(t *testing.T) {
	args := map[string]any{
		"sheets": []any{
			map[string]any{
				"name": "Numbers",
				"rows": []any{
					[]any{"a", "b", "c"},
					[]any{"1", "2", "3"},
				},
			},
			map[string]any{
				"name": "Letters",
				"rows": []any{
					[]any{"x", "y"},
				},
			},
		},
	}
	res, err := handleCreateXLSX(context.Background(), args)
	if err != nil || res.IsError {
		t.Fatalf("create failed: err=%v res=%+v", err, res)
	}
	body := resultJSON(t, res)
	encoded, _ := body["bytes_b64"].(string)
	if encoded == "" {
		t.Fatal("expected bytes_b64 in response")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}

	doc, err := parseXLSX(raw)
	if err != nil {
		t.Fatalf("parse roundtrip: %v", err)
	}
	if len(doc.sheets) != 2 {
		t.Fatalf("got %d sheets, want 2", len(doc.sheets))
	}
	if doc.sheets[0].name != "Numbers" || doc.sheets[1].name != "Letters" {
		t.Errorf("sheet names: %+v", []string{doc.sheets[0].name, doc.sheets[1].name})
	}
	// All-string input → cells round-trip as strings (no auto-numeric coercion).
	if got := doc.sheets[0].rows[1][2]; got != "3" {
		t.Errorf("Numbers[1][2] = %v (%T), want \"3\"", got, got)
	}
}

// TestHandleCreateXLSX_TypedValues verifies that JSON numbers, booleans, and
// strings keep their native types in the workbook, and that "="-prefixed
// strings become live formulas that compute on open.
func TestHandleCreateXLSX_TypedValues(t *testing.T) {
	args := map[string]any{
		"sheets": []any{
			map[string]any{
				"name": "Mixed",
				"rows": []any{
					[]any{"label", "value"},
					[]any{"int", float64(42)},   // JSON numbers arrive as float64
					[]any{"float", 3.14},        // float stays float
					[]any{"bool_t", true},       // booleans typed
					[]any{"bool_f", false},      //
					[]any{"phone", "555-1234"},  // string that LOOKS numeric stays string
					[]any{"sum", "=SUM(B2:B3)"}, // formula: integer + float = 45.14
				},
			},
		},
	}
	res, err := handleCreateXLSX(context.Background(), args)
	if err != nil || res.IsError {
		t.Fatalf("create failed: err=%v res=%+v", err, res)
	}
	body := resultJSON(t, res)
	raw, err := base64.StdEncoding.DecodeString(body["bytes_b64"].(string))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseXLSX(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	s := doc.sheets[0]
	// row 0 = header (strings)
	if s.rows[0][0] != "label" || s.rows[0][1] != "value" {
		t.Errorf("header: %v", s.rows[0])
	}
	// row 1 = ("int", 42.0)
	if got, ok := s.rows[1][1].(float64); !ok || got != 42 {
		t.Errorf("int cell: %v (%T), want 42 as float64", s.rows[1][1], s.rows[1][1])
	}
	// row 2 = ("float", 3.14)
	if got, ok := s.rows[2][1].(float64); !ok || got != 3.14 {
		t.Errorf("float cell: %v (%T), want 3.14", s.rows[2][1], s.rows[2][1])
	}
	// row 3 = ("bool_t", true) — excelize stores bools as "TRUE"/"FALSE"
	// strings in the displayable layer; coerceCellValue maps that back to bool.
	if got, ok := s.rows[3][1].(bool); !ok || !got {
		t.Errorf("bool_t cell: %v (%T), want true", s.rows[3][1], s.rows[3][1])
	}
	if got, ok := s.rows[4][1].(bool); !ok || got {
		t.Errorf("bool_f cell: %v (%T), want false", s.rows[4][1], s.rows[4][1])
	}
	// row 5 = ("phone", "555-1234") — string-typed, not numeric
	if got := s.rows[5][1]; got != "555-1234" {
		t.Errorf("phone cell: %v (%T), want \"555-1234\"", got, got)
	}
	// row 6 = ("sum", =SUM(B2:B3)) — formula's cached result should be 45.14
	if got, ok := s.rows[6][1].(float64); !ok || got != 45.14 {
		t.Errorf("formula cell: %v (%T), want 45.14", s.rows[6][1], s.rows[6][1])
	}
}

// TestHandleCreateXLSX_ForwardFormula verifies that a formula referencing
// cells defined later in the workbook still gets its result cached. The
// post-pass after all writes is what makes this work — at the time the
// formula was written, the referenced cells didn't exist yet.
func TestHandleCreateXLSX_ForwardFormula(t *testing.T) {
	args := map[string]any{
		"sheets": []any{
			map[string]any{
				"name": "Fwd",
				"rows": []any{
					[]any{"=SUM(A2:A3)"}, // forward reference at A1
					[]any{float64(10)},
					[]any{float64(20)},
				},
			},
		},
	}
	res, err := handleCreateXLSX(context.Background(), args)
	if err != nil || res.IsError {
		t.Fatalf("create failed: err=%v res=%+v", err, res)
	}
	body := resultJSON(t, res)
	raw, _ := base64.StdEncoding.DecodeString(body["bytes_b64"].(string))
	doc, err := parseXLSX(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, ok := doc.sheets[0].rows[0][0].(float64)
	if !ok || got != 30 {
		t.Errorf("A1 (forward formula) = %v (%T), want 30 as float64", doc.sheets[0].rows[0][0], doc.sheets[0].rows[0][0])
	}
}

func TestHandleCreateXLSX_MissingSheets(t *testing.T) {
	res, err := handleCreateXLSX(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for missing sheets")
	}
}

// ---------- handler: modify_docx -------------------------------------------

func TestHandleModifyDOCX(t *testing.T) {
	docx := buildDOCX(t, []docxParagraph{
		{style: "Normal", text: "Hello {{name}}, welcome to {{place}}."},
	})
	res, err := handleModifyDOCX(context.Background(), map[string]any{
		"bytes_b64": base64.StdEncoding.EncodeToString(docx),
		"replacements": map[string]any{
			"{{name}}":  "Cody",
			"{{place}}": "Loom",
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("unexpected: err=%v res=%+v", err, res)
	}
	body := resultJSON(t, res)
	encoded, _ := body["bytes_b64"].(string)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseDOCX(raw)
	if err != nil {
		t.Fatalf("parse modified: %v", err)
	}
	got := doc.fullText()
	if !strings.Contains(got, "Hello Cody, welcome to Loom.") {
		t.Errorf("modified text wrong: %q", got)
	}
	missing, _ := body["unmatched_placeholders"].([]any)
	if len(missing) != 0 {
		t.Errorf("expected zero unmatched, got %v", missing)
	}
}

// TestHandleModifyDOCX_SplitRuns covers the realistic case where Word has
// split a placeholder across multiple <w:t> runs. Naive per-CharData
// replacement misses these; the paragraph-scoped rewriter must join them.
func TestHandleModifyDOCX_SplitRuns(t *testing.T) {
	cases := []struct {
		name string
		runs []string // each entry is one <w:t> run within a single <w:p>
	}{
		{"split at start", []string{"{", "{name}}"}},
		{"split mid-key", []string{"Hello {{na", "me}} world"}},
		{"split across three", []string{"prefix {{", "na", "me}} suffix"}},
		{"two placeholders, both split", []string{"{{na", "me}} and {{pla", "ce}}"}},
		{"placeholder spans entire run", []string{"Hi ", "{{name}}", " bye"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docx := buildDOCXMultiRun(t, tc.runs)
			res, err := handleModifyDOCX(context.Background(), map[string]any{
				"bytes_b64": base64.StdEncoding.EncodeToString(docx),
				"replacements": map[string]any{
					"{{name}}":  "Cody",
					"{{place}}": "Loom",
				},
			})
			if err != nil || res.IsError {
				t.Fatalf("unexpected: err=%v res=%+v", err, res)
			}
			body := resultJSON(t, res)
			raw, err := base64.StdEncoding.DecodeString(body["bytes_b64"].(string))
			if err != nil {
				t.Fatal(err)
			}
			doc, err := parseDOCX(raw)
			if err != nil {
				t.Fatalf("parse modified: %v", err)
			}
			got := doc.fullText()
			if strings.Contains(got, "{{name}}") || strings.Contains(got, "{{place}}") {
				t.Errorf("placeholders not replaced: %q", got)
			}
			if !strings.Contains(got, "Cody") {
				t.Errorf("expected Cody in: %q", got)
			}
			missing, _ := body["unmatched_placeholders"].([]any)
			// Only {{place}} should be missing when the fixture lacks it.
			expectMissingPlace := !strings.Contains(strings.Join(tc.runs, ""), "{{place}}") &&
				!strings.Contains(strings.Join(tc.runs, ""), "{{pla")
			if expectMissingPlace {
				found := false
				for _, m := range missing {
					if m == "{{place}}" {
						found = true
					}
				}
				if !found {
					t.Errorf("expected {{place}} in unmatched, got %v", missing)
				}
			}
		})
	}
}

func TestHandleModifyDOCX_WrongFormat(t *testing.T) {
	res, err := handleModifyDOCX(context.Background(), map[string]any{
		"bytes_b64":    base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 fake")),
		"replacements": map[string]any{"x": "y"},
		"format":       "pdf",
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for non-docx modify")
	}
}

// ---------- handler: search ------------------------------------------------

func TestHandleSearch_DOCX(t *testing.T) {
	docx := buildDOCX(t, []docxParagraph{
		{text: "alpha beta"},
		{text: "ALPHA gamma"},
		{text: "nothing here"},
	})
	res, err := handleSearch(context.Background(), map[string]any{
		"bytes_b64":      base64.StdEncoding.EncodeToString(docx),
		"format":         "docx",
		"query":          "alpha",
		"case_sensitive": false,
	})
	if err != nil || res.IsError {
		t.Fatalf("unexpected: err=%v res=%+v", err, res)
	}
	body := resultJSON(t, res)
	matches, _ := body["matches"].([]any)
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

// ---------- handler: extract_structure ------------------------------------

func TestHandleExtractStructure_XLSX(t *testing.T) {
	xlsxBytes := createXLSXOrFatal(t, []sheetSpec{
		{Name: "S1", Rows: [][]any{{"a", "b"}, {"c", "d"}}},
		{Name: "S2", Rows: [][]any{{"x"}}},
	})
	res, err := handleExtractStructure(context.Background(), map[string]any{
		"bytes_b64": base64.StdEncoding.EncodeToString(xlsxBytes),
		"format":    "xlsx",
	})
	if err != nil || res.IsError {
		t.Fatalf("unexpected: err=%v res=%+v", err, res)
	}
	body := resultJSON(t, res)
	sheets, _ := body["sheets"].([]any)
	if len(sheets) != 2 {
		t.Errorf("got %d sheets", len(sheets))
	}
}

// ---------- helpers --------------------------------------------------------

func resultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	// CallToolResult exposes content as JSON via TextContent items. Use the
	// stdlib path: marshal a wrapper struct via reflection-free trick.
	type ct = struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var w ct
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(w.Content) == 0 {
		t.Fatalf("no content in result: %s", raw)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(w.Content[0].Text), &out); err != nil {
		t.Fatalf("unmarshal payload: %v\npayload=%s", err, w.Content[0].Text)
	}
	return out
}

func createXLSXOrFatal(t *testing.T, sheets []sheetSpec) []byte {
	t.Helper()
	out, err := createXLSX(sheets)
	if err != nil {
		t.Fatalf("createXLSX: %v", err)
	}
	return out
}

package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
	"github.com/crb2nu/loom/pkg/validate"
)

// registerReadTools wires up the six read-only tools.
func registerReadTools(srv *mcpscaffold.Server) {
	srv.AddTracedTool(mcp.Tool{
		Name:        "office_extract_text",
		Description: "Extract plain text from a .pdf, .docx, .xlsx, or .pptx document. Auto-detects format from extension or magic bytes.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: mergeProps(commonSourceProps(), map[string]any{
				"max_chars": map[string]any{
					"type":        "integer",
					"description": "Optional output truncation (default 200000). 0 disables truncation.",
				},
			}),
		},
	}, handleExtractText)

	srv.AddTracedTool(mcp.Tool{
		Name:        "office_extract_metadata",
		Description: "Extract metadata from a document (title, author, page/sheet/slide count, timestamps, etc.).",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: commonSourceProps(),
		},
	}, handleExtractMetadata)

	srv.AddTracedTool(mcp.Tool{
		Name:        "office_extract_structure",
		Description: "Extract document outline: PDF page texts, DOCX headings/paragraphs grouped by style, XLSX sheet names with row counts, or PPTX slide titles.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: commonSourceProps(),
		},
	}, handleExtractStructure)

	srv.AddTracedTool(mcp.Tool{
		Name:        "office_extract_tables",
		Description: "Extract tabular data as JSON. For XLSX returns sheets/rows; for DOCX returns tables found inline. PDF/PPTX tables are not supported.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: mergeProps(commonSourceProps(), map[string]any{
				"sheet": map[string]any{
					"type":        "string",
					"description": "XLSX only: restrict output to a single sheet by name.",
				},
			}),
		},
	}, handleExtractTables)

	srv.AddTracedTool(mcp.Tool{
		Name:        "office_search",
		Description: "Search a document for a substring or regex. Returns matches with location (page/slide/sheet/paragraph index).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: mergeProps(commonSourceProps(), map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Substring or regex (when regex=true).",
				},
				"regex": map[string]any{
					"type":        "boolean",
					"description": "Treat query as a Go regular expression (default false).",
				},
				"case_sensitive": map[string]any{
					"type":        "boolean",
					"description": "Case-sensitive match (default false).",
				},
				"max_matches": map[string]any{
					"type":        "integer",
					"description": "Cap returned matches (default 100).",
				},
			}),
			Required: []string{"query"},
		},
	}, handleSearch)

	srv.AddTracedTool(mcp.Tool{
		Name:        "office_inspect",
		Description: "Quick inspection: detected format, byte size, page/sheet/slide count, key metadata. Cheaper than full extraction.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: commonSourceProps(),
		},
	}, handleInspect)
}

func mergeProps(maps ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// loadFromArgs is the shared input parser for read tools. Returns the loaded
// source or an mcp.ErrorResult-ready error.
func loadFromArgs(args map[string]any) (*source, *validate.Args, *mcp.CallToolResult) {
	v := validate.NewArgs(args)
	path := v.String("path", "")
	bytesB64 := v.String("bytes_b64", "")
	format := v.String("format", "")
	if err := v.Validate(); err != nil {
		return nil, v, mcp.ErrorResult(err)
	}
	src, err := loadSource(path, bytesB64, format)
	if err != nil {
		return nil, v, mcp.ErrorResult(err)
	}
	return src, v, nil
}

func handleExtractText(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	src, v, errRes := loadFromArgs(args)
	if errRes != nil {
		return errRes, nil
	}
	maxChars := v.Int("max_chars", 200000)

	text, err := extractTextFor(src)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	truncated := false
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars]
		truncated = true
	}
	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"format":    src.format,
		"name":      src.name,
		"bytes":     len(src.bytes),
		"text":      text,
		"truncated": truncated,
	})
}

func extractTextFor(src *source) (string, error) {
	switch src.format {
	case "pdf":
		d, err := parsePDF(src.bytes)
		if err != nil {
			return "", err
		}
		return d.fullText(), nil
	case "docx":
		d, err := parseDOCX(src.bytes)
		if err != nil {
			return "", err
		}
		return d.fullText(), nil
	case "xlsx":
		d, err := parseXLSX(src.bytes)
		if err != nil {
			return "", err
		}
		return d.fullText(), nil
	case "pptx":
		d, err := parsePPTX(src.bytes)
		if err != nil {
			return "", err
		}
		return d.fullText(), nil
	}
	return "", fmt.Errorf("unsupported format: %s", src.format)
}

func handleExtractMetadata(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	src, _, errRes := loadFromArgs(args)
	if errRes != nil {
		return errRes, nil
	}

	meta, err := metadataFor(src)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"format":   src.format,
		"name":     src.name,
		"metadata": meta,
	})
}

func metadataFor(src *source) (map[string]string, error) {
	switch src.format {
	case "pdf":
		d, err := parsePDF(src.bytes)
		if err != nil {
			return nil, err
		}
		return d.metadata, nil
	case "docx":
		d, err := parseDOCX(src.bytes)
		if err != nil {
			return nil, err
		}
		return d.metadata, nil
	case "xlsx":
		d, err := parseXLSX(src.bytes)
		if err != nil {
			return nil, err
		}
		return d.metadata, nil
	case "pptx":
		d, err := parsePPTX(src.bytes)
		if err != nil {
			return nil, err
		}
		return d.metadata, nil
	}
	return nil, fmt.Errorf("unsupported format: %s", src.format)
}

func handleExtractStructure(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	src, _, errRes := loadFromArgs(args)
	if errRes != nil {
		return errRes, nil
	}

	switch src.format {
	case "pdf":
		d, err := parsePDF(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		pages := make([]map[string]any, 0, len(d.pages))
		for i, p := range d.pages {
			pages = append(pages, map[string]any{
				"page":  i + 1,
				"chars": len(p),
			})
		}
		return mcp.JSONResult(map[string]any{"ok": true, "format": "pdf", "pages": pages})
	case "docx":
		d, err := parseDOCX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		paras := make([]map[string]any, 0, len(d.paragraphs))
		for i, p := range d.paragraphs {
			paras = append(paras, map[string]any{
				"index": i,
				"style": p.style,
				"text":  p.text,
			})
		}
		return mcp.JSONResult(map[string]any{
			"ok":          true,
			"format":      "docx",
			"paragraphs":  paras,
			"table_count": len(d.tables),
		})
	case "xlsx":
		d, err := parseXLSX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		sheets := make([]map[string]any, 0, len(d.sheets))
		for _, s := range d.sheets {
			cols := 0
			for _, row := range s.rows {
				if len(row) > cols {
					cols = len(row)
				}
			}
			sheets = append(sheets, map[string]any{
				"name": s.name,
				"rows": len(s.rows),
				"cols": cols,
			})
		}
		return mcp.JSONResult(map[string]any{"ok": true, "format": "xlsx", "sheets": sheets})
	case "pptx":
		d, err := parsePPTX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		slides := make([]map[string]any, 0, len(d.slides))
		for _, s := range d.slides {
			slides = append(slides, map[string]any{
				"index":      s.index,
				"title":      s.title,
				"body_chars": len(s.body),
				"has_notes":  s.notes != "",
			})
		}
		return mcp.JSONResult(map[string]any{"ok": true, "format": "pptx", "slides": slides})
	}
	return mcp.ErrorResult(fmt.Errorf("unsupported format: %s", src.format)), nil
}

func handleExtractTables(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	src, v, errRes := loadFromArgs(args)
	if errRes != nil {
		return errRes, nil
	}
	sheet := v.String("sheet", "")

	switch src.format {
	case "xlsx":
		d, err := parseXLSX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		out := make([]map[string]any, 0, len(d.sheets))
		for _, s := range d.sheets {
			if sheet != "" && s.name != sheet {
				continue
			}
			out = append(out, map[string]any{"name": s.name, "rows": s.rows})
		}
		return mcp.JSONResult(map[string]any{"ok": true, "format": "xlsx", "sheets": out})
	case "docx":
		d, err := parseDOCX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		return mcp.JSONResult(map[string]any{"ok": true, "format": "docx", "tables": d.tables})
	case "pdf", "pptx":
		return mcp.ErrorResult(fmt.Errorf("table extraction is not supported for %s", src.format)), nil
	}
	return mcp.ErrorResult(fmt.Errorf("unsupported format: %s", src.format)), nil
}

type searchMatch struct {
	Location string `json:"location"` // human-readable: page=N, slide=N, sheet=X!A1, para=N
	Snippet  string `json:"snippet"`
}

func handleSearch(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	src, v, errRes := loadFromArgs(args)
	if errRes != nil {
		return errRes, nil
	}
	query := v.Required("query")
	useRegex := v.Bool("regex", false)
	caseSensitive := v.Bool("case_sensitive", false)
	maxMatches := v.IntRange("max_matches", 100, 1, 10000)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	matcher, err := buildMatcher(query, useRegex, caseSensitive)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	matches, err := searchSource(src, matcher, maxMatches)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"format":  src.format,
		"matches": matches,
		"count":   len(matches),
	})
}

type matcherFn func(s string) []int // returns start offsets of matches

func buildMatcher(query string, useRegex, caseSensitive bool) (matcherFn, error) {
	if useRegex {
		pat := query
		if !caseSensitive {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("compile regex: %w", err)
		}
		return func(s string) []int {
			locs := re.FindAllStringIndex(s, -1)
			out := make([]int, 0, len(locs))
			for _, l := range locs {
				out = append(out, l[0])
			}
			return out
		}, nil
	}
	q := query
	if !caseSensitive {
		q = strings.ToLower(q)
	}
	return func(s string) []int {
		hay := s
		if !caseSensitive {
			hay = strings.ToLower(hay)
		}
		var out []int
		start := 0
		for {
			i := strings.Index(hay[start:], q)
			if i < 0 {
				return out
			}
			out = append(out, start+i)
			start = start + i + len(q)
		}
	}, nil
}

func snippet(s string, offset int) string {
	const radius = 60
	lo := offset - radius
	if lo < 0 {
		lo = 0
	}
	hi := offset + radius
	if hi > len(s) {
		hi = len(s)
	}
	out := s[lo:hi]
	if lo > 0 {
		out = "…" + out
	}
	if hi < len(s) {
		out = out + "…"
	}
	return strings.ReplaceAll(out, "\n", " ")
}

func searchSource(src *source, m matcherFn, maxMatches int) ([]searchMatch, error) {
	var out []searchMatch
	add := func(loc, text string) bool {
		for _, off := range m(text) {
			out = append(out, searchMatch{Location: loc, Snippet: snippet(text, off)})
			if len(out) >= maxMatches {
				return true
			}
		}
		return false
	}

	switch src.format {
	case "pdf":
		d, err := parsePDF(src.bytes)
		if err != nil {
			return nil, err
		}
		for i, p := range d.pages {
			if add(fmt.Sprintf("page=%d", i+1), p) {
				return out, nil
			}
		}
	case "docx":
		d, err := parseDOCX(src.bytes)
		if err != nil {
			return nil, err
		}
		for i, p := range d.paragraphs {
			if add(fmt.Sprintf("para=%d", i), p.text) {
				return out, nil
			}
		}
		for ti, tbl := range d.tables {
			for ri, row := range tbl {
				if add(fmt.Sprintf("table=%d row=%d", ti, ri), strings.Join(row, "\t")) {
					return out, nil
				}
			}
		}
	case "xlsx":
		d, err := parseXLSX(src.bytes)
		if err != nil {
			return nil, err
		}
		for _, s := range d.sheets {
			for ri, row := range s.rows {
				for ci, cell := range row {
					if add(fmt.Sprintf("%s!%s%d", s.name, colName(ci+1), ri+1), anyToString(cell)) {
						return out, nil
					}
				}
			}
		}
	case "pptx":
		d, err := parsePPTX(src.bytes)
		if err != nil {
			return nil, err
		}
		for _, s := range d.slides {
			if add(fmt.Sprintf("slide=%d title", s.index), s.title) {
				return out, nil
			}
			if add(fmt.Sprintf("slide=%d body", s.index), s.body) {
				return out, nil
			}
			if s.notes != "" {
				if add(fmt.Sprintf("slide=%d notes", s.index), s.notes) {
					return out, nil
				}
			}
		}
	default:
		return nil, fmt.Errorf("unsupported format: %s", src.format)
	}
	return out, nil
}

// colName converts a 1-indexed column to A1-style letters (1->A, 27->AA).
func colName(n int) string {
	var b []byte
	for n > 0 {
		n--
		b = append([]byte{byte('A' + n%26)}, b...)
		n /= 26
	}
	return string(b)
}

func handleInspect(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	src, _, errRes := loadFromArgs(args)
	if errRes != nil {
		return errRes, nil
	}

	out := map[string]any{
		"ok":     true,
		"format": src.format,
		"name":   src.name,
		"bytes":  len(src.bytes),
	}

	switch src.format {
	case "pdf":
		d, err := parsePDF(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		out["page_count"] = len(d.pages)
		out["title"] = d.metadata["title"]
		out["author"] = d.metadata["author"]
	case "docx":
		d, err := parseDOCX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		out["paragraph_count"] = len(d.paragraphs)
		out["table_count"] = len(d.tables)
		out["title"] = d.metadata["title"]
		out["author"] = d.metadata["author"]
		out["page_count"] = d.metadata["page_count"]
	case "xlsx":
		d, err := parseXLSX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		out["sheet_count"] = len(d.sheets)
		names := make([]string, 0, len(d.sheets))
		for _, s := range d.sheets {
			names = append(names, s.name)
		}
		out["sheets"] = names
	case "pptx":
		d, err := parsePPTX(src.bytes)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		out["slide_count"] = len(d.slides)
		out["title"] = d.metadata["title"]
	}
	return mcp.JSONResult(out)
}

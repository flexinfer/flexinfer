package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// docxDoc holds parsed content from a .docx file.
type docxDoc struct {
	paragraphs []docxParagraph
	tables     [][][]string // tables[t][row][col]
	metadata   map[string]string
}

type docxParagraph struct {
	style string // e.g., "Heading1", "Title", "Normal"
	text  string
}

// docx XML is OOXML/WordprocessingML. We parse the bare minimum:
//   - word/document.xml: paragraphs (w:p), runs (w:r), text (w:t), tables (w:tbl)
//   - docProps/core.xml: dublin-core metadata
//   - docProps/app.xml: application properties (page count, etc.)
type docXMLPara struct {
	XMLName xml.Name     `xml:"p"`
	Props   docXMLPProps `xml:"pPr"`
	Runs    []docXMLRun  `xml:"r"`
}

type docXMLPProps struct {
	PStyle docXMLVal `xml:"pStyle"`
}

type docXMLVal struct {
	Val string `xml:"val,attr"`
}

type docXMLRun struct {
	Texts []docXMLText `xml:"t"`
}

type docXMLText struct {
	Value string `xml:",chardata"`
}

type docXMLTable struct {
	Rows []docXMLRow `xml:"tr"`
}

type docXMLRow struct {
	Cells []docXMLCell `xml:"tc"`
}

type docXMLCell struct {
	Paragraphs []docXMLPara `xml:"p"`
}

// coreProps/appProps share these shapes.
type coreProps struct {
	XMLName        xml.Name `xml:"coreProperties"`
	Title          string   `xml:"title"`
	Subject        string   `xml:"subject"`
	Creator        string   `xml:"creator"`
	Keywords       string   `xml:"keywords"`
	Description    string   `xml:"description"`
	LastModifiedBy string   `xml:"lastModifiedBy"`
	Revision       string   `xml:"revision"`
	Created        string   `xml:"created"`
	Modified       string   `xml:"modified"`
}

type appProps struct {
	XMLName  xml.Name `xml:"Properties"`
	Pages    string   `xml:"Pages"`
	Words    string   `xml:"Words"`
	Chars    string   `xml:"Characters"`
	AppName  string   `xml:"Application"`
	Template string   `xml:"Template"`
	Company  string   `xml:"Company"`
}

func parseDOCX(data []byte) (*docxDoc, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open docx zip: %w", err)
	}

	doc := &docxDoc{metadata: map[string]string{}}

	for _, f := range zr.File {
		switch f.Name {
		case "word/document.xml":
			if err := parseDocxBody(f, doc); err != nil {
				return nil, err
			}
		case "docProps/core.xml":
			if err := parseDocxCore(f, doc); err != nil {
				return nil, err
			}
		case "docProps/app.xml":
			if err := parseDocxApp(f, doc); err != nil {
				return nil, err
			}
		}
	}

	return doc, nil
}

func parseDocxBody(f *zip.File, doc *docxDoc) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open document.xml: %w", err)
	}
	defer rc.Close()

	// Iterate body children manually with a token stream so paragraphs and
	// tables retain their document order.
	dec := xml.NewDecoder(rc)
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("decode document.xml: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		depth++
		switch se.Name.Local {
		case "p":
			var p docXMLPara
			if err := dec.DecodeElement(&p, &se); err != nil {
				return fmt.Errorf("decode paragraph: %w", err)
			}
			doc.paragraphs = append(doc.paragraphs, paragraphFrom(p))
			depth--
		case "tbl":
			var t docXMLTable
			if err := dec.DecodeElement(&t, &se); err != nil {
				return fmt.Errorf("decode table: %w", err)
			}
			doc.tables = append(doc.tables, tableFrom(t))
			depth--
		}
	}
	return nil
}

func paragraphFrom(p docXMLPara) docxParagraph {
	var b strings.Builder
	for _, r := range p.Runs {
		for _, t := range r.Texts {
			b.WriteString(t.Value)
		}
	}
	return docxParagraph{style: p.Props.PStyle.Val, text: b.String()}
}

func tableFrom(t docXMLTable) [][]string {
	rows := make([][]string, 0, len(t.Rows))
	for _, r := range t.Rows {
		cells := make([]string, 0, len(r.Cells))
		for _, c := range r.Cells {
			var b strings.Builder
			for i, p := range c.Paragraphs {
				if i > 0 {
					b.WriteString("\n")
				}
				b.WriteString(paragraphFrom(p).text)
			}
			cells = append(cells, b.String())
		}
		rows = append(rows, cells)
	}
	return rows
}

func parseDocxCore(f *zip.File, doc *docxDoc) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	var cp coreProps
	if err := xml.NewDecoder(rc).Decode(&cp); err != nil {
		return fmt.Errorf("decode core.xml: %w", err)
	}
	setIf(doc.metadata, "title", cp.Title)
	setIf(doc.metadata, "subject", cp.Subject)
	setIf(doc.metadata, "author", cp.Creator)
	setIf(doc.metadata, "keywords", cp.Keywords)
	setIf(doc.metadata, "description", cp.Description)
	setIf(doc.metadata, "last_modified_by", cp.LastModifiedBy)
	setIf(doc.metadata, "revision", cp.Revision)
	setIf(doc.metadata, "created", cp.Created)
	setIf(doc.metadata, "modified", cp.Modified)
	return nil
}

func parseDocxApp(f *zip.File, doc *docxDoc) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	var ap appProps
	if err := xml.NewDecoder(rc).Decode(&ap); err != nil {
		return fmt.Errorf("decode app.xml: %w", err)
	}
	setIf(doc.metadata, "page_count", ap.Pages)
	setIf(doc.metadata, "word_count", ap.Words)
	setIf(doc.metadata, "char_count", ap.Chars)
	setIf(doc.metadata, "application", ap.AppName)
	setIf(doc.metadata, "template", ap.Template)
	setIf(doc.metadata, "company", ap.Company)
	return nil
}

func setIf(m map[string]string, k, v string) {
	if v = strings.TrimSpace(v); v != "" {
		m[k] = v
	}
}

func (d *docxDoc) fullText() string {
	var b strings.Builder
	for i, p := range d.paragraphs {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(p.text)
	}
	for _, t := range d.tables {
		b.WriteString("\n")
		for _, row := range t {
			b.WriteString("\n")
			b.WriteString(strings.Join(row, "\t"))
		}
	}
	return b.String()
}

// modifyDOCX rewrites a docx by performing literal string replacements inside
// every w:t text run. It preserves all other XML/formatting. Replacements is a
// map of placeholder -> replacement; pass `{{name}}` style keys.
//
// Word frequently splits placeholders across multiple <w:t> runs (after editing,
// spellcheck, or autocorrect). We handle that by joining every <w:t> text
// inside a single <w:p> into one logical string for matching, then writing the
// replacement back into the first affected run and clearing the others. The
// match uses the first run's formatting; any intra-placeholder formatting is
// lost (which is expected since the placeholder itself is being replaced).
func modifyDOCX(data []byte, replacements map[string]string) ([]byte, []string, error) {
	if len(replacements) == 0 {
		return nil, nil, fmt.Errorf("replacements is empty")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("open docx zip: %w", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	defer zw.Close()

	seen := map[string]int{}
	for _, f := range zr.File {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: f.Method, Modified: f.Modified})
		if err != nil {
			return nil, nil, fmt.Errorf("create entry %s: %w", f.Name, err)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("open entry %s: %w", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("read entry %s: %w", f.Name, err)
		}

		// Only rewrite the document body and headers/footers where prose lives.
		if isTextPart(f.Name) {
			body = rewriteWordText(body, replacements, seen)
		}

		if _, err := w.Write(body); err != nil {
			return nil, nil, fmt.Errorf("write entry %s: %w", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, nil, fmt.Errorf("close zip: %w", err)
	}

	// Report which placeholders were unreplaced — useful diagnostic for the
	// run-splitting caveat above.
	missing := make([]string, 0)
	for k := range replacements {
		if seen[k] == 0 {
			missing = append(missing, k)
		}
	}
	return buf.Bytes(), missing, nil
}

func isTextPart(name string) bool {
	switch {
	case name == "word/document.xml":
		return true
	case strings.HasPrefix(name, "word/header") && strings.HasSuffix(name, ".xml"):
		return true
	case strings.HasPrefix(name, "word/footer") && strings.HasSuffix(name, ".xml"):
		return true
	case strings.HasPrefix(name, "word/footnotes") && strings.HasSuffix(name, ".xml"):
		return true
	case strings.HasPrefix(name, "word/endnotes") && strings.HasSuffix(name, ".xml"):
		return true
	}
	return false
}

// pToken pairs a buffered XML token with metadata about whether it carries
// user-visible text inside a <w:t> element.
type pToken struct {
	tok       xml.Token
	isWtChars bool
}

// rewriteWordText walks the XML token stream, buffers each <w:p>...</w:p>
// scope, and applies placeholder replacements against the joined text of all
// <w:t> runs in the paragraph. Replacements are written back into the first
// affected run; other runs in the matched span are cleared. Non-paragraph
// tokens are passed through unchanged so formatting/attributes survive.
func rewriteWordText(body []byte, replacements map[string]string, seen map[string]int) []byte {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)

	var paraBuf []pToken
	inPara := false
	wtDepth := 0 // depth of nested <w:t>; 0 means we're not in a text node

	flush := func() error {
		applyPlaceholdersInParagraph(paraBuf, replacements, seen)
		for _, pt := range paraBuf {
			if err := enc.EncodeToken(pt.tok); err != nil {
				return err
			}
		}
		paraBuf = paraBuf[:0]
		return nil
	}

	emit := func(tok xml.Token) bool {
		if inPara {
			paraBuf = append(paraBuf, pToken{tok: xml.CopyToken(tok), isWtChars: false})
			return true
		}
		if err := enc.EncodeToken(tok); err != nil {
			return false
		}
		return true
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Unparseable XML: return original to avoid corrupting the file.
			return body
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if !inPara && t.Name.Local == "p" {
				inPara = true
				paraBuf = append(paraBuf, pToken{tok: xml.CopyToken(tok)})
				continue
			}
			if inPara && t.Name.Local == "t" {
				wtDepth++
			}
			if !emit(tok) {
				return body
			}
		case xml.EndElement:
			if inPara && t.Name.Local == "t" && wtDepth > 0 {
				wtDepth--
			}
			if inPara && t.Name.Local == "p" {
				paraBuf = append(paraBuf, pToken{tok: xml.CopyToken(tok)})
				if err := flush(); err != nil {
					return body
				}
				inPara = false
				continue
			}
			if !emit(tok) {
				return body
			}
		case xml.CharData:
			if inPara {
				paraBuf = append(paraBuf, pToken{tok: xml.CopyToken(tok), isWtChars: wtDepth > 0})
				continue
			}
			if err := enc.EncodeToken(tok); err != nil {
				return body
			}
		default:
			if !emit(tok) {
				return body
			}
		}
	}
	if err := enc.Flush(); err != nil {
		return body
	}
	return out.Bytes()
}

// applyPlaceholdersInParagraph mutates the buffered tokens in place. For each
// replacement key, it joins every <w:t> CharData in the paragraph and scans
// for occurrences. When found, the first run covering the match gets the
// replacement text written into it (preserving any prefix/suffix outside the
// placeholder); intermediate runs are cleared; the final run keeps any suffix
// after the placeholder ended.
func applyPlaceholdersInParagraph(buf []pToken, replacements map[string]string, seen map[string]int) {
	// Collect indices of CharData tokens inside <w:t> elements.
	tIdx := make([]int, 0, len(buf))
	for i, pt := range buf {
		if pt.isWtChars {
			tIdx = append(tIdx, i)
		}
	}
	if len(tIdx) == 0 {
		return
	}

	textAt := func(i int) string { return string(buf[tIdx[i]].tok.(xml.CharData)) }
	setTextAt := func(i int, s string) { buf[tIdx[i]].tok = xml.CharData(s) }

	for k, v := range replacements {
		// Loop because each replacement may itself contain or expose new
		// matches; capped to prevent infinite loops if v itself contains k.
		for guard := 0; guard < 10000; guard++ {
			texts := make([]string, len(tIdx))
			offsets := make([]int, len(tIdx)+1)
			for i := range tIdx {
				texts[i] = textAt(i)
				offsets[i+1] = offsets[i] + len(texts[i])
			}
			joined := strings.Join(texts, "")
			idx := strings.Index(joined, k)
			if idx < 0 {
				break
			}
			seen[k]++
			end := idx + len(k)

			firstWritten := false
			for i := range tIdx {
				tokStart, tokEnd := offsets[i], offsets[i+1]
				if tokEnd <= idx || tokStart >= end {
					continue
				}
				switch {
				case tokStart <= idx && tokEnd >= end:
					// Match entirely inside this run.
					setTextAt(i, texts[i][:idx-tokStart]+v+texts[i][end-tokStart:])
					firstWritten = true
				case !firstWritten && tokStart <= idx:
					// First run of a multi-run match: keep prefix + insert
					// the entire replacement here.
					setTextAt(i, texts[i][:idx-tokStart]+v)
					firstWritten = true
				case tokEnd >= end:
					// Last run of a multi-run match: keep the suffix after
					// the placeholder ended.
					setTextAt(i, texts[i][end-tokStart:])
				default:
					// Middle run fully consumed by the match.
					setTextAt(i, "")
				}
			}
			// If v contains k (e.g., user passed identity replacement) we
			// would loop forever; the guard above bounds iterations and the
			// match index will advance past v on the next pass via Index.
			if strings.Contains(v, k) {
				break
			}
		}
	}
}

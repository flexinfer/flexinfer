package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// pdfDoc holds the parsed PDF for downstream tools.
type pdfDoc struct {
	pages    []string // 1-indexed: pages[0] is page 1
	metadata map[string]string
}

func parsePDF(data []byte) (*pdfDoc, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}

	doc := &pdfDoc{metadata: map[string]string{}}
	total := r.NumPage()
	for i := 1; i <= total; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			doc.pages = append(doc.pages, "")
			continue
		}
		text, terr := page.GetPlainText(nil)
		if terr != nil {
			// keep going on per-page errors so a single bad page doesn't sink
			// the whole document
			text = ""
		}
		doc.pages = append(doc.pages, text)
	}

	// Best-effort metadata — ledongthuc/pdf exposes the trailer Info dict.
	trailer := r.Trailer()
	if info := trailer.Key("Info"); !info.IsNull() {
		for _, key := range []string{"Title", "Author", "Subject", "Keywords", "Creator", "Producer", "CreationDate", "ModDate"} {
			if v := info.Key(key); !v.IsNull() {
				if s := v.Text(); s != "" {
					doc.metadata[strings.ToLower(key)] = s
				}
			}
		}
	}
	doc.metadata["page_count"] = fmt.Sprintf("%d", total)
	return doc, nil
}

func (d *pdfDoc) fullText() string {
	var b strings.Builder
	for i, p := range d.pages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(p)
	}
	return b.String()
}

// ensure io.Reader is used (silence unused import if refactored)
var _ = io.EOF

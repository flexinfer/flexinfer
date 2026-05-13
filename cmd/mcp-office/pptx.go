package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// pptxDoc holds parsed content from a .pptx file. Slides are ordered by their
// numeric suffix (slide1.xml, slide2.xml, ...).
type pptxDoc struct {
	slides   []pptxSlide
	metadata map[string]string
}

type pptxSlide struct {
	index int // 1-based
	title string
	body  string // concatenated non-title text
	notes string // associated notesSlide text, if any
}

// PowerPoint XML uses DrawingML; relevant elements are a:t (text runs).
var slideNumRE = regexp.MustCompile(`ppt/slides/slide(\d+)\.xml$`)
var notesNumRE = regexp.MustCompile(`ppt/notesSlides/notesSlide(\d+)\.xml$`)
var aTextRE = regexp.MustCompile(`<a:t[^>]*>([^<]*)</a:t>`)
var titleSpRE = regexp.MustCompile(`(?s)<p:sp>.*?<p:nvSpPr>.*?<p:nvPr>.*?<p:ph[^>]*type="(?:title|ctrTitle)".*?</p:sp>`)

func parsePPTX(data []byte) (*pptxDoc, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open pptx zip: %w", err)
	}

	doc := &pptxDoc{metadata: map[string]string{}}
	slides := map[int]*pptxSlide{}
	notes := map[int]string{}

	for _, f := range zr.File {
		if m := slideNumRE.FindStringSubmatch(f.Name); m != nil {
			idx, _ := strconv.Atoi(m[1])
			title, body, err := readSlide(f)
			if err != nil {
				return nil, fmt.Errorf("slide %d: %w", idx, err)
			}
			slides[idx] = &pptxSlide{index: idx, title: title, body: body}
			continue
		}
		if m := notesNumRE.FindStringSubmatch(f.Name); m != nil {
			idx, _ := strconv.Atoi(m[1])
			text, err := readAllText(f)
			if err != nil {
				return nil, fmt.Errorf("notes %d: %w", idx, err)
			}
			notes[idx] = text
			continue
		}
		switch f.Name {
		case "docProps/core.xml":
			if err := parsePptxCore(f, doc); err != nil {
				return nil, err
			}
		case "docProps/app.xml":
			if err := parsePptxApp(f, doc); err != nil {
				return nil, err
			}
		}
	}

	indices := make([]int, 0, len(slides))
	for k := range slides {
		indices = append(indices, k)
	}
	sort.Ints(indices)
	for _, i := range indices {
		s := slides[i]
		if n, ok := notes[i]; ok {
			s.notes = n
		}
		doc.slides = append(doc.slides, *s)
	}
	doc.metadata["slide_count"] = fmt.Sprintf("%d", len(doc.slides))
	return doc, nil
}

func readSlide(f *zip.File) (title, body string, err error) {
	rc, err := f.Open()
	if err != nil {
		return "", "", err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return "", "", err
	}

	// Title: the first <p:sp> whose nvSpPr/nvPr contains a title placeholder.
	titleBlock := titleSpRE.Find(raw)
	var titleParts []string
	if titleBlock != nil {
		for _, m := range aTextRE.FindAllSubmatch(titleBlock, -1) {
			titleParts = append(titleParts, string(m[1]))
		}
	}
	title = strings.TrimSpace(strings.Join(titleParts, " "))

	// Body: all a:t outside the title block.
	bodyRaw := raw
	if titleBlock != nil {
		bodyRaw = bytes.Replace(raw, titleBlock, []byte{}, 1)
	}
	var bodyParts []string
	for _, m := range aTextRE.FindAllSubmatch(bodyRaw, -1) {
		s := strings.TrimSpace(string(m[1]))
		if s != "" {
			bodyParts = append(bodyParts, s)
		}
	}
	body = strings.Join(bodyParts, "\n")
	return title, body, nil
}

func readAllText(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, m := range aTextRE.FindAllSubmatch(raw, -1) {
		s := strings.TrimSpace(string(m[1]))
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n"), nil
}

func parsePptxCore(f *zip.File, doc *pptxDoc) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	var cp coreProps // same shape as docx
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

type pptAppProps struct {
	XMLName      xml.Name `xml:"Properties"`
	Slides       string   `xml:"Slides"`
	Notes        string   `xml:"Notes"`
	HiddenSlides string   `xml:"HiddenSlides"`
	Words        string   `xml:"Words"`
	AppName      string   `xml:"Application"`
	Company      string   `xml:"Company"`
}

func parsePptxApp(f *zip.File, doc *pptxDoc) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	var ap pptAppProps
	if err := xml.NewDecoder(rc).Decode(&ap); err != nil {
		return fmt.Errorf("decode app.xml: %w", err)
	}
	setIf(doc.metadata, "slide_count_reported", ap.Slides)
	setIf(doc.metadata, "notes_count", ap.Notes)
	setIf(doc.metadata, "hidden_slides", ap.HiddenSlides)
	setIf(doc.metadata, "word_count", ap.Words)
	setIf(doc.metadata, "application", ap.AppName)
	setIf(doc.metadata, "company", ap.Company)
	return nil
}

func (d *pptxDoc) fullText() string {
	var b strings.Builder
	for i, s := range d.slides {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## Slide %d", s.index)
		if s.title != "" {
			b.WriteString(": ")
			b.WriteString(s.title)
		}
		b.WriteString("\n")
		if s.body != "" {
			b.WriteString(s.body)
		}
		if s.notes != "" {
			b.WriteString("\n\nNotes:\n")
			b.WriteString(s.notes)
		}
	}
	return b.String()
}

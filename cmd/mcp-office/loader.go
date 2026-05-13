package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxBytes caps document size to keep memory bounded and tool responses
// reasonable. Override with OFFICE_MAX_BYTES env if needed.
const defaultMaxBytes = 64 * 1024 * 1024 // 64 MiB

// source represents a loaded document ready for parsing.
type source struct {
	bytes  []byte
	name   string // best-effort filename for diagnostics
	format string // one of: pdf, docx, xlsx, pptx
}

// loadSource resolves the (path | bytes_b64) inputs into raw bytes and detects
// the format. Either path or bytes_b64 must be set; not both. If format is
// passed explicitly it overrides detection.
func loadSource(path, bytesB64, format string) (*source, error) {
	if path == "" && bytesB64 == "" {
		return nil, fmt.Errorf("either path or bytes_b64 is required")
	}
	if path != "" && bytesB64 != "" {
		return nil, fmt.Errorf("path and bytes_b64 are mutually exclusive")
	}

	max := defaultMaxBytes
	if env := os.Getenv("OFFICE_MAX_BYTES"); env != "" {
		var v int
		if _, err := fmt.Sscanf(env, "%d", &v); err == nil && v > 0 {
			max = v
		}
	}

	var (
		data []byte
		name string
		err  error
	)
	switch {
	case path != "":
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, fmt.Errorf("stat %s: %w", path, statErr)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("path is a directory: %s", path)
		}
		if info.Size() > int64(max) {
			return nil, fmt.Errorf("file size %d exceeds OFFICE_MAX_BYTES (%d)", info.Size(), max)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		name = filepath.Base(path)
	case bytesB64 != "":
		data, err = base64.StdEncoding.DecodeString(bytesB64)
		if err != nil {
			return nil, fmt.Errorf("decode bytes_b64: %w", err)
		}
		if len(data) > max {
			return nil, fmt.Errorf("decoded size %d exceeds OFFICE_MAX_BYTES (%d)", len(data), max)
		}
		name = "<inline>"
	}

	detected := strings.ToLower(strings.TrimSpace(format))
	if detected == "" {
		detected = detectFormat(name, data)
	}
	if detected == "" {
		return nil, fmt.Errorf("unable to detect document format from %s (pass format=)", name)
	}

	return &source{bytes: data, name: name, format: detected}, nil
}

// detectFormat infers the document format from filename extension and magic
// bytes. The OOXML formats (docx/xlsx/pptx) all begin with PK\x03\x04 so the
// filename is the disambiguator; we also fall back to scanning the central
// directory for a well-known entry name.
func detectFormat(name string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".pdf":
		return "pdf"
	case ".docx":
		return "docx"
	case ".xlsx":
		return "xlsx"
	case ".pptx":
		return "pptx"
	}

	if len(data) >= 5 && bytes.HasPrefix(data, []byte("%PDF-")) {
		return "pdf"
	}
	if len(data) >= 4 && bytes.HasPrefix(data, []byte{0x50, 0x4B, 0x03, 0x04}) {
		// OOXML zip — sniff for type-specific entry names.
		head := data
		if len(head) > 4096 {
			head = head[:4096]
		}
		switch {
		case bytes.Contains(head, []byte("word/")):
			return "docx"
		case bytes.Contains(head, []byte("xl/")):
			return "xlsx"
		case bytes.Contains(head, []byte("ppt/")):
			return "pptx"
		}
	}
	return ""
}

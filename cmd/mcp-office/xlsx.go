package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// xlsxDoc holds parsed content from an .xlsx workbook.
type xlsxDoc struct {
	sheets   []xlsxSheet
	metadata map[string]string
}

// xlsxSheet preserves cell values with their original types (float64 for
// numbers, bool, string, time.Time). Empty/missing cells are nil.
type xlsxSheet struct {
	name string
	rows [][]any
}

func parseXLSX(data []byte) (*xlsxDoc, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open xlsx: %w", err)
	}
	defer func() { _ = f.Close() }()

	doc := &xlsxDoc{metadata: map[string]string{}}
	for _, name := range f.GetSheetList() {
		rows, err := readSheetTyped(f, name)
		if err != nil {
			return nil, fmt.Errorf("read sheet %s: %w", name, err)
		}
		doc.sheets = append(doc.sheets, xlsxSheet{name: name, rows: rows})
	}

	props, _ := f.GetDocProps()
	if props != nil {
		setIf(doc.metadata, "title", props.Title)
		setIf(doc.metadata, "subject", props.Subject)
		setIf(doc.metadata, "author", props.Creator)
		setIf(doc.metadata, "keywords", props.Keywords)
		setIf(doc.metadata, "description", props.Description)
		setIf(doc.metadata, "last_modified_by", props.LastModifiedBy)
		setIf(doc.metadata, "revision", props.Revision)
		setIf(doc.metadata, "created", props.Created)
		setIf(doc.metadata, "modified", props.Modified)
		setIf(doc.metadata, "category", props.Category)
	}
	doc.metadata["sheet_count"] = fmt.Sprintf("%d", len(doc.sheets))
	return doc, nil
}

// readSheetTyped walks every cell with GetCellType so numbers come back as
// float64, booleans as bool, and dates as a formatted string. Empty trailing
// cells are not included (matches GetRows behavior).
func readSheetTyped(f *excelize.File, sheet string) ([][]any, error) {
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	out := make([][]any, len(rows))
	for r, row := range rows {
		typed := make([]any, len(row))
		for c, raw := range row {
			cell, cerr := excelize.CoordinatesToCellName(c+1, r+1)
			if cerr != nil {
				typed[c] = raw
				continue
			}
			ct, cerr := f.GetCellType(sheet, cell)
			if cerr != nil {
				typed[c] = raw
				continue
			}
			typed[c] = coerceCellValue(raw, ct)
		}
		out[r] = typed
	}
	return out, nil
}

// coerceCellValue converts the raw string from GetRows into a typed value
// using the cell's declared type. Anything we can't parse falls back to the
// raw string so we never lose data.
//
// Note on CellTypeUnset: cells written with SetCellFloat leave `c.T` empty in
// the OOXML, which maps to CellTypeUnset. We optimistically try to parse them
// as numbers; if parsing fails (genuinely non-numeric content) we keep the raw
// string. This matches Excel's "default = number" convention.
func coerceCellValue(raw string, ct excelize.CellType) any {
	switch ct {
	case excelize.CellTypeBool:
		switch strings.ToLower(raw) {
		case "1", "true":
			return true
		case "0", "false":
			return false
		}
	case excelize.CellTypeNumber, excelize.CellTypeUnset:
		if raw == "" {
			return raw
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return f
		}
	case excelize.CellTypeFormula:
		// Formula cells store their cached result as a string regardless of
		// the result's actual type. Try numeric/bool first so SUM/COUNT/etc.
		// expose the right type; fall back to the raw string for text results
		// like =CONCAT(...) or =IF(...,"yes","no").
		if raw == "" {
			return raw
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			return f
		}
		switch strings.ToLower(raw) {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return raw
}

func (d *xlsxDoc) fullText() string {
	var b strings.Builder
	for i, s := range d.sheets {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("# ")
		b.WriteString(s.name)
		b.WriteString("\n")
		for _, row := range s.rows {
			for ci, cell := range row {
				if ci > 0 {
					b.WriteString("\t")
				}
				b.WriteString(anyToString(cell))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// anyToString formats a typed cell value for text/search output. Numbers use
// %g so 1.0 prints as "1" and 1.5 as "1.5".
func anyToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "TRUE"
		}
		return "FALSE"
	case float64:
		return fmt.Sprintf("%g", t)
	case float32:
		return fmt.Sprintf("%g", t)
	case int:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	}
	return fmt.Sprintf("%v", v)
}

// sheetSpec describes a sheet to create. Rows are typed so JSON numbers and
// booleans round-trip into the workbook with their native types. Strings that
// start with "=" are written as formulas.
type sheetSpec struct {
	Name string  `json:"name"`
	Rows [][]any `json:"rows"`
}

// formulaRef tracks formula cells so we can compute and cache their values
// after every cell has been written — necessary for forward references like
// =A10 that point at a cell defined later.
type formulaRef struct {
	sheet, cell, formula string
}

func createXLSX(sheets []sheetSpec) ([]byte, error) {
	if len(sheets) == 0 {
		return nil, fmt.Errorf("at least one sheet is required")
	}
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	var formulas []formulaRef
	for i, s := range sheets {
		if s.Name == "" {
			return nil, fmt.Errorf("sheet %d: name is required", i)
		}
		if i == 0 {
			defaultName := f.GetSheetName(0)
			if err := f.SetSheetName(defaultName, s.Name); err != nil {
				return nil, fmt.Errorf("rename default sheet to %s: %w", s.Name, err)
			}
		} else {
			if _, err := f.NewSheet(s.Name); err != nil {
				return nil, fmt.Errorf("create sheet %s: %w", s.Name, err)
			}
		}
		for r, row := range s.Rows {
			for c, val := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					return nil, fmt.Errorf("sheet %s cell (%d,%d): %w", s.Name, r+1, c+1, err)
				}
				formula, err := writeCell(f, s.Name, cell, val)
				if err != nil {
					return nil, fmt.Errorf("set %s!%s: %w", s.Name, cell, err)
				}
				if formula != "" {
					formulas = append(formulas, formulaRef{s.Name, cell, formula})
				}
			}
		}
	}

	// Post-pass: compute and cache formula results so downstream readers see
	// values without needing Excel to open and recalc the file.
	for _, ref := range formulas {
		cacheFormulaResult(f, ref)
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, fmt.Errorf("write xlsx: %w", err)
	}
	return buf.Bytes(), nil
}

// cacheFormulaResult computes the formula at ref and writes both the value
// (<v>) and the formula (<f>) into the cell. Excelize's SetCellFloat/Str
// clears the formula, so we re-attach it after the value write.
func cacheFormulaResult(f *excelize.File, ref formulaRef) {
	result, err := f.CalcCellValue(ref.sheet, ref.cell)
	if err != nil || result == "" {
		return
	}
	if fl, perr := strconv.ParseFloat(result, 64); perr == nil {
		_ = f.SetCellFloat(ref.sheet, ref.cell, fl, -1, 64)
	} else {
		_ = f.SetCellStr(ref.sheet, ref.cell, result)
	}
	// Re-attach the formula; SetCellFloat/Str cleared it.
	_ = f.SetCellFormula(ref.sheet, ref.cell, ref.formula)
}

// writeCell dispatches to the right excelize setter based on the value's Go
// type. Strings prefixed with "=" are treated as formulas so SUM/AVERAGE/etc.
// compute at open time. JSON numbers arrive as float64 and are written as
// numeric cells. Returns the formula string (or "") so the caller can later
// recompute and cache its result.
func writeCell(f *excelize.File, sheet, cell string, val any) (string, error) {
	switch v := val.(type) {
	case nil:
		return "", nil
	case string:
		if strings.HasPrefix(v, "=") && len(v) > 1 {
			return v, f.SetCellFormula(sheet, cell, v)
		}
		return "", f.SetCellStr(sheet, cell, v)
	case bool:
		return "", f.SetCellBool(sheet, cell, v)
	case float64:
		return "", f.SetCellFloat(sheet, cell, v, -1, 64)
	case float32:
		return "", f.SetCellFloat(sheet, cell, float64(v), -1, 64)
	case int:
		return "", f.SetCellInt(sheet, cell, int64(v))
	case int64:
		return "", f.SetCellInt(sheet, cell, v)
	case json.Number:
		return "", setJSONNumber(f, sheet, cell, v)
	}
	return "", f.SetCellValue(sheet, cell, val)
}

// setJSONNumber is split out so we only import encoding/json in one place and
// don't pay for json.Number type assertions in the hot path of writeCell.
func setJSONNumber(f *excelize.File, sheet, cell string, n json.Number) error {
	if i, err := n.Int64(); err == nil {
		return f.SetCellInt(sheet, cell, i)
	}
	if fl, err := n.Float64(); err == nil {
		return f.SetCellFloat(sheet, cell, fl, -1, 64)
	}
	return f.SetCellStr(sheet, cell, n.String())
}

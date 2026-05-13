package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
	"github.com/crb2nu/loom/pkg/validate"
)

// registerWriteTools wires up the production/modification tools.
func registerWriteTools(srv *mcpscaffold.Server) {
	srv.AddTracedTool(mcp.Tool{
		Name:        "office_create_xlsx",
		Description: "Create an .xlsx workbook from one or more sheet specifications. Returns base64-encoded bytes; optionally writes to out_path.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"sheets": map[string]any{
					"type":        "array",
					"description": "Ordered list of sheets. Each sheet has `name` and `rows` (array of string arrays).",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name": map[string]any{"type": "string"},
							"rows": map[string]any{
								"type":  "array",
								"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
							},
						},
						"required": []string{"name", "rows"},
					},
				},
				"out_path": map[string]any{
					"type":        "string",
					"description": "Optional filesystem path to write the workbook to. When omitted, bytes_b64 is returned in the response.",
				},
			},
			Required: []string{"sheets"},
		},
	}, handleCreateXLSX)

	srv.AddTracedTool(mcp.Tool{
		Name:        "office_modify_docx",
		Description: "Modify a .docx by replacing placeholder strings inside text runs. Preserves formatting. Returns base64-encoded bytes; optionally writes to out_path. Reports placeholders that did not match (often caused by Word splitting a placeholder across runs).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: mergeProps(commonSourceProps(), map[string]any{
				"replacements": map[string]any{
					"type":                 "object",
					"description":          "Map of placeholder string -> replacement string (e.g., {\"{{name}}\": \"Cody\"}).",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"out_path": map[string]any{
					"type":        "string",
					"description": "Optional filesystem path to write the modified document to.",
				},
			}),
			Required: []string{"replacements"},
		},
	}, handleModifyDOCX)
}

func handleCreateXLSX(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	outPath := v.String("out_path", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	raw, ok := args["sheets"].([]any)
	if !ok || len(raw) == 0 {
		return mcp.ErrorResult(fmt.Errorf("sheets must be a non-empty array")), nil
	}
	sheets, err := coerceSheetSpecs(raw)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	out, err := createXLSX(sheets)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp := map[string]any{
		"ok":     true,
		"format": "xlsx",
		"bytes":  len(out),
	}
	if outPath != "" {
		if err := os.WriteFile(outPath, out, 0o644); err != nil {
			return mcp.ErrorResult(fmt.Errorf("write %s: %w", outPath, err)), nil
		}
		resp["path"] = outPath
	} else {
		resp["bytes_b64"] = base64.StdEncoding.EncodeToString(out)
	}
	return mcp.JSONResult(resp)
}

func coerceSheetSpecs(raw []any) ([]sheetSpec, error) {
	out := make([]sheetSpec, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("sheets[%d] must be an object", i)
		}
		name, _ := m["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("sheets[%d].name is required", i)
		}
		rowsRaw, ok := m["rows"].([]any)
		if !ok {
			return nil, fmt.Errorf("sheets[%d].rows must be an array", i)
		}
		rows := make([][]any, 0, len(rowsRaw))
		for ri, r := range rowsRaw {
			cellsRaw, ok := r.([]any)
			if !ok {
				return nil, fmt.Errorf("sheets[%d].rows[%d] must be an array", i, ri)
			}
			// Keep raw types so JSON numbers/bools/formulas reach writeCell
			// with their native types.
			cells := append([]any{}, cellsRaw...)
			rows = append(rows, cells)
		}
		out = append(out, sheetSpec{Name: name, Rows: rows})
	}
	return out, nil
}

func handleModifyDOCX(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	src, v, errRes := loadFromArgs(args)
	if errRes != nil {
		return errRes, nil
	}
	if src.format != "docx" {
		return mcp.ErrorResult(fmt.Errorf("modify only supports docx, got %s", src.format)), nil
	}
	outPath := v.String("out_path", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	rawRep, ok := args["replacements"].(map[string]any)
	if !ok || len(rawRep) == 0 {
		return mcp.ErrorResult(fmt.Errorf("replacements must be a non-empty object")), nil
	}
	repl := make(map[string]string, len(rawRep))
	for k, raw := range rawRep {
		repl[k] = fmt.Sprintf("%v", raw)
	}

	out, missing, err := modifyDOCX(src.bytes, repl)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp := map[string]any{
		"ok":                     true,
		"format":                 "docx",
		"bytes":                  len(out),
		"unmatched_placeholders": missing,
	}
	if outPath != "" {
		if err := os.WriteFile(outPath, out, 0o644); err != nil {
			return mcp.ErrorResult(fmt.Errorf("write %s: %w", outPath, err)), nil
		}
		resp["path"] = outPath
	} else {
		resp["bytes_b64"] = base64.StdEncoding.EncodeToString(out)
	}
	return mcp.JSONResult(resp)
}

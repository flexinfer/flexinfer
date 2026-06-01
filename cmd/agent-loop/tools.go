package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flexinfer/flexinfer/internal/agentloop"
)

// fsTools builds the read-only filesystem tool set rooted at workdir. Both
// tools are jailed to the root: a path that escapes it (via .. or an
// absolute prefix outside root) is rejected. Read-only by design — this
// slice ships no write/exec tools, so a misbehaving model cannot mutate the
// host.
func fsTools(root string) ([]agentloop.Tool, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workdir: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("workdir %q: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workdir %q is not a directory", absRoot)
	}
	return []agentloop.Tool{
		readFileTool(absRoot),
		listDirTool(absRoot),
	}, nil
}

// resolveInRoot cleans rel against root and verifies the result stays inside
// root. An absolute path, or a relative path that climbs out of root via
// "..", is rejected outright (not silently re-anchored) so a misbehaving
// model gets a clear error rather than an unexpected file. Returns the
// absolute path on success.
func resolveInRoot(root, rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute path %q not allowed; use a path relative to the working directory", rel)
	}
	joined := filepath.Join(root, cleaned)
	if joined != root && !strings.HasPrefix(joined, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes workdir", rel)
	}
	return joined, nil
}

const maxReadBytes = 64 * 1024

func readFileTool(root string) agentloop.Tool {
	return agentloop.FunctionTool{
		Def: agentloop.ToolDef{
			Type: "function",
			Function: agentloop.ToolFunctionDef{
				Name:        "read_file",
				Description: "Read a UTF-8 text file under the working directory. Returns up to 64KB; larger files are truncated.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"file path relative to the working directory"}},"required":["path"]}`),
			},
		},
		Fn: func(_ context.Context, args string) (string, error) {
			var p struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(args), &p); err != nil {
				return "", fmt.Errorf("bad arguments: %w", err)
			}
			abs, err := resolveInRoot(root, p.Path)
			if err != nil {
				return "", err
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", err
			}
			if len(data) > maxReadBytes {
				return string(data[:maxReadBytes]) + "\n…[truncated]", nil
			}
			return string(data), nil
		},
	}
}

func listDirTool(root string) agentloop.Tool {
	return agentloop.FunctionTool{
		Def: agentloop.ToolDef{
			Type: "function",
			Function: agentloop.ToolFunctionDef{
				Name:        "list_dir",
				Description: "List entries of a directory under the working directory. Directories are suffixed with '/'.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"directory path relative to the working directory; default is the working directory root"}}}`),
			},
		},
		Fn: func(_ context.Context, args string) (string, error) {
			var p struct {
				Path string `json:"path"`
			}
			// Arguments may be empty / "{}" — default to root.
			if strings.TrimSpace(args) != "" {
				if err := json.Unmarshal([]byte(args), &p); err != nil {
					return "", fmt.Errorf("bad arguments: %w", err)
				}
			}
			abs, err := resolveInRoot(root, p.Path)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(abs)
			if err != nil {
				return "", err
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				names = append(names, name)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return "(empty directory)", nil
			}
			return strings.Join(names, "\n"), nil
		},
	}
}

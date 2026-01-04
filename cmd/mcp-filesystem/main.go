// mcp-filesystem is a server for safe filesystem access.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

// Request parameters
type listDirParams struct {
	Path string `json:"path"`
}

type readFileParams struct {
	Path string `json:"path"`
}

type searchFilesParams struct {
	Root    string `json:"root"`
	Pattern string `json:"pattern"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	server := mcp.NewServer("mcp-filesystem", version)
	server.SetInstructions("Safe filesystem access. Tools: list_directory, read_file, search_files")

	// list_directory
	server.AddTool(mcp.Tool{
		Name:        "list_directory",
		Description: "List contents of a directory",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to directory",
				},
			},
			Required: []string{"path"},
		},
	}, handleListDirectory)

	// read_file
	server.AddTool(mcp.Tool{
		Name:        "read_file",
		Description: "Read contents of a file",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Absolute path to file",
				},
			},
			Required: []string{"path"},
		},
	}, handleReadFile)

	// search_files (simple glob)
	server.AddTool(mcp.Tool{
		Name:        "search_files",
		Description: "Search for files matching a glob pattern",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"root": map[string]any{
					"type":        "string",
					"description": "Root directory to search in",
				},
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern (e.g. *.go)",
				},
			},
			Required: []string{"root", "pattern"},
		},
	}, handleSearchFiles)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleListDirectory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}

	var result []map[string]any
	for _, entry := range entries {
		info, _ := entry.Info()
		result = append(result, map[string]any{
			"name":  entry.Name(),
			"isDir": entry.IsDir(),
			"size":  info.Size(),
			"mode":  info.Mode().String(),
		})
	}

	return mcp.JSONResult(map[string]any{
		"path":    path,
		"entries": result,
	})
}

func handleReadFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Detect if binary? For now assume text or return base64 if needed.
	// MCP protocol handles strings.
	return mcp.JSONResult(map[string]any{
		"path":    path,
		"content": string(data),
		"size":    len(data),
	})
}

func handleSearchFiles(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	root, _ := args["root"].(string)
	pattern, _ := args["pattern"].(string)

	if root == "" {
		root = "."
	}
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	var matches []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // ignore errors
		}
		if d.IsDir() {
			return nil
		}
		matched, err := filepath.Match(pattern, d.Name())
		if err != nil {
			return err
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk dir: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"root":    root,
		"pattern": pattern,
		"matches": matches,
	})
}
